package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "version" {
		fmt.Println("opm-api-dev")
		return
	}
	addr := ":8096"
	if v := os.Getenv("LISTEN_ADDR"); v != "" {
		addr = v
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "opm-api"})
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("opm-api skeleton listening on %s", addr)
	log.Fatal(srv.ListenAndServe())
}
