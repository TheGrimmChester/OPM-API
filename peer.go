package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

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
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("PEER_OPA_URL")), "/")
	if base == "" {
		return nil, openclient.ErrPeerUnavailable
	}
	// Prefer service JWT when secret is configured; hub tenancy routes are open.
	cfg := peerOPAConfig("health:read")
	var out map[string]interface{}
	if err := openclient.PeerJSON(ctx, cfg, http.MethodGet, path, nil, &out); err == nil {
		return out, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/"+strings.TrimLeft(path, "/"), nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("hub %s: %s", path, resp.Status)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
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
