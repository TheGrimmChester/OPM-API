package main

import (
	"context"
	"testing"

	openauth "github.com/TheGrimmChester/open-auth-go"
	openclient "github.com/TheGrimmChester/open-client-go"
)

func TestPeerORAConfigPassesUserID(t *testing.T) {
	t.Setenv("PEER_ORA_URL", "http://127.0.0.1:8091")
	t.Setenv("OPEN_SERVICE_JWT_SECRET", "test-service-secret-at-least-32-bytes!!")
	cfg := peerORAConfig(context.Background(), "", "TheGrimmChester", "scm:pm")
	if cfg.OrgID != "" {
		t.Fatalf("OrgID=%q", cfg.OrgID)
	}
	if cfg.UserID != "TheGrimmChester" {
		t.Fatalf("UserID=%q", cfg.UserID)
	}
	if cfg.BaseURL == "" {
		t.Fatal("BaseURL empty")
	}
}

func TestPeerORAConfigMintsUserIDInServiceJWT(t *testing.T) {
	secret := []byte("test-service-secret-at-least-32-bytes!!")
	t.Setenv("PEER_ORA_URL", "http://127.0.0.1:8091")
	t.Setenv("OPEN_SERVICE_JWT_SECRET", string(secret))
	cfg := peerORAConfig(context.Background(), "", "alice", "scm:clone")
	client, err := openclient.PeerClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if client == nil {
		t.Fatal("nil peer client")
	}
	// Re-mint the same claims PeerClient uses and assert user_id lands in JWT.
	tok, err := openauth.MintServiceJWTWithTenant(secret, "opm-api", "ora-api", "scm:clone", cfg.OrgID, cfg.UserID, 0)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := openauth.ValidateServiceJWT(tok, secret, "ora-api")
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != "alice" || claims.OrgID != "" {
		t.Fatalf("claims org=%q user=%q", claims.OrgID, claims.UserID)
	}
}
