// opm-runner — entrypoint binary for opm-runner-task containers.
// Default model path: Cursor Agent CLI (`agent -p --model auto`) when
// OPM_MODEL_API_KEY or CURSOR_API_KEY is set. Set OPM_MODEL_PROVIDER=openai
// to use OpenAI-compatible /chat/completions instead. Without a key, emits
// an honest fallback so the control plane can use builtins.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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
	ProjectName   string `json:"projectName,omitempty"`
	IdeationType  string `json:"ideationType,omitempty"`
	// Competitors and AudienceNotes drive the roadmap pipeline. Named competitors
	// are analysed exactly as given — see competitorPrompts for why inventing them
	// is worse than describing a category.
	Competitors   []string `json:"competitors,omitempty"`
	AudienceNotes string   `json:"audienceNotes,omitempty"`
	// ExistingIdeaTitles is a newline-joined list of titles already on the board
	// so the model proposes net-new ideas instead of duplicates.
	ExistingIdeaTitles string `json:"existingIdeaTitles,omitempty"`
	TaskTitles         string `json:"taskTitles,omitempty"`
	CloneHonestyNote   string `json:"cloneHonestyNote,omitempty"`
	// RepoDir is where the control plane mounted the prepared clone, read-only.
	// Empty means no repository was available for this run.
	RepoDir string `json:"repoDir,omitempty"`
	// Context is the nested per-action pack from the control plane.
	Context map[string]any `json:"context,omitempty"`
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

	// Which endpoint actually served this run, and at which position in the
	// candidate list. After a failover the endpoint that ran is not the one that
	// was offered first, so recording only the model leaves "why did this cost
	// what it cost" unanswerable.
	Endpoint     string `json:"endpoint,omitempty"`
	EndpointKind string `json:"endpointKind,omitempty"`
	Attempt      int    `json:"attempt,omitempty"`

	SpecMarkdown string          `json:"specMarkdown,omitempty"`
	Plan         json.RawMessage `json:"plan,omitempty"`

	ImplementationNotes string `json:"implementationNotes,omitempty"`
	CompletedSubtaskID  string `json:"completedSubtaskId,omitempty"`

	// Files and CommitMessage are the deliverable output of an implementation run.
	Files         []fileChange `json:"files,omitempty"`
	CommitMessage string       `json:"commitMessage,omitempty"`

	ReviewMarkdown string `json:"reviewMarkdown,omitempty"`
	ReviewPass     *bool  `json:"reviewPass,omitempty"`

	// Ideas is model-produced ideation keyed by type (run-ideation).
	Ideas map[string][]ideaOut `json:"ideas,omitempty"`

	// Competitor is the run-roadmap-competitor artifact. Raw rather than typed
	// because the control plane owns its shape, and a struct here would silently
	// drop any field the model returned that this build does not know about.
	Competitor json.RawMessage `json:"competitor,omitempty"`

	Vision         string          `json:"vision,omitempty"`
	TargetAudience string          `json:"targetAudience,omitempty"`
	Phases         json.RawMessage `json:"phases,omitempty"`
	Features       json.RawMessage `json:"features,omitempty"`
	Discovery      json.RawMessage `json:"discovery,omitempty"`

	RawExcerpt string `json:"rawExcerpt,omitempty"`
}

type ideaOut struct {
	ID                     string   `json:"id,omitempty"`
	Type                   string   `json:"type,omitempty"`
	Title                  string   `json:"title"`
	Description            string   `json:"description,omitempty"`
	Rationale              string   `json:"rationale,omitempty"`
	EstimatedEffort        string   `json:"estimated_effort,omitempty"`
	AffectedFiles          []string `json:"affected_files,omitempty"`
	ImplementationApproach string   `json:"implementation_approach,omitempty"`
	Status                 string   `json:"status,omitempty"`
	CreatedAt              string   `json:"created_at,omitempty"`
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

	// The ordered endpoint list OAM resolved for this job, or a single candidate
	// synthesised from the legacy OPM_MODEL*/CURSOR_API_KEY environment when the
	// control plane did not supply one.
	cands := loadCandidates()
	if len(cands) == 0 {
		emit(outDir, runnerResult{
			Mode:   "fallback",
			Reason: "no model credential resolved; control plane will use builtin artifact helpers",
			Action: in.Action,
		})
		return
	}

	model := modelForAction(in.Action)
	// The model comes from the resolved candidate when there is one: OAM resolved
	// it per agent for the acting user, and modelForAction is the legacy
	// deployment-wide fallback. Preferring the env here would reintroduce exactly
	// the global-variable behaviour the binding replaced.
	if m := strings.TrimSpace(cands[0].Model); m != "" {
		model = m
	}
	system, user := promptsFor(in)

	content, used, err := callWithFailover(cands, system, user)
	if err != nil {
		emit(outDir, runnerResult{
			Mode:   "fallback",
			Reason: "model call failed: " + err.Error(),
			Action: in.Action,
			Model:  model,
		})
		return
	}
	// Report which endpoint actually served the job, not which one was offered
	// first. After a failover those differ, and "which model ran this" is
	// unanswerable otherwise.
	if m := strings.TrimSpace(used.Model); m != "" {
		model = m
	}
	res := parseModelContent(in.Action, model, content)
	res.Endpoint = used.name()
	res.EndpointKind = used.Kind
	res.Attempt = used.Index
	emit(outDir, res)
}

