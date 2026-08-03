package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	openjob "github.com/TheGrimmChester/open-job-go"
)

func registerOPMMux(mux *http.ServeMux, store *Store, authView, authAdmin func(string, http.HandlerFunc)) {
	registerDiscoveryMux(mux, authView)

	authView("/api/projects", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			projects, err := store.ListProjects()
			if err != nil {
				writeError(w, 500, err.Error())
				return
			}
			writeJSON(w, map[string]interface{}{"projects": projects})
		case http.MethodPost:
			in, err := decodeLinkProjectBody(r)
			if err != nil {
				if err == errLegacyPath {
					writeError(w, 400, err.Error())
					return
				}
				writeError(w, 400, "invalid json")
				return
			}
			if in.OrganizationID == "" {
				in.OrganizationID = orgHeader(r)
			}
			p, err := store.CreateProject(in)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, p)
		default:
			writeError(w, 405, "method not allowed")
		}
	})

	mux.HandleFunc("/api/projects/", func(w http.ResponseWriter, r *http.Request) {
		h := handleProjectRoute(store)
		if authRequiredEnv() {
			AuthMiddleware(h, "viewer")(w, r)
			return
		}
		h(w, r)
	})
}

func handleProjectRoute(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/projects/")
		path = strings.Trim(path, "/")
		if path == "" {
			writeError(w, 404, "not found")
			return
		}
		parts := strings.Split(path, "/")
		projectID := parts[0]

		if len(parts) == 1 {
			switch r.Method {
			case http.MethodGet:
				p, err := store.GetProject(projectID)
				if err != nil {
					writeError(w, 404, err.Error())
					return
				}
				writeJSON(w, p)
			case http.MethodDelete:
				if err := store.DeleteProject(projectID); err != nil {
					writeError(w, 404, err.Error())
					return
				}
				w.WriteHeader(http.StatusNoContent)
			default:
				writeError(w, 405, "method not allowed")
			}
			return
		}

		switch parts[1] {
		case "init":
			if r.Method != http.MethodPost {
				writeError(w, 405, "method not allowed")
				return
			}
			if err := store.InitProject(projectID); err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, map[string]string{"status": "ok"})
		case "board":
			handleBoard(w, r, store, projectID)
		case "tasks":
			handleTasks(w, r, store, projectID, parts[2:])
		case "roadmap":
			handleRoadmap(w, r, store, projectID)
		case "ideation":
			handleIdeation(w, r, store, projectID)
		case "changelog":
			handleChangelog(w, r, store, projectID)
		case "status":
			if r.Method != http.MethodGet {
				writeError(w, 405, "method not allowed")
				return
			}
			st, err := store.GetStatus(projectID)
			if err != nil {
				writeError(w, 404, err.Error())
				return
			}
			writeJSON(w, st)
		case "jobs":
			handleJobs(w, r, store, projectID, parts[2:])
		default:
			writeError(w, 404, "not found")
		}
	}
}

func handleBoard(w http.ResponseWriter, r *http.Request, store *Store, projectID string) {
	switch r.Method {
	case http.MethodGet:
		board, err := store.GetBoard(projectID)
		if err != nil {
			writeError(w, 404, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{
			"columns": BoardColumns,
			"board":   board,
		})
	case http.MethodPut:
		var board Board
		if err := json.NewDecoder(r.Body).Decode(&board); err != nil {
			writeError(w, 400, "invalid json")
			return
		}
		if err := store.PutBoard(projectID, board); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"columns": BoardColumns, "board": board})
	default:
		writeError(w, 405, "method not allowed")
	}
}

func handleTasks(w http.ResponseWriter, r *http.Request, store *Store, projectID string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			tasks, err := store.ListTasks(projectID)
			if err != nil {
				writeError(w, 404, err.Error())
				return
			}
			writeJSON(w, map[string]interface{}{"tasks": tasks})
		case http.MethodPost:
			var body struct {
				Title                     string `json:"title"`
				Description               string `json:"description"`
				RequireReviewBeforeCoding bool   `json:"requireReviewBeforeCoding"`
				IdeationID                string `json:"ideationId"`
				IdeationTypeKey           string `json:"ideationTypeKey"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, 400, "invalid json")
				return
			}
			t, err := store.CreateTask(projectID, body.Title, body.Description, body.RequireReviewBeforeCoding, body.IdeationID, body.IdeationTypeKey)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, t)
		default:
			writeError(w, 405, "method not allowed")
		}
		return
	}

	specID := rest[0]
	if len(rest) == 1 {
		switch r.Method {
		case http.MethodGet:
			t, err := store.GetTask(projectID, specID)
			if err != nil {
				writeError(w, 404, err.Error())
				return
			}
			writeJSON(w, t)
		case http.MethodPatch:
			var patch map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				writeError(w, 400, "invalid json")
				return
			}
			t, err := store.UpdateTask(projectID, specID, patch)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, t)
		case http.MethodDelete:
			if err := store.DeleteTask(projectID, specID); err != nil {
				writeError(w, 400, err.Error())
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, 405, "method not allowed")
		}
		return
	}

	switch rest[1] {
	case "move":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		var body struct {
			ToStatus string `json:"toStatus"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, 400, "invalid json")
			return
		}
		t, err := store.MoveTask(projectID, specID, body.ToStatus)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, t)
	case "plan":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		plan, err := store.GetPlan(projectID, specID)
		if err != nil {
			writeError(w, 404, err.Error())
			return
		}
		writeJSON(w, plan)
	case "progress":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		prog, err := store.GetProgress(projectID, specID)
		if err != nil {
			writeError(w, 404, err.Error())
			return
		}
		writeJSON(w, prog)
	default:
		writeError(w, 404, "not found")
	}
}

