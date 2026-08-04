// opm-runner — entrypoint binary for opm-runner-task containers.
// Invokes an OpenAI-compatible chat completions API when OPM_MODEL_API_KEY is set;
// otherwise emits an honest fallback result so the control plane can use builtins.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type runnerInput struct {
	Action        string `json:"action"`
	RunID         string `json:"runId"`
	SpecID        string `json:"specId"`
	ProjectID     string `json:"projectId"`
	TaskTitle     string `json:"taskTitle"`
	TaskDesc      string `json:"taskDescription"`
	PlanJSON      string `json:"planJson,omitempty"`
	SpecExcerpt   string `json:"specExcerpt,omitempty"`
	OwnerRepo     string `json:"ownerRepo,omitempty"`
	DefaultBranch string `json:"defaultBranch,omitempty"`
	// RepoDir is where the control plane mounted the prepared clone, read-only.
	// Empty means no repository was available for this run.
	RepoDir string `json:"repoDir,omitempty"`
}

// fileChange is one source change the runner proposes. Exactly one of contents,
// patch or delete carries the change; the control plane applies it in the job
// workspace and commits only what it could apply.
type fileChange struct {
	Path     string  `json:"path"`
	Contents *string `json:"contents,omitempty"`
	Patch    string  `json:"patch,omitempty"`
	Delete   bool    `json:"delete,omitempty"`
}

type runnerResult struct {
	Mode   string `json:"mode"`
	Reason string `json:"reason,omitempty"`
	Action string `json:"action,omitempty"`
	Model  string `json:"model,omitempty"`

	SpecMarkdown string          `json:"specMarkdown,omitempty"`
	Plan         json.RawMessage `json:"plan,omitempty"`

	ImplementationNotes string `json:"implementationNotes,omitempty"`
	CompletedSubtaskID  string `json:"completedSubtaskId,omitempty"`

	// Files and CommitMessage are the deliverable output of an implementation run.
	Files         []fileChange `json:"files,omitempty"`
	CommitMessage string       `json:"commitMessage,omitempty"`

	ReviewMarkdown string `json:"reviewMarkdown,omitempty"`
	ReviewPass     *bool  `json:"reviewPass,omitempty"`

	RawExcerpt string `json:"rawExcerpt,omitempty"`
}

type chatReq struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func main() {
	in := loadInput()
	outDir := envOr("OPM_OUT_DIR", "/out")
	_ = os.MkdirAll(outDir, 0o755)

	key := strings.TrimSpace(os.Getenv("OPM_MODEL_API_KEY"))
	if key == "" {
		emit(outDir, runnerResult{
			Mode:   "fallback",
			Reason: "OPM_MODEL_API_KEY not set; control plane will use builtin artifact helpers",
			Action: in.Action,
		})
		return
	}

	base := strings.TrimRight(envOr("OPM_MODEL_BASE_URL", "https://api.openai.com/v1"), "/")
	model := modelForAction(in.Action)
	system, user := promptsFor(in)

	content, err := callChat(base, key, model, system, user)
	if err != nil {
		emit(outDir, runnerResult{
			Mode:   "fallback",
			Reason: "model call failed: " + err.Error(),
			Action: in.Action,
			Model:  model,
		})
		return
	}

	emit(outDir, parseModelContent(in.Action, model, content))
}

func loadInput() runnerInput {
	in := runnerInput{
		Action:    envOr("OPM_ACTION", "none"),
		RunID:     envOr("OPM_RUN_ID", ""),
		SpecID:    envOr("OPM_SPEC_ID", ""),
		ProjectID: envOr("OPM_PROJECT", ""),
		TaskTitle: envOr("OPM_TASK_TITLE", ""),
		TaskDesc:  envOr("OPM_TASK_DESCRIPTION", ""),
		RepoDir:   envOr("OPM_REPO_DIR", ""),
	}
	inPath := envOr("OPM_IN_FILE", "/in/input.json")
	if b, err := os.ReadFile(inPath); err == nil {
		_ = json.Unmarshal(b, &in)
	}
	if in.RepoDir == "" {
		in.RepoDir = envOr("OPM_REPO_DIR", "")
	}
	return in
}

// repoInventoryLimits bound how much of the mounted tree is described to the
// model, so a large repository cannot blow the prompt budget.
const (
	maxInventoryFiles   = 400
	maxInventoryBytes   = 6000
	maxExcerptFiles     = 8
	maxExcerptFileBytes = 4000
)

// skippedDir is true for directories that never carry reviewable source.
func skippedDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "build", "target",
		".venv", "venv", "__pycache__", ".next", ".cache", "coverage":
		return true
	}
	return false
}

