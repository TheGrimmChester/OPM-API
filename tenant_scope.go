package main

import (
	"net/http"
	"strings"

	opentenant "github.com/TheGrimmChester/open-tenant-go"
)

// initTenantAuth mirrors OPA_AUTH_REQUIRED into Open-Tenant-Go so "all"/empty
// org headers cannot widen scope when auth is on.
func initTenantAuth() {
	opentenant.SetAuthEnforced(authRequiredEnv())
}

// resolveRequestOrg returns the organization this request may access.
// When auth is enforced, empty/"all" collapse to default-org (WriteTenant).
// When auth is off, empty/"all" mean unscoped ("").
func resolveRequestOrg(r *http.Request) string {
	ctx := opentenant.FromRequest(r)
	if ctx.OrganizationID == "" {
		if q := strings.TrimSpace(r.URL.Query().Get("organizationId")); q != "" {
			if !(opentenant.AuthEnforced() && q == opentenant.All) {
				ctx.OrganizationID = q
			}
		}
	}
	if opentenant.AuthEnforced() {
		org, _ := ctx.WriteTenant()
		return org
	}
	if ctx.OrganizationID == "" || ctx.OrganizationID == opentenant.All {
		return ""
	}
	return ctx.OrganizationID
}

func projectOrgID(p Project) string {
	org := strings.TrimSpace(p.OrganizationID)
	if org == "" || org == opentenant.All {
		return opentenant.DefaultOrganizationID
	}
	return org
}

func projectInOrg(p Project, org string) bool {
	if org == "" {
		return true
	}
	return projectOrgID(p) == org
}

func normalizeWriteOrg(org string) string {
	org = strings.TrimSpace(org)
	if org == "" || org == opentenant.All {
		return opentenant.DefaultOrganizationID
	}
	return org
}

// concreteProjectHeader reports whether the client sent a real project id
// (not empty, "all", or the ClickHouse-family lab sentinel).
func concreteProjectHeader(headerProj string) bool {
	headerProj = strings.TrimSpace(headerProj)
	if headerProj == "" || strings.EqualFold(headerProj, opentenant.All) {
		return false
	}
	if headerProj == "default-project" {
		return false
	}
	return true
}

// enforcePathProjectHeader keeps nested /api/projects/{id}/… routes aligned with
// X-Project-ID. Path is the source of truth for artifacts/jobs; a mismatched
// concrete header is rejected so ideation/roadmap/jobs cannot run against a
// different project than the selected (header) tenant. Empty/"all"/default-project
// headers are pinned to the path id.
func enforcePathProjectHeader(w http.ResponseWriter, r *http.Request, projectID string) bool {
	headerProj := strings.TrimSpace(r.Header.Get("X-Project-ID"))
	if concreteProjectHeader(headerProj) && headerProj != projectID {
		writeError(w, http.StatusForbidden, "X-Project-ID does not match path project")
		return false
	}
	r.Header.Set("X-Project-ID", projectID)
	return true
}
