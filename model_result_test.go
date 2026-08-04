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

func TestModelConfigHonestyNoKey(t *testing.T) {
	t.Setenv("OPM_MODEL_API_KEY", "")
	h := modelConfigHonesty()
	if !strings.Contains(h, "OPM_MODEL_API_KEY unset") {
		t.Fatalf("%s", h)
	}
}
