package main

import (
	"encoding/json"
	"strings"
)

// RunnerResult is the JSON contract between opm-runner-task and the control plane.
// Written to /out/result.json (and echoed on stdout) by the runner binary.
type RunnerResult struct {
	Mode   string `json:"mode"` // "model" | "fallback"
	Reason string `json:"reason,omitempty"`
	Action string `json:"action,omitempty"`
	Model  string `json:"model,omitempty"`

	SpecMarkdown string              `json:"specMarkdown,omitempty"`
	Plan         *ImplementationPlan `json:"plan,omitempty"`

	ImplementationNotes string `json:"implementationNotes,omitempty"`
	CompletedSubtaskID  string `json:"completedSubtaskId,omitempty"`

	// Files are the source changes the runner produced against the mounted repo,
	// and CommitMessage the message to commit them under. They are the only thing
	// that can become a commit — notes and excerpts never are.
	Files         []FileChange `json:"files,omitempty"`
	CommitMessage string       `json:"commitMessage,omitempty"`

	ReviewMarkdown string `json:"reviewMarkdown,omitempty"`
	ReviewPass     *bool  `json:"reviewPass,omitempty"`

	RawExcerpt string `json:"rawExcerpt,omitempty"`
}

func parseRunnerResultJSON(raw []byte) (RunnerResult, bool) {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return RunnerResult{}, false
	}
	try := func(b []byte) (RunnerResult, bool) {
		var r RunnerResult
		if err := json.Unmarshal(b, &r); err != nil {
			return RunnerResult{}, false
		}
		r.Mode = strings.ToLower(strings.TrimSpace(r.Mode))
		if r.Mode == "" {
			return RunnerResult{}, false
		}
		return r, true
	}
	if rr, ok := try([]byte(s)); ok {
		return rr, true
	}
	// Banner lines before JSON — take from the first object brace.
	if i := strings.Index(s, "{"); i >= 0 {
		return try([]byte(s[i:]))
	}
	return RunnerResult{}, false
}

func modelAPIKeyPresent() bool {
	return strings.TrimSpace(envOr("OPM_MODEL_API_KEY", "")) != ""
}

func modelBaseURL() string {
	return strings.TrimRight(envOr("OPM_MODEL_BASE_URL", "https://api.openai.com/v1"), "/")
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

func modelConfigHonesty() string {
	if !modelAPIKeyPresent() {
		return "OPM_MODEL_API_KEY unset — runner reports fallback; control plane uses builtin artifact helpers."
	}
	return "OPM_MODEL_API_KEY set; runner will call " + modelBaseURL() + "/chat/completions (OpenAI-compatible). Set OPM_MODEL / OPM_MODEL_PLANNING|CODING|REVIEW as needed."
}
