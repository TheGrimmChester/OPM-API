package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func peerORAConfigPM(orgID string) openclient.PeerConfig {
	return peerORAConfig(orgID, "scm:pm")
}

func peerListMilestones(ctx context.Context, orgID, connectorID, ownerRepo string) (map[string]interface{}, error) {
	cfg := peerORAConfigPM(orgID)
	body := map[string]interface{}{
		"connector_id":   connectorID,
		"repo_full_name": ownerRepo,
	}
	var out map[string]interface{}
	err := openclient.PeerJSON(ctx, cfg, http.MethodPost, "/api/peer/scm/milestones/list", body, &out)
	return out, err
}

func peerUpsertMilestone(ctx context.Context, orgID, connectorID, ownerRepo string, number int, title, description, state string) (map[string]interface{}, error) {
	cfg := peerORAConfigPM(orgID)
	body := map[string]interface{}{
		"connector_id":   connectorID,
		"repo_full_name": ownerRepo,
		"number":         number,
		"title":          title,
		"description":    description,
		"state":          state,
	}
	var out map[string]interface{}
	err := openclient.PeerJSON(ctx, cfg, http.MethodPost, "/api/peer/scm/milestones/upsert", body, &out)
	return out, err
}

func peerListGitHubProjects(ctx context.Context, orgID, connectorID, ownerRepo string) (map[string]interface{}, error) {
	cfg := peerORAConfigPM(orgID)
	body := map[string]interface{}{
		"connector_id":   connectorID,
		"repo_full_name": ownerRepo,
	}
	var out map[string]interface{}
	err := openclient.PeerJSON(ctx, cfg, http.MethodPost, "/api/peer/scm/projects/list", body, &out)
	return out, err
}

func peerUpsertProjectItem(ctx context.Context, orgID, connectorID, projectID, itemID, title, bodyText, statusHint string) (map[string]interface{}, error) {
	cfg := peerORAConfigPM(orgID)
	body := map[string]interface{}{
		"connector_id": connectorID,
		"project_id":   projectID,
		"item_id":      itemID,
		"title":        title,
		"body":         bodyText,
		"status_hint":  statusHint,
	}
	var out map[string]interface{}
	err := openclient.PeerJSON(ctx, cfg, http.MethodPost, "/api/peer/scm/projects/items/upsert", body, &out)
	return out, err
}

func peerSetProjectItemStatus(ctx context.Context, orgID, connectorID, projectID, itemID, statusHint string) (map[string]interface{}, error) {
	cfg := peerORAConfigPM(orgID)
	body := map[string]interface{}{
		"connector_id": connectorID,
		"project_id":   projectID,
		"item_id":      itemID,
		"status_hint":  statusHint,
	}
	var out map[string]interface{}
	err := openclient.PeerJSON(ctx, cfg, http.MethodPost, "/api/peer/scm/projects/items/status", body, &out)
	return out, err
}

// peerIssueFault is a failed ORA Issues call, carrying ORA's machine-readable
// status and full error text. PeerJSON collapses non-2xx into a truncated string,
// which would drop the status ORA sends, so Issues calls read the response
// directly and keep it intact.
type peerIssueFault struct {
	HTTPStatus int
	Status     string
	Message    string
	Detail     string
	Missing    []string
}

func (e *peerIssueFault) Error() string {
	msg := e.Message
	if msg == "" {
		msg = "github issue call failed"
	}
	if e.Detail != "" {
		msg += " (" + e.Detail + ")"
	}
	return msg
}

// peerIssueCall posts to an ORA Issues peer endpoint and returns the parsed body
// plus a *peerIssueFault on any non-2xx, so the concrete reason survives.
func peerIssueCall(ctx context.Context, orgID, path string, body map[string]interface{}) (map[string]interface{}, error) {
	cfg := peerORAConfigPM(orgID)
	client, err := openclient.PeerClient(cfg)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.OrgID != "" {
		req.Header.Set("X-Organization-ID", cfg.OrgID)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respRaw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var out map[string]interface{}
	// A non-JSON body (e.g. http.Error text) is still reported verbatim below.
	_ = json.Unmarshal(respRaw, &out)
	if resp.StatusCode >= 300 {
		fault := &peerIssueFault{HTTPStatus: resp.StatusCode}
		if out != nil {
			fault.Status = strFromAny(out["status"])
			fault.Message = strFromAny(out["error"])
			fault.Detail = strFromAny(out["detail"])
			if list, ok := out["missing"].([]interface{}); ok {
				for _, m := range list {
					if s := strFromAny(m); s != "" {
						fault.Missing = append(fault.Missing, s)
					}
				}
			}
		}
		if fault.Message == "" {
			fault.Message = strings.TrimSpace(string(respRaw))
			if fault.Message == "" {
				fault.Message = resp.Status
			}
		}
		if fault.Status == "" {
			fault.Status = "upstream_error"
		}
		return out, fault
	}
	if out == nil {
		return nil, fmt.Errorf("ora returned a non-JSON body for %s", path)
	}
	return out, nil
}

// peerGetIssue reads a single issue from the project's linked repo.
func peerGetIssue(ctx context.Context, orgID, connectorID, ownerRepo string, number int) (map[string]interface{}, error) {
	return peerIssueCall(ctx, orgID, "/api/peer/scm/issues/get", map[string]interface{}{
		"connector_id":   connectorID,
		"repo_full_name": ownerRepo,
		"number":         number,
	})
}

func peerCreateIssue(ctx context.Context, orgID, connectorID, ownerRepo, title, bodyText string, labels []string, milestone int) (map[string]interface{}, error) {
	return peerIssueCall(ctx, orgID, "/api/peer/scm/issues/create", map[string]interface{}{
		"connector_id":   connectorID,
		"repo_full_name": ownerRepo,
		"title":          title,
		"body":           bodyText,
		"labels":         labels,
		"milestone":      milestone,
	})
}

// peerUpdateIssue patches an issue. Blank title/body/state are left untouched by
// ORA; milestone is >0 set, <0 clear, 0 leave alone.
func peerUpdateIssue(ctx context.Context, orgID, connectorID, ownerRepo string, number int, title, bodyText, state string, milestone int, labels []string) (map[string]interface{}, error) {
	body := map[string]interface{}{
		"connector_id":   connectorID,
		"repo_full_name": ownerRepo,
		"number":         number,
		"title":          title,
		"body":           bodyText,
		"state":          state,
		"milestone":      milestone,
	}
	if labels != nil {
		body["labels"] = labels
	}
	return peerIssueCall(ctx, orgID, "/api/peer/scm/issues/update", body)
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
