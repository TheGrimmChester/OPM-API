package main

import (
	"context"
	"strings"
	"testing"
)

// agentKeyForAction is the mapping OAM deliberately refuses to guess. If these
// drift from modelForAction's switch, a phase silently resolves another phase's
// binding.
func TestAgentKeyForAction(t *testing.T) {
	cases := map[string]string{
		"run-planning":          agentKeyPlanning,
		"run-followup-planning": agentKeyPlanning, // both are the planning phase
		"run-implementation":    agentKeyCoding,   // NOT "implementation"
		"run-review":            agentKeyReview,
		"run-qa-fix":            agentKeyQAFix,
		"run-ideation":          agentKeyIdeation,
		"run-roadmap-discovery": agentKeyRoadmapDiscovery,
		"run-roadmap-features":  agentKeyRoadmapFeatures,
	}
	for action, want := range cases {
		if got := agentKeyForAction(action); got != want {
			t.Fatalf("agentKeyForAction(%q)=%q want %q", action, got, want)
		}
	}
	// An action with no AI phase resolves nothing, rather than acquiring a
	// binding (and a credential) nobody configured for it.
	for _, action := range []string{"deliver", "skip-to-phase", "", "sync-board"} {
		if got := agentKeyForAction(action); got != "" {
			t.Fatalf("agentKeyForAction(%q)=%q want empty", action, got)
		}
	}
}

// The mapping in agent_keys.go must stay in step with modelForAction, which is
// the same taxonomy seen from the legacy env side. This test fails if someone
// adds a phase to one and not the other.
func TestAgentKeysCoverEveryModelPhase(t *testing.T) {
	phaseActions := []string{
		"run-planning", "run-followup-planning", "run-implementation",
		"run-review", "run-ideation", "run-roadmap-discovery", "run-roadmap-features",
	}
	for _, action := range phaseActions {
		if agentKeyForAction(action) == "" {
			t.Fatalf("action %q has a model phase in modelForAction but no agent key", action)
		}
	}
	// Every published catalog entry must be reachable from some action, or the
	// console would offer a picker for an agent that never runs.
	reachable := map[string]bool{}
	for _, action := range append(phaseActions, "run-qa-fix") {
		reachable[agentKeyForAction(action)] = true
	}
	for _, e := range agentCatalog() {
		if !reachable[e.AgentKey] {
			t.Fatalf("catalog advertises %q but no action maps to it", e.AgentKey)
		}
	}
}

// With PEER_OAM_URL unset the legacy environment path must be unchanged —
// including per-phase overrides. Reading a bare OPM_MODEL here would silently
// drop OPM_MODEL_CODING for deployments that set it.
func TestLegacyEnvPathKeepsPerPhaseModels(t *testing.T) {
	t.Setenv("PEER_OAM_URL", "")
	t.Setenv("OPM_MODEL", "auto")
	t.Setenv("OPM_MODEL_CODING", "strong-model")
	t.Setenv("OPM_MODEL_PLANNING", "light-model")
	t.Setenv("OPM_MODEL_API_KEY", "crsr_test")

	coding, err := resolveJobModelConfig(context.Background(), Job{Action: "run-implementation"})
	if err != nil {
		t.Fatalf("legacy resolve: %v", err)
	}
	planning, err := resolveJobModelConfig(context.Background(), Job{Action: "run-planning"})
	if err != nil {
		t.Fatalf("legacy resolve: %v", err)
	}
	if coding.Model != "strong-model" {
		t.Fatalf("coding model=%q want strong-model (OPM_MODEL_CODING lost)", coding.Model)
	}
	if planning.Model != "light-model" {
		t.Fatalf("planning model=%q want light-model", planning.Model)
	}
	if coding.FromOAM || planning.FromOAM {
		t.Fatal("legacy path must not be marked as resolved from OAM")
	}
	if !coding.hasKey() {
		t.Fatal("legacy path should carry the env key")
	}
}

