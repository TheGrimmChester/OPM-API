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

Task fields: `githubMilestoneNumber`, `githubMilestoneTitle`, `githubMilestoneUrl`, `githubProjectId`, `githubProjectItemId` (also via `PATCH …/tasks/{specId}`).

Roadmap phase/feature fields: `github_milestone_number`, `github_milestone_title`, `github_milestone_url`; features also `github_project_id`, `github_project_item_id`.

**Required GitHub App / token scopes (ORA connector):**

| Capability | App permission / PAT |
|------------|----------------------|
| List/create/update milestones | `issues: write` (+ `metadata: read`) |
| List Projects v2 / draft items / Status | `organization_projects: write` (App) or fine-grained **Projects** read/write (PAT) |

Without `organization_projects`, milestone bind still works; Projects list returns `missing_organization_projects`. Moving a board task with a linked Project item best-effort syncs Status.

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
- `POST /api/projects/{id}/jobs` `{ action, specId? }`
- `GET /api/projects/{id}/jobs/{runId}`
- `POST /api/projects/{id}/jobs/{runId}/cancel`

Job actions include: `run-planning`, `run-implementation`, `run-review`, `run-qa-fix`, `run-followup-planning`, `recover-subtask`, `mark-stuck`, `pause-task`, `resume-task`, `run-roadmap-discovery`, `run-roadmap-features`, `run-ideation`, `generate-changelog`.

Jobs prepare an ephemeral clone (via ORA clone credentials when configured). When `/api/spawn-probe` reports `spawnReady: true` (docker CLI + daemon + `opm-runner-task:nas`), the job **docker-runs** one hardened ephemeral runner (`execution: "container"`), which invokes a chat-completions-compatible model endpoint when `OPM_MODEL_API_KEY` is set and writes `/out/result.json`. The control plane persists model output into `spec.md` / plan / progress / review / logs. Without a key (or on model failure), the runner reports `mode=fallback` and shared builtin helpers still write artifacts. On spawn failure or `OPM_FORCE_BUILTIN=1`, jobs fall back to builtin-only (`execution: "builtin"`). Orchestrator `/api/spawn-probe` includes `modelConfigured` / `modelHonesty`.

### What jobs do and do not do

Three limits are easy to misread from the action list above. Each is a property of the current code, not a
configuration you can switch on:

- **Jobs never modify the cloned repository.** `prepareJobWorkspace` creates the clone and `executeJob`
  discards the handle immediately (`job_runner.go:45-50`, `_ = workDir`). The only git commands in this service
  are two `git clone` invocations (`workspace.go:57`, `:69`) — there is no `git add`, `commit`, `push`, branch
  creation, or pull-request call anywhere. `run-implementation` advances the **plan**: it flips the next
  subtask to `completed` (`job_runner.go:321-341`) and, on the model path, appends a section to
  `IMPLEMENTATION_NOTES.md` (`model_apply.go:111-135`). `run-review` derives PASS/FAIL from plan completeness
  (`model_apply.go:146-152`), not from reading a diff.
- **`run-roadmap-discovery`, `run-roadmap-features`, and `run-ideation` record an acknowledgement only.** All
  three route to `builtinMetaNote` (`job_runner.go:147-148`), which appends one project-log line and returns
  `"builtin placeholder — agent prompts not wired yet"` (`job_runner.go:688-691`). The job reports
  `state: completed`, so treat a success here as "the request was logged", not "content was generated".
  Setting `OPM_MODEL_API_KEY` does not change this: `applyRunnerResult` handles planning, implementation, and
  review only and returns `false` for everything else (`model_apply.go:17-29`).
- **`skip-to-phase` is not implemented.** `POST …/jobs` with that action falls into the `default` branch and
  fails with `unsupported action` (`job_runner.go:149-151`). There is no `targetPhase` field on the job model.
