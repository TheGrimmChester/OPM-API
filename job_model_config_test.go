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
		"run-ideation":          agentKeyIdeation,
		"run-roadmap-discovery": agentKeyRoadmapDiscovery,
		"run-roadmap-features":  agentKeyRoadmapFeatures,
	}
	for action, want := range cases {
		if got := agentKeyForAction(action); got != want {
			t.Fatalf("agentKeyForAction(%q)=%q want %q", action, got, want)
		}
	}
	// Review / qa-fix are ORA's domain — no OPM agent key.
	for _, action := range []string{"run-review", "run-qa-fix", "deliver", "skip-to-phase", "", "sync-board", "run-roadmap-publish"} {
		if got := agentKeyForAction(action); got != "" {
			t.Fatalf("agentKeyForAction(%q)=%q want empty", action, got)
		}
	}
}

// The mapping in agent_keys.go must stay in step with modelForAction.
func TestAgentKeysCoverEveryModelPhase(t *testing.T) {
	phaseActions := []string{
		"run-planning", "run-followup-planning", "run-implementation",
		"run-ideation", "run-roadmap-discovery", "run-roadmap-features",
		"run-roadmap-competitor",
	}
	for _, action := range phaseActions {
		if agentKeyForAction(action) == "" {
			t.Fatalf("action %q has a model phase in modelForAction but no agent key", action)
		}
	}
	reachable := map[string]bool{}
	for _, action := range phaseActions {
		reachable[agentKeyForAction(action)] = true
	}
	for _, e := range agentCatalog() {
		if !reachable[e.AgentKey] {
			t.Fatalf("catalog advertises %q but no action maps to it", e.AgentKey)
		}
	}
}

// Without PEER_OAM_URL, AI-phase jobs fail closed — no host OPM_MODEL* break-glass.
func TestNoOAMRefusesModelBackedJobs(t *testing.T) {
	t.Setenv("PEER_OAM_URL", "")
	t.Setenv("OPA_AUTH_REQUIRED", "0")
	t.Setenv("OPM_MODEL_API_KEY", "crsr_test")

	_, err := resolveJobModelConfig(context.Background(), Job{Action: "run-implementation"})
	if err == nil {
		t.Fatal("expected error when OAM is unset")
	}
	if !strings.Contains(err.Error(), "PEER_OAM_URL") {
		t.Fatalf("error should mention OAM requirement: %v", err)
	}
}

// An action with no AI phase gets no model and no credential, even with OAM configured.
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

// Only ONE model reaches the runner.
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
	if len(modelEnvForJob(jobModelConfig{})) != 0 {
		t.Fatal("empty config should contribute no env")
	}
}

// A resolve failure must fail the job, not silently fall back to a host key.
func TestResolveFailureDoesNotFallBackToEnv(t *testing.T) {
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
		t.Fatal("a failed resolve must not yield a host key")
	}
}

// The spawn probe is what people read to decide whether the model path is live.
func TestProbeHonestyReflectsOAM(t *testing.T) {
	t.Setenv("OPM_MODEL_API_KEY", "")
	t.Setenv("CURSOR_API_KEY", "")
	t.Setenv("OPA_AUTH_REQUIRED", "0")

	t.Setenv("PEER_OAM_URL", "")
	if modelAPIKeyPresent() {
		t.Fatal("with no OAM the model path is not available")
	}
	if !strings.Contains(modelConfigHonesty(), "PEER_OAM_URL") {
		t.Fatalf("honesty should require OAM: %q", modelConfigHonesty())
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
