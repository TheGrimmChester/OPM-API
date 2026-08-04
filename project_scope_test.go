package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	opentenant "github.com/TheGrimmChester/open-tenant-go"
)

func TestEnforcePathProjectHeader(t *testing.T) {
	opentenant.SetAuthEnforced(true)
	t.Cleanup(func() { opentenant.SetAuthEnforced(false) })

	pathID := "proj-selected"

	t.Run("mismatch_forbidden", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/projects/"+pathID+"/jobs", strings.NewReader(`{}`))
		r.Header.Set("X-Organization-ID", "default-org")
		r.Header.Set("X-Project-ID", "proj-other")
		rec := httptest.NewRecorder()
		if enforcePathProjectHeader(rec, r, pathID) {
			t.Fatal("expected mismatch to fail")
		}
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d want 403 body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("match_ok", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/api/projects/"+pathID+"/roadmap", nil)
		r.Header.Set("X-Project-ID", pathID)
		rec := httptest.NewRecorder()
		if !enforcePathProjectHeader(rec, r, pathID) {
			t.Fatal("expected match to pass")
		}
		if r.Header.Get("X-Project-ID") != pathID {
			t.Fatalf("header=%q", r.Header.Get("X-Project-ID"))
		}
	})

	t.Run("default_project_pinned_to_path", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/api/projects/"+pathID+"/ideation", nil)
		r.Header.Set("X-Project-ID", "default-project")
		rec := httptest.NewRecorder()
		if !enforcePathProjectHeader(rec, r, pathID) {
			t.Fatal("expected sentinel to pin")
		}
		if r.Header.Get("X-Project-ID") != pathID {
			t.Fatalf("header=%q want path pin", r.Header.Get("X-Project-ID"))
		}
	})

	t.Run("empty_header_pinned_to_path", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/api/projects/"+pathID+"/jobs", nil)
		rec := httptest.NewRecorder()
		if !enforcePathProjectHeader(rec, r, pathID) {
			t.Fatal("expected empty to pin")
		}
		if r.Header.Get("X-Project-ID") != pathID {
			t.Fatalf("header=%q", r.Header.Get("X-Project-ID"))
		}
	})
}

func TestPathProjectHeaderOnJobsRoute(t *testing.T) {
	opentenant.SetAuthEnforced(false)
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
		OwnerRepo: "acme/b", ConnectorID: "c1", OrganizationID: "default-org",
	})
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	registerOPMMux(mux, store, func(path string, h http.HandlerFunc) {
		mux.HandleFunc(path, h)
	}, func(string, http.HandlerFunc) {})

	// Enqueue against path A while claiming selected project B → 403.
	body := `{"action":"run-ideation"}`
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+a.ID+"/jobs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Organization-ID", "default-org")
	req.Header.Set("X-Project-ID", b.ID)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("mismatch job: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Matching selected project → accepted (queued).
	req2 := httptest.NewRequest(http.MethodPost, "/api/projects/"+b.ID+"/jobs", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Organization-ID", "default-org")
	req2.Header.Set("X-Project-ID", b.ID)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("match job: status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), `"projectId":"`+b.ID+`"`) {
		t.Fatalf("job must bind selected project B: %s", rec2.Body.String())
	}
}
