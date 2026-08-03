package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func registerDiscoveryMux(mux *http.ServeMux, authView func(string, http.HandlerFunc)) {
	authView("/api/hub/status", handleHubStatus)
	authView("/api/hub/organizations", handleHubOrganizations)
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
	out := map[string]interface{}{
		"peer_opa_configured": peerOPAConfigured(),
		"peer_ora_configured": peerORAConfigured(),
		"credentials_home":    "ora",
		"hub_role":            "identity_and_tenancy",
		"note":                "OPM links to OPA-Hub for identity/orgs and to ORA for GitHub App/PAT connectors. No local folder projects.",
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

func handleHubOrganizations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	if !peerOPAConfigured() {
		writeJSON(w, map[string]interface{}{
			"organizations": []map[string]interface{}{
				{"id": "default-org", "agent_count": 0, "source": "local_default"},
			},
			"note": "PEER_OPA_URL unset — returning default-org only. Set PEER_OPA_URL for hub tenancy discovery.",
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	out, err := peerHubOrganizations(ctx)
	if err != nil {
		writeError(w, 502, "hub unavailable: "+err.Error())
		return
	}
	writeJSON(w, out)
}

func handleGitHubConnectors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	if !peerORAConfigured() {
		writeError(w, 503, "PEER_ORA_URL not configured — GitHub connectors live in ORA")
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organizationId"))
	if orgID == "" {
		orgID = strings.TrimSpace(r.Header.Get("X-Organization-ID"))
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, err := peerListConnectors(ctx, orgID)
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
	orgID := strings.TrimSpace(r.URL.Query().Get("organizationId"))
	if orgID == "" {
		orgID = strings.TrimSpace(r.Header.Get("X-Organization-ID"))
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	out, err := peerListConnectorRepos(ctx, orgID, connectorID)
	if err != nil {
		writeError(w, 502, "ora unavailable: "+err.Error())
		return
	}
	writeJSON(w, out)
}

func orgHeader(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("X-Organization-ID"))
}

// decodeLinkProjectBody parses POST /api/projects for GitHub link payloads.
func decodeLinkProjectBody(r *http.Request) (Project, error) {
	var body struct {
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
