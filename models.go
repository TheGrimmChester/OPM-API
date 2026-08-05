package main

import (
	"strings"
	"time"
)

// BoardColumns is the ordered Kanban workflow for OPM.
// "review" is the automated review-runner column (neutral naming).
var BoardColumns = []string{
	"backlog",
	"queue",
	"in_progress",
	"review",
	"human_review",
	"done",
}

// Project is a linked GitHub repository (kanban/roadmap keyed by owner/repo).
// Board and task state live under OPM_DATA_DIR — never under a durable local clone.
type Project struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	OwnerRepo      string `json:"ownerRepo"`
	GithubRepoID   string `json:"githubRepoId,omitempty"`
	ConnectorID    string `json:"connectorId"`
	OrganizationID string `json:"organizationId,omitempty"`
	HTMLURL        string `json:"htmlUrl,omitempty"`
	DefaultBranch  string `json:"defaultBranch,omitempty"`
	// GitHub Projects v2 binding for this OPM project (org/user project node id).
	GithubProjectID    string `json:"githubProjectId,omitempty"`
	GithubProjectTitle string `json:"githubProjectTitle,omitempty"`
	GithubProjectURL   string `json:"githubProjectUrl,omitempty"`
	// AutoStartOnMove starts the job a board column represents when a card is
	// dragged there. Omitted/null in stored JSON is treated as ON, so existing
	// projects get the behaviour; set it to false to keep a manual board.
	AutoStartOnMove *bool     `json:"autoStartOnMove,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// autoStartOnMove reports whether dragging a card should start its column's job.
// Defaults to true for a project that predates the field.
func autoStartOnMove(p Project) bool {
	return p.AutoStartOnMove == nil || *p.AutoStartOnMove
}

type projectsFile struct {
	Projects []Project `json:"projects"`
}

// Task is a work item tracked on the board (spec_id keyed).
type Task struct {
	ID                        string `json:"id"`
	SpecID                    string `json:"specId"`
	Title                     string `json:"title"`
	Description               string `json:"description"`
	Status                    string `json:"status"`
	RequireReviewBeforeCoding bool   `json:"requireReviewBeforeCoding"`
	// HumanReviewRequired gates post-delivery automation. When true (default), the bot
	// runs review (+ qa-fix if needed) then stops at human_review. When false, the full
	// pipeline continues through merge and done without a human gate. Omitted/null in
	// stored JSON is treated as true.
	HumanReviewRequired *bool  `json:"humanReviewRequired"`
	IdeationID          string `json:"ideationId,omitempty"`
	IdeationTypeKey     string `json:"ideationTypeKey,omitempty"`
	// GitHub Milestone bind (repo milestones via ORA peer scm:pm).
	GithubMilestoneNumber int    `json:"githubMilestoneNumber,omitempty"`
	GithubMilestoneTitle  string `json:"githubMilestoneTitle,omitempty"`
	GithubMilestoneURL    string `json:"githubMilestoneUrl,omitempty"`
	// GitHub Projects v2 item bind.
	GithubProjectID       string     `json:"githubProjectId,omitempty"`
	GithubProjectItemID   string     `json:"githubProjectItemId,omitempty"`
	GithubProjectSyncedAt *time.Time `json:"githubProjectSyncedAt,omitempty"`
	// GithubProjectSyncError holds the last board-sync failure reason, including a
	// title refresh that ORA accepted but could not apply. Cleared on the next
	// success so the UI never shows a stale error, and never blanked on failure so
	// a board that no longer matches cannot masquerade as synced.
	GithubProjectSyncError string `json:"githubProjectSyncError,omitempty"`
	// GitHub Issue link in the project's linked repo (via ORA peer scm:pm).
	// GithubIssueState/Title/Assignee/Labels mirror GitHub and are refreshed by
	// pull; they are never treated as the source of truth for OPM fields.
	GithubIssueNumber   int        `json:"githubIssueNumber,omitempty"`
	GithubIssueURL      string     `json:"githubIssueUrl,omitempty"`
	GithubIssueState    string     `json:"githubIssueState,omitempty"` // open | closed
	GithubIssueTitle    string     `json:"githubIssueTitle,omitempty"`
	GithubIssueAssignee string     `json:"githubIssueAssignee,omitempty"`
	GithubIssueLabels   []string   `json:"githubIssueLabels,omitempty"`
	GithubIssueSyncedAt *time.Time `json:"githubIssueSyncedAt,omitempty"`
	// GithubIssueSyncError holds the last push/pull failure reason. It is cleared
	// on the next success so the UI never shows a stale error, and never blanked
	// on failure so a broken link cannot masquerade as healthy.
	GithubIssueSyncError string `json:"githubIssueSyncError,omitempty"`
	// Code delivery: the branch pushed to the linked repository and the pull
	// request opened from it. DeliveryFiles is the changed-file summary as it was
	// actually committed — not what a runner proposed.
	DeliveryBranch    string     `json:"deliveryBranch,omitempty"`
	DeliveryCommitSha string     `json:"deliveryCommitSha,omitempty"`
	DeliveryFiles     []string   `json:"deliveryFiles,omitempty"`
	DeliveredAt       *time.Time `json:"deliveredAt,omitempty"`
	PRNumber          int        `json:"prNumber,omitempty"`
	PRURL             string     `json:"prUrl,omitempty"`
	PRState           string     `json:"prState,omitempty"` // open | closed | merged
	// DeliveryStatus is the machine-readable outcome of the last delivery attempt
	// (see delivery.go). DeliveryError holds its reason when it failed; both are
	// cleared on the next success so the UI never shows a stale failure, and
	// DeliveryError is never blanked on failure so a broken delivery cannot
	// masquerade as a shipped one.
	DeliveryStatus string    `json:"deliveryStatus,omitempty"`
	DeliveryError  string    `json:"deliveryError,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// Board maps column id → ordered spec ids.
