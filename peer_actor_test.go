package main

import (
	"context"
	"testing"
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
