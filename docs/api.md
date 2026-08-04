# API

Base path: `/api`. JSON request/response. Auth optional unless `OPA_AUTH_REQUIRED` is set.

When auth is required (NAS), send `Authorization: Bearer <hub-jwt>` plus **`X-Organization-ID`** / **`X-Project-ID`**. See [interop.md](interop.md#tenant-headers) and [security.md](security.md).

## Health

- `GET /api/health` → `{ status, service: "opm-api", version }`
- `GET /api/peer/health` → service JWT probe (`health:read` when `OPEN_SERVICE_JWT_SECRET` set)

## Hub and GitHub discovery

- `GET /api/hub/status` → hub/ORA linkage summary (`credentials_home: ora`)
- `GET /api/hub/organizations` → organizations from OPA-Hub (falls back to `default-org`)
- `GET /api/github/connectors?organizationId=` → proxies ORA connector list
- `GET /api/github/connectors/{id}/repos?organizationId=` → proxies installable GitHub repos

Requires `PEER_OPA_URL` / `PEER_ORA_URL` as documented in [interop.md](interop.md).

## Projects (GitHub repos)

- `GET /api/projects` → `{ projects: Project[] }`
- `POST /api/projects` `{ connectorId, ownerRepo, organizationId?, name?, githubRepoId?, htmlUrl?, defaultBranch? }` → linked `Project`
- `GET /api/projects/{id}` → `Project`
- `DELETE /api/projects/{id}` → 204 (removes registry entry + server-side board data)
- `POST /api/projects/{id}/init` → ensure data-dir layout

`Project` fields: `id`, `name`, `ownerRepo`, `connectorId`, `organizationId`, `githubRepoId`, `htmlUrl`, `defaultBranch`, optional `githubProjectId` / `githubProjectTitle` / `githubProjectUrl` (Projects v2 bind), timestamps.

Local folder / `path` create payloads are rejected.

## GitHub Milestones and Projects (via ORA)

Credentials stay in **ORA**. OPM calls peer `scm:pm` endpoints; the dashboard uses these project-scoped routes (send org/project headers).

- `GET /api/projects/{id}/github/milestones` → `{ milestones: [{ number, title, description, state, html_url }] }`
- `POST /api/projects/{id}/github/milestones/assign` `{ specId?|featureId?|phaseId?, milestoneNumber?, title?, description?, state?, createIfMissing? }` → bind + optional upsert on GitHub
- `GET /api/projects/{id}/github/projects` → `{ projects: [{ id, title, url, number }], boundProjectId, … }` (Projects v2 GraphQL)
- `POST /api/projects/{id}/github/projects/bind` `{ projectId, projectTitle?, projectUrl? }` → bind OPM project to a GitHub Project
- `POST /api/projects/{id}/github/projects/sync-item` `{ specId?|featureId?, title?, body?, status? }` → create/update draft Project item; maps OPM column → Status option best-effort
- `POST /api/projects/{id}/github/sync-task/{specId}` → push task title/status to bound Project item + optional milestone state

### Task ↔ Issue sync

- `POST /api/projects/{id}/github/issues/link` `{ specId, issueNumber }` attaches an existing issue (verified to exist first), or `{ specId, create: true, title?, body?, labels? }` opens a new one — title/body default to the task's → `{ ok, mode: "attached"|"created", issue, task }`
- `POST /api/projects/{id}/github/issues/unlink` `{ specId }` → drops the link only; the issue is left untouched
- `POST /api/projects/{id}/github/issues/push` `{ specId }` → `{ ok, pushed: [fields], boardColumn, issueState, issue, task }`
- `POST /api/projects/{id}/github/issues/pull` `{ specId, adoptTitle? }` → `{ ok, changed: [fields], movedTo, titleDiverged, issue, task }`

Push sends task title, description and the state implied by the board column (`done` → `closed`, every other column → `open`). Pull mirrors issue state, assignee, labels and milestone; a `closed` issue moves the task to `done` and reopening returns it to `in_progress`, while an `open` issue on any other column moves nothing because `open` matches five columns. The issue title is mirrored into `githubIssueTitle` without overwriting the task title unless `adoptTitle` is set, so a refresh cannot discard a local edit.

Failures carry `status` (`missing_issues_permission` 403, `issue_not_found` 404, `no_issue_linked` / `not_linked_repo` 400, `upstream_error` 502), are persisted on the task as `githubIssueSyncError`, and are appended to the spec log `github-issue-sync`. Full mapping and permission tables: [github-setup.md](github-setup.md).

Task fields: `githubMilestoneNumber`, `githubMilestoneTitle`, `githubMilestoneUrl`, `githubProjectId`, `githubProjectItemId` (also via `PATCH …/tasks/{specId}`); issue link `githubIssueNumber`, `githubIssueUrl`, `githubIssueState`, `githubIssueTitle`, `githubIssueAssignee`, `githubIssueLabels`, `githubIssueSyncedAt`, `githubIssueSyncError`. Patching `githubIssueNumber` to `0` clears the link.

Roadmap phase/feature fields: `github_milestone_number`, `github_milestone_title`, `github_milestone_url`; features also `github_project_id`, `github_project_item_id`.

**Required GitHub App / token scopes (ORA connector):**

| Capability | App permission / PAT |
|------------|----------------------|
| List/create/update milestones | `issues: write` (+ `metadata: read`) |
| Link / push / pull task issues | `issues: write` (+ `metadata: read`) |
| List Projects v2 / draft items / Status | `organization_projects: write` (App) or fine-grained **Projects** read/write (PAT) |

Issue sync adds no permission beyond the `issues: write` milestones already need. Without `organization_projects`, milestone bind and issue sync still work; Projects list returns `missing_organization_projects`. Moving a board task with a linked Project item best-effort syncs Status.

## Board and tasks

- `GET /api/projects/{id}/board` → `{ columns, board }`
- `PUT /api/projects/{id}/board` → replace column membership
- `GET /api/projects/{id}/tasks` → `{ tasks }`
- `POST /api/projects/{id}/tasks` `{ title?, description, requireReviewBeforeCoding?, ideationId? }`
- `GET /api/projects/{id}/tasks/{specId}`
- `PATCH /api/projects/{id}/tasks/{specId}`
- `DELETE /api/projects/{id}/tasks/{specId}`
- `POST /api/projects/{id}/tasks/{specId}/move` `{ toStatus }`
- `POST /api/projects/{id}/tasks/{specId}/approve` → move `human_review` → `queue` (approve for coding)
- `GET /api/projects/{id}/tasks/{specId}/plan`
- `GET /api/projects/{id}/tasks/{specId}/progress`
- `GET /api/projects/{id}/tasks/{specId}/spec` → `{ markdown }`
- `GET /api/projects/{id}/tasks/{specId}/logs` → `{ logs: { planning?, implementation?, validation? } }`
- `GET /api/projects/{id}/tasks/{specId}/actions` → valid run actions (`planning`, `implementation`, `review`, `qaFix`, `approve`, …)
- `GET /api/projects/{id}/tasks/{specId}/changes` → the pending change set summary (paths + modes, never file bodies)
- `POST /api/projects/{id}/tasks/{specId}/deliver` → commit + push + open a pull request (see **Code delivery**)

Columns: `backlog`, `queue`, `in_progress`, `review`, `human_review`, `done`.

## Code delivery

`POST /api/projects/{id}/tasks/{specId}/deliver` turns the change set an
implementation run recorded into real repository history:

1. prepare an ephemeral clone of the linked repo (ORA `scm:clone` credentials)
2. apply the recorded `files[]` into that workspace
3. commit them on a task branch, staging **only** the paths that applied
4. push the branch with a short-lived Contents-write credential (ORA `scm:pr`)
5. open the pull request (ORA `scm:pr`)

Optional body: `{ "branch": "...", "base": "...", "draft": false }`. `branch`
defaults to `<OPM_DELIVERY_BRANCH_PREFIX>/<specId>` (prefix default `opm`) and is
sanitized; `base` defaults to the project's default branch.

Success (`200`):

```json
{
  "ok": true, "status": "delivered", "specId": "001-add-login",
  "branch": "opm/001-add-login", "commitSha": "…",
  "prNumber": 42, "prUrl": "https://github.com/acme/demo/pull/42",
  "prState": "open", "files": ["src/login.go", "src/login_test.go"]
}
```

`deliveryBranch`, `deliveryCommitSha`, `deliveryFiles`, `deliveredAt`,
`prNumber`, `prUrl`, `prState`, `deliveryStatus` and `deliveryError` are
persisted on the task and returned by `GET …/tasks/{specId}`.

### Failure is explicit

Every failure persists `deliveryStatus` + `deliveryError` on the task, appends a
line to the task's `delivery` spec log, and answers with a machine-readable
`status`. **A run that produced no file changes never reports success.**

| `status` | HTTP | Meaning |
|----------|------|---------|
| `delivered` | 200 | Branch pushed and pull request open |
| `no_changes_produced` | 409 | No run recorded file changes — nothing to deliver |
| `push_rejected` | 409 | The branch exists on the remote with different commits; retry with a new `branch` |
| `not_linked_repo` | 400 | The project has no `ownerRepo` + `connectorId` |
| `unsafe_path` | 400 | A change targeted `..`, an absolute path, `.git/`, or `.github/workflows/` |
| `change_set_too_large` | 400 | Over `OPM_DELIVERY_MAX_FILES` / `OPM_DELIVERY_MAX_BYTES` |
| `task_not_found` | 404 | Unknown `specId` |
| `missing_contents_permission` | 403 | The installation cannot push |
| `missing_pull_requests_permission` | 403 | The installation cannot open pull requests |
| `apply_failed` | 422 | A patch did not apply against the real source |
| `git_failed` / `push_failed` / `workspace_unavailable` | 422 | Branch, commit, push or clone failed |
| `peer_not_configured` | 503 | `PEER_ORA_URL` unset — credentials and pull requests live in ORA |
| `upstream_error` | 502 | Anything else ORA or GitHub returned |

Permission failures relay ORA's `detail` and `missing` permission list.

### Where the change set comes from

`GET …/tasks/{specId}/changes` reports what is waiting:

```json
{
  "deliverable": true, "fileCount": 2, "commitMessage": "Add login form",
  "source": "model", "model": "…", "runId": "…",
  "files": [{"path": "src/login.go", "mode": "write", "bytes": 812}]
}
```

`mode` is `write` (full new body), `patch` (unified diff) or `delete`. Only an
implementation run with a model runner configured records a change set; the
builtin path advances plan state and writes no code, so `deliverable` stays
`false` and `deliver` answers `no_changes_produced`. The change set is consumed
on a successful delivery so the same work cannot be committed twice.

## Roadmap / ideation / changelog

- `GET|PUT /api/projects/{id}/roadmap`
- `GET|PUT /api/projects/{id}/ideation`
- `GET|PUT /api/projects/{id}/changelog` → `{ markdown }`

## Status and jobs

- `GET /api/projects/{id}/status`
- `GET /api/projects/{id}/jobs`
- `POST /api/projects/{id}/jobs` `{ action, specId?, targetPhase?, ideationType? }`
- `GET /api/projects/{id}/jobs/{runId}`
- `POST /api/projects/{id}/jobs/{runId}/cancel`

Job actions include: `run-planning`, `run-implementation`, `run-review`, `run-qa-fix`, `run-followup-planning`, `recover-subtask`, `mark-stuck`, `pause-task`, `resume-task`, `skip-to-phase`, `run-roadmap-discovery`, `run-roadmap-features`, `run-ideation`, `generate-changelog`.

- `skip-to-phase` requires `specId` and 1-based `targetPhase` (plan phase number).
- `run-ideation` may set `ideationType` to one of the ideation type keys; omit to fill all types.

Jobs prepare an ephemeral clone (via ORA clone credentials when configured). When `/api/spawn-probe` reports `spawnReady: true` (docker CLI + daemon + `opm-runner-task:nas`), the job **docker-runs** one hardened ephemeral runner (`execution: "container"`), which invokes an OpenAI-compatible model when `OPM_MODEL_API_KEY` is set and writes `/out/result.json`. The control plane persists model output into `spec.md` / plan / progress / review / logs / roadmap / ideation. Without a key (or on model failure), the runner reports `mode=fallback` and shared builtin helpers still write artifacts. On spawn failure or `OPM_FORCE_BUILTIN=1`, jobs fall back to builtin-only (`execution: "builtin"`). Orchestrator `/api/spawn-probe` includes `modelConfigured` / `modelHonesty`.

### What jobs do and do not do

Limits that are easy to misread from the action list above:

- **Jobs never modify the cloned repository.** The job clone is discarded after the run (`_ = workDir`);
  there is no `git add`/`commit`/`push`, branch creation, or pull-request call. `run-implementation` advances
  the **plan**; `run-review` judges plan completeness, not a diff.
- **`run-roadmap-discovery`, `run-roadmap-features`, and `run-ideation`** write vision/phases/features/ideas
  via builtin helpers (after optional container spawn). Model apply still covers planning/implementation/review only.
- **`skip-to-phase`** requires `specId` and 1-based `targetPhase`; it marks earlier plan subtasks complete and advances progress.

