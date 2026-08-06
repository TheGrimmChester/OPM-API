package main

import "net/http"

var authEnforced bool

func setAuthEnforced(v bool) {
	authEnforced = v
}

func registerPeerAuth(mux *http.ServeMux, pattern, readScope, writeScope string, h http.HandlerFunc) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if !authEnforced {
			h(w, r)
			return
		}
		scope := writeScope
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			scope = readScope
		}
		AuthUserOrServiceMiddleware(h, "viewer", scope)(w, r)
	})
}
