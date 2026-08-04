package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	openclient "github.com/TheGrimmChester/open-client-go"
)

func peerORAConfig(orgID, scope string) openclient.PeerConfig {
	cfg := openclient.PeerFromEnv("PEER_ORA_URL", "opm-api", "ora-api", scope)
	cfg.OrgID = orgID
	return cfg
}

func peerOPAConfig(scope string) openclient.PeerConfig {
	return openclient.PeerFromEnv("PEER_OPA_URL", "opm-api", "opa-hub", scope)
}

func peerORAConfigured() bool {
	return strings.TrimSpace(os.Getenv("PEER_ORA_URL")) != ""
}

func peerOPAConfigured() bool {
	return strings.TrimSpace(os.Getenv("PEER_OPA_URL")) != ""
}

func peerListConnectors(ctx context.Context, orgID string) (map[string]interface{}, error) {
	cfg := peerORAConfig(orgID, "connectors:read")
	var out map[string]interface{}
	err := openclient.PeerJSON(ctx, cfg, http.MethodGet, "/api/connectors", nil, &out)
	return out, err
}

func peerListConnectorRepos(ctx context.Context, orgID, connectorID string) (map[string]interface{}, error) {
	cfg := peerORAConfig(orgID, "connectors:read")
	path := fmt.Sprintf("/api/connectors/%s/repos", connectorID)
	var out map[string]interface{}
	err := openclient.PeerJSON(ctx, cfg, http.MethodGet, path, nil, &out)
	return out, err
}

func peerCloneCredentials(ctx context.Context, orgID, connectorID, ownerRepo string) (map[string]interface{}, error) {
	cfg := peerORAConfig(orgID, "scm:clone")
	body := map[string]interface{}{
		"connector_id":   connectorID,
		"repo_full_name": ownerRepo,
	}
	var out map[string]interface{}
	err := openclient.PeerJSON(ctx, cfg, http.MethodPost, "/api/peer/scm/clone-credentials", body, &out)
	return out, err
}

func peerHubGET(ctx context.Context, path string) (map[string]interface{}, error) {
	if !peerOPAConfigured() {
		return nil, openclient.ErrPeerUnavailable
	}
	cfg := peerOPAConfig("health:read")
	var out map[string]interface{}
	if err := openclient.PeerJSON(ctx, cfg, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func peerHubOrganizations(ctx context.Context) (map[string]interface{}, error) {
	return peerHubGET(ctx, "/api/tenancy/organizations")
}

func peerHubGitHubStatus(ctx context.Context) (map[string]interface{}, error) {
	return peerHubGET(ctx, "/api/github/status")
}
