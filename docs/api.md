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
- `POST /api/projects/{id}/github/projects/bind` `{ projectId, projectTitle?, projectUrl? }` → bind OPM project to a GitHub Project. A blank `projectId` is rejected — use `unbind`
- `POST /api/projects/{id}/github/projects/unbind` → clears the project-level bind; the GitHub Project and its items are left untouched
- `POST /api/projects/{id}/github/projects/items/unbind` `{ specId }` → clears one task's board-item bind; the board item is left untouched
- `POST /api/projects/{id}/github/projects/sync-item` `{ specId?|featureId?, title?, body?, status? }` → create/update draft Project item; maps OPM column → Status option best-effort
- `POST /api/projects/{id}/github/sync-task/{specId}` → push task title/description/status to bound Project item + optional milestone state

### Task ↔ board item sync

A draft item's title and body genuinely update: ORA resolves the board item to its backing draft issue and calls
the Projects v2 update mutation. Renaming a task or editing its description via `PATCH …/tasks/{specId}`
re-syncs the bound board item automatically (the response shape is unchanged — the returned task carries the
outcome).

Nothing about a failed board sync is silent. `sync-item` and `sync-task` answer with `status` and a real HTTP
code, the reason is persisted on the task as `githubProjectSyncError`, and it is appended to the spec log
`github-project-sync`. A success stamps `githubProjectSyncedAt` and clears the error.

| `status` | HTTP | Meaning |
|----------|------|---------|
| `ok` | 200 | The board item matches the task. |
| `nothing_to_sync` | 200 | Nothing was requested (no title or body). |
| `missing_organization_projects` | 403 | The App installation lacks organization projects write. Body carries `missing`. |
| `title_sync_unsupported` | 422 | The card is backed by a real Issue or PR, whose title lives on the issue — use the Issue sync routes instead. |
| `item_not_found` | 404 | The board item, or its draft content, is gone. |
| `no_project_bound` | 400 | No GitHub Project is bound to the task or project. |
| `upstream_error` | 502 | Anything else, including an ORA that reports no `title_status` at all. |

A `200` from ORA whose `title_synced` is false is a **partial failure** — the card exists but the rename did not
land — and is recorded exactly like a hard failure. A column move that fails to sync Status is persisted on the
task and logged even though its HTTP response has already been sent.

### Task ↔ Issue sync

- `POST /api/projects/{id}/github/issues/link` `{ specId, issueNumber }` attaches an existing issue (verified to exist first), or `{ specId, create: true, title?, body?, labels? }` opens a new one — title/body default to the task's → `{ ok, mode: "attached"|"created", issue, task }`
- `POST /api/projects/{id}/github/issues/unlink` `{ specId }` → drops the link only; the issue is left untouched
- `POST /api/projects/{id}/github/issues/push` `{ specId }` → `{ ok, pushed: [fields], boardColumn, issueState, issue, task }`
- `POST /api/projects/{id}/github/issues/pull` `{ specId, adoptTitle? }` → `{ ok, changed: [fields], movedTo, titleDiverged, issue, task }`

Push sends task title, description and the state implied by the board column (`done` → `closed`, every other column → `open`). Pull mirrors issue state, assignee, labels and milestone; a `closed` issue moves the task to `done` and reopening returns it to `in_progress`, while an `open` issue on any other column moves nothing because `open` matches five columns. The issue title is mirrored into `githubIssueTitle` without overwriting the task title unless `adoptTitle` is set, so a refresh cannot discard a local edit.

Failures carry `status` (`missing_issues_permission` 403, `issue_not_found` 404, `no_issue_linked` / `not_linked_repo` 400, `upstream_error` 502), are persisted on the task as `githubIssueSyncError`, and are appended to the spec log `github-issue-sync`. Full mapping and permission tables: [github-setup.md](github-setup.md).

Task fields: `githubMilestoneNumber`, `githubMilestoneTitle`, `githubMilestoneUrl`, `githubProjectId`, `githubProjectItemId`, `githubProjectSyncedAt`, `githubProjectSyncError` (also via `PATCH …/tasks/{specId}`); issue link `githubIssueNumber`, `githubIssueUrl`, `githubIssueState`, `githubIssueTitle`, `githubIssueAssignee`, `githubIssueLabels`, `githubIssueSyncedAt`, `githubIssueSyncError`. Patching `githubIssueNumber` to `0` clears the link.

Roadmap phase/feature fields: `github_milestone_number`, `github_milestone_title`, `github_milestone_url`; features also `github_project_id`, `github_project_item_id`.

**Required GitHub App / token scopes (ORA connector):**

| Capability | App permission / PAT |
|------------|----------------------|
| List/create/update milestones | `issues: write` (+ `metadata: read`) |
| Link / push / pull task issues | `issues: write` (+ `metadata: read`) |
| List Projects v2 / draft items / Status / **title refresh** | `organization_projects: write` (App — under **Organization permissions › Projects: Read and write**) or fine-grained **Projects** read/write (PAT) |

Issue sync adds no permission beyond the `issues: write` milestones already need.

**The current App installation does not have `organization_projects`, so the whole Projects v2 path — list, bind
sync, title refresh, Status on move — returns `missing_organization_projects` until it is granted and the
installation permissions are re-accepted.** Milestone bind and Issue sync are unaffected. Proving the live title
update end to end therefore needs a deployment with that permission granted.

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

Columns: `backlog`, `queue`, `in_progress`, `review`, `human_review`, `done`.

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

