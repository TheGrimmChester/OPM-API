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

	// Ideas is model-produced ideation keyed by type (run-ideation).
	Ideas Ideation `json:"ideas,omitempty"`

	// Roadmap discovery / features (returned as JSON to the control plane).
	Vision         string           `json:"vision,omitempty"`
	TargetAudience string           `json:"targetAudience,omitempty"`
	Phases         []RoadmapPhase   `json:"phases,omitempty"`
	Features       []RoadmapFeature `json:"features,omitempty"`
	// DiscoveryBlob is the richer discovery object stashed under roadmap.metadata["discovery"].
	DiscoveryBlob map[string]any `json:"discovery,omitempty"`

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

// modelAPIKeyPresent reports whether a model-backed run is possible.
//
// With OAM configured this is true because the key is resolved per job from the
// acting user's org — there is no single process-wide key to inspect, and this
// function cannot know whether a *particular* org has one without resolving. The
// spawn probe therefore reports availability, not a guarantee; a job with no
// credential for its org fails closed at resolve time with credential_unavailable.
//
// Getting this wrong is user-visible: the probe is what people read to decide
// whether the model path is live, and before OAM it was the only signal. A bare
// env check here would report "unset — fallback" on a correctly configured OAM
// deployment.
func modelAPIKeyPresent() bool {
	if oamConfigured() {
		return true
	}
	return strings.TrimSpace(envOr("OPM_MODEL_API_KEY", "")) != "" ||
		strings.TrimSpace(envOr("CURSOR_API_KEY", "")) != ""
}

func modelBaseURL() string {
	return strings.TrimRight(envOr("OPM_MODEL_BASE_URL", "https://api.openai.com/v1"), "/")
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
	default:
		return envOr("OPM_MODEL", "auto")
	}
}

func modelConfigHonesty() string {
	if oamConfigured() {
		return "models and API keys resolve per job from OAM (PEER_OAM_URL), scoped to the acting user's org " +
			"and their per-agent binding; a job whose org has no credential fails closed with credential_unavailable. " +
			"OPM_MODEL* environment variables no longer participate."
	}
	if !modelAPIKeyPresent() {
		return "OPM_MODEL_API_KEY/CURSOR_API_KEY unset — runner reports fallback; control plane uses builtin artifact helpers."
	}
	if strings.EqualFold(strings.TrimSpace(envOr("OPM_MODEL_PROVIDER", "")), "openai") {
		return "model key set; runner will call " + modelBaseURL() + "/chat/completions (OpenAI-compatible). Default model is auto unless OPM_MODEL* overrides."
	}
	return "model key set; runner will invoke Cursor agent CLI (default --model auto). Set OPM_MODEL_PROVIDER=openai for OpenAI-compatible chat completions."
}