// textLikeFile is true for extensions the runner may read and rewrite. Binaries
// are deliberately excluded: the change contract carries text, not blobs.
func textLikeFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go", ".js", ".jsx", ".ts", ".tsx", ".py", ".rb", ".php", ".rs", ".java",
		".kt", ".c", ".h", ".cc", ".cpp", ".hpp", ".cs", ".sh", ".sql",
		".md", ".txt", ".json", ".yaml", ".yml", ".toml", ".ini", ".css", ".html":
		return true
	}
	switch name {
	case "Dockerfile", "Makefile", "LICENSE", "README":
		return true
	}
	return false
}

// repoInventory lists the tracked-looking source paths under root, newest first
// is not attempted — a stable lexical order keeps prompts reproducible.
func repoInventory(root string) []string {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	var out []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && skippedDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if len(out) >= maxInventoryFiles {
			return fs.SkipAll
		}
		if !textLikeFile(d.Name()) {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out)
	return out
}

// repoExcerpts reads a handful of files the plan points at, so the model edits
// real source instead of guessing at it.
func repoExcerpts(root string, wanted []string) map[string]string {
	out := map[string]string{}
	if strings.TrimSpace(root) == "" {
		return out
	}
	for _, rel := range wanted {
		if len(out) >= maxExcerptFiles {
			break
		}
		rel = strings.TrimSpace(strings.TrimPrefix(filepath.ToSlash(rel), "./"))
		if rel == "" || strings.Contains(rel, "..") || filepath.IsAbs(rel) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		out[rel] = truncate(string(b), maxExcerptFileBytes)
	}
	return out
}

