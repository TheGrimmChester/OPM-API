package main

import (
	"log"
	"net/http"

	openauth "github.com/TheGrimmChester/open-auth-go"
)

// Thin product wiring around Open-Auth-Go. No local JWT/middleware copies.
// OPM validates user JWTs (typically hub-issued); it does not register local login.

var authGate *openauth.Gate

func authRequiredEnv() bool { return openauth.AuthRequiredEnv() }

func initAuthGate() {
	g, err := openauth.BootstrapFromEnv("opm-api", "opm-api")
	if err != nil {
		log.Fatalf("auth: %v", err)
	}
	authGate = g
}

func AuthMiddleware(handler http.HandlerFunc, requiredRole string) http.HandlerFunc {
	return authGate.Middleware(requiredRole, handler)
}
