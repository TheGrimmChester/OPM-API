package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequireEnabledOAMProjectSkipsWhenUnset(t *testing.T) {
	t.Setenv("PEER_OAM_URL", "")
	req := httptest.NewRequest(http.MethodPost, "/api/projects", nil)
	req.Header.Set("X-Project-ID", "checkout-api")
	if st, msg := requireEnabledOAMProject(req, "opm"); st != 0 || msg != "" {
		t.Fatalf("expected skip, got %d %q", st, msg)
	}
}

func TestRequireEnabledOAMProjectSkipsAll(t *testing.T) {
	t.Setenv("PEER_OAM_URL", "http://oam.invalid")
	req := httptest.NewRequest(http.MethodPost, "/api/projects", nil)
	req.Header.Set("X-Project-ID", "all")
	if st, msg := requireEnabledOAMProject(req, "opm"); st != 0 || msg != "" {
		t.Fatalf("expected skip for all, got %d %q", st, msg)
	}
}

func TestOAMDirectoryHasProject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "product=opm") {
			t.Errorf("missing product: %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"projects": []map[string]string{{"id": "web"}, {"id": "api"}},
		})
	}))
	defer srv.Close()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ok, err := oamDirectoryHasProject(req.Context(), req, srv.URL, "opm", "web")
	if err != nil || !ok {
		t.Fatalf("want found, got ok=%v err=%v", ok, err)
	}
	ok, err = oamDirectoryHasProject(req.Context(), req, srv.URL, "opm", "missing")
	if err != nil || ok {
		t.Fatalf("want missing, got ok=%v err=%v", ok, err)
	}
}

func TestRequireEnabledOAMProjectRejectsDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"projects": []map[string]string{{"id": "enabled-only"}},
		})
	}))
	defer srv.Close()
	t.Setenv("PEER_OAM_URL", srv.URL)

	req := httptest.NewRequest(http.MethodPost, "/api/projects", nil)
	req.Header.Set("X-Project-ID", "disabled-here")
	st, msg := requireEnabledOAMProject(req, "opm")
	if st != 403 || msg == "" {
		t.Fatalf("want 403, got %d %q", st, msg)
	}
}