// planFilesToModify pulls files_to_modify hints out of the existing plan so the
// excerpts shown to the model are the ones the plan actually names.
func planFilesToModify(planJSON string) []string {
	if strings.TrimSpace(planJSON) == "" {
		return nil
	}
	var plan struct {
		Phases []struct {
			Subtasks []struct {
				FilesToModify []string `json:"files_to_modify"`
			} `json:"subtasks"`
		} `json:"phases"`
	}
	if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, ph := range plan.Phases {
		for _, st := range ph.Subtasks {
			for _, f := range st.FilesToModify {
				f = strings.TrimSpace(f)
				if f == "" || seen[f] {
					continue
				}
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

func modelForAction(action string) string {
	switch action {
	case "run-planning", "run-followup-planning":
		if m := envOr("OPM_MODEL_PLANNING", ""); m != "" {
			return m
		}
	case "run-implementation":
		if m := envOr("OPM_MODEL_CODING", ""); m != "" {
			return m
		}
	case "run-review":
		if m := envOr("OPM_MODEL_REVIEW", ""); m != "" {
			return m
		}
	}
	return envOr("OPM_MODEL", "gpt-4o-mini")
}

func promptsFor(in runnerInput) (system, user string) {
	system = `You are the OPM task-automation agent. Reply with a single JSON object only (no markdown fences).
Keys by action:
- run-planning / run-followup-planning: specMarkdown (string), plan (object: feature, workflow_type, phases[{phase,name,type,subtasks[{id,description,status,verification,files_to_modify}]}])
- run-implementation: files (array), commitMessage (string), implementationNotes (string), completedSubtaskId (string, optional)
- run-review: reviewMarkdown (string), reviewPass (boolean)

For run-implementation, "files" is the actual source change and the only thing that becomes a commit:
  [{"path":"relative/path.go","contents":"<complete new file body>"}]
  [{"path":"relative/path.go","patch":"<unified diff with correct context>"}]
  [{"path":"relative/path.go","delete":true}]
Rules for files:
- paths are repository-relative, forward slashes, never absolute and never containing ".."
- never touch .git/ or .github/workflows/
- prefer "contents" for new or small files; use "patch" only when the diff context is exact
- return an empty files array when you cannot make a correct change — do not invent one
- commitMessage is a plain subject line, optionally a blank line and a short body

Keep plans concrete and small (3 phases: planning, coding, review). Status values: pending|completed.`

	user = fmt.Sprintf("action=%s\nrunId=%s\nspecId=%s\ntitle=%s\ndescription=%s\n",
		in.Action, in.RunID, in.SpecID, in.TaskTitle, in.TaskDesc)
	if in.OwnerRepo != "" {
		base := strings.TrimSpace(in.DefaultBranch)
		if base == "" {
			base = "main"
		}
		user += fmt.Sprintf("repository=%s\nbaseBranch=%s\n", in.OwnerRepo, base)
	}
	if in.SpecExcerpt != "" {
		user += "\n--- existing spec ---\n" + truncate(in.SpecExcerpt, 4000) + "\n"
	}
	if in.PlanJSON != "" {
		user += "\n--- existing plan json ---\n" + truncate(in.PlanJSON, 6000) + "\n"
	}

	if in.RepoDir == "" {
		user += "\nNo repository was mounted for this run: you cannot see the source. " +
			"Return an empty files array and say so in implementationNotes.\n"
		return system, user
	}
	inv := repoInventory(in.RepoDir)
	if len(inv) == 0 {
		user += "\nThe mounted repository at " + in.RepoDir + " contains no readable source files.\n"
		return system, user
	}
	user += "\n--- repository files (" + fmt.Sprint(len(inv)) + " shown) ---\n" +
		truncate(strings.Join(inv, "\n"), maxInventoryBytes) + "\n"

	wanted := planFilesToModify(in.PlanJSON)
	if len(wanted) == 0 {
		wanted = inv
	}
	for rel, body := range repoExcerpts(in.RepoDir, wanted) {
		user += "\n--- file " + rel + " ---\n" + body + "\n"
	}
	return system, user
}

func callChat(base, key, model, system, user string) (string, error) {
	body, _ := json.Marshal(chatReq{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Temperature: 0.2,
	})
	url := base + "/chat/completions"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(raw), 240))
	}
	var cr chatResp
	if err := json.Unmarshal(raw, &cr); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if cr.Error != nil && cr.Error.Message != "" {
		return "", fmt.Errorf("%s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("empty choices")
	}
	return strings.TrimSpace(cr.Choices[0].Message.Content), nil
}

func parseModelContent(action, model, content string) runnerResult {
	content = stripFences(content)
	res := runnerResult{
		Mode:       "model",
		Action:     action,
		Model:      model,
		RawExcerpt: truncate(content, 800),
	}
	var blob map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &blob); err != nil {
		res.Mode = "fallback"
		res.Reason = "model returned non-JSON; control plane will use builtin helpers"
		return res
	}
	if v, ok := blob["specMarkdown"]; ok {
		_ = json.Unmarshal(v, &res.SpecMarkdown)
	}
	if v, ok := blob["plan"]; ok {
		res.Plan = v
	}
	if v, ok := blob["implementationNotes"]; ok {
		_ = json.Unmarshal(v, &res.ImplementationNotes)
	}
	if v, ok := blob["completedSubtaskId"]; ok {
		_ = json.Unmarshal(v, &res.CompletedSubtaskID)
	}
	if v, ok := blob["reviewMarkdown"]; ok {
		_ = json.Unmarshal(v, &res.ReviewMarkdown)
	}
	if v, ok := blob["reviewPass"]; ok {
		var b bool
		if json.Unmarshal(v, &b) == nil {
			res.ReviewPass = &b
		}
	}
	if v, ok := blob["files"]; ok {
		var files []fileChange
		if json.Unmarshal(v, &files) == nil {
			res.Files = sanitizeFileChanges(files)
		}
	}
	if v, ok := blob["commitMessage"]; ok {
		_ = json.Unmarshal(v, &res.CommitMessage)
	}
	switch action {
	case "run-planning", "run-followup-planning":
		if strings.TrimSpace(res.SpecMarkdown) == "" && len(res.Plan) == 0 {
			res.Mode = "fallback"
			res.Reason = "model JSON missing specMarkdown and plan"
		}
	case "run-review":
		if strings.TrimSpace(res.ReviewMarkdown) == "" && res.ReviewPass == nil {
			res.Mode = "fallback"
			res.Reason = "model JSON missing reviewMarkdown and reviewPass"
		}
	case "run-implementation":
		// An implementation run that produced no files stays mode=model — the plan
		// and notes are still real output — but says plainly that nothing is
		// deliverable, so the control plane never presents it as shipped code.
		if len(res.Files) == 0 {
			res.Reason = "model returned no file changes; nothing to deliver"
		}
	}
	return res
}

// sanitizeFileChanges drops entries the control plane would refuse anyway, so the
// recorded change set does not carry known-bad paths. The control plane validates
// again against the real workspace — this is a first pass, not the only one.
func sanitizeFileChanges(files []fileChange) []fileChange {
	out := make([]fileChange, 0, len(files))
	for _, f := range files {
		f.Path = strings.TrimSpace(strings.TrimPrefix(filepath.ToSlash(f.Path), "./"))
		if f.Path == "" || filepath.IsAbs(f.Path) || strings.Contains(f.Path, "..") {
			continue
		}
		if strings.HasPrefix(f.Path, ".git/") || f.Path == ".git" ||
			strings.HasPrefix(f.Path, ".github/workflows/") {
			continue
		}
		if !f.Delete && f.Patch == "" && f.Contents == nil {
			continue
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func emit(outDir string, res runnerResult) {
	b, _ := json.MarshalIndent(res, "", "  ")
	path := filepath.Join(outDir, "result.json")
	_ = os.WriteFile(path, b, 0o644)
	fmt.Println(string(b))
}

func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```JSON")
		s = strings.TrimPrefix(s, "```")
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	if i := strings.Index(s, "{"); i > 0 {
		s = s[i:]
	}
	return strings.TrimSpace(s)
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
