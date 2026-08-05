package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OPM ↔ GitHub Milestone bind under /api/projects/{id}/github/milestones/…
//
// `assign` is the forward direction: it upserts the milestone on GitHub and
// stores the resulting number/title/url on a task, a roadmap feature or a
// roadmap phase. `unassign` is its explicit inverse and is deliberately
// OPM-side only: the GitHub milestone is neither closed nor deleted, exactly
// like `issues/unlink` leaves the issue open and `projects/unbind` leaves the
// board item on the board.
//
// Clearing a bind used to be reachable only as a quirk of UpdateTask — patching
// githubMilestoneNumber to 0 wipes the three fields. That still works, but it is
// undocumented, silent, and cannot touch a feature or a phase. This route is the
// documented path, and it writes what it did to the spec log.

const milestoneSyncLogPhase = "github-milestone-sync"

// Statuses answered by the milestone routes. All are decided locally: unassign
// never calls ORA, so there is no upstream fault to relay.
const (
	milestoneSyncOK            = "ok"
	milestoneInvalidRequest    = "invalid_request"
	milestoneTaskNotFound      = "task_not_found"
	milestoneFeatureNotFound   = "feature_not_found"
	milestonePhaseNotFound     = "phase_not_found"
	milestoneNotBound          = "not_bound"
	milestoneRoadmapUnreadable = "roadmap_unreadable"
)

// milestoneUnassignNote is returned and logged so no caller can read an unassign
// as an action on GitHub.
const milestoneUnassignNote = "the GitHub milestone was left untouched — it was neither closed nor deleted"

// clearMilestoneLink drops the milestone bind from a task. The milestone itself
// is left alone on GitHub — unassigning is an OPM-side operation.
func clearMilestoneLink(t *Task) {
	t.GithubMilestoneNumber = 0
	t.GithubMilestoneTitle = ""
	t.GithubMilestoneURL = ""
}

// clearFeatureMilestoneLink is clearMilestoneLink for a roadmap feature.
func clearFeatureMilestoneLink(f *RoadmapFeature) {
	f.GithubMilestoneNumber = 0
	f.GithubMilestoneTitle = ""
	f.GithubMilestoneURL = ""
}

// clearPhaseMilestoneLink is clearMilestoneLink for a roadmap phase.
func clearPhaseMilestoneLink(ph *RoadmapPhase) {
	ph.GithubMilestoneNumber = 0
	ph.GithubMilestoneTitle = ""
	ph.GithubMilestoneURL = ""
}

// clearedMilestone records one target whose bind was dropped.
type clearedMilestone struct {
	Target          string `json:"target"` // task | feature | phase
	ID              string `json:"id"`
	MilestoneNumber int    `json:"milestoneNumber"`
	MilestoneTitle  string `json:"milestoneTitle,omitempty"`
}

// milestoneBound is true for a target carrying any part of a milestone bind. A
// title or url without a number still counts, so a half-written bind can be
// cleaned up rather than being reported as nothing to do.
func milestoneBound(number int, title, url string) bool {
	return number > 0 || title != "" || url != ""
}

