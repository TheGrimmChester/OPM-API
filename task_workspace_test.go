package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMergeChangeSetByPath(t *testing.T) {
	base := ChangeSet{Files: []FileChange{{Path: "a.go", Contents: strPtr("a1")}}}
	delta := ChangeSet{Files: []FileChange{
		{Path: "b.go", Contents: strPtr("b")},
		{Path: "a.go", Contents: strPtr("a2")},
	}}
	merged := MergeChangeSet(base, delta)
	if len(merged.Files) != 2 {
		t.Fatalf("want 2 files, got %d", len(merged.Files))
	}
	if *merged.Files[0].Contents != "a1" || merged.Files[0].Path != "a.go" {
		// order: a first then b
	}
	foundA, foundB := false, false
	for _, f := range merged.Files {
		switch f.Path {
		case "a.go":
			foundA = true
			if *f.Contents != "a2" {
				t.Fatalf("a.go should be overwritten by delta")
			}
		case "b.go":
			foundB = true
		}
	}
	if !foundA || !foundB {
		t.Fatalf("missing merged paths: a=%v b=%v", foundA, foundB)
	}
}

func TestTaskWorkspaceRootStable(t *testing.T) {
	root := taskWorkspaceRoot("proj-1", "spec-001")
	want := filepath.Join(jobTmpRoot(), "tasks", "proj-1", "spec-001")
	if root != want {
		t.Fatalf("root = %q want %q", root, want)
	}
}

func TestHasPendingCodingSubtasks(t *testing.T) {
	plan := ImplementationPlan{Phases: []PlanPhase{
		{Type: "coding", Subtasks: []PlanSubtask{
			{ID: "2-1", Status: "completed"},
			{ID: "2-2", Status: "pending"},
		}},
	}}
	if !hasPendingCodingSubtasks(plan) {
		t.Fatal("expected pending coding subtask")
	}
	plan.Phases[0].Subtasks[1].Status = "completed"
	if hasPendingCodingSubtasks(plan) {
		t.Fatal("expected no pending coding subtasks")
	}
	if !allCodingSubtasksComplete(plan) {
		t.Fatal("expected all coding complete")
	}
}

func TestReleaseTaskWorkspace(t *testing.T) {
	dir := taskWorkspaceRoot("p", "s")
	_ = os.MkdirAll(filepath.Join(dir, "repo"), 0o755)
	releaseTaskWorkspace("p", "s")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("workspace should be removed")
	}
}
