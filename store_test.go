package main

import (
	"path/filepath"
	"strings"
	"testing"

	opentenant "github.com/TheGrimmChester/open-tenant-go"
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

	movedHR, err := store.MoveTask(p.ID, task.SpecID, "human_review")
	if err != nil {
		t.Fatal(err)
	}
	if movedHR.Status != "human_review" {
		t.Fatalf("status=%s", movedHR.Status)
	}
	approved, err := store.ApproveForCoding(p.ID, task.SpecID)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != "queue" {
		t.Fatalf("approved status=%s", approved.Status)
	}
	actions, err := store.GetTaskValidActions(p.ID, task.SpecID)
	if err != nil {
		t.Fatal(err)
	}
	if !actions.Planning || !actions.Implementation {
		t.Fatalf("actions=%+v", actions)
	}
	md, err := store.GetSpecMarkdown(p.ID, task.SpecID)
	if err != nil || !strings.Contains(md, "First feature") {
		t.Fatalf("spec md=%q err=%v", md, err)
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

func TestOrgScopedProjectAccess(t *testing.T) {
	opentenant.SetAuthEnforced(true)
	t.Cleanup(func() { opentenant.SetAuthEnforced(false) })

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := store.CreateProject(Project{
		OwnerRepo: "acme/a", ConnectorID: "c1", OrganizationID: "default-org",
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.CreateProject(Project{
		OwnerRepo: "acme/b", ConnectorID: "c1", OrganizationID: "other-org",
	})
	if err != nil {
		t.Fatal(err)
	}

	list, err := store.ListProjectsForOrg("default-org")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("default-org list=%v", list)
	}
	empty, err := store.ListProjectsForOrg("missing-org")
	if err != nil || len(empty) != 0 {
		t.Fatalf("missing-org should be empty: %v %v", empty, err)
	}
	if _, err := store.GetProjectForOrg(b.ID, "default-org"); err == nil {
		t.Fatal("expected IDOR blocked for cross-org GetProjectForOrg")
	}
	if _, err := store.GetProjectForOrg(b.ID, "other-org"); err != nil {
		t.Fatalf("same-org get: %v", err)
	}
}

func TestNormalizeWriteOrg(t *testing.T) {
	if got := normalizeWriteOrg(""); got != "default-org" {
		t.Fatalf("got %s", got)
	}
	if got := normalizeWriteOrg("all"); got != "default-org" {
		t.Fatalf("got %s", got)
	}
	if got := normalizeWriteOrg("nas"); got != "nas" {
		t.Fatalf("got %s", got)
	}
}