type Board map[string][]string

func emptyBoard() Board {
	b := Board{}
	for _, c := range BoardColumns {
		b[c] = []string{}
	}
	return b
}

// ImplementationPlan is the task plan with phases and subtasks.
type ImplementationPlan struct {
	Feature          string      `json:"feature"`
	WorkflowType     string      `json:"workflow_type"`
	ServicesInvolved []string    `json:"services_involved"`
	SpecFile         string      `json:"spec_file"`
	Status           string      `json:"status"`
	PlanStatus       string      `json:"planStatus"`
	FinalAcceptance  []string    `json:"final_acceptance"`
	Phases           []PlanPhase `json:"phases"`
}

type PlanPhase struct {
	Phase        int           `json:"phase"`
	Name         string        `json:"name"`
	Type         string        `json:"type"`
	ParallelSafe bool          `json:"parallel_safe"`
	DependsOn    []int         `json:"depends_on,omitempty"`
	Subtasks     []PlanSubtask `json:"subtasks"`
}

type PlanSubtask struct {
	ID            string   `json:"id"`
	Description   string   `json:"description"`
	Status        string   `json:"status"`
	Service       string   `json:"service,omitempty"`
	FilesToModify []string `json:"files_to_modify,omitempty"`
	Verification  string   `json:"verification,omitempty"`
}

