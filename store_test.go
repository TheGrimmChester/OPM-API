package main

import (
	"path/filepath"
	"testing"
)

func TestStoreLifecycle(t *testing.T) {
	tmp := t.TempDir()
	data := filepath.Join(tmp, "opm-data")
	store, err := NewStore(data)
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{
		Name:           "Demo",
		OwnerRepo:      "acme/demo",
		ConnectorID:    "conn-1",
		OrganizationID: "default-org",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.OwnerRepo != "acme/demo" {
		t.Fatalf("ownerRepo=%s", p.OwnerRepo)
	}
	if err := store.InitProject(p.ID); err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(p.ID, "First feature", "desc", false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "backlog" {
		t.Fatalf("status=%s", task.Status)
	}
	moved, err := store.MoveTask(p.ID, task.SpecID, "queue")
	if err != nil {
		t.Fatal(err)
	}
	if moved.Status != "queue" {
		t.Fatalf("moved=%s", moved.Status)
	}
	board, err := store.GetBoard(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range board["queue"] {
		if id == task.SpecID {
			found = true
		}
	}
	if !found {
		t.Fatalf("spec not on queue: %#v", board)
	}
	j, err := store.CreateJob(p.ID, "run-planning", task.SpecID, "opm-runner-task:smoke")
	if err != nil {
		t.Fatal(err)
	}
	if j.State != "queued" {
		t.Fatalf("job state=%s", j.State)
	}
	// Board state must live under data dir, not a local workspace path.
	wantDir := filepath.Join(data, "projects", p.ID)
	if store.projectDir(p) != wantDir {
		t.Fatalf("projectDir=%s want %s", store.projectDir(p), wantDir)
	}
}

func TestCreateProjectRejectsBadRepo(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateProject(Project{OwnerRepo: "bad", ConnectorID: "c"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNormalizeOwnerRepo(t *testing.T) {
	if got := normalizeOwnerRepo("https://github.com/Acme/Demo.git"); got != "Acme/Demo" {
		t.Fatalf("got %s", got)
	}
}
