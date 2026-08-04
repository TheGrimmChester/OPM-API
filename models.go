package main

import "time"

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
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	OwnerRepo      string    `json:"ownerRepo"`
	GithubRepoID   string    `json:"githubRepoId,omitempty"`
	ConnectorID    string    `json:"connectorId"`
	OrganizationID string    `json:"organizationId,omitempty"`
	HTMLURL        string    `json:"htmlUrl,omitempty"`
	DefaultBranch  string    `json:"defaultBranch,omitempty"`
	// GitHub Projects v2 binding for this OPM project (org/user project node id).
	GithubProjectID    string `json:"githubProjectId,omitempty"`
	GithubProjectTitle string `json:"githubProjectTitle,omitempty"`
	GithubProjectURL   string `json:"githubProjectUrl,omitempty"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type projectsFile struct {
	Projects []Project `json:"projects"`
}

// Task is a work item tracked on the board (spec_id keyed).
type Task struct {
	ID                        string    `json:"id"`
	SpecID                    string    `json:"specId"`
	Title                     string    `json:"title"`
	Description               string    `json:"description"`
	Status                    string    `json:"status"`
	RequireReviewBeforeCoding bool      `json:"requireReviewBeforeCoding"`
	IdeationID                string    `json:"ideationId,omitempty"`
	IdeationTypeKey           string    `json:"ideationTypeKey,omitempty"`
	// GitHub Milestone bind (repo milestones via ORA peer scm:pm).
	GithubMilestoneNumber int    `json:"githubMilestoneNumber,omitempty"`
	GithubMilestoneTitle  string `json:"githubMilestoneTitle,omitempty"`
	GithubMilestoneURL    string `json:"githubMilestoneUrl,omitempty"`
	// GitHub Projects v2 item bind.
	GithubProjectID     string `json:"githubProjectId,omitempty"`
	GithubProjectItemID string `json:"githubProjectItemId,omitempty"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
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
	Feature           string              `json:"feature"`
	WorkflowType      string              `json:"workflow_type"`
	ServicesInvolved  []string            `json:"services_involved"`
	SpecFile          string              `json:"spec_file"`
	Status            string              `json:"status"`
	PlanStatus        string              `json:"planStatus"`
	FinalAcceptance   []string            `json:"final_acceptance"`
	Phases            []PlanPhase         `json:"phases"`
}

type PlanPhase struct {
	Phase        int          `json:"phase"`
	Name         string       `json:"name"`
	Type         string       `json:"type"`
	ParallelSafe bool         `json:"parallel_safe"`
	DependsOn    []int        `json:"depends_on,omitempty"`
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
	IsRunning         bool       `json:"is_running"`
	Paused            bool       `json:"paused,omitempty"`
	Action            string     `json:"action,omitempty"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
	Progress          int        `json:"progress"`
	SubtaskCompleted  int        `json:"subtask_completed"`
	SubtaskTotal      int        `json:"subtask_total"`
	CurrentPhaseName  string     `json:"current_phase_name,omitempty"`
	RunID             string     `json:"run_id,omitempty"`
	StuckSubtaskID    string     `json:"stuck_subtask_id,omitempty"`
}

// ProjectStatus is the live automation status for a project.
type ProjectStatus struct {
	Active           bool   `json:"active"`
	Spec             string `json:"spec"`
	State            string `json:"state"` // idle | planning | building | qa | error | stuck
	Subtasks         struct {
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
	ID                  string   `json:"id"`
	Title               string   `json:"title"`
	Description         string   `json:"description"`
	Rationale           string   `json:"rationale,omitempty"`
	Priority            string   `json:"priority"`
	Complexity          string   `json:"complexity,omitempty"`
	Impact              string   `json:"impact,omitempty"`
	PhaseID             string   `json:"phase_id,omitempty"`
	Dependencies        []string `json:"dependencies,omitempty"`
	Status              string   `json:"status"`
	AcceptanceCriteria  []string `json:"acceptance_criteria,omitempty"`
	UserStories         []string `json:"user_stories,omitempty"`
	GithubMilestoneNumber int    `json:"github_milestone_number,omitempty"`
	GithubMilestoneTitle  string `json:"github_milestone_title,omitempty"`
	GithubMilestoneURL    string `json:"github_milestone_url,omitempty"`
	GithubProjectID       string `json:"github_project_id,omitempty"`
	GithubProjectItemID   string `json:"github_project_item_id,omitempty"`
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