func modelAPIKey() string {
	if k := strings.TrimSpace(os.Getenv("OPM_MODEL_API_KEY")); k != "" {
		return k
	}
	return strings.TrimSpace(os.Getenv("CURSOR_API_KEY"))
}

func useOpenAIProvider() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("OPM_MODEL_PROVIDER")), "openai")
}

func callModel(key, model, system, user string) (string, error) {
	if useOpenAIProvider() {
		base := strings.TrimRight(envOr("OPM_MODEL_BASE_URL", "https://api.openai.com/v1"), "/")
		return callChat(base, key, model, system, user)
	}
	return callCursorAgent(key, model, system, user)
}

func callCursorAgent(key, model, system, user string) (string, error) {
	bin := envOr("OPM_CURSOR_AGENT_BIN", "agent")
	if _, err := exec.LookPath(bin); err != nil {
		return "", fmt.Errorf("cursor agent binary %q not found in PATH (rebuild opm-runner-task with agent CLI)", bin)
	}
	prompt := system + "\n\n" + user
	args := []string{
		"-p", "--trust", "--approve-mcps",
		"--output-format", "text",
		"--mode", "ask",
		"--model", model,
	}
	// Prefer workspace = mounted repo when present (parity with agent cwd=/repo).
	repoDir := strings.TrimSpace(os.Getenv("OPM_REPO_DIR"))
	if repoDir == "" {
		if b, err := os.ReadFile(envOr("OPM_IN_FILE", "/in/input.json")); err == nil {
			var tip struct {
				RepoDir string `json:"repoDir"`
			}
			_ = json.Unmarshal(b, &tip)
			repoDir = tip.RepoDir
		}
	}
	if repoDir != "" {
		if st, err := os.Stat(repoDir); err == nil && st.IsDir() {
			args = append(args, "--workspace", repoDir)
		}
	}
	args = append(args, prompt)
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "CURSOR_API_KEY="+key)
	if repoDir != "" {
		if st, err := os.Stat(repoDir); err == nil && st.IsDir() {
			cmd.Dir = repoDir
		}
	}
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	text = strings.ReplaceAll(text, key, "***")
	if err != nil {
		return "", fmt.Errorf("cursor agent: %w (%s)", err, truncate(text, 240))
	}
	if text == "" {
		return "", fmt.Errorf("cursor agent returned empty output")
	}
	return text, nil
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
		".kt", ".kts", ".c", ".h", ".cc", ".cpp", ".hpp", ".cs", ".sh", ".sql",
		".md", ".txt", ".json", ".yaml", ".yml", ".toml", ".ini", ".css", ".html", ".xml", ".gradle":
		return true
	}
	switch name {
	case "Dockerfile", "Makefile", "LICENSE", "README", "AndroidManifest.xml":
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

func phaseModel(phaseKey string) string {
	return envOr(phaseKey, envOr("OPM_MODEL", "auto"))
}

func modelForAction(action string) string {
	switch action {
	case "run-planning", "run-followup-planning":
		return phaseModel("OPM_MODEL_PLANNING")
	case "run-implementation":
		return phaseModel("OPM_MODEL_CODING")
	case "run-review":
		return phaseModel("OPM_MODEL_REVIEW")
	case "run-ideation":
		return phaseModel("OPM_MODEL_IDEATION")
	case "run-roadmap-discovery":
		return phaseModel("OPM_MODEL_ROADMAP_DISCOVERY")
	case "run-roadmap-features":
		return phaseModel("OPM_MODEL_ROADMAP_FEATURES")
	case "run-roadmap-competitor":
		return phaseModel("OPM_MODEL_ROADMAP_COMPETITOR")
	default:
		return envOr("OPM_MODEL", "auto")
	}
}

// promptsFor lives in prompts.go (per-action system contracts).

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
	// The whole object IS the competitor artifact for that action: the prompt asks
	// for {competitors, market_gaps, differentiators} at the top level, not nested
	// under a key.
	if action == "run-roadmap-competitor" {
		if _, hasCompetitors := blob["competitors"]; hasCompetitors {
			res.Competitor = json.RawMessage(content)
		}
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
	if v, ok := blob["ideas"]; ok {
		var ideas map[string][]ideaOut
		if json.Unmarshal(v, &ideas) == nil {
			res.Ideas = normalizeIdeasKeys(ideas)
		}
	}
	if v, ok := blob["vision"]; ok {
		_ = json.Unmarshal(v, &res.Vision)
	}
	if v, ok := blob["targetAudience"]; ok {
		_ = json.Unmarshal(v, &res.TargetAudience)
	}
	// Accept snake_case aliases from AutoCursor-shaped output.
	if res.TargetAudience == "" {
		if v, ok := blob["target_audience"]; ok {
			var raw json.RawMessage
			if json.Unmarshal(v, &raw) == nil {
				var s string
				if json.Unmarshal(raw, &s) == nil {
					res.TargetAudience = s
				} else {
					res.TargetAudience = compactJSONAudience(raw)
				}
			}
		}
	}
	if v, ok := blob["phases"]; ok {
		res.Phases = v
	}
	if v, ok := blob["features"]; ok {
		res.Features = v
	}
	if v, ok := blob["discovery"]; ok {
		res.Discovery = v
	}
	// product_vision.one_liner → vision when vision missing
	if strings.TrimSpace(res.Vision) == "" {
		if v, ok := blob["product_vision"]; ok {
			var pv map[string]any
			if json.Unmarshal(v, &pv) == nil {
				if ol, ok := pv["one_liner"].(string); ok {
					res.Vision = strings.TrimSpace(ol)
				}
			}
		}
		if strings.TrimSpace(res.Vision) == "" && len(res.Discovery) > 0 {
			var disc map[string]any
			if json.Unmarshal(res.Discovery, &disc) == nil {
				if pv, ok := disc["product_vision"].(map[string]any); ok {
					if ol, ok := pv["one_liner"].(string); ok {
						res.Vision = strings.TrimSpace(ol)
					}
				}
			}
		}
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
	case "run-ideation":
		if len(res.Ideas) == 0 {
			res.Mode = "fallback"
			res.Reason = "model JSON missing ideas"
		}
	case "run-roadmap-discovery":
		if strings.TrimSpace(res.Vision) == "" && len(res.Phases) == 0 && len(res.Discovery) == 0 {
			res.Mode = "fallback"
			res.Reason = "model JSON missing vision/phases/discovery"
		}
	case "run-roadmap-features":
		if len(res.Features) == 0 && strings.TrimSpace(res.Vision) == "" && len(res.Phases) == 0 {
			res.Mode = "fallback"
			res.Reason = "model JSON missing features (and no discovery bootstrap)"
		}
	case "run-roadmap-competitor":
		// An empty competitor object is a miss, not a finding: it would otherwise
		// flow into discovery as though the analysis had concluded there are no
		// competitors.
		if len(res.Competitor) == 0 {
			res.Mode = "fallback"
			res.Reason = "model JSON missing competitor analysis"
		}
	}
	return res
}

func normalizeIdeasKeys(ideas map[string][]ideaOut) map[string][]ideaOut {
	if len(ideas) == 0 {
		return ideas
	}
	aliases := map[string]string{
		"security_hardening": "security",
		"code-improvements":  "code_improvements",
		"code-quality":       "code_quality",
		"ui-ux":              "ui_ux",
		"uiux":               "ui_ux",
	}
	out := map[string][]ideaOut{}
	for k, v := range ideas {
		key := strings.TrimSpace(k)
		if alt, ok := aliases[key]; ok {
			key = alt
		}
		key = strings.ReplaceAll(key, "-", "_")
		out[key] = append(out[key], v...)
	}
	return out
}

func compactJSONAudience(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return strings.TrimSpace(string(raw))
	}
	parts := []string{}
	if p, ok := m["primary_persona"].(string); ok && strings.TrimSpace(p) != "" {
		parts = append(parts, strings.TrimSpace(p))
	} else if p, ok := m["primary"].(string); ok && strings.TrimSpace(p) != "" {
		parts = append(parts, strings.TrimSpace(p))
	}
	if u, ok := m["usage_context"].(string); ok && strings.TrimSpace(u) != "" {
		parts = append(parts, strings.TrimSpace(u))
	}
	return strings.Join(parts, " — ")
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

// envInt reads an integer env var, returning def when unset or unparseable.
func envInt(k string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(k)))
	if err != nil {
		return def
	}
	return n
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
