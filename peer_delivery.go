package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	openclient "github.com/TheGrimmChester/open-client-go"
)

// ORA peer calls for code delivery, under the narrow scope scm:pr.
//
// OPM holds no GitHub App key and no PAT. It asks ORA for a short-lived
// Contents-write credential for exactly one push, and asks ORA to open the pull
// request. Both live behind scm:pr so the issue/milestone scope (scm:pm) can
// never be used to write code.

// peerPRScope is the service-JWT scope for the delivery surface.
const peerPRScope = "scm:pr"

// peerDeliveryFault is a failed ORA delivery call carrying ORA's machine-readable
// status alongside the full error text. PeerJSON collapses non-2xx into a
// truncated string, which would drop the status, so these calls read the response
// directly and keep it intact.
type peerDeliveryFault struct {
	HTTPStatus int
	Status     string
	Message    string
	Detail     string
	Missing    []string
}

func (e *peerDeliveryFault) Error() string {
	msg := e.Message
	if msg == "" {
		msg = "delivery call failed"
	}
	if e.Detail != "" {
		msg += " (" + e.Detail + ")"
	}
	return msg
}

// peerDeliveryCall posts to an ORA delivery endpoint and returns the parsed body
// plus a *peerDeliveryFault on any non-2xx, so the concrete reason survives.
func peerDeliveryCall(ctx context.Context, orgID, userID, path string, body map[string]interface{}) (map[string]interface{}, error) {
	cfg := peerORAConfig(ctx, orgID, userID, peerPRScope)
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
		fault := &peerDeliveryFault{HTTPStatus: resp.StatusCode}
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

// pushCredential is the request-scoped credential for one delivery push. It is
// deliberately not a struct field on anything long-lived: it exists for the
// duration of the push and is then discarded.
type pushCredential struct {
	Token    string
	CloneURL string
}

// peerPushCredentials asks ORA for a Contents-write credential for this repo.
func peerPushCredentials(ctx context.Context, orgID, userID, connectorID, ownerRepo string) (pushCredential, error) {
	out, err := peerDeliveryCall(ctx, orgID, userID, "/api/peer/scm/push-credentials", map[string]interface{}{
		"connector_id":   connectorID,
		"repo_full_name": ownerRepo,
	})
	if err != nil {
		return pushCredential{}, err
	}
	cred := pushCredential{
		Token:    strFromAny(out["token"]),
		CloneURL: strFromAny(out["clone_url"]),
	}
	if cred.Token == "" || cred.CloneURL == "" {
		return pushCredential{}, fmt.Errorf("ora returned incomplete push credentials")
	}
	return cred, nil
}

// pullRequestMeta is the normalized pull request decoded from an ORA response.
type pullRequestMeta struct {
	Number  int
	HTMLURL string
	State   string
	Title   string
	HeadRef string
	BaseRef string
	Draft   bool
	// AlreadyExisted is true when ORA resolved an open PR instead of creating one.
	AlreadyExisted bool
}

// peerCreatePullRequest opens the delivery pull request through ORA.
func peerCreatePullRequest(ctx context.Context, orgID, userID, connectorID, ownerRepo, title, body, head, base string, draft bool) (pullRequestMeta, error) {
	out, err := peerDeliveryCall(ctx, orgID, userID, "/api/peer/scm/pull-requests/create", map[string]interface{}{
		"connector_id":   connectorID,
		"repo_full_name": ownerRepo,
		"title":          title,
		"body":           body,
		"head":           head,
		"base":           base,
		"draft":          draft,
	})
	if err != nil {
		return pullRequestMeta{}, err
	}
	raw, ok := out["pull_request"].(map[string]interface{})
	if !ok {
		return pullRequestMeta{}, fmt.Errorf("ora response did not contain a pull_request object")
	}
	meta := pullRequestMeta{
		Number:  intFromAny(raw["number"]),
		HTMLURL: strFromAny(raw["html_url"]),
		State:   strFromAny(raw["state"]),
		Title:   strFromAny(raw["title"]),
		HeadRef: strFromAny(raw["head_ref"]),
		BaseRef: strFromAny(raw["base_ref"]),
	}
	if v, ok := raw["draft"].(bool); ok {
		meta.Draft = v
	}
	if v, ok := out["already_existed"].(bool); ok {
		meta.AlreadyExisted = v
	}
	if meta.Number <= 0 {
		return pullRequestMeta{}, fmt.Errorf("ora response contained no pull request number")
	}
	if meta.State == "" {
		meta.State = "open"
	}
	return meta, nil
}

// peerMergePullRequest merges an open delivery pull request through ORA.
func peerMergePullRequest(ctx context.Context, orgID, userID, connectorID, ownerRepo string, prNumber int) (pullRequestMeta, error) {
	out, err := peerDeliveryCall(ctx, orgID, userID, "/api/peer/scm/pull-requests/merge", map[string]interface{}{
		"connector_id":   connectorID,
		"repo_full_name": ownerRepo,
		"number":         prNumber,
	})
	if err != nil {
		return pullRequestMeta{}, err
	}
	raw, ok := out["pull_request"].(map[string]interface{})
	if !ok {
		return pullRequestMeta{}, fmt.Errorf("ora response did not contain a pull_request object")
	}
	meta := pullRequestMeta{
		Number:  intFromAny(raw["number"]),
		HTMLURL: strFromAny(raw["html_url"]),
		State:   strFromAny(raw["state"]),
		Title:   strFromAny(raw["title"]),
		HeadRef: strFromAny(raw["head_ref"]),
		BaseRef: strFromAny(raw["base_ref"]),
	}
	if meta.Number <= 0 {
		meta.Number = prNumber
	}
	if meta.State == "" {
		meta.State = "merged"
	}
	return meta, nil
}
