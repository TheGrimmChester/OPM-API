package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	openauth "github.com/TheGrimmChester/open-auth-go"
	openjob "github.com/TheGrimmChester/open-job-go"
	openlogger "github.com/TheGrimmChester/open-logger-go"
)

var buildVersion = "opm-api-dev"

func main() {
	openlogger.LogInfo("opm-api starting", map[string]interface{}{"version": buildVersion})
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "orchestrator":
			runOrchestrator()
			return
		case "version":
			fmt.Println(buildVersion)
			return
		}
	}

	addr := envOr("LISTEN_ADDR", envOr("HTTP_ADDR", ":8096"))
	dataDir := envOr("OPM_DATA_DIR", "")

	store, err := NewStore(dataDir)
	if err != nil {
		log.Fatalf("store: %v", err)
	}

	initAuthGate()
	authRequired := authRequiredEnv()
	if authRequired {
		log.Printf("auth: ENABLED (OPA_AUTH_REQUIRED)")
	} else {
		log.Printf("auth: disabled — endpoints open")
	}

	mux := http.NewServeMux()
	authView := func(pattern string, h http.HandlerFunc) {
		if authRequired {
			mux.HandleFunc(pattern, AuthMiddleware(h, "viewer"))
		} else {
			mux.HandleFunc(pattern, h)
		}
	}
	authAdmin := func(pattern string, h http.HandlerFunc) {
		if authRequired {
			mux.HandleFunc(pattern, AuthMiddleware(h, "admin"))
		} else {
			mux.HandleFunc(pattern, h)
		}
	}
	_ = authAdmin

	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		mode := openauth.ModeCodeployed
		if authGate != nil {
			mode = authGate.Mode
		}
		writeJSON(w, map[string]interface{}{
			"status":    "ok",
			"service":   "opm-api",
			"version":   buildVersion,
			"auth_mode": string(mode),
		})
	})

	registerLocalAuthMux(mux)
	registerOPMMux(mux, store, authView, authAdmin)
	registerServiceAuthProbe(mux)

	srv := &http.Server{
		Addr:              addr,
		Handler:           corsMiddleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	log.Printf("opm-api listening on %s (data=%s)", addr, nz(dataDir, "~/.config/opm"))
	_ = openjob.RunnerImage("opm", "task", envOr("OPM_RUNNER_TAG", "smoke"))
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
}

func runOrchestrator() {
	addr := envOr("ORCHESTRATOR_LISTEN_ADDR", ":8099")
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"status":  "ok",
			"service": "opm-orchestrator",
			"version": buildVersion,
			"runners": []string{openjob.RunnerImage("opm", "task", envOr("OPM_RUNNER_TAG", "smoke"))},
		})
	})
	log.Printf("opm-orchestrator listening on %s (job scheduler stub; one container per task-automation run)", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func registerServiceAuthProbe(mux *http.ServeMux) {
	mux.HandleFunc("/api/peer/health", func(w http.ResponseWriter, r *http.Request) {
		secret := []byte(strings.TrimSpace(os.Getenv("OPEN_SERVICE_JWT_SECRET")))
		if len(secret) == 0 {
			writeJSON(w, map[string]interface{}{"status": "ok", "service_auth": "disabled"})
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "unauthorized", 401)
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		claims, err := openauth.ValidateServiceJWT(token, secret, "opm-api")
		if err != nil {
			http.Error(w, "invalid service token", 401)
			return
		}
		if err := openauth.RequireScope(claims, "health:read"); err != nil {
			http.Error(w, "missing scope", 403)
			return
		}
		writeJSON(w, map[string]interface{}{
			"status":  "ok",
			"service": "opm-api",
			"iss":     claims.Issuer,
			"scope":   claims.Scope,
		})
	})
}
