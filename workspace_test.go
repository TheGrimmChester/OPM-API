package main

import (
	"strings"
	"testing"
)

func TestCloneErrorOutputRedactsToken(t *testing.T) {
	tok := "ghs_clone_token_abcdefgh"
	raw := []byte("fatal: authentication failed for user " + tok)
	got := redactSecret(truncateBytes(raw, 200), tok)
	if strings.Contains(got, tok) {
		t.Fatalf("clone error output leaked token: %q", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("expected redaction marker: %q", got)
	}
}
