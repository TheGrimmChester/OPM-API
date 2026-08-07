package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestOAMProjectsTargetForwardsProduct(t *testing.T) {
	q := url.Values{}
	q.Set("organization_id", "acme")
	q.Set("product", "opm")
	got := oamProjectsTarget("http://oam:8090/", q)
	want := "http://oam:8090/api/projects?organization_id=acme&product=opm"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestOAMProjectsTargetDropsAllOrg(t *testing.T) {
	q := url.Values{}
	q.Set("organization_id", "all")
	q.Set("product", "opm")
	got := oamProjectsTarget("http://oam:8090", q)
	want := "http://oam:8090/api/projects?product=opm"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestAliasDirectoryIDsAddsProjectID(t *testing.T) {
	raw := []byte(`{"projects":[{"id":"web","name":"Web"}]}`)
	out := aliasDirectoryIDs(raw, "projects", "project_id")
	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatal(err)
	}
	list := payload["projects"].([]interface{})
	row := list[0].(map[string]interface{})
	if row["project_id"] != "web" || row["id"] != "web" {
		t.Fatalf("alias missing: %#v", row)
	}
}

func TestBoardProjectFromOAM(t *testing.T) {
	dir := &oamDirectoryProject{
		ID:            "balansun-website",
		Name:          "balansun-website",
		ExternalKey:   "balansun/balansun-website",
		HTMLURL:       "https://github.com/balansun/balansun-website",
		DefaultBranch: "main",
		ConnectorIDs:  []string{"conn-a", "conn-b"},
		OrganizationID: "default-org",
	}
	personal := boardProjectFromOAM(dir, "")
	if personal.ID != "balansun-website" || personal.OwnerRepo != "balansun/balansun-website" {
		t.Fatalf("personal map: %#v", personal)
	}
	if personal.ConnectorID != "conn-a" {
		t.Fatalf("connector=%s", personal.ConnectorID)
	}
	if personal.OrganizationID != "" {
		t.Fatalf("personal must keep empty org, got %q", personal.OrganizationID)
	}
	org := boardProjectFromOAM(dir, "default-org")
	if org.OrganizationID != "default-org" {
		t.Fatalf("org stamp=%q", org.OrganizationID)
	}
}

func TestFillProjectSCMFromOAM(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"projects": []map[string]interface{}{
				{
					"id":            "web",
					"name":          "Web",
					"external_key":  "acme/web",
					"connector_ids": []string{"conn-1"},
					"html_url":      "https://github.com/acme/web",
					"default_branch": "main",
				},
			},
		})
	}))
	defer srv.Close()
	t.Setenv("PEER_OAM_URL", srv.URL)

	req := httptest.NewRequest(http.MethodPost, "/api/projects", nil)
	req.Header.Set("X-Project-ID", "web")
	in := Project{ID: "web", OrganizationID: "default-org"}
	got, st, msg := fillProjectSCMFromOAM(req, in)
	if msg != "" || st != 0 {
		t.Fatalf("unexpected err %d %q", st, msg)
	}
	if got.ConnectorID != "conn-1" || got.OwnerRepo != "acme/web" || got.Name != "Web" {
		t.Fatalf("filled=%#v", got)
	}

	// Explicit client fields win.
	in2 := Project{ID: "web", ConnectorID: "keep", OwnerRepo: "acme/other", OrganizationID: "default-org"}
	got2, st, msg := fillProjectSCMFromOAM(req, in2)
	if msg != "" || st != 0 {
		t.Fatalf("explicit err %d %q", st, msg)
	}
	if got2.ConnectorID != "keep" || got2.OwnerRepo != "acme/other" {
		t.Fatalf("explicit overwritten: %#v", got2)
	}

	t.Setenv("PEER_OAM_URL", "")
	_, st, msg = fillProjectSCMFromOAM(req, Project{ID: "web"})
	if st != 400 || msg == "" {
		t.Fatalf("unset peer want 400, got %d %q", st, msg)
	}
}
