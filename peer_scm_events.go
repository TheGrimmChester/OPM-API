package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// SCM checker fan-out from ora-api. Stub this ship — future checker:
//   opm:delivery — PR merge / issue events → task state sync via ORA peer SCM APIs.

const peerSCMEventsScope = "scm:events"

func registerPeerSCMMux(mux *http.ServeMux) {
	registerPeerAuth(mux, "/api/peer/scm/events", peerSCMEventsScope, peerSCMEventsScope, handlePeerSCMEvents)
}

type peerSCMEventRequest struct {
	ID             string   `json:"id,omitempty"`
	EventType      string   `json:"event_type"`
	OrganizationID string   `json:"organization_id"`
	ProjectID      string   `json:"project_id"`
	ConnectorID    string   `json:"connector_id"`
	RepoFullName   string   `json:"repo_full_name"`
	Ref            string   `json:"ref,omitempty"`
	DefaultBranch  string   `json:"default_branch,omitempty"`
	PRNumber       int      `json:"pr_number,omitempty"`
	CommitSHA      string   `json:"commit_sha"`
	SCMJobID       string   `json:"scm_job_id,omitempty"`
	ChangedPaths   []string `json:"changed_paths"`
	Checks         []string `json:"checks"`
	Dispatch       *bool    `json:"dispatch,omitempty"`
}

type peerCheckerResponse struct {
	ID           string `json:"id"`
	CheckRunName string `json:"check_run_name"`
	ShouldRun    bool   `json:"should_run"`
	Reason       string `json:"reason,omitempty"`
}

func handlePeerSCMEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body peerSCMEventRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	eventID := strings.TrimSpace(body.ID)
	if eventID == "" {
		eventID = strings.TrimSpace(body.CommitSHA)
	}
	if peerSCMEventDedupSeen(r.Context(), body.SCMJobID, eventID) {
		writeJSON(w, map[string]interface{}{
			"checkers":  []peerCheckerResponse{},
			"duplicate": true,
		})
		return
	}
	writeJSON(w, map[string]interface{}{
		"checkers": []peerCheckerResponse{},
		"note":     "future checker opm:delivery on PR merge / issue sync hooks",
	})
}