// handleMilestoneUnassign is the inverse of the assign route: it drops the
// milestone bind from a task, a roadmap feature and/or a roadmap phase without
// touching GitHub.
func handleMilestoneUnassign(w http.ResponseWriter, r *http.Request, store *Store, p Project) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	var body struct {
		SpecID    string `json:"specId"`
		FeatureID string `json:"featureId"`
		PhaseID   string `json:"phaseId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
			"error": "invalid json", "status": milestoneInvalidRequest,
		})
		return
	}
	specID := strings.TrimSpace(body.SpecID)
	featureID := strings.TrimSpace(body.FeatureID)
	phaseID := strings.TrimSpace(body.PhaseID)
	if specID == "" && featureID == "" && phaseID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
			"error": "one of specId, featureId or phaseId is required", "status": milestoneInvalidRequest,
		})
		return
	}

	result := map[string]interface{}{"ok": true, "status": milestoneSyncOK}
	cleared := []clearedMilestone{}

	// --- task ---------------------------------------------------------------
	var prevTask Task
	if specID != "" {
		result["specId"] = specID
		var err error
		prevTask, err = store.GetTask(p.ID, specID)
		if err != nil {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{
				"error": err.Error(), "status": milestoneTaskNotFound, "specId": specID,
			})
			return
		}
		if milestoneBound(prevTask.GithubMilestoneNumber, prevTask.GithubMilestoneTitle, prevTask.GithubMilestoneURL) {
			updated, err := store.MutateTask(p.ID, specID, clearMilestoneLink)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			result["task"] = updated
			cleared = append(cleared, clearedMilestone{
				Target: "task", ID: specID,
				MilestoneNumber: prevTask.GithubMilestoneNumber,
				MilestoneTitle:  prevTask.GithubMilestoneTitle,
			})
		} else {
			result["task"] = prevTask
		}
	}

	// --- roadmap feature / phase --------------------------------------------
	if featureID != "" || phaseID != "" {
		// The layout is self-healing, so a missing roadmap is scaffolded rather than
		// reported: the only way this fails is a roadmap.json that cannot be parsed.
		rm, err := store.GetRoadmap(p.ID)
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
				"error":  "roadmap could not be read: " + err.Error(),
				"status": milestoneRoadmapUnreadable,
			})
			return
		}
		changed := false
		if featureID != "" {
			result["featureId"] = featureID
			idx := -1
			for i := range rm.Features {
				if rm.Features[i].ID == featureID {
					idx = i
					break
				}
			}
			if idx < 0 {
				writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{
					"error":  "feature " + featureID + " not found in roadmap",
					"status": milestoneFeatureNotFound, "featureId": featureID,
				})
				return
			}
			f := &rm.Features[idx]
			if milestoneBound(f.GithubMilestoneNumber, f.GithubMilestoneTitle, f.GithubMilestoneURL) {
				cleared = append(cleared, clearedMilestone{
					Target: "feature", ID: featureID,
					MilestoneNumber: f.GithubMilestoneNumber,
					MilestoneTitle:  f.GithubMilestoneTitle,
				})
				clearFeatureMilestoneLink(f)
				changed = true
			}
		}
		if phaseID != "" {
			result["phaseId"] = phaseID
			idx := -1
			for i := range rm.Phases {
				if rm.Phases[i].ID == phaseID {
					idx = i
					break
				}
			}
			if idx < 0 {
				writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{
					"error":  "phase " + phaseID + " not found in roadmap",
					"status": milestonePhaseNotFound, "phaseId": phaseID,
				})
				return
			}
			ph := &rm.Phases[idx]
			if milestoneBound(ph.GithubMilestoneNumber, ph.GithubMilestoneTitle, ph.GithubMilestoneURL) {
				cleared = append(cleared, clearedMilestone{
					Target: "phase", ID: phaseID,
					MilestoneNumber: ph.GithubMilestoneNumber,
					MilestoneTitle:  ph.GithubMilestoneTitle,
				})
				clearPhaseMilestoneLink(ph)
				changed = true
			}
		}
		if changed {
			if err := store.PutRoadmap(p.ID, rm); err != nil {
				writeError(w, 400, err.Error())
				return
			}
		}
		result["roadmap"] = rm
	}

	// Nothing was bound anywhere, so there is nothing to unassign. Saying so is
	// better than answering 200 for a no-op the caller did not intend.
	if len(cleared) == 0 {
		payload := map[string]interface{}{
			"error": "no GitHub milestone is bound to the requested target", "status": milestoneNotBound,
		}
		for _, k := range []string{"specId", "featureId", "phaseId"} {
			if v, ok := result[k]; ok {
				payload[k] = v
			}
		}
		writeJSONStatus(w, http.StatusBadRequest, payload)
		return
	}

	result["cleared"] = cleared
	// Convenience mirror of the first cleared target; `cleared` stays authoritative
	// when one call unassigns several targets at once.
	result["unassignedMilestoneNumber"] = cleared[0].MilestoneNumber
	result["unassignedMilestoneTitle"] = cleared[0].MilestoneTitle
	result["note"] = milestoneUnassignNote

	// The spec log is per-task, so it is written only when a task was unassigned.
	// The task is resolved first, so it is cleared[0] whenever it was bound.
	if specID != "" && cleared[0].Target == "task" {
		label := fmt.Sprintf("#%d", prevTask.GithubMilestoneNumber)
		if prevTask.GithubMilestoneTitle != "" {
			label += " " + prevTask.GithubMilestoneTitle
		}
		_ = store.AppendSpecLog(p.ID, specID, milestoneSyncLogPhase,
			fmt.Sprintf("[%s] unassign milestone %s (%s)\n",
				nowUTC().Format(time.RFC3339), label, milestoneUnassignNote))
	}
	writeJSON(w, result)
}
