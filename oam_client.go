package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	openclient "github.com/TheGrimmChester/open-client-go"
)

// Peer client for OAM (Open Account Manager), the family's account and credential
// plane. Same shape as OSA's requestORACloneCredentials: PeerFromEnv mints a
// short-lived service JWT, PeerJSON makes the call.
//
// Everything here is gated on PEER_OAM_URL. With it unset, oamConfigured() is
// false and every caller falls back to the pre-OAM environment path — so a
// deployment that has not adopted OAM behaves exactly as it did before.

const oamResolveScope = "creds:resolve health:read"

func oamPeerURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("PEER_OAM_URL")), "/")
}

func oamConfigured() bool { return oamPeerURL() != "" }

// resolvedAgentBinding is the model configuration and credential for one job,
// resolved together by OAM.
//
// Together is the point: two calls (one for the model, one for the key) can
// disagree — an org's key paired with a user's model, or a rotation landing
// between the lookups — and a job must never run that combination.
type resolvedAgentBinding struct {
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	BaseURL     string `json:"base_url"`
	MaxTokens   uint32 `json:"max_tokens"`
	Effort      string `json:"effort"`
	Timeout     uint32 `json:"timeout_seconds"`
	APIKey      string `json:"api_key"`
	KeyScope    string `json:"key_scope"`
	ModelSource string `json:"model_source"`
	LogicalKey  string `json:"logical_key"`
	// AgentKeyKnown is false when OPM asked for an agent key it never published
	// to OAM's catalog. Resolution still succeeds against a default, but this
	// means a job kind was added here without being registered — the drift case,
	// where a new kind silently inherits another agent's model.
	AgentKeyKnown bool `json:"agent_key_known"`
}

// modelOverride is a per-task model choice: "plan with the light model, implement
// with the strong one on THIS task" without editing org configuration. It carries
// no credential — only a model choice.
type modelOverride struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

func (o *modelOverride) empty() bool {
	return o == nil || (strings.TrimSpace(o.Provider) == "" && strings.TrimSpace(o.Model) == "")
}

type oamResolveRequest struct {
	OrganizationID string         `json:"organization_id,omitempty"`
	ProjectID      string         `json:"project_id,omitempty"`
	UserID         string         `json:"user_id,omitempty"`
	Product        string         `json:"product"`
	AgentKey       string         `json:"agent_key"`
	Override       *modelOverride `json:"override,omitempty"`
}

// errOAMNotConfigured is returned when PEER_OAM_URL is unset. Callers treat it as
// "use the legacy env path", not as a failure.
var errOAMNotConfigured = fmt.Errorf("PEER_OAM_URL not configured")

// resolveAgentFromOAM asks OAM for the model + key this job should run with.
//
// A failure here is NOT silently swallowed into the env fallback: once a
// deployment has adopted OAM, a resolve error means the job would run with the
// wrong credentials or none, and the caller surfaces it. The one soft case is
// errOAMNotConfigured, which means OAM was never adopted.
func resolveAgentFromOAM(ctx context.Context, org, proj, actor, agentKey string, override *modelOverride) (resolvedAgentBinding, error) {
	var out resolvedAgentBinding
	if !oamConfigured() {
		return out, errOAMNotConfigured
	}
	agentKey = strings.TrimSpace(agentKey)
	if agentKey == "" {
		return out, fmt.Errorf("agent_key required")
	}
	cfg := openclient.PeerFromEnv("PEER_OAM_URL", "opm-api", "oam-api", oamResolveScope)
	cfg.OrgID = strings.TrimSpace(org)

	req := oamResolveRequest{
		OrganizationID: strings.TrimSpace(org),
		ProjectID:      strings.TrimSpace(proj),
		UserID:         strings.TrimSpace(actor),
		Product:        "opm",
		AgentKey:       agentKey,
	}
	if !override.empty() {
		req.Override = override
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if err := openclient.PeerJSON(ctx, cfg, http.MethodPost, "/api/agents/resolve", req, &out); err != nil {
		return resolvedAgentBinding{}, err
	}
	if strings.TrimSpace(out.APIKey) == "" {
		// OAM fails closed with 404 rather than returning an empty key, so this
		// is a contract violation, not a missing credential. Treat it as fatal
		// for the job rather than launching a runner with no key.
		return resolvedAgentBinding{}, fmt.Errorf("oam returned an empty api key for agent %q", agentKey)
	}
	if !out.AgentKeyKnown {
		log.Printf("oam: agent key %q is not in OPM's published catalog — "+
			"it resolved against a default binding. Publish it via /api/agents/catalog/publish.", agentKey)
	}
	return out, nil
}

// publishAgentCatalog registers OPM's agents with OAM on boot.
//
// Without this a newly added job kind resolves against the product default and
// nobody notices. It is best-effort: OAM being unavailable must not stop OPM from
// serving, so the failure is logged and the boot continues.
func publishAgentCatalog(ctx context.Context) {
	if !oamConfigured() {
		return
	}
	cfg := openclient.PeerFromEnv("PEER_OAM_URL", "opm-api", "oam-api", "catalog:write")
	body := map[string]interface{}{"product": "opm", "agents": agentCatalog()}
	var out struct {
		Agents []string `json:"agents"`
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := openclient.PeerJSON(ctx, cfg, http.MethodPost, "/api/agents/catalog/publish", body, &out); err != nil {
		log.Printf("oam: agent catalog publish failed (continuing): %v", err)
		return
	}
	log.Printf("oam: published %d agent keys: %s", len(out.Agents), strings.Join(out.Agents, ","))
}
