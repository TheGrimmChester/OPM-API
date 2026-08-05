package main

import (
	"testing"
)

func TestHumanReviewRequiredDefaultTrue(t *testing.T) {
	t.Parallel()
	if !humanReviewRequired(Task{}) {
		t.Fatal("nil HumanReviewRequired should default to true")
	}
	if humanReviewRequired(Task{HumanReviewRequired: boolPtr(false)}) {
		t.Fatal("explicit false should disable human gate")
	}
}

func TestUsesTaskWorkspaceAutopilot(t *testing.T) {
	t.Parallel()
	if !usesTaskWorkspace("run-implementation", false) {
		t.Fatal("implementation always uses task workspace")
	}
	if usesTaskWorkspace("run-review", false) {
		t.Fatal("review with human gate should not use task workspace")
	}
	if !usesTaskWorkspace("run-review", true) {
		t.Fatal("autopilot review should use task workspace")
	}
	if !usesTaskWorkspace("run-qa-fix", true) {
		t.Fatal("autopilot qa-fix should use task workspace")
	}
	if usesTaskWorkspace("run-qa-fix", false) {
		t.Fatal("manual qa-fix should not use task workspace")
	}
}

func TestTaskWorkspaceWriteMount(t *testing.T) {
	t.Parallel()
	if !taskWorkspaceWriteMount("run-implementation") {
		t.Fatal("implementation needs rw")
	}
	if !taskWorkspaceWriteMount("run-qa-fix") {
		t.Fatal("qa-fix needs rw")
	}
	if taskWorkspaceWriteMount("run-review") {
		t.Fatal("review should be read-only mount")
	}
}

func TestReviewPassStatus(t *testing.T) {
	t.Parallel()
	hr := Task{HumanReviewRequired: boolPtr(true)}
	if reviewPassStatus(hr) != "human_review" {
		t.Fatalf("got %q", reviewPassStatus(hr))
	}
	auto := Task{HumanReviewRequired: boolPtr(false)}
	if reviewPassStatus(auto) != "review" {
		t.Fatalf("got %q", reviewPassStatus(auto))
	}
}

func TestCreateTaskHumanReviewDefaultTrue(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{OwnerRepo: "acme/x", ConnectorID: "c", OrganizationID: "default-org"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.InitProject(p.ID)
	task, err := store.CreateTask(p.ID, "T", "d", false, true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !humanReviewRequired(task) {
		t.Fatal("CreateTask should store humanReviewRequired true by default")
	}
	auto, err := store.CreateTask(p.ID, "Auto", "d", false, false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if humanReviewRequired(auto) {
		t.Fatal("explicit false should enable autopilot")
	}
}

func TestBuiltinReviewAutopilotStaysInReview(t *testing.T) {
	t.Setenv("OPM_FORCE_BUILTIN", "1")
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{OwnerRepo: "acme/x", ConnectorID: "c", OrganizationID: "default-org"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.InitProject(p.ID)
	task, err := store.CreateTask(p.ID, "Auto task", "d", false, false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	pj, _ := store.CreateJob(p.ID, "run-planning", task.SpecID, "opm-runner-task:nas")
	executeJob(store, pj)
	for i := 0; i < 10; i++ {
		ij, _ := store.CreateJob(p.ID, "run-implementation", task.SpecID, "opm-runner-task:nas")
		executeJob(store, ij)
		cur, _ := store.GetTask(p.ID, task.SpecID)
		if cur.Status == "review" {
			break
		}
	}
	rj, _ := store.CreateJob(p.ID, "run-review", task.SpecID, "opm-runner-task:nas")
	executeJob(store, rj)
	cur, _ := store.GetTask(p.ID, task.SpecID)
	if cur.Status == "human_review" {
		t.Fatal("autopilot review PASS should not stop at human_review")
	}
	if cur.Status != "review" {
		t.Fatalf("want review got %s", cur.Status)
	}
}
