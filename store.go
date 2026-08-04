package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Store is a filesystem-backed OPM data store.
// Registry: OPM_DATA_DIR/projects.json
// Per-project board/tasks: OPM_DATA_DIR/projects/<id>/ (not a durable local git tree)
type Store struct {
	mu      sync.RWMutex
	dataDir string
}

func NewStore(dataDir string) (*Store, error) {
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dataDir = filepath.Join(home, ".config", "opm")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "projects"), 0o755); err != nil {
		return nil, err
	}
	s := &Store{dataDir: dataDir}
	if _, err := os.Stat(s.registryPath()); os.IsNotExist(err) {
		if err := s.writeJSON(s.registryPath(), projectsFile{Projects: []Project{}}); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) registryPath() string {
	return filepath.Join(s.dataDir, "projects.json")
}

func (s *Store) projectDir(p Project) string {
	return filepath.Join(s.dataDir, "projects", p.ID)
}

func (s *Store) writeJSON(path string, v interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) readJSON(path string, v interface{}) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func (s *Store) ListProjects() ([]Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var f projectsFile
	if err := s.readJSON(s.registryPath(), &f); err != nil {
		return nil, err
	}
	if f.Projects == nil {
		f.Projects = []Project{}
	}
	return f.Projects, nil
}

func (s *Store) GetProject(id string) (Project, error) {
	projects, err := s.ListProjects()
	if err != nil {
		return Project{}, err
	}
	for _, p := range projects {
		if p.ID == id {
			return p, nil
		}
	}
	return Project{}, fmt.Errorf("project not found")
}

// ListProjectsForOrg returns projects belonging to org. Empty org = unscoped (auth off).
func (s *Store) ListProjectsForOrg(org string) ([]Project, error) {
	all, err := s.ListProjects()
	if err != nil {
		return nil, err
	}
	if org == "" {
		return all, nil
	}
	out := make([]Project, 0, len(all))
	for _, p := range all {
		if projectInOrg(p, org) {
			out = append(out, p)
		}
	}
	return out, nil
}

// GetProjectForOrg returns the project only when it belongs to org (404 otherwise).
func (s *Store) GetProjectForOrg(id, org string) (Project, error) {
	p, err := s.GetProject(id)
	if err != nil {
		return Project{}, err
	}
	if !projectInOrg(p, org) {
		return Project{}, fmt.Errorf("project not found")
	}
	return p, nil
}

func (s *Store) CreateProject(in Project) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ownerRepo := normalizeOwnerRepo(in.OwnerRepo)
	name := strings.TrimSpace(in.Name)
	connectorID := strings.TrimSpace(in.ConnectorID)
	if ownerRepo == "" || connectorID == "" {
		return Project{}, fmt.Errorf("ownerRepo and connectorId required")
	}
	if name == "" {
		parts := strings.SplitN(ownerRepo, "/", 2)
		name = parts[len(parts)-1]
	}
	var f projectsFile
	if err := s.readJSON(s.registryPath(), &f); err != nil {
		return Project{}, err
	}
	for _, p := range f.Projects {
		if normalizeOwnerRepo(p.OwnerRepo) == ownerRepo {
			return Project{}, fmt.Errorf("repository already linked")
		}
	}
	now := nowUTC()
	p := Project{
		ID:             uuid.NewString(),
		Name:           name,
		OwnerRepo:      ownerRepo,
		GithubRepoID:   strings.TrimSpace(in.GithubRepoID),
		ConnectorID:    connectorID,
		OrganizationID: normalizeWriteOrg(in.OrganizationID),
		HTMLURL:        strings.TrimSpace(in.HTMLURL),
		DefaultBranch:  nz(strings.TrimSpace(in.DefaultBranch), "main"),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if p.HTMLURL == "" {
		p.HTMLURL = "https://github.com/" + ownerRepo
	}
	f.Projects = append(f.Projects, p)
	if err := s.writeJSON(s.registryPath(), f); err != nil {
		return Project{}, err
	}
	if err := s.ensureLayoutLocked(p); err != nil {
		return Project{}, err
	}
	return p, nil
}

