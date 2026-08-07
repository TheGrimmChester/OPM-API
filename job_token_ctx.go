package main

import (
	"context"
	"strings"
	"time"

	openclient "github.com/TheGrimmChester/open-client-go"
)

type jobTokenCtxKey struct{}

// jobPeerScopes covers ORA peer calls made during one OPM job (clone, PM, delivery).
const jobPeerScopes = "scm:clone scm:pm scm:pr"

func withJobToken(ctx context.Context, token string) context.Context {
	token = strings.TrimSpace(token)
	if token == "" || ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, jobTokenCtxKey{}, token)
}

func jobTokenFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	v, ok := ctx.Value(jobTokenCtxKey{}).(string)
	v = strings.TrimSpace(v)
	return v, ok && v != ""
}

func applyJobTokenPeer(cfg openclient.PeerConfig, ctx context.Context) openclient.PeerConfig {
	if tok, ok := jobTokenFromContext(ctx); ok {
		cfg.BearerToken = tok
	}
	return cfg
}

// beginJobPeerAuth mints a short-lived job token for ORA peer calls during one job.
func beginJobPeerAuth(ctx context.Context, j Job) (context.Context, func()) {
	agentKey := strings.TrimSpace(j.AgentKey)
	if agentKey == "" {
		agentKey = agentKeyForAction(j.Action)
	}
	tok, err := mintJobToken(ctx, "ora-api", jobPeerScopes, j.OrganizationID, j.ActorUsername, agentKey, 15*time.Minute)
	if err != nil {
		return ctx, func() {}
	}
	return withJobToken(ctx, tok.Token), func() { revokeJobToken(ctx, tok.JTI) }
}
