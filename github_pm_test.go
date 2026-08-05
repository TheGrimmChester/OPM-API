package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestBindGitHubProjectAndTaskFields(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{
		OwnerRepo: "acme/demo", ConnectorID: "c1", OrganizationID: "default-org",
	})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := store.BindGitHubProject(p.ID, "PVT_abc", "Acme Board", "https://github.com/orgs/acme/projects/1")
	if err != nil {
		t.Fatal(err)
	}
	if bound.GithubProjectID != "PVT_abc" || bound.GithubProjectTitle != "Acme Board" {
		t.Fatalf("bind: %+v", bound)
	}
	got, err := store.GetProject(p.ID)
	if err != nil || got.GithubProjectID != "PVT_abc" {
		t.Fatalf("persist: %+v %v", got, err)
	}

	task, err := store.CreateTask(p.ID, "Ship sync", "desc", false, true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.UpdateTask(p.ID, task.SpecID, map[string]interface{}{
		"githubMilestoneNumber": float64(3),
		"githubMilestoneTitle":  "M3",
		"githubMilestoneUrl":    "https://github.com/acme/demo/milestone/3",
		"githubProjectId":       "PVT_abc",
		"githubProjectItemId":   "PVTI_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.GithubMilestoneNumber != 3 || updated.GithubProjectItemID != "PVTI_1" {
		t.Fatalf("task fields: %+v", updated)
	}
}

func TestGitHubRoutesRequireORA(t *testing.T) {
	dir := t.TempDir()
	_ = os.Unsetenv("PEER_ORA_URL")
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{
		OwnerRepo: "acme/demo", ConnectorID: "c1", OrganizationID: "default-org",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	authView := func(pattern string, h http.HandlerFunc) { mux.HandleFunc(pattern, h) }
	authAdmin := func(pattern string, h http.HandlerFunc) { mux.HandleFunc(pattern, h) }
	registerOPMMux(mux, store, authView, authAdmin)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+p.ID+"/github/milestones", nil)
	req.Header.Set("X-Organization-ID", "default-org")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != 503 {
		t.Fatalf("want 503 without PEER_ORA_URL, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestRoadmapMilestoneFieldsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{
		OwnerRepo: "acme/x", ConnectorID: "c", OrganizationID: "default-org",
	})
	if err != nil {
		t.Fatal(err)
	}
	rm := Roadmap{
		ID: "r1", ProjectName: "x", Version: "0.1.0",
		Phases: []RoadmapPhase{{
			ID: "ph1", Name: "Phase 1", Status: "planned", Order: 1,
			GithubMilestoneNumber: 1, GithubMilestoneTitle: "P1",
		}},
		Features: []RoadmapFeature{{
			ID: "f1", Title: "Feat", Priority: "high", Status: "planned", PhaseID: "ph1",
			GithubMilestoneNumber: 1, GithubProjectID: "PVT_1",
		}},
	}
	if err := store.PutRoadmap(p.ID, rm); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRoadmap(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phases[0].GithubMilestoneNumber != 1 || got.Features[0].GithubProjectID != "PVT_1" {
		b, _ := json.Marshal(got)
		t.Fatalf("roadmap fields lost: %s", b)
	}
}