func handleRoadmap(w http.ResponseWriter, r *http.Request, store *Store, projectID string) {
	switch r.Method {
	case http.MethodGet:
		rm, err := store.GetRoadmap(projectID)
		if err != nil {
			writeError(w, 404, err.Error())
			return
		}
		writeJSON(w, rm)
	case http.MethodPut, http.MethodPost:
		var rm Roadmap
		if err := json.NewDecoder(r.Body).Decode(&rm); err != nil {
			writeError(w, 400, "invalid json")
			return
		}
		if err := store.PutRoadmap(projectID, rm); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, rm)
	default:
		writeError(w, 405, "method not allowed")
	}
}

func handleIdeation(w http.ResponseWriter, r *http.Request, store *Store, projectID string) {
	switch r.Method {
	case http.MethodGet:
		ideas, err := store.GetIdeation(projectID)
		if err != nil {
			writeError(w, 404, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"types": IdeationTypes, "ideation": ideas})
	case http.MethodPut, http.MethodPost:
		var ideas Ideation
		if err := json.NewDecoder(r.Body).Decode(&ideas); err != nil {
			writeError(w, 400, "invalid json")
			return
		}
		if err := store.PutIdeation(projectID, ideas); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"types": IdeationTypes, "ideation": ideas})
	default:
		writeError(w, 405, "method not allowed")
	}
}

func handleChangelog(w http.ResponseWriter, r *http.Request, store *Store, projectID string) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	content, err := store.GetChangelog(projectID)
	if err != nil {
		writeError(w, 404, err.Error())
		return
	}
	writeJSON(w, map[string]string{"markdown": content})
}

func handleJobs(w http.ResponseWriter, r *http.Request, store *Store, projectID string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			jobs, err := store.ListJobs(projectID)
			if err != nil {
				writeError(w, 404, err.Error())
				return
			}
			writeJSON(w, map[string]interface{}{"jobs": jobs})
		case http.MethodPost:
			var body struct {
				Action string `json:"action"`
				SpecID string `json:"specId"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, 400, "invalid json")
				return
			}
			body.Action = strings.TrimSpace(body.Action)
			if body.Action == "" {
				writeError(w, 400, "action required")
				return
			}
			runner := openjob.RunnerImage("opm", "task", envOr("OPM_RUNNER_TAG", "smoke"))
			j, err := store.CreateJob(projectID, body.Action, body.SpecID, runner)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			// Stub: mark starting then complete asynchronously without spawning yet.
			go stubRunJob(store, j)
			writeJSON(w, j)
		default:
			writeError(w, 405, "method not allowed")
		}
		return
	}

	runID := rest[0]
	if len(rest) == 1 {
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		j, err := store.GetJob(projectID, runID)
		if err != nil {
			writeError(w, 404, err.Error())
			return
		}
		writeJSON(w, j)
		return
	}

	if rest[1] == "cancel" && r.Method == http.MethodPost {
		j, err := store.GetJob(projectID, runID)
		if err != nil {
			writeError(w, 404, err.Error())
			return
		}
		if j.State == "completed" || j.State == "failed" || j.State == "cancelled" {
			writeJSON(w, j)
			return
		}
		now := nowUTC()
		j.State = "cancelled"
		j.CompletedAt = &now
		_ = store.UpdateJob(projectID, j)
		writeJSON(w, j)
		return
	}
	writeError(w, 404, "not found")
}

func stubRunJob(store *Store, j Job) {
	defer cleanupWorkspace(j.RunID)

	p, err := store.GetProject(j.ProjectID)
	if err != nil {
		j.State = "failed"
		j.Error = err.Error()
		now := nowUTC()
		j.CompletedAt = &now
		_ = store.UpdateJob(j.ProjectID, j)
		return
	}

	time.Sleep(100 * time.Millisecond)
	now := nowUTC()
	j.State = "starting"
	j.StartedAt = &now
	_ = store.UpdateJob(j.ProjectID, j)

	workDir, err := prepareJobWorkspace(p, j.RunID)
	if err != nil {
		fail := nowUTC()
		j.State = "failed"
		j.Error = "workspace: " + err.Error()
		j.CompletedAt = &fail
		_ = store.UpdateJob(j.ProjectID, j)
		return
	}

	time.Sleep(200 * time.Millisecond)
	j, err = store.GetJob(j.ProjectID, j.RunID)
	if err != nil || j.State == "cancelled" {
		return
	}
	j.State = "running"
	pct := 50
	j.ProgressPct = &pct
	_ = store.UpdateJob(j.ProjectID, j)

	_ = openjob.DockerRunArgv(j.RunnerImage, openjob.Labels{
		Product:  "opm",
		JobID:    j.RunID,
		Instance: j.ProjectID,
	}, openjob.ScrubEnv(map[string]string{
		"OPM_ACTION":     j.Action,
		"OPM_SPEC_ID":    j.SpecID,
		"OPM_PROJECT_ID": j.ProjectID,
		"OPM_OWNER_REPO": p.OwnerRepo,
		"OPM_WORKSPACE":  workDir,
		"JWT_SECRET":     "should-be-scrubbed",
	}))

	time.Sleep(300 * time.Millisecond)
	j, err = store.GetJob(j.ProjectID, j.RunID)
	if err != nil || j.State == "cancelled" {
		return
	}
	done := nowUTC()
	j.State = "completed"
	j.CompletedAt = &done
	pct = 100
	j.ProgressPct = &pct
	_ = store.UpdateJob(j.ProjectID, j)

	st := idleStatus()
	st.LastUpdate = done.Format(time.RFC3339)
	st.RunID = j.RunID
	_ = store.withProject(j.ProjectID, func(proj Project) error {
		return store.writeJSON(store.projectDir(proj)+"/status.json", st)
	})
}
