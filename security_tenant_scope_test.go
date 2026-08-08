package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	openauth "github.com/TheGrimmChester/open-auth-go"
	opentenant "github.com/TheGrimmChester/open-tenant-go"
)

func securityTenantAuthSetup(t *testing.T) {
	t.Helper()
	prevGate := authGate
	t.Cleanup(func() { authGate = prevGate })

	secret := "test-jwt-secret-at-least-32-bytes-ok!!"
	t.Setenv("JWT_SECRET", secret)
	t.Setenv("OPA_AUTH_REQUIRED", "1")
	t.Setenv("AUTH_MODE", "codeployed")
	t.Setenv("PEER_OPA_URL", "http://127.0.0.1:18080")
	initAuthGate()
	if authGate == nil {
		t.Fatal("expected auth gate")
	}
	opentenant.SetAuthEnforced(true)
	t.Cleanup(func() { opentenant.SetAuthEnforced(false) })
}

func TestSecurityTenantGetProjectForeignOrgHTTP(t *testing.T) {
	opentenant.SetAuthEnforced(true)
	t.Cleanup(func() { opentenant.SetAuthEnforced(false) })

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := store.CreateProject(Project{
		OwnerRepo: "acme/a", ConnectorID: "c1", OrganizationID: "org-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(Project{
		OwnerRepo: "acme/b", ConnectorID: "c1", OrganizationID: "org-b",
	}); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	registerOPMMux(mux, store, func(path string, h http.HandlerFunc) {
		mux.HandleFunc(path, h)
	}, func(string, http.HandlerFunc) {})

	// Same-org GET succeeds.
	okReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+a.ID, nil)
	okReq.Header.Set("X-Organization-ID", "org-a")
	okRec := httptest.NewRecorder()
	mux.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Fatalf("same-org GET: status=%d body=%s", okRec.Code, okRec.Body.String())
	}

	// Foreign org must 404 (never 200) — IDOR blocked at GetProjectForOrg.
	badReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+a.ID, nil)
	badReq.Header.Set("X-Organization-ID", "org-b")
	badRec := httptest.NewRecorder()
	mux.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusNotFound {
		t.Fatalf("foreign org GET: status=%d want 404 body=%s", badRec.Code, badRec.Body.String())
	}
	if badRec.Code == http.StatusOK {
		t.Fatal("foreign org must never return 200")
	}
}

func TestSecurityTenantAuthMiddleware(t *testing.T) {
	securityTenantAuthSetup(t)

	h := AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, "viewer")

	t.Run("no_jwt_401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("code=%d want 401", rec.Code)
		}
	})

	t.Run("personal_plus_org_403", func(t *testing.T) {
		tok, err := openauth.MintUserJWTWithAccount(
			authGate.Secret, "alice", "viewer", "opm-api",
			openauth.AccountTypePersonal, "", nil, 0,
		)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("X-Organization-ID", "acme")
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("code=%d want 403 body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("foreign_org_403", func(t *testing.T) {
		tok, err := openauth.MintUserJWTWithAccount(
			authGate.Secret, "bob", "viewer", "opm-api",
			openauth.AccountTypeOrganization, "acme", nil, 0,
		)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("X-Organization-ID", "other-org")
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("code=%d want 403 body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("project_ids_over_acl_403", func(t *testing.T) {
		tok, err := openauth.MintUserJWTWithACL(
			authGate.Secret, "dev", "viewer", "opm-api",
			"acme", []string{"alpha", "beta"}, 0,
		)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("X-Organization-ID", "acme")
		req.Header.Set(openauth.HeaderProjectIDs, "alpha,gamma")
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("code=%d want 403 body=%s", rec.Code, rec.Body.String())
		}
	})
}

func TestSecurityTenantEmptyOrgListExcludesDefaultOrgHTTP(t *testing.T) {
	securityTenantAuthSetup(t)

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(Project{
		OwnerRepo: "acme/shared", ConnectorID: "c1", OrganizationID: "default-org",
	}); err != nil {
		t.Fatal(err)
	}
	personal, err := store.CreateProject(Project{
		OwnerRepo: "acme/mine", ConnectorID: "c1", OrganizationID: "",
	})
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	registerOPMMux(mux, store, func(path string, h http.HandlerFunc) {
		mux.HandleFunc(path, AuthMiddleware(h, "viewer"))
	}, func(string, http.HandlerFunc) {})

	// Unbound admin with no org header: list must not silently invent default-org.
	adminTok, err := openauth.MintUserJWT(authGate.Secret, "root", "admin", "opm-api", 0)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Projects []Project `json:"projects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("json: %v body=%s", err, rec.Body.String())
	}
	if len(out.Projects) != 1 || out.Projects[0].ID != personal.ID {
		t.Fatalf("empty-org list under auth must be personal-only, never default-org fixture: %+v", out.Projects)
	}
	for _, p := range out.Projects {
		if p.OrganizationID == "default-org" {
			t.Fatalf("must not silently return default-org-only rows: %+v", p)
		}
	}

	// Personal JWT likewise sees only empty-org rows.
	personalTok, err := openauth.MintUserJWTWithAccount(
		authGate.Secret, "alice", "viewer", "opm-api",
		openauth.AccountTypePersonal, "", nil, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req2.Header.Set("Authorization", "Bearer "+personalTok)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("personal list status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	var out2 struct {
		Projects []Project `json:"projects"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &out2); err != nil {
		t.Fatal(err)
	}
	if len(out2.Projects) != 1 || out2.Projects[0].ID != personal.ID {
		t.Fatalf("personal list: %+v", out2.Projects)
	}
}
