package main

import (
	"net/http"
	"strings"

	openauth "github.com/TheGrimmChester/open-auth-go"
)

// Acting identity at the enqueue boundary.
//
// A job used to carry no identity at all, so it could only ever use one
// deployment-wide credential. Stamping the requester here is what makes
// "resolve THIS user's key and model" possible at run time — and it is the reason
// a board drag or an API call is attributable afterwards.

// actorFromRequest reads the authenticated username from the request.
//
// Open-Auth-Go's middleware sets X-User-Username after validating the token, so
// that is the trusted source. The Bearer/cookie fallback exists for the
// auth-disabled deployment (the middleware is skipped there, but the console
// still sends a login token) — user-scoped credentials and model overrides key off
// the username, so losing it would silently downgrade every job to org scope.
func actorFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if u := strings.TrimSpace(r.Header.Get("X-User-Username")); u != "" {
		return u
	}
	if claims := claimsFromRequestToken(r); claims != nil {
		return strings.TrimSpace(claims.Username)
	}
	return ""
}

// jobOriginFromRequest builds the origin for a human-initiated job.
func jobOriginFromRequest(r *http.Request, override *modelOverride) JobOrigin {
	return JobOrigin{
		OrganizationID: resolveRequestOrg(r),
		ActorUsername:  actorFromRequest(r),
		ModelOverride:  override,
	}
}

// claimsFromRequestToken parses a Bearer header or the auth cookie without
// enforcing auth. Returns nil when absent or invalid — never an error, because a
// missing identity is a normal state (an unauthenticated lab deployment), not a
// failure to report.
func claimsFromRequestToken(r *http.Request) *openauth.UserClaims {
	token := ""
	if authHeader := r.Header.Get("Authorization"); authHeader != "" {
		parts := strings.Split(authHeader, " ")
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			token = parts[1]
		}
	} else if c, err := r.Cookie(openauth.CookieName); err == nil {
		token = c.Value
	}
	if token == "" || authGate == nil {
		return nil
	}
	claims, err := authGate.ParseUser(token)
	if err != nil {
		return nil
	}
	return claims
}
