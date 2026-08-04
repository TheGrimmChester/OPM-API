package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// GitHub Milestones + Projects v2 routes under /api/projects/{id}/github/...
// Credentials stay in ORA; OPM calls peer scm:pm endpoints.

func handleProjectGitHub(w http.ResponseWriter, r *http.Request, store *Store, projectID string, rest []string) {
	if len(rest) == 0 {
		writeError(w, 404, "not found")
		return
	}
	org := resolveRequestOrg(r)
	p, err := store.GetProjectForOrg(projectID, org)
	if err != nil {
		writeError(w, 404, err.Error())
		return
	}
	if !peerORAConfigured() {
		writeError(w, 503, "PEER_ORA_URL not configured — GitHub PM sync requires ORA")
		return
	}

	switch rest[0] {
	case "milestones":
		handleGitHubMilestones(w, r, store, p, rest[1:])
	case "projects":
		handleGitHubProjects(w, r, store, p, rest[1:])
	case "sync-task":
		if len(rest) < 2 {
			writeError(w, 404, "not found")
			return
		}
		handleGitHubSyncTask(w, r, store, p, rest[1])
	default:
		writeError(w, 404, "not found")
	}
}

func handleGitHubMilestones(w http.ResponseWriter, r *http.Request, store *Store, p Project, rest []string) {
	if len(rest) == 0 {
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		out, err := peerListMilestones(ctx, p.OrganizationID, p.ConnectorID, p.OwnerRepo)
		if err != nil {
			writeError(w, 502, "ora milestones: "+err.Error())
			return
		}
		writeJSON(w, out)
		return
	}
	if rest[0] == "assign" && r.Method == http.MethodPost {
		var body struct {
			SpecID          string `json:"specId"`
			FeatureID       string `json:"featureId"`
			PhaseID         string `json:"phaseId"`
			MilestoneNumber int    `json:"milestoneNumber"`
			Title           string `json:"title"`
			Description     string `json:"description"`
			State           string `json:"state"`
			CreateIfMissing bool   `json:"createIfMissing"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, 400, "invalid json")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()

		number := body.MilestoneNumber
		title := strings.TrimSpace(body.Title)
		msTitle, msURL := "", ""
		if number > 0 || title != "" || body.CreateIfMissing {
			up, err := peerUpsertMilestone(ctx, p.OrganizationID, p.ConnectorID, p.OwnerRepo,
				number, title, body.Description, body.State)
			if err != nil {
				writeError(w, 502, "ora milestone upsert: "+err.Error())
				return
			}
			if m, ok := up["milestone"].(map[string]interface{}); ok {
				number = intFromAny(m["number"])
				msTitle = strFromAny(m["title"])
				msURL = strFromAny(m["html_url"])
			}
		}

		result := map[string]interface{}{
			"ok": true, "milestoneNumber": number, "milestoneTitle": msTitle, "milestoneUrl": msURL,
		}

		if specID := strings.TrimSpace(body.SpecID); specID != "" {
			t, err := store.UpdateTask(p.ID, specID, map[string]interface{}{
				"githubMilestoneNumber": float64(number),
				"githubMilestoneTitle":  msTitle,
				"githubMilestoneUrl":    msURL,
			})
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			result["task"] = t
		}
		if featureID := strings.TrimSpace(body.FeatureID); featureID != "" || strings.TrimSpace(body.PhaseID) != "" {
			rm, err := store.GetRoadmap(p.ID)
			if err != nil {
				writeError(w, 404, err.Error())
				return
			}
			if featureID != "" {
				for i := range rm.Features {
					if rm.Features[i].ID == featureID {
						rm.Features[i].GithubMilestoneNumber = number
						rm.Features[i].GithubMilestoneTitle = msTitle
						rm.Features[i].GithubMilestoneURL = msURL
						break
					}
				}
			}
			if phaseID := strings.TrimSpace(body.PhaseID); phaseID != "" {
				for i := range rm.Phases {
					if rm.Phases[i].ID == phaseID {
						rm.Phases[i].GithubMilestoneNumber = number
						rm.Phases[i].GithubMilestoneTitle = msTitle
						rm.Phases[i].GithubMilestoneURL = msURL
						break
					}
				}
			}
			if err := store.PutRoadmap(p.ID, rm); err != nil {
				writeError(w, 400, err.Error())
				return
			}
			result["roadmap"] = rm
		}
		writeJSON(w, result)
		return
	}
	writeError(w, 404, "not found")
}

func handleGitHubProjects(w http.ResponseWriter, r *http.Request, store *Store, p Project, rest []string) {
	if len(rest) == 0 {
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		out, err := peerListGitHubProjects(ctx, p.OrganizationID, p.ConnectorID, p.OwnerRepo)
		if err != nil {
			writeError(w, 502, "ora projects: "+err.Error())
			return
		}
		out["boundProjectId"] = p.GithubProjectID
		out["boundProjectTitle"] = p.GithubProjectTitle
		out["boundProjectUrl"] = p.GithubProjectURL
		writeJSON(w, out)
		return
	}
	if rest[0] == "bind" && r.Method == http.MethodPost {
		var body struct {
			ProjectID    string `json:"projectId"`
			ProjectTitle string `json:"projectTitle"`
			ProjectURL   string `json:"projectUrl"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, 400, "invalid json")
			return
		}
		body.ProjectID = strings.TrimSpace(body.ProjectID)
		updated, err := store.BindGitHubProject(p.ID, body.ProjectID, body.ProjectTitle, body.ProjectURL)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, updated)
		return
	}
	if rest[0] == "sync-item" && r.Method == http.MethodPost {
		var body struct {
			SpecID    string `json:"specId"`
			FeatureID string `json:"featureId"`
			Title     string `json:"title"`
			Body      string `json:"body"`
			Status    string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, 400, "invalid json")
			return
		}
		ghProjectID := p.GithubProjectID
		itemID := ""
		title := strings.TrimSpace(body.Title)
		statusHint := strings.TrimSpace(body.Status)

		if specID := strings.TrimSpace(body.SpecID); specID != "" {
			t, err := store.GetTask(p.ID, specID)
			if err != nil {
				writeError(w, 404, err.Error())
				return
			}
			if title == "" {
				title = t.Title
			}
			if statusHint == "" {
				statusHint = t.Status
			}
			if t.GithubProjectID != "" {
				ghProjectID = t.GithubProjectID
			}
			itemID = t.GithubProjectItemID
			if body.Body == "" {
				body.Body = t.Description
			}
		}
		if ghProjectID == "" {
			writeError(w, 400, "bind a GitHub Project first (POST …/github/projects/bind)")
			return
		}
		if title == "" {
			writeError(w, 400, "title required")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		out, err := peerUpsertProjectItem(ctx, p.OrganizationID, p.ConnectorID, ghProjectID, itemID, title, body.Body, statusHint)
		if err != nil {
			writeError(w, 502, "ora project item: "+err.Error())
			return
		}
		newItemID := strFromAny(out["item_id"])
		if newItemID == "" {
			newItemID = itemID
		}

		result := map[string]interface{}{"ok": true, "sync": out, "githubProjectId": ghProjectID, "itemId": newItemID}
		if specID := strings.TrimSpace(body.SpecID); specID != "" {
			t, err := store.UpdateTask(p.ID, specID, map[string]interface{}{
				"githubProjectId":     ghProjectID,
				"githubProjectItemId": newItemID,
			})
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			result["task"] = t
		}
		if featureID := strings.TrimSpace(body.FeatureID); featureID != "" {
			rm, err := store.GetRoadmap(p.ID)
			if err != nil {
				writeError(w, 404, err.Error())
				return
			}
			for i := range rm.Features {
				if rm.Features[i].ID == featureID {
					rm.Features[i].GithubProjectID = ghProjectID
					rm.Features[i].GithubProjectItemID = newItemID
					break
				}
			}
			if err := store.PutRoadmap(p.ID, rm); err != nil {
				writeError(w, 400, err.Error())
				return
			}
			result["roadmap"] = rm
		}
		writeJSON(w, result)
		return
	}
	writeError(w, 404, "not found")
}

