package main

import (
	"log"
	"net/http"

	openauth "github.com/TheGrimmChester/open-auth-go"
)

// Thin product wiring around Open-Auth-Go. No local JWT/middleware copies.
// OPM validates user JWTs (typically hub-issued); it does not register local login.
// Gate.Middleware calls ApplyUserTenantHeaders then EnforceProjectACL
// (Open-Auth-Go #6 / project_ids). Trust hub-minted claims; role admin bypasses.

var authGate *openauth.Gate

func authRequiredEnv() bool { return openauth.AuthRequiredEnv() }

func initAuthGate() {
	g, err := openauth.BootstrapFromEnv("opm-api", "opm-api")
	if err != nil {
		log.Fatalf("auth: %v", err)
	}
	authGate = g
}

// registerLocalAuthMux mounts /api/auth/status (+ login 503 in codeployed).
// OPM never issues local login tokens; hub is the identity home when co-deployed.
func registerLocalAuthMux(mux *http.ServeMux) {
	if authGate != nil {
		authGate.RegisterLocalAuth(mux)
	}
}

func AuthMiddleware(handler http.HandlerFunc, requiredRole string) http.HandlerFunc {
	return authGate.Middleware(requiredRole, handler)
}