// TaskProgress tracks automation progress for a spec.
type TaskProgress struct {
	IsRunning        bool       `json:"is_running"`
	Paused           bool       `json:"paused,omitempty"`
	Action           string     `json:"action,omitempty"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	Progress         int        `json:"progress"`
	SubtaskCompleted int        `json:"subtask_completed"`
	SubtaskTotal     int        `json:"subtask_total"`
	CurrentPhaseName string     `json:"current_phase_name,omitempty"`
	RunID            string     `json:"run_id,omitempty"`
	StuckSubtaskID   string     `json:"stuck_subtask_id,omitempty"`
}

// ProjectStatus is the live automation status for a project.
type ProjectStatus struct {
	Active   bool   `json:"active"`
	Spec     string `json:"spec"`
	State    string `json:"state"` // idle | planning | building | qa | error | stuck
	Subtasks struct {
		Completed  int `json:"completed"`
		Total      int `json:"total"`
		InProgress int `json:"in_progress"`
		Failed     int `json:"failed"`
	} `json:"subtasks"`
	Phase struct {
		Current string `json:"current"`
		ID      int    `json:"id"`
		Total   int    `json:"total"`
	} `json:"phase"`
	Workers struct {
		Active int `json:"active"`
		Max    int `json:"max"`
	} `json:"workers"`
	LastUpdate string `json:"last_update"`
	RunID      string `json:"run_id,omitempty"`
}

func idleStatus() ProjectStatus {
	s := ProjectStatus{Active: false, Spec: "", State: "idle", LastUpdate: nowUTC().Format(time.RFC3339)}
	s.Workers.Max = 1
	return s
}

// Job is an async task-automation run.
type Job struct {
	RunID       string     `json:"runId"`
	ProjectID   string     `json:"projectId"`
	SpecID      string     `json:"specId,omitempty"`
	Action      string     `json:"action"`
	State       string     `json:"state"` // queued | starting | running | completed | failed | cancelled
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	Error       string     `json:"error,omitempty"`
	Attempt     int        `json:"attempt"`
	ProgressPct *int       `json:"progressPct,omitempty"`
	RunnerImage string     `json:"runnerImage,omitempty"`
	// Execution is "container" when opm-runner-task was docker-run for this job,
	// or "builtin" when the in-process executor ran (fallback / OPM_FORCE_BUILTIN).
	Execution string `json:"execution,omitempty"`
	// Message is operator-facing status (spawn + artifact outcome).
	Message string `json:"message,omitempty"`
	// TargetPhase is 1-based plan phase number for skip-to-phase.
	TargetPhase int `json:"targetPhase,omitempty"`
	// IdeationType optionally scopes run-ideation to one IdeationTypes key.
	IdeationType string `json:"ideationType,omitempty"`

	// Competitors and AudienceNotes are the roadmap pipeline's inputs, carried
	// from ORA's /api/scm/roadmap/generate request. They are per-run rather than
	// per-project: which competitors matter is a question you answer differently
	// each time you re-run discovery.
	Competitors   []string `json:"competitors,omitempty"`
	AudienceNotes string   `json:"audienceNotes,omitempty"`

	// --- Acting identity -----------------------------------------------------
	// Stamped at enqueue so the job can resolve *this user's* credentials and
	// model at run time. Before these existed a job carried no identity at all,
	// so it structurally could not use anything but one deployment-wide key.
	OrganizationID string `json:"organizationId,omitempty"`
	ActorUsername  string `json:"actorUsername,omitempty"`

	// AgentKey is the canonical OAM agent key for this job's action
	// (agentKeyForAction). Empty means the action has no AI phase.
	AgentKey string `json:"agentKey,omitempty"`

	// ModelOverride is a per-task model choice for this job only.
	ModelOverride *modelOverride `json:"modelOverride,omitempty"`

	// --- Pinned resolution ---------------------------------------------------
	// What actually ran, recorded (never the key itself). Two reasons:
	//
	//  1. "Which model ran this job, as whom" must be answerable afterwards.
	//  2. A chained job inherits these instead of re-resolving, so an org edit or
	//     key rotation mid-chain cannot switch models between subtask 2.1 and
	//     2.2 of the same task.
	ResolvedProvider    string `json:"resolvedProvider,omitempty"`
	ResolvedModel       string `json:"resolvedModel,omitempty"`
	ResolvedModelSource string `json:"resolvedModelSource,omitempty"`
	ResolvedKeyScope    string `json:"resolvedKeyScope,omitempty"`
	// ResolvedAgentKey records which agent the pinned binding belongs to, so a
	// successor in a different phase does not inherit the wrong phase's model.
	ResolvedAgentKey string `json:"resolvedAgentKey,omitempty"`
}

// JobOrigin is the identity and pinned configuration a new job starts with.
//
// Two different things are inherited down an autopilot chain, and conflating them
// is a bug in both directions:
//
//   - The ACTOR (org + username) is inherited unconditionally. It is the identity
//     the whole chain runs as; a chain that changed actor mid-flight would resolve
//     someone else's credentials.
//
//   - The MODEL is inherited only within one agent phase. A chain crosses phases
//     (coding → review → qa_fix), and each phase has its own binding — that is the
//     entire point of per-agent models. Carrying the coding model into review
//     would silently defeat the feature. Carrying it across the coding subtasks of
//     one task is what stops an org edit from switching models between subtask 2.1
//     and 2.2 on a shared workspace.
type JobOrigin struct {
	OrganizationID string
	ActorUsername  string
	ModelOverride  *modelOverride

	// Pinned resolution inherited from a parent job, valid only for
	// ResolvedAgentKey. Empty on a fresh session.
	ResolvedProvider    string
	ResolvedModel       string
	ResolvedModelSource string
	ResolvedKeyScope    string
	ResolvedAgentKey    string
}

// jobOriginFrom carries a parent job's identity and pinned binding into its
// successor. CreateJobWithOrigin drops the pin when the successor is a different
// agent phase.
func jobOriginFrom(j Job) JobOrigin {
	return JobOrigin{
		OrganizationID:      j.OrganizationID,
		ActorUsername:       j.ActorUsername,
		ModelOverride:       j.ModelOverride,
		ResolvedProvider:    j.ResolvedProvider,
		ResolvedModel:       j.ResolvedModel,
		ResolvedModelSource: j.ResolvedModelSource,
		ResolvedKeyScope:    j.ResolvedKeyScope,
		ResolvedAgentKey:    j.ResolvedAgentKey,
	}
}

// pinnedFor reports whether this origin carries a resolved binding that is valid
// for agentKey. A pin from another phase is not reusable.
func (o JobOrigin) pinnedFor(agentKey string) bool {
	if strings.TrimSpace(o.ResolvedProvider) == "" || strings.TrimSpace(o.ResolvedModel) == "" {
		return false
	}
	return strings.TrimSpace(o.ResolvedAgentKey) == strings.TrimSpace(agentKey) && agentKey != ""
}

// Roadmap document.
type Roadmap struct {
	ID             string           `json:"id"`
	ProjectName    string           `json:"project_name"`
	Version        string           `json:"version"`
	Vision         string           `json:"vision"`
	TargetAudience string           `json:"target_audience"`
	Phases         []RoadmapPhase   `json:"phases"`
	Features       []RoadmapFeature `json:"features"`
	Metadata       map[string]any   `json:"metadata,omitempty"`
}

type RoadmapPhase struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Order       int      `json:"order"`
	Status      string   `json:"status"`
	Features    []string `json:"features"`
	// Optional GitHub Milestone bind for this phase.
	GithubMilestoneNumber int    `json:"github_milestone_number,omitempty"`
	GithubMilestoneTitle  string `json:"github_milestone_title,omitempty"`
	GithubMilestoneURL    string `json:"github_milestone_url,omitempty"`
}

type RoadmapFeature struct {
	ID                    string   `json:"id"`
	Title                 string   `json:"title"`
	Description           string   `json:"description"`
	Rationale             string   `json:"rationale,omitempty"`
	Priority              string   `json:"priority"`
	Complexity            string   `json:"complexity,omitempty"`
	Impact                string   `json:"impact,omitempty"`
	PhaseID               string   `json:"phase_id,omitempty"`
	Dependencies          []string `json:"dependencies,omitempty"`
	Status                string   `json:"status"`
	AcceptanceCriteria    []string `json:"acceptance_criteria,omitempty"`
	UserStories           []string `json:"user_stories,omitempty"`
	GithubMilestoneNumber int      `json:"github_milestone_number,omitempty"`
	GithubMilestoneTitle  string   `json:"github_milestone_title,omitempty"`
	GithubMilestoneURL    string   `json:"github_milestone_url,omitempty"`
	GithubProjectID       string   `json:"github_project_id,omitempty"`
	GithubProjectItemID   string   `json:"github_project_item_id,omitempty"`
}

// Ideation stores ideas grouped by type key.
type Ideation map[string][]Idea

type Idea struct {
	ID                     string   `json:"id"`
	Type                   string   `json:"type"`
	Title                  string   `json:"title"`
	Description            string   `json:"description"`
	Rationale              string   `json:"rationale,omitempty"`
	EstimatedEffort        string   `json:"estimated_effort,omitempty"`
	AffectedFiles          []string `json:"affected_files,omitempty"`
	ImplementationApproach string   `json:"implementation_approach,omitempty"`
	Status                 string   `json:"status"`
	CreatedAt              string   `json:"created_at"`
}

var IdeationTypes = []string{
	"code_improvements",
	"security",
	"performance",
	"documentation",
	"ui_ux",
	"code_quality",
}

func emptyIdeation() Ideation {
	out := Ideation{}
	for _, t := range IdeationTypes {
		out[t] = []Idea{}
	}
	return out
}