func handleGitHubSyncTask(w http.ResponseWriter, r *http.Request, store *Store, p Project, specID string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	t, err := store.GetTask(p.ID, specID)
	if err != nil {
		writeError(w, 404, err.Error())
		return
	}
	result := map[string]interface{}{"ok": true, "specId": specID}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	ghProjectID := t.GithubProjectID
	if ghProjectID == "" {
		ghProjectID = p.GithubProjectID
	}
	if ghProjectID != "" {
		out, err := peerUpsertProjectItem(ctx, p.OrganizationID, p.ConnectorID, ghProjectID,
			t.GithubProjectItemID, t.Title, t.Description, t.Status)
		if err != nil {
			result["projectSync"] = map[string]interface{}{"ok": false, "error": err.Error()}
		} else {
			result["projectSync"] = out
			if id := strFromAny(out["item_id"]); id != "" && id != t.GithubProjectItemID {
				t, _ = store.UpdateTask(p.ID, specID, map[string]interface{}{
					"githubProjectId": ghProjectID, "githubProjectItemId": id,
				})
			}
		}
	} else {
		result["projectSync"] = map[string]interface{}{"ok": false, "note": "no GitHub Project bound"}
	}

	if t.GithubMilestoneNumber > 0 || t.GithubMilestoneTitle != "" {
		state := ""
		if t.Status == "done" {
			state = "closed"
		}
		up, err := peerUpsertMilestone(ctx, p.OrganizationID, p.ConnectorID, p.OwnerRepo,
			t.GithubMilestoneNumber, t.GithubMilestoneTitle, "", state)
		if err != nil {
			result["milestoneSync"] = map[string]interface{}{"ok": false, "error": err.Error()}
		} else {
			result["milestoneSync"] = up
		}
	}
	result["task"] = t
	writeJSON(w, result)
}

// syncTaskGitHubAfterMove best-effort updates Projects v2 Status when a linked task moves.
func syncTaskGitHubAfterMove(store *Store, p Project, t Task) {
	if !peerORAConfigured() {
		return
	}
	ghProjectID := t.GithubProjectID
	if ghProjectID == "" {
		ghProjectID = p.GithubProjectID
	}
	if ghProjectID == "" || t.GithubProjectItemID == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = peerSetProjectItemStatus(ctx, p.OrganizationID, p.ConnectorID, ghProjectID, t.GithubProjectItemID, t.Status)
	}()
}

func strFromAny(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	s := strings.Trim(string(b), `"`)
	return s
}

func intFromAny(v interface{}) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case json.Number:
		i, _ := t.Int64()
		return int(i)
	default:
		return 0
	}
}