// An action with no AI phase gets no model and no credential, even with a key in
// the environment and OAM configured.
func TestNonAIActionResolvesNothing(t *testing.T) {
	t.Setenv("PEER_OAM_URL", "http://oam.invalid:8090")
	cfg, err := resolveJobModelConfig(context.Background(), Job{Action: "deliver"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.hasKey() || cfg.Model != "" {
		t.Fatalf("non-AI action got model=%q key_present=%v", cfg.Model, cfg.hasKey())
	}
}

// Only ONE model reaches the runner. The pre-OAM code passed all six
// OPM_MODEL_<PHASE> variables and let the runner pick, which re-derived a decision
// the control plane had already made and carried every phase's config into every
// container.
func TestRunnerEnvCarriesOneModelOnly(t *testing.T) {
	env := modelEnvForJob(jobModelConfig{
		Provider: "cli_cursor", Model: "auto", FromOAM: true,
	})
	if env["OPM_MODEL"] != "auto" {
		t.Fatalf("OPM_MODEL=%q want auto", env["OPM_MODEL"])
	}
	if env["OPM_MODEL_PROVIDER"] != "cli_cursor" {
		t.Fatalf("OPM_MODEL_PROVIDER=%q", env["OPM_MODEL_PROVIDER"])
	}
	for k := range env {
		for _, phase := range []string{
			"OPM_MODEL_PLANNING", "OPM_MODEL_CODING", "OPM_MODEL_REVIEW",
			"OPM_MODEL_IDEATION", "OPM_MODEL_ROADMAP_DISCOVERY", "OPM_MODEL_ROADMAP_FEATURES",
		} {
			if k == phase {
				t.Fatalf("phase variable %s must not reach the runner", phase)
			}
		}
	}
	// An empty config contributes nothing rather than an empty OPM_MODEL, which
	// the runner would read as a model named "".
	if len(modelEnvForJob(jobModelConfig{})) != 0 {
		t.Fatal("empty config should contribute no env")
	}
}

// A resolve failure must fail the job, not silently fall back to the
// deployment-wide key — that would run the job as somebody else.
func TestResolveFailureDoesNotFallBackToEnv(t *testing.T) {
	// Unroutable host so the peer call fails fast rather than hanging.
	t.Setenv("PEER_OAM_URL", "http://127.0.0.1:1")
	t.Setenv("OPEN_SERVICE_JWT_SECRET", "test-secret-value-at-least-32-chars-long")
	t.Setenv("OPM_MODEL_API_KEY", "crsr_deployment_wide_key")

	cfg, err := resolveJobModelConfig(context.Background(), Job{
		Action: "run-implementation", OrganizationID: "org-a", ActorUsername: "alice",
	})
	if err == nil {
		t.Fatalf("expected an error, got config provider=%q model=%q", cfg.Provider, cfg.Model)
	}
	if cfg.hasKey() {
		t.Fatal("a failed resolve must not yield the deployment-wide key")
	}
}

// The spawn probe is what people read to decide whether the model path is live.
// With OAM configured it must not report "unset — fallback" just because the
// legacy environment variables are empty.
func TestProbeHonestyReflectsOAM(t *testing.T) {
	t.Setenv("OPM_MODEL_API_KEY", "")
	t.Setenv("CURSOR_API_KEY", "")

	t.Setenv("PEER_OAM_URL", "")
	if modelAPIKeyPresent() {
		t.Fatal("with no OAM and no env key, the model path is not available")
	}
	if !strings.Contains(modelConfigHonesty(), "unset") {
		t.Fatalf("legacy honesty should say the key is unset: %q", modelConfigHonesty())
	}

	t.Setenv("PEER_OAM_URL", "http://oam:8090")
	if !modelAPIKeyPresent() {
		t.Fatal("with OAM configured the model path IS available (keys resolve per job)")
	}
	h := modelConfigHonesty()
	if !strings.Contains(h, "OAM") || !strings.Contains(h, "credential_unavailable") {
		t.Fatalf("OAM honesty should explain per-job resolution and fail-closed: %q", h)
	}
}
