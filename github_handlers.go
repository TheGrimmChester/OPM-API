package main

import (
	"context"
	"encoding/json"
	opentenant "github.com/TheGrimmChester/open-tenant-go"
	"net/http"
	"strings"
	"time"
)

func registerDiscoveryMux(mux *http.ServeMux, authView func(string, http.HandlerFunc)) {
	authView("/api/hub/status", handleHubStatus)
	authView("/api/oam/organizations", handleOAMOrganizations)
	authView("/api/oam/projects", handleOAMProjects)
	authView("/api/github/connectors", handleGitHubConnectors)
	mux.HandleFunc("/api/github/connectors/", func(w http.ResponseWriter, r *http.Request) {
		h := handleGitHubConnectorSub
		if authRequiredEnv() {
			AuthMiddleware(h, "viewer")(w, r)
			return
		}
		h(w, r)
	})
}

func handleHubStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	credHome := "env"
	note := "OPM links to OAM for organization/project directory when PEER_OAM_URL is set. No local folder projects."
	switch {
	case oamConfigured():
		credHome = "oam"
		note = "OPM links to OAM for organizations, projects, connectors/credentials and model bindings, and ORA for GitHub SCM protocol (list/clone/PR). No local folder projects."
	case peerORAConfigured():
		credHome = "ora"
		note = "OPM links to ORA for GitHub SCM protocol (list/clone/PR). Set PEER_OAM_URL so organizations, connectors/credentials and model bindings resolve from OAM. No local folder projects."
	}
	out := map[string]interface{}{
		"peer_opa_configured": peerOPAConfigured(),
		"peer_ora_configured": peerORAConfigured(),
		"peer_oam_configured": oamConfigured(),
		"credentials_home":    credHome,
		"hub_role":            "optional_peer",
		"note":                note,
	}
	if peerOPAConfigured() {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if st, err := peerHubGitHubStatus(ctx); err == nil {
			out["hub_github"] = st
		} else {
			out["hub_error"] = err.Error()
		}
	}
	writeJSON(w, out)
}

func handleOAMOrganizations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	base := oamPeerURL()
	if base == "" {
		writeJSON(w, map[string]interface{}{
			"organizations": []map[string]interface{}{},
			"note":          "PEER_OAM_URL unset — no OAM organization discovery. Set PEER_OAM_URL to list organizations.",
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	raw, status, err := proxyOAMProjectsGET(ctx, strings.TrimRight(base, "/")+"/api/organizations", r)
	if err != nil {
		writeError(w, 502, "oam unavailable: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func handleGitHubConnectors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	// Prefer OAM user-JWT proxy so personal (empty-org, user-scoped) connectors
	// are visible — ORA connectors:read with a bare service JWT hides them.
	if base := oamPeerURL(); base != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		target := strings.TrimRight(base, "/") + "/api/connectors"
		if q := r.URL.RawQuery; q != "" {
			target += "?" + q
		}
		raw, status, err := proxyOAMProjectsGET(ctx, target, r)
		if err != nil {
			writeError(w, 502, "oam unavailable: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(raw)
		return
	}
	if !peerORAConfigured() {
		writeError(w, 503, "PEER_ORA_URL not configured — list connectors via ORA peer API (storage is OAM when PEER_OAM_URL is set)")
		return
	}
	orgID := resolveRequestOrg(r)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, err := peerListConnectors(ctx, orgID, actorFromRequest(r))
	if err != nil {
		writeError(w, 502, "ora unavailable: "+err.Error())
		return
	}
	writeJSON(w, out)
}

func handleGitHubConnectorSub(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/github/connectors/")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[1] != "repos" || r.Method != http.MethodGet {
		writeError(w, 404, "not found")
		return
	}
	if !peerORAConfigured() {
		writeError(w, 503, "PEER_ORA_URL not configured")
		return
	}
	connectorID := parts[0]
	orgID := resolveRequestOrg(r)
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	out, err := peerListConnectorRepos(ctx, orgID, actorFromRequest(r), connectorID)
	if err != nil {
		writeError(w, 502, "ora unavailable: "+err.Error())
		return
	}
	writeJSON(w, out)
}

func orgHeader(r *http.Request) string {
	// Prefer resolveRequestOrg so auth-enforced defaults apply.
	if org := resolveRequestOrg(r); org != "" {
		return org
	}
	return opentenant.FromRequest(r).OrganizationID
}

// decodeLinkProjectBody parses POST /api/projects for GitHub link payloads.
// The board registry id must be the OAM directory project id (body.id or X-Project-ID).
func decodeLinkProjectBody(r *http.Request) (Project, error) {
	var body struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		OwnerRepo      string `json:"ownerRepo"`
		RepoFullName   string `json:"repo_full_name"`
		GithubRepoID   string `json:"githubRepoId"`
		ConnectorID    string `json:"connectorId"`
		OrganizationID string `json:"organizationId"`
		HTMLURL        string `json:"htmlUrl"`
		DefaultBranch  string `json:"defaultBranch"`
		// Reject legacy local-folder fields explicitly.
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return Project{}, err
	}
	if strings.TrimSpace(body.Path) != "" {
		return Project{}, errLegacyPath
	}
	owner := body.OwnerRepo
	if owner == "" {
		owner = body.RepoFullName
	}
	return Project{
		ID:             strings.TrimSpace(body.ID),
		Name:           body.Name,
		OwnerRepo:      owner,
		GithubRepoID:   body.GithubRepoID,
		ConnectorID:    body.ConnectorID,
		OrganizationID: body.OrganizationID,
		HTMLURL:        body.HTMLURL,
		DefaultBranch:  body.DefaultBranch,
	}, nil
}

type legacyPathError struct{}

func (legacyPathError) Error() string {
	return "local folder projects are not supported; link a GitHub repository (ownerRepo + connectorId)"
}

var errLegacyPath = legacyPathError{}
