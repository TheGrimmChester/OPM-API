// opm-runner — entrypoint binary for opm-runner-task containers.
// Invokes an OpenAI-compatible chat completions API when OPM_MODEL_API_KEY is set;
// otherwise emits an honest fallback result so the control plane can use builtins.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type runnerInput struct {
	Action      string `json:"action"`
	RunID       string `json:"runId"`
	SpecID      string `json:"specId"`
	ProjectID   string `json:"projectId"`
	TaskTitle   string `json:"taskTitle"`
	TaskDesc    string `json:"taskDescription"`
	PlanJSON    string `json:"planJson,omitempty"`
	SpecExcerpt string `json:"specExcerpt,omitempty"`
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
	}
	inPath := envOr("OPM_IN_FILE", "/in/input.json")
	if b, err := os.ReadFile(inPath); err == nil {
		_ = json.Unmarshal(b, &in)
	}
	return in
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
- run-planning / run-followup-planning: specMarkdown (string), plan (object: feature, workflow_type, phases[{phase,name,type,subtasks[{id,description,status,verification}]}])
- run-implementation: implementationNotes (string), completedSubtaskId (string, optional)
- run-review: reviewMarkdown (string), reviewPass (boolean)
Keep plans concrete and small (3 phases: planning, coding, review). Status values: pending|completed.`

	user = fmt.Sprintf("action=%s\nrunId=%s\nspecId=%s\ntitle=%s\ndescription=%s\n",
		in.Action, in.RunID, in.SpecID, in.TaskTitle, in.TaskDesc)
	if in.SpecExcerpt != "" {
		user += "\n--- existing spec ---\n" + truncate(in.SpecExcerpt, 4000) + "\n"
	}
	if in.PlanJSON != "" {
		user += "\n--- existing plan json ---\n" + truncate(in.PlanJSON, 6000) + "\n"
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
	}
	return res
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
