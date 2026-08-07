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

	// Competitor is the run-roadmap-competitor artifact, stored under
	// roadmap.metadata["competitor_analysis"] and read back as an input by
	// discovery and features. Raw so a field this build does not know about
	// survives the round trip instead of being dropped.
	Competitor json.RawMessage `json:"competitor,omitempty"`

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
// Keys resolve per job from OAM — there is no process-wide host key. The spawn
// probe therefore reports availability when PEER_OAM_URL is set; a job whose org
// has no credential fails closed at resolve time with credential_unavailable.
func modelAPIKeyPresent() bool {
	return oamConfigured()
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
			"Host OPM_MODEL*/CURSOR_API_KEY are not used as credentials."
	}
	return "PEER_OAM_URL unset — model-backed jobs are refused until OAM is configured. " +
		"Legacy deployment-wide OPM_MODEL*/CURSOR_API_KEY credentials have been removed."
}
