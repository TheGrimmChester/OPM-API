package main

import (
	"strings"
	"testing"
)

func TestParseRunnerResultJSON(t *testing.T) {
	raw := []byte(`banner
{"mode":"fallback","reason":"OPM_MODEL_API_KEY not set; control plane will use builtin artifact helpers","action":"run-planning"}
`)
	rr, ok := parseRunnerResultJSON(raw)
	if !ok || rr.Mode != "fallback" {
		t.Fatalf("got %+v ok=%v", rr, ok)
	}
	if !strings.Contains(rr.Reason, "OPM_MODEL_API_KEY") {
		t.Fatalf("reason=%q", rr.Reason)
	}
}

func TestParseRunnerResultModelPlan(t *testing.T) {
	raw := []byte(`{"mode":"model","model":"gpt-test","specMarkdown":"# Hello","plan":{"feature":"X","phases":[{"phase":1,"name":"Design","type":"planning","subtasks":[{"id":"1-0","description":"a","status":"completed"}]}]}}`)
	rr, ok := parseRunnerResultJSON(raw)
	if !ok || rr.Mode != "model" || rr.Plan == nil || len(rr.Plan.Phases) != 1 {
		t.Fatalf("got %+v ok=%v", rr, ok)
	}
}

func TestApplyModelPlanningPersists(t *testing.T) {
	t.Setenv("OPM_FORCE_BUILTIN", "1")
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{Name: "m", OwnerRepo: "o/r", ConnectorID: "c", OrganizationID: "default-org"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.InitProject(p.ID)
	task, err := store.CreateTask(p.ID, "Model plan me", "desc", false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	j := Job{RunID: "run-m1", ProjectID: p.ID, SpecID: task.SpecID, Action: "run-planning"}
	rr := RunnerResult{
		Mode:         "model",
		Model:        "gpt-test",
		SpecMarkdown: "# Model Spec\n\nFrom model.\n",
		Plan: &ImplementationPlan{
			Feature: "Model Spec", Status: "ready", PlanStatus: "ready",
			Phases: []PlanPhase{{
				Phase: 1, Name: "Design", Type: "planning",
				Subtasks: []PlanSubtask{{ID: "1-0", Description: "scope", Status: "completed"}},
			}},
		},
		RawExcerpt: `{"specMarkdown":"# Model Spec"}`,
	}
	handled, msg, err := applyRunnerResult(store, j, rr)
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v msg=%s", handled, err, msg)
	}
	if !strings.Contains(msg, "model/gpt-test") {
		t.Fatalf("msg=%s", msg)
	}
	md, err := store.GetSpecMarkdown(p.ID, task.SpecID)
	if err != nil || !strings.Contains(md, "From model") {
		t.Fatalf("spec=%q err=%v", md, err)
	}
	logs, _ := store.GetSpecLogs(p.ID, task.SpecID)
	if !strings.Contains(logs["planning"], "model planning") {
		t.Fatalf("logs=%v", logs)
	}
}

func TestApplyModelRoadmapDiscoveryAndFeatures(t *testing.T) {
	t.Setenv("OPM_FORCE_BUILTIN", "1")
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{Name: "ArrM", OwnerRepo: "o/arrm", ConnectorID: "c", OrganizationID: "default-org"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.InitProject(p.ID)

	j := Job{RunID: "run-d1", ProjectID: p.ID, Action: "run-roadmap-discovery"}
	rr := RunnerResult{
		Mode: "model", Model: "auto",
		Vision:         "Self-hosted media management for operators",
		TargetAudience: "Homelab operators",
		Phases: []RoadmapPhase{
			{ID: "phase-1", Name: "Core library", Description: "Index and serve media", Order: 1, Status: "planned"},
			{ID: "phase-2", Name: "Integrations", Description: "Download clients", Order: 2, Status: "planned"},
		},
		DiscoveryBlob: map[string]any{
			"project_type": "api",
			"product_vision": map[string]any{
				"one_liner": "Self-hosted media management for operators",
			},
		},
	}
	handled, msg, err := applyRunnerResult(store, j, rr)
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v msg=%s", handled, err, msg)
	}
	rm, err := store.GetRoadmap(p.ID)
	if err != nil || !strings.Contains(rm.Vision, "media") {
		t.Fatalf("vision=%q err=%v", rm.Vision, err)
	}
	if len(rm.Phases) != 2 {
		t.Fatalf("phases=%d", len(rm.Phases))
	}
	if rm.Metadata["discovery"] == nil {
		t.Fatal("expected discovery metadata")
	}

	// Preserve milestone bind on name match.
	rm.Phases[0].GithubMilestoneNumber = 7
	rm.Phases[0].GithubMilestoneTitle = "M1"
	_ = store.PutRoadmap(p.ID, rm)

	fj := Job{RunID: "run-f1", ProjectID: p.ID, Action: "run-roadmap-features"}
	frr := RunnerResult{
		Mode: "model", Model: "auto",
		Features: []RoadmapFeature{
			{
				Title: "Quality profile rules", Description: "MoSCoW must", Priority: "must",
				Complexity: "m", Impact: "high", PhaseID: "phase-1", Status: "planned",
				AcceptanceCriteria: []string{"Profiles apply to movies"},
			},
			{
				Title: "Already shipped indexer", Priority: "must", PhaseID: "phase-1", Status: "planned",
			},
		},
	}
	// Mark one feature implemented so the second title is skipped on a re-run style filter.
	rm, _ = store.GetRoadmap(p.ID)
	rm.Features = append(rm.Features, RoadmapFeature{
		ID: "feat-old", Title: "Already shipped indexer", Status: "shipped", PhaseID: rm.Phases[0].ID,
	})
	_ = store.PutRoadmap(p.ID, rm)

	handled, msg, err = applyRunnerResult(store, fj, frr)
	if !handled || err != nil {
		t.Fatalf("features handled=%v err=%v msg=%s", handled, err, msg)
	}
	rm, _ = store.GetRoadmap(p.ID)
	found := false
	for _, f := range rm.Features {
		if f.Title == "Quality profile rules" {
			found = true
			if f.Priority != "must" || len(f.AcceptanceCriteria) == 0 {
				t.Fatalf("feature=%+v", f)
			}
		}
	}
	if !found {
		t.Fatalf("features=%v msg=%s", rm.Features, msg)
	}
	// Milestone bind preserved after discovery re-merge is not invoked here; check prior phase.
	if rm.Phases[0].GithubMilestoneNumber != 7 {
		t.Fatalf("milestone bind lost: %+v", rm.Phases[0])
	}
}

func TestApplyModelIdeationSkipsImplemented(t *testing.T) {
	t.Setenv("OPM_FORCE_BUILTIN", "1")
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{Name: "x", OwnerRepo: "o/r", ConnectorID: "c", OrganizationID: "default-org"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.InitProject(p.ID)
	task, err := store.CreateTask(p.ID, "Fix auth bypass", "d", false, "idea-1", "security")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = store.MoveTask(p.ID, task.SpecID, "done")

	j := Job{RunID: "run-i1", ProjectID: p.ID, Action: "run-ideation", IdeationType: "security"}
	rr := RunnerResult{
		Mode: "model", Model: "auto",
		Ideas: Ideation{
			"security": {
				{ID: "idea-1", Title: "Other title same id", Description: "skip by id"},
				{Title: "Fix auth bypass", Description: "skip by title"},
				{Title: "New CSRF check", Description: "add me", Rationale: "real", EstimatedEffort: "S"},
			},
		},
	}
	handled, msg, err := applyRunnerResult(store, j, rr)
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v msg=%s", handled, err, msg)
	}
	ideas, _ := store.GetIdeation(p.ID)
	if len(ideas["security"]) != 1 || ideas["security"][0].Title != "New CSRF check" {
		t.Fatalf("ideas=%v msg=%s", ideas["security"], msg)
	}
}

func TestParseRunnerResultRoadmapJSON(t *testing.T) {
	raw := []byte(`{"mode":"model","model":"auto","vision":"Media app","targetAudience":"operators",
"phases":[{"id":"p1","name":"Core","order":1,"status":"planned"}],
"features":[{"id":"f1","title":"Queue sync","priority":"must","phase_id":"p1","status":"planned"}],
"discovery":{"project_type":"mobile-app"}}`)
	rr, ok := parseRunnerResultJSON(raw)
	if !ok || rr.Mode != "model" {
		t.Fatalf("parse failed: %+v ok=%v", rr, ok)
	}
	if rr.Vision != "Media app" || len(rr.Phases) != 1 || len(rr.Features) != 1 {
		t.Fatalf("roadmap fields: vision=%q phases=%d features=%d", rr.Vision, len(rr.Phases), len(rr.Features))
	}
	if rr.DiscoveryBlob == nil {
		t.Fatal("expected discovery blob")
	}
}

func TestModelForActionPhaseKeys(t *testing.T) {
	t.Setenv("OPM_MODEL", "auto")
	t.Setenv("OPM_MODEL_IDEATION", "ideation-model")
	t.Setenv("OPM_MODEL_ROADMAP_DISCOVERY", "disc-model")
	t.Setenv("OPM_MODEL_ROADMAP_FEATURES", "feat-model")
	if modelForAction("run-ideation") != "ideation-model" {
		t.Fatal(modelForAction("run-ideation"))
	}
	if modelForAction("run-roadmap-discovery") != "disc-model" {
		t.Fatal(modelForAction("run-roadmap-discovery"))
	}
	if modelForAction("run-roadmap-features") != "feat-model" {
		t.Fatal(modelForAction("run-roadmap-features"))
	}
}

func TestModelForActionDefaultsToAuto(t *testing.T) {
	for _, k := range []string{
		"OPM_MODEL", "OPM_MODEL_PLANNING", "OPM_MODEL_CODING", "OPM_MODEL_REVIEW",
		"OPM_MODEL_IDEATION", "OPM_MODEL_ROADMAP_DISCOVERY", "OPM_MODEL_ROADMAP_FEATURES",
	} {
		t.Setenv(k, "")
	}
	for _, action := range []string{
		"run-planning", "run-implementation", "run-review",
		"run-ideation", "run-roadmap-discovery", "run-roadmap-features",
	} {
		if modelForAction(action) != "auto" {
			t.Fatalf("%s: got %q want auto", action, modelForAction(action))
		}
	}
}
