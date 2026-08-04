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
