package main

import (
	"net/http"
	"strings"

	openauth "github.com/TheGrimmChester/open-auth-go"
	opentenant "github.com/TheGrimmChester/open-tenant-go"
)

// initTenantAuth mirrors OPA_AUTH_REQUIRED into Open-Tenant-Go so "all"/empty
// org headers cannot widen scope when auth is on.
func initTenantAuth() {
	opentenant.SetAuthEnforced(authRequiredEnv())
}

// resolveRequestOrg returns the organization this request may access.
// Never invents default-org. Personal JWTs (and impersonated-personal) pin
// empty org. When auth is enforced, empty/"all" stay empty (WriteTenant).
// When auth is off, empty/"all" mean unscoped ("").
// Prefer JWT org_id for organization accounts so jobs are not stamped from
// headers alone.
func resolveRequestOrg(r *http.Request) string {
	if claims := claimsFromRequestToken(r); claims != nil && openauth.IsPersonalAccount(claims) {
		return ""
	}
	if org := jobOrgFromJWT(r); org != "" {
		return org
	}
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

// jobOrgFromJWT reads the acting user's home organization from validated JWT
// claims. Personal accounts return "" (never default-org). Organization
// accounts return the fixed JWT org_id. Unbound legacy admins fall back to
// headers via resolveRequestOrg.
func jobOrgFromJWT(r *http.Request) string {
	claims := claimsFromRequestToken(r)
	if claims == nil {
		return ""
	}
	if openauth.IsPersonalAccount(claims) {
		return ""
	}
	if org := strings.TrimSpace(claims.OrgID); org != "" {
		return org
	}
	return ""
}

func projectOrgID(p Project) string {
	org := strings.TrimSpace(p.OrganizationID)
	if org == opentenant.All {
		return ""
	}
	return org
}

// projectInOrg reports whether project p belongs to org.
// Empty org under auth matches only empty-org (personal) rows — never the
// shared default-org bucket and never "all projects". Auth off + empty org
// remains unscoped (lab mode).
func projectInOrg(p Project, org string) bool {
	org = strings.TrimSpace(org)
	if org == opentenant.All {
		org = ""
	}
	if org == "" {
		if !opentenant.AuthEnforced() {
			return true
		}
		return projectOrgID(p) == ""
	}
	return projectOrgID(p) == org
}

// normalizeWriteOrg keeps empty/"all" as empty — never invents default-org.
// Explicit default-org is preserved when the caller selects it for legacy rows.
func normalizeWriteOrg(org string) string {
	org = strings.TrimSpace(org)
	if org == "" || org == opentenant.All {
		return ""
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