func normalizeOwnerRepo(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "https://github.com/")
	s = strings.TrimPrefix(s, "http://github.com/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")
	parts := strings.Split(s, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

func (s *Store) DeleteProject(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var f projectsFile
	if err := s.readJSON(s.registryPath(), &f); err != nil {
		return err
	}
	out := make([]Project, 0, len(f.Projects))
	found := false
	var removed Project
	for _, p := range f.Projects {
		if p.ID == id {
			found = true
			removed = p
			continue
		}
		out = append(out, p)
	}
	if !found {
		return fmt.Errorf("project not found")
	}
	f.Projects = out
	if err := s.writeJSON(s.registryPath(), f); err != nil {
		return err
	}
	_ = os.RemoveAll(s.projectDir(removed))
	return nil
}

func (s *Store) InitProject(id string) error {
	p, err := s.GetProject(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureLayoutLocked(p)
}

func (s *Store) ensureLayoutLocked(p Project) error {
	base := s.projectDir(p)
	dirs := []string{
		base,
		filepath.Join(base, "specs"),
		filepath.Join(base, "roadmap"),
		filepath.Join(base, "ideation"),
		filepath.Join(base, "runs"),
		filepath.Join(base, "logs"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	boardPath := filepath.Join(base, "board.json")
	if _, err := os.Stat(boardPath); os.IsNotExist(err) {
		if err := s.writeJSON(boardPath, emptyBoard()); err != nil {
			return err
		}
	}
	statusPath := filepath.Join(base, "status.json")
	if _, err := os.Stat(statusPath); os.IsNotExist(err) {
		if err := s.writeJSON(statusPath, idleStatus()); err != nil {
			return err
		}
	}
	roadmapPath := filepath.Join(base, "roadmap", "roadmap.json")
	if _, err := os.Stat(roadmapPath); os.IsNotExist(err) {
		rm := Roadmap{
			ID:          "roadmap-1",
			ProjectName: p.Name,
			Version:     "0.1.0",
			Vision:      "",
			Phases:      []RoadmapPhase{},
			Features:    []RoadmapFeature{},
		}
		if err := s.writeJSON(roadmapPath, rm); err != nil {
			return err
		}
	}
	ideationPath := filepath.Join(base, "ideation", "ideation.json")
	if _, err := os.Stat(ideationPath); os.IsNotExist(err) {
		if err := s.writeJSON(ideationPath, emptyIdeation()); err != nil {
			return err
		}
	}
	changelogPath := filepath.Join(base, "changelog.md")
	if _, err := os.Stat(changelogPath); os.IsNotExist(err) {
		if err := os.WriteFile(changelogPath, []byte("# Changelog\n\nNo entries yet.\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) withProject(id string, fn func(Project) error) error {
	p, err := s.GetProject(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLayoutLocked(p); err != nil {
		return err
	}
	return fn(p)
}

func (s *Store) GetBoard(id string) (Board, error) {
	var board Board
	err := s.withProject(id, func(p Project) error {
		return s.readJSON(filepath.Join(s.projectDir(p), "board.json"), &board)
	})
	if err != nil {
		return nil, err
	}
	for _, c := range BoardColumns {
		if board[c] == nil {
			board[c] = []string{}
		}
	}
	// Map legacy "ai_review" → "review" if present.
	if legacy, ok := board["ai_review"]; ok && len(legacy) > 0 {
		board["review"] = append(board["review"], legacy...)
		delete(board, "ai_review")
	}
	return board, nil
}

func (s *Store) PutBoard(id string, board Board) error {
	return s.withProject(id, func(p Project) error {
		normalized := emptyBoard()
		for _, c := range BoardColumns {
			if board[c] != nil {
				normalized[c] = board[c]
			}
		}
		return s.writeJSON(filepath.Join(s.projectDir(p), "board.json"), normalized)
	})
}

func (s *Store) ListTasks(id string) ([]Task, error) {
	var tasks []Task
	err := s.withProject(id, func(p Project) error {
		specsDir := filepath.Join(s.projectDir(p), "specs")
		entries, err := os.ReadDir(specsDir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			var t Task
			path := filepath.Join(specsDir, e.Name(), "task.json")
			if err := s.readJSON(path, &t); err != nil {
				continue
			}
			tasks = append(tasks, t)
		}
		return nil
	})
	if tasks == nil {
		tasks = []Task{}
	}
	return tasks, err
}

func (s *Store) GetTask(projectID, specID string) (Task, error) {
	var t Task
	err := s.withProject(projectID, func(p Project) error {
		path := filepath.Join(s.projectDir(p), "specs", specID, "task.json")
		return s.readJSON(path, &t)
	})
	return t, err
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(title string) string {
	s := strings.ToLower(strings.TrimSpace(title))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "task"
	}
	if len(s) > 48 {
		s = s[:48]
		s = strings.Trim(s, "-")
	}
	return s
}

func (s *Store) nextSpecID(p Project, title string) (string, error) {
	specsDir := filepath.Join(s.projectDir(p), "specs")
	entries, err := os.ReadDir(specsDir)
	if err != nil {
		return "", err
	}
	maxN := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		var n int
		if _, err := fmt.Sscanf(name, "%d-", &n); err == nil && n > maxN {
			maxN = n
		}
	}
	return fmt.Sprintf("%03d-%s", maxN+1, slugify(title)), nil
}

func (s *Store) CreateTask(projectID string, title, description string, requireReview bool, ideationID, ideationType string) (Task, error) {
	var created Task
	err := s.withProject(projectID, func(p Project) error {
		title = nz(strings.TrimSpace(title), "Untitled task")
		specID, err := s.nextSpecID(p, title)
		if err != nil {
			return err
		}
		now := nowUTC()
		t := Task{
			ID:                        uuid.NewString(),
			SpecID:                    specID,
			Title:                     title,
			Description:               description,
			Status:                    "backlog",
			RequireReviewBeforeCoding: requireReview,
			IdeationID:                ideationID,
			IdeationTypeKey:           ideationType,
			CreatedAt:                 now,
			UpdatedAt:                 now,
		}
		dir := filepath.Join(s.projectDir(p), "specs", specID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := s.writeJSON(filepath.Join(dir, "task.json"), t); err != nil {
			return err
		}
		plan := ImplementationPlan{
			Feature:          title,
			WorkflowType:     "feature",
			ServicesInvolved: []string{},
			SpecFile:         "spec.md",
			Status:           "pending",
			PlanStatus:       "drafted",
			FinalAcceptance:  []string{},
			Phases:           []PlanPhase{},
		}
		_ = s.writeJSON(filepath.Join(dir, "implementation_plan.json"), plan)
		_ = s.writeJSON(filepath.Join(dir, "progress.json"), TaskProgress{})
		_ = os.WriteFile(filepath.Join(dir, "spec.md"), []byte("# "+title+"\n\n"+description+"\n"), 0o644)

		var board Board
		boardPath := filepath.Join(s.projectDir(p), "board.json")
		if err := s.readJSON(boardPath, &board); err != nil {
			board = emptyBoard()
		}
		for _, c := range BoardColumns {
			if board[c] == nil {
				board[c] = []string{}
			}
		}
		board["backlog"] = append(board["backlog"], specID)
		if err := s.writeJSON(boardPath, board); err != nil {
			return err
		}
		created = t
		return nil
	})
	return created, err
}

func (s *Store) UpdateTask(projectID, specID string, patch map[string]interface{}) (Task, error) {
	var out Task
	err := s.withProject(projectID, func(p Project) error {
		path := filepath.Join(s.projectDir(p), "specs", specID, "task.json")
		var t Task
		if err := s.readJSON(path, &t); err != nil {
			return err
		}
		if v, ok := patch["title"].(string); ok && strings.TrimSpace(v) != "" {
			t.Title = v
		}
		if v, ok := patch["description"].(string); ok {
			t.Description = v
		}
		if v, ok := patch["requireReviewBeforeCoding"].(bool); ok {
			t.RequireReviewBeforeCoding = v
		}
		t.UpdatedAt = nowUTC()
		if err := s.writeJSON(path, t); err != nil {
			return err
		}
		out = t
		return nil
	})
	return out, err
}

func (s *Store) MoveTask(projectID, specID, toStatus string) (Task, error) {
	toStatus = strings.TrimSpace(toStatus)
	valid := false
	for _, c := range BoardColumns {
		if c == toStatus {
			valid = true
			break
		}
	}
	if !valid {
		return Task{}, fmt.Errorf("invalid status %q", toStatus)
	}
	var out Task
	err := s.withProject(projectID, func(p Project) error {
		taskPath := filepath.Join(s.projectDir(p), "specs", specID, "task.json")
		var t Task
		if err := s.readJSON(taskPath, &t); err != nil {
			return err
		}
		boardPath := filepath.Join(s.projectDir(p), "board.json")
		var board Board
		if err := s.readJSON(boardPath, &board); err != nil {
			return err
		}
		for _, c := range BoardColumns {
			if board[c] == nil {
				board[c] = []string{}
			}
			filtered := make([]string, 0, len(board[c]))
			for _, id := range board[c] {
				if id != specID {
					filtered = append(filtered, id)
				}
			}
			board[c] = filtered
		}
		board[toStatus] = append(board[toStatus], specID)
		t.Status = toStatus
		t.UpdatedAt = nowUTC()
		if err := s.writeJSON(taskPath, t); err != nil {
			return err
		}
		if err := s.writeJSON(boardPath, board); err != nil {
			return err
		}
		out = t
		return nil
	})
	return out, err
}

func (s *Store) DeleteTask(projectID, specID string) error {
	return s.withProject(projectID, func(p Project) error {
		boardPath := filepath.Join(s.projectDir(p), "board.json")
		var board Board
		if err := s.readJSON(boardPath, &board); err == nil {
			for _, c := range BoardColumns {
				filtered := make([]string, 0)
				for _, id := range board[c] {
					if id != specID {
						filtered = append(filtered, id)
					}
				}
				board[c] = filtered
			}
			_ = s.writeJSON(boardPath, board)
		}
		return os.RemoveAll(filepath.Join(s.projectDir(p), "specs", specID))
	})
}

// ApproveForCoding moves a task from human_review → queue after human gate.
func (s *Store) ApproveForCoding(projectID, specID string) (Task, error) {
	t, err := s.GetTask(projectID, specID)
	if err != nil {
		return Task{}, err
	}
	if t.Status != "human_review" {
		return Task{}, fmt.Errorf("task must be in human_review (got %q)", t.Status)
	}
	moved, err := s.MoveTask(projectID, specID, "queue")
	if err != nil {
		return Task{}, err
	}
	_ = s.withProject(projectID, func(p Project) error {
		state := map[string]interface{}{
			"approved":    true,
			"approved_by": "user",
			"approved_at": nowUTC().Format(time.RFC3339),
		}
		return s.writeJSON(filepath.Join(s.projectDir(p), "specs", specID, "review_state.json"), state)
	})
	return moved, nil
}

func (s *Store) GetSpecMarkdown(projectID, specID string) (string, error) {
	var content string
	err := s.withProject(projectID, func(p Project) error {
		b, err := os.ReadFile(filepath.Join(s.projectDir(p), "specs", specID, "spec.md"))
		if err != nil {
			return err
		}
		content = string(b)
		return nil
	})
	return content, err
}

// TaskValidActions lists which run actions the board/detail UI may offer.
type TaskValidActions struct {
	Planning          bool `json:"planning"`
	Implementation    bool `json:"implementation"`
	Review            bool `json:"review"`
	QaFix             bool `json:"qaFix"`
	Approve           bool `json:"approve"`
	FollowupPlanning  bool `json:"followupPlanning"`
	GenerateChangelog bool `json:"generateChangelog"`
	Recover           bool `json:"recover"`
	MarkStuck         bool `json:"markStuck"`
	Pause             bool `json:"pause"`
	Resume            bool `json:"resume"`
	SkipToPhase       bool `json:"skipToPhase"`
}

func (s *Store) GetTaskValidActions(projectID, specID string) (TaskValidActions, error) {
	var out TaskValidActions
	t, err := s.GetTask(projectID, specID)
	if err != nil {
		return out, err
	}
	plan, _ := s.GetPlan(projectID, specID)
	prog, _ := s.GetProgress(projectID, specID)
	hasPlan := len(plan.Phases) > 0 || plan.PlanStatus != "" && plan.PlanStatus != "drafted"
	approved := true
	if t.RequireReviewBeforeCoding {
		approved = false
		_ = s.withProject(projectID, func(p Project) error {
			var st map[string]interface{}
			path := filepath.Join(s.projectDir(p), "specs", specID, "review_state.json")
			if err := s.readJSON(path, &st); err != nil {
				return nil
			}
			if v, ok := st["approved"].(bool); ok && v {
				approved = true
			}
			return nil
		})
	}
	status := t.Status
	paused := prog.Paused
	stuckCount := countStuckOrFailed(plan)
	out.Planning = !paused && (status == "backlog" || status == "queue" || status == "human_review")
	out.Implementation = !paused && (status == "queue" || status == "in_progress") && approved
	out.Review = !paused && (status == "in_progress" || status == "review")
	out.QaFix = !paused && (status == "review" || status == "in_progress")
	out.Approve = status == "human_review"
	out.FollowupPlanning = !paused && hasPlan && (status == "queue" || status == "in_progress" || status == "review" || status == "done")
	out.GenerateChangelog = true
	out.Recover = hasPlan && (stuckCount > 0 || prog.StuckSubtaskID != "" || status == "in_progress" || status == "review")
	out.MarkStuck = !paused && hasPlan && (status == "queue" || status == "in_progress" || status == "review") && stuckCount == 0
	out.Pause = !paused && hasPlan && (status == "queue" || status == "in_progress" || status == "review")
	out.Resume = paused
	out.SkipToPhase = !paused && hasPlan && len(plan.Phases) > 1 &&
		(status == "queue" || status == "in_progress" || status == "review")
	return out, nil
}

func countStuckOrFailed(plan ImplementationPlan) int {
	n := 0
	for _, ph := range plan.Phases {
		for _, st := range ph.Subtasks {
			if st.Status == "stuck" || st.Status == "failed" {
				n++
			}
		}
	}
	return n
}

func (s *Store) PutPlan(projectID, specID string, plan ImplementationPlan) error {
	return s.withProject(projectID, func(p Project) error {
		return s.writeJSON(filepath.Join(s.projectDir(p), "specs", specID, "implementation_plan.json"), plan)
	})
}

func (s *Store) PutProgress(projectID, specID string, prog TaskProgress) error {
	return s.withProject(projectID, func(p Project) error {
		return s.writeJSON(filepath.Join(s.projectDir(p), "specs", specID, "progress.json"), prog)
	})
}

func (s *Store) PutSpecMarkdown(projectID, specID, content string) error {
	return s.withProject(projectID, func(p Project) error {
		return os.WriteFile(filepath.Join(s.projectDir(p), "specs", specID, "spec.md"), []byte(content), 0o644)
	})
}

func (s *Store) PutSpecFile(projectID, specID, name, content string) error {
	name = filepath.Base(name)
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("invalid file name")
	}
	return s.withProject(projectID, func(p Project) error {
		return os.WriteFile(filepath.Join(s.projectDir(p), "specs", specID, name), []byte(content), 0o644)
	})
}

func (s *Store) PutChangelog(projectID, content string) error {
	return s.withProject(projectID, func(p Project) error {
		return os.WriteFile(filepath.Join(s.projectDir(p), "changelog.md"), []byte(content), 0o644)
	})
}

func (s *Store) AppendSpecLog(projectID, specID, phase, line string) error {
	return s.withProject(projectID, func(p Project) error {
		dir := filepath.Join(s.projectDir(p), "specs", specID, "logs")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		path := filepath.Join(dir, phase+".log")
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteString(line)
		return err
	})
}

func (s *Store) AppendProjectLog(projectID, name, line string) error {
	name = filepath.Base(name)
	return s.withProject(projectID, func(p Project) error {
		dir := filepath.Join(s.projectDir(p), "logs")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		path := filepath.Join(dir, name+".log")
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteString(line)
		return err
	})
}

func (s *Store) GetSpecLogs(projectID, specID string) (map[string]string, error) {
	out := map[string]string{}
	err := s.withProject(projectID, func(p Project) error {
		dir := filepath.Join(s.projectDir(p), "specs", specID, "logs")
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			key := strings.TrimSuffix(e.Name(), ".log")
			out[key] = string(b)
		}
		return nil
	})
	return out, err
}

func (s *Store) GetPlan(projectID, specID string) (ImplementationPlan, error) {
	var plan ImplementationPlan
	err := s.withProject(projectID, func(p Project) error {
		return s.readJSON(filepath.Join(s.projectDir(p), "specs", specID, "implementation_plan.json"), &plan)
	})
	return plan, err
}

func (s *Store) GetProgress(projectID, specID string) (TaskProgress, error) {
	var prog TaskProgress
	err := s.withProject(projectID, func(p Project) error {
		path := filepath.Join(s.projectDir(p), "specs", specID, "progress.json")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			prog = TaskProgress{}
			return nil
		}
		return s.readJSON(path, &prog)
	})
	return prog, err
}

func (s *Store) GetStatus(projectID string) (ProjectStatus, error) {
	var st ProjectStatus
	err := s.withProject(projectID, func(p Project) error {
		return s.readJSON(filepath.Join(s.projectDir(p), "status.json"), &st)
	})
	return st, err
}

func (s *Store) GetRoadmap(projectID string) (Roadmap, error) {
	var rm Roadmap
	err := s.withProject(projectID, func(p Project) error {
		return s.readJSON(filepath.Join(s.projectDir(p), "roadmap", "roadmap.json"), &rm)
	})
	return rm, err
}

func (s *Store) PutRoadmap(projectID string, rm Roadmap) error {
	return s.withProject(projectID, func(p Project) error {
		if rm.ID == "" {
			rm.ID = "roadmap-1"
		}
		return s.writeJSON(filepath.Join(s.projectDir(p), "roadmap", "roadmap.json"), rm)
	})
}

func (s *Store) GetIdeation(projectID string) (Ideation, error) {
	var ideas Ideation
	err := s.withProject(projectID, func(p Project) error {
		return s.readJSON(filepath.Join(s.projectDir(p), "ideation", "ideation.json"), &ideas)
	})
	if ideas == nil {
		ideas = emptyIdeation()
	}
	return ideas, err
}

func (s *Store) PutIdeation(projectID string, ideas Ideation) error {
	return s.withProject(projectID, func(p Project) error {
		return s.writeJSON(filepath.Join(s.projectDir(p), "ideation", "ideation.json"), ideas)
	})
}

func (s *Store) GetChangelog(projectID string) (string, error) {
	var content string
	err := s.withProject(projectID, func(p Project) error {
		b, err := os.ReadFile(filepath.Join(s.projectDir(p), "changelog.md"))
		if err != nil {
			return err
		}
		content = string(b)
		return nil
	})
	return content, err
}

func (s *Store) ListJobs(projectID string) ([]Job, error) {
	var jobs []Job
	err := s.withProject(projectID, func(p Project) error {
		dir := filepath.Join(s.projectDir(p), "runs")
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			var j Job
			if err := s.readJSON(filepath.Join(dir, e.Name()), &j); err != nil {
				continue
			}
			jobs = append(jobs, j)
		}
		return nil
	})
	if jobs == nil {
		jobs = []Job{}
	}
	return jobs, err
}

func (s *Store) GetJob(projectID, runID string) (Job, error) {
	var j Job
	err := s.withProject(projectID, func(p Project) error {
		return s.readJSON(filepath.Join(s.projectDir(p), "runs", runID+".json"), &j)
	})
	return j, err
}

func (s *Store) CreateJob(projectID, action, specID, runnerImage string) (Job, error) {
	var created Job
	err := s.withProject(projectID, func(p Project) error {
		now := nowUTC()
		runID := fmt.Sprintf("%d-%s", now.UnixMilli(), uuid.NewString())
		j := Job{
			RunID:       runID,
			ProjectID:   projectID,
			SpecID:      specID,
			Action:      action,
			State:       "queued",
			CreatedAt:   now,
			UpdatedAt:   now,
			Attempt:     1,
			RunnerImage: runnerImage,
		}
		if err := s.writeJSON(filepath.Join(s.projectDir(p), "runs", runID+".json"), j); err != nil {
			return err
		}
		st := idleStatus()
		st.Active = true
		st.Spec = specID
		st.State = mapActionToState(action)
		st.RunID = runID
		st.LastUpdate = now.Format(time.RFC3339)
		_ = s.writeJSON(filepath.Join(s.projectDir(p), "status.json"), st)
		created = j
		return nil
	})
	return created, err
}

func (s *Store) UpdateJob(projectID string, j Job) error {
	return s.withProject(projectID, func(p Project) error {
		j.UpdatedAt = nowUTC()
		return s.writeJSON(filepath.Join(s.projectDir(p), "runs", j.RunID+".json"), j)
	})
}

func mapActionToState(action string) string {
	switch action {
	case "run-planning", "run-followup-planning", "run-roadmap-discovery", "run-roadmap-features", "run-ideation":
		return "planning"
	case "run-implementation", "skip-to-phase":
		return "building"
	case "run-qa-fix", "run-review":
		return "qa"
	case "generate-changelog":
		return "planning"
	default:
		return "building"
	}
}
