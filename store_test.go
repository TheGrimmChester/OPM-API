package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreLifecycle(t *testing.T) {
	tmp := t.TempDir()
	data := filepath.Join(tmp, "opm-data")
	projPath := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(projPath, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(data)
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject("Demo", projPath)
	if err != nil {
		t.Fatal(err)
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
}
