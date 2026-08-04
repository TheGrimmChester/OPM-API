package main

import (
	"encoding/json"
	openhttp "github.com/TheGrimmChester/open-http-go"
	"net/http"
	"os"
	"strings"
	"time"
)

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	openhttp.WriteError(w, status, "error", msg)
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func corsMiddleware(next http.Handler) http.Handler {
	return openhttp.MiddlewareCORS(next)
}

func nowUTC() time.Time { return time.Now().UTC() }

func nz(s, d string) string {
	if strings.TrimSpace(s) != "" {
		return s
	}
	return d
}
