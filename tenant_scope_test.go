package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	openauth "github.com/TheGrimmChester/open-auth-go"
)

func TestJobOrgFromJWT(t *testing.T) {
	prevGate := authGate
	t.Cleanup(func() { authGate = prevGate })

	secret := "test-jwt-secret-at-least-32-bytes-ok!!"
	t.Setenv("JWT_SECRET", secret)
	initAuthGate()
	if authGate == nil {
		t.Fatal("expected auth gate")
	}

	mint := func(accountType, orgID string) string {
		t.Helper()
		tok, err := openauth.MintUserJWTWithAccount(
			authGate.Secret, "dev", "viewer", "opm-api", accountType, orgID, nil, 0,
		)
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		return tok
	}

	reqWithToken := func(tok string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/projects/p1/jobs", nil)
		if tok != "" {
			r.Header.Set("Authorization", "Bearer "+tok)
		}
		return r
	}

	t.Run("no_token", func(t *testing.T) {
		if got := jobOrgFromJWT(reqWithToken("")); got != "" {
			t.Fatalf("got %q want empty", got)
		}
	})

	t.Run("personal_account", func(t *testing.T) {
		tok := mint(openauth.AccountTypePersonal, "")
		if got := jobOrgFromJWT(reqWithToken(tok)); got != "" {
			t.Fatalf("got %q want empty (never invent default-org)", got)
		}
		if got := resolveRequestOrg(reqWithToken(tok)); got != "" {
			t.Fatalf("resolveRequestOrg got %q want empty", got)
		}
	})

	t.Run("organization_account", func(t *testing.T) {
		tok := mint(openauth.AccountTypeOrganization, "acme")
		if got := jobOrgFromJWT(reqWithToken(tok)); got != "acme" {
			t.Fatalf("got %q want acme", got)
		}
	})

	t.Run("legacy_org_bound", func(t *testing.T) {
		tok, err := openauth.MintUserJWTWithACL(authGate.Secret, "dev", "viewer", "opm-api", "legacy-org", nil, 0)
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		if got := jobOrgFromJWT(reqWithToken(tok)); got != "legacy-org" {
			t.Fatalf("got %q want legacy-org", got)
		}
	})

	t.Run("legacy_unbound_admin", func(t *testing.T) {
		tok, err := openauth.MintUserJWT(authGate.Secret, "admin", "admin", "opm-api", 0)
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		if got := jobOrgFromJWT(reqWithToken(tok)); got != "" {
			t.Fatalf("got %q want empty (header fallback)", got)
		}
	})
}
