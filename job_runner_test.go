package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuiltinPlanningAndImplementation(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{
		Name: "Demo", OwnerRepo: "acme/demo", ConnectorID: "c1", OrganizationID: "default-org",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.InitProject(p.ID)
	task, err := store.CreateTask(p.ID, "Ship login", "Add login form", false, "", "")
	if err != nil {
		t.Fatal(err)
	}

	j, err := store.CreateJob(p.ID, "run-planning", task.SpecID, "opm-runner-task:nas")
	if err != nil {
		t.Fatal(err)
	}
	executeJob(store, j)
	j, err = store.GetJob(p.ID, j.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != "completed" || j.Execution != "builtin" {
		t.Fatalf("job=%+v", j)
	}
	md, err := store.GetSpecMarkdown(p.ID, task.SpecID)
	if err != nil || !strings.Contains(md, "Ship login") {
		t.Fatalf("spec=%q err=%v", md, err)
	}
	plan, err := store.GetPlan(p.ID, task.SpecID)
	if err != nil || len(plan.Phases) < 2 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	moved, _ := store.GetTask(p.ID, task.SpecID)
	if moved.Status != "queue" {
		t.Fatalf("status after planning=%s", moved.Status)
	}

	// Drain implementation subtasks
	for i := 0; i < 10; i++ {
		ij, err := store.CreateJob(p.ID, "run-implementation", task.SpecID, "opm-runner-task:nas")
		if err != nil {
			t.Fatal(err)
		}
		executeJob(store, ij)
		ij, _ = store.GetJob(p.ID, ij.RunID)
		if ij.State != "completed" {
			t.Fatalf("impl job state=%s msg=%s err=%s", ij.State, ij.Message, ij.Error)
		}
		if strings.Contains(ij.Message, "all") && strings.Contains(ij.Message, "review") {
			break
		}
		cur, _ := store.GetTask(p.ID, task.SpecID)
		if cur.Status == "review" {
			break
		}
	}
	cur, _ := store.GetTask(p.ID, task.SpecID)
	if cur.Status != "review" {
		t.Fatalf("want review got %s", cur.Status)
	}

	rj, err := store.CreateJob(p.ID, "run-review", task.SpecID, "opm-runner-task:nas")
	if err != nil {
		t.Fatal(err)
	}
	executeJob(store, rj)
	rj, _ = store.GetJob(p.ID, rj.RunID)
	if rj.State != "completed" || !strings.Contains(rj.Message, "PASS") {
		t.Fatalf("review=%+v", rj)
	}
	cur, _ = store.GetTask(p.ID, task.SpecID)
	if cur.Status != "human_review" {
		t.Fatalf("after review status=%s", cur.Status)
	}

	_, err = store.ApproveForCoding(p.ID, task.SpecID)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = store.MoveTask(p.ID, task.SpecID, "done")

	cj, err := store.CreateJob(p.ID, "generate-changelog", "", "opm-runner-task:nas")
	if err != nil {
		t.Fatal(err)
	}
	executeJob(store, cj)
	cl, err := store.GetChangelog(p.ID)
	if err != nil || !strings.Contains(cl, "Ship login") {
		t.Fatalf("changelog=%q err=%v", cl, err)
	}
	_ = filepath.Join(store.dataDir, "projects", p.ID)
	_ = time.Second
}

func TestBuiltinPlanningRequireReview(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{OwnerRepo: "acme/x", ConnectorID: "c", OrganizationID: "default-org"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.InitProject(p.ID)
	task, err := store.CreateTask(p.ID, "Gated", "desc", true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	j, _ := store.CreateJob(p.ID, "run-planning", task.SpecID, "opm-runner-task:nas")
	executeJob(store, j)
	cur, _ := store.GetTask(p.ID, task.SpecID)
	if cur.Status != "human_review" {
		t.Fatalf("status=%s", cur.Status)
	}
}
