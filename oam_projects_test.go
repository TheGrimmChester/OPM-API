package main

import (
	"encoding/json"
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
