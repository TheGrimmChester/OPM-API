# API

Base path: `/api`. JSON request/response. Auth optional unless `OPA_AUTH_REQUIRED` is set.

When auth is required (NAS), send `Authorization: Bearer <oam-jwt>` plus **`X-Organization-ID`** / **`X-Project-ID`**. See [interop.md](interop.md#tenant-headers) and [security.md](security.md).

## Health

- `GET /api/health` → `{ status, service: "opm-api", version }`
- `GET /api/peer/health` → service JWT probe (`health:read` when `OPEN_SERVICE_JWT_SECRET` set)

## Hub and GitHub discovery

- `GET /api/hub/status` → hub / OAM / ORA linkage (`credentials_home` is `oam` when `PEER_OAM_URL` is set, else `ora` / `env`; plus `peer_oam_configured`, …)
- `GET /api/hub/organizations` → organizations from OPA-Hub (falls back to `default-org`)
- `GET /api/github/connectors?organizationId=` → proxies ORA connector list (storage in OAM)
- `GET /api/github/connectors/{id}/repos?organizationId=` → proxies installable GitHub repos

Requires `PEER_OPA_URL` / `PEER_ORA_URL` (and `PEER_OAM_URL` for per-job models/keys) as documented in [interop.md](interop.md).

## Projects (GitHub repos)

- `GET /api/projects` → `{ projects: Project[] }`
- `POST /api/projects` `{ connectorId, ownerRepo, organizationId?, name?, githubRepoId?, htmlUrl?, defaultBranch? }` → linked `Project`
- `GET /api/projects/{id}` → `Project`
- `DELETE /api/projects/{id}` → 204 (removes registry entry + server-side board data)
- `POST /api/projects/{id}/init` → ensure data-dir layout

`Project` fields: `id`, `name`, `ownerRepo`, `connectorId`, `organizationId`, `githubRepoId`, `htmlUrl`, `defaultBranch`, optional `githubProjectId` / `githubProjectTitle` / `githubProjectUrl` (Projects v2 bind), timestamps.

Local folder / `path` create payloads are rejected.

## GitHub Milestones and Projects (via ORA)

Credentials are **stored** in **OAM** and exercised via **ORA** peer `scm:pm` endpoints; the dashboard uses these project-scoped routes (send org/project headers).

- `GET /api/projects/{id}/github/milestones` → `{ milestones: [{ number, title, description, state, html_url }] }`
- `POST /api/projects/{id}/github/milestones/assign` `{ specId?|featureId?|phaseId?, milestoneNumber?, title?, description?, state?, createIfMissing? }` → bind + optional upsert on GitHub
- `POST /api/projects/{id}/github/milestones/unassign` `{ specId?|featureId?|phaseId? }` → clears the milestone bind; the GitHub milestone is left untouched (see below)
- `GET /api/projects/{id}/github/projects` → `{ projects: [{ id, title, url, number }], boundProjectId, … }` (Projects v2 GraphQL)
- `POST /api/projects/{id}/github/projects/bind` `{ projectId, projectTitle?, projectUrl? }` → bind OPM project to a GitHub Project. A blank `projectId` is rejected — use `unbind`
- `POST /api/projects/{id}/github/projects/unbind` → clears the project-level bind; the GitHub Project and its items are left untouched
- `POST /api/projects/{id}/github/projects/items/unbind` `{ specId }` → clears one task's board-item bind; the board item is left untouched
- `POST /api/projects/{id}/github/projects/sync-item` `{ specId?|featureId?, title?, body?, status? }` → create/update draft Project item; maps OPM column → Status option best-effort
- `POST /api/projects/{id}/github/sync-task/{specId}` → push task title/description/status to bound Project item + optional milestone state

### Milestone bind ↔ unassign

`assign` is the forward direction; `unassign` is its explicit inverse and is **OPM-side only** — the GitHub
milestone is neither closed nor deleted, exactly like `issues/unlink` leaves the issue open and
`projects/unbind` leaves the board item on the board.

Send at least one of `specId`, `featureId`, `phaseId`; several may be unassigned in one call. Success answers
`200` with `status: "ok"`, a `cleared` array (`[{ target: "task"|"feature"|"phase", id, milestoneNumber,
milestoneTitle }]`), `unassignedMilestoneNumber` / `unassignedMilestoneTitle` mirroring `cleared[0]`, a `note`
saying GitHub was left alone, and the updated `task` and/or `roadmap`. When a task is unassigned the same is
appended to the spec log `github-milestone-sync`.

| `status` | HTTP | Meaning |
|----------|------|---------|
| `ok` | 200 | The bind was cleared. `cleared` lists every target. |
| `invalid_request` | 400 | Malformed JSON, or none of `specId` / `featureId` / `phaseId` was sent. |
| `not_bound` | 400 | No requested target carried a milestone bind, so there was nothing to clear. |
| `task_not_found` | 404 | `specId` does not resolve in this project. |
| `feature_not_found` / `phase_not_found` | 404 | The id is absent from the roadmap. Nothing is written — a bad `phaseId` cannot leave the feature half-unassigned. |
| `roadmap_unreadable` | 500 | `roadmap.json` could not be parsed. |

A bind counts as present when *any* of number, title or url is set, so a half-written bind can be cleaned up
rather than being reported as `not_bound`. `unassign` makes no peer call at all, but it shares the `…/github/…`
precondition and so still answers `503` when `PEER_ORA_URL` is unset — the same as `projects/unbind`.

Patching `githubMilestoneNumber` to `0` via `PATCH …/tasks/{specId}` still clears the three task fields, but it
is silent, writes no spec-log entry, and cannot touch a roadmap feature or phase. Prefer `unassign`.

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

Task fields: `githubMilestoneNumber`, `githubMilestoneTitle`, `githubMilestoneUrl`, `githubProjectId`, `githubProjectItemId`, `githubProjectSyncedAt`, `githubProjectSyncError` (also via `PATCH …/tasks/{specId}`); issue link `githubIssueNumber`, `githubIssueUrl`, `githubIssueState`, `githubIssueTitle`, `githubIssueAssignee`, `githubIssueLabels`, `githubIssueSyncedAt`, `githubIssueSyncError`. Patching `githubIssueNumber` to `0` clears the link; patching `githubMilestoneNumber` to `0` clears the milestone fields, though `milestones/unassign` is the documented inverse.

Roadmap phase/feature fields: `github_milestone_number`, `github_milestone_title`, `github_milestone_url`; features also `github_project_id`, `github_project_item_id`. The milestone bind on a feature or phase is cleared only by `milestones/unassign`.

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
- `POST /api/projects/{id}/tasks` `{ title?, description, requireReviewBeforeCoding?, humanReviewRequired?, ideationId? }` — defaults to autopilot (`humanReviewRequired=false`) and always enqueues `run-pipeline` (plan → all subtasks → review → deliver → `done`)
- `GET /api/projects/{id}/tasks/{specId}`
- `PATCH /api/projects/{id}/tasks/{specId}` — includes `humanReviewRequired` (boolean). When **true** (default), after coding the bot runs automated review (+ qa-fix if needed), opens a PR after review PASS when auto-deliver is on, and stops at `human_review` for a human. When **false** (autopilot), the bot continues through deliver/merge and marks the task `done` without a human gate; the task session workspace is retained until completion.
- `DELETE /api/projects/{id}/tasks/{specId}`
- `POST /api/projects/{id}/tasks/{specId}/move` `{ toStatus }` → the task fields (unchanged) plus `trigger`. **Moving a card starts the job that column represents** — see [Board moves start jobs](#board-moves-start-jobs).
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

Jobs prepare an ephemeral clone (via ORA clone credentials when configured). When `/api/spawn-probe` reports `spawnReady: true` (docker CLI + daemon + `opm-runner-task:nas`), the job **docker-runs** one hardened ephemeral runner (`execution: "container"`). With `PEER_OAM_URL` set, the runner gets the model + API key resolved from OAM for that job’s agent phase (fail closed if unavailable). Without OAM, the legacy path uses `OPM_MODEL_API_KEY` / `CURSOR_API_KEY` (or OpenAI-compatible chat when `OPM_MODEL_PROVIDER=openai`). The runner writes `/out/result.json`; the control plane persists model output into `spec.md` / plan / progress / review / logs / roadmap / ideation. Without a key (or on model failure), the runner reports `mode=fallback` and shared builtin helpers still write artifacts. On spawn failure or `OPM_FORCE_BUILTIN=1`, jobs fall back to builtin-only (`execution: "builtin"`). Orchestrator `/api/spawn-probe` includes `modelConfigured` / `modelHonesty`.

**Repo-aware ideation / roadmap:** for `run-ideation`, `run-roadmap-discovery`, and `run-roadmap-features`, the control plane builds a `projectIndex` from the mounted default-branch clone, writes a nested `context` pack into runner `input.json` (discovery vs features vs ideation — including `ideation_context`, implemented Done-board idea filters, and implemented feature exclusions), and applies model JSON (`vision`/`phases`/`features`/`ideas`) into the OPM store. With OAM unset, phase model env keys: `OPM_MODEL_IDEATION`, `OPM_MODEL_ROADMAP_DISCOVERY`, `OPM_MODEL_ROADMAP_FEATURES` (fallback `OPM_MODEL` → `auto`). When a linked repo+connector exists but no usable `.git` clone is mounted, the input includes an honesty note so the agent does not invent file paths.

### What jobs do and do not do

Limits that are easy to misread from the action list above:

- **Implementation uses a shared task workspace, not a throwaway clone per subtask.**
  Coding and review jobs mount `$OPM_JOB_TMP/tasks/{projectId}/{specId}/repo` (coding read-write; review read-only) at `/repo`.
  Each subtask still runs in a **fresh ephemeral runner container**, but all coding steps share
  the same host volume; model `files[]` output is applied to disk immediately and merged into
  the task change set. When coding completes, the bot chains `run-review`. After review **PASS**
  (and a complete plan), delivery can run automatically (`OPM_IMPL_AUTO_DELIVER`, default on).
  **Human review gate** (`humanReviewRequired`, default on): after review PASS + optional deliver,
  the bot stops at `human_review` and releases the session workspace. **Autopilot**
  (`humanReviewRequired: false`): the same session stays until the PR is merged and the task is
  `done`. **Pause** keeps the workspace; auto-chain stops until **resume**.
- **Other jobs still use ephemeral clones.** Planning, ideation, and roadmap jobs clone
  under `$OPM_JOB_TMP/{runId}/repo`, mount read-only, and discard scratch after the run.
  Delivery without a live task workspace still clones under `$OPM_JOB_TMP/deliver-<uuid>/repo`.
- **`run-roadmap-discovery`, `run-roadmap-features`, and `run-ideation`** prefer model
  apply when the runner returns typed JSON (`mode=model`); builtins remain fallback only.
  Model generators use the default-branch clone + per-action context packs; they do not
  write agent state into the customer repository.
- **`skip-to-phase`** requires `specId` and 1-based `targetPhase`; it marks earlier plan subtasks complete and advances progress.


## Board moves start jobs

Dragging a card to a column starts the job that column represents, running as the
user who moved it.

| Target column | Starts | Notes |
|---|---|---|
| `queue` | `run-planning` | needs a spec |
| `in_progress` | `run-implementation` | opens/continues the task session |
| `review` | `run-review` | `humanReviewRequired` then decides whether autopilot continues to merge |
| `backlog` | nothing | parking |
| `human_review` | nothing | this *is* the human gate — where autopilot parks |
| `done` | nothing | autopilot already merges and moves here itself |

The move response adds a `trigger` object; the task fields stay at the top level,
so existing callers are unaffected.

```json
{ "specId": "001-thing", "status": "in_progress", "...": "…",
  "trigger": { "started": true, "action": "run-implementation",
               "runId": "1785…", "reason": "board move to in_progress started run-implementation" } }
```

A no-op always says why (`"started": false` with a `reason`) — "I dragged the card
and nothing happened" should never need a log dive.

### Why the trigger is not in the store

`MoveTask` is the single validated choke point for column changes, which makes it
the obvious place for this hook and the wrong one. **Jobs move cards themselves**
when they finish (`model_apply.go`, `job_runner.go`, `autopilot.go`), and
`github_issue_sync.go` moves them when a GitHub issue is pulled. A trigger in the
store would make each of those start another job — review → `human_review` →
review, forever, burning model tokens while looking like a board bug.

So the trigger lives at the HTTP boundary and only a real board drag reaches it.
`TestTriggerIsReachableOnlyFromTheMoveHandler` scans the package sources and fails
if any other file references it.

### Guards

- **Dedupe.** Any active job (`queued`/`starting`/`running`) on the same task
  blocks a new one — not just the same action. A double drag, an optimistic-UI
  retry, or a drag during a running job cannot fan out two `run-implementation`
  jobs writing the same shared workspace. If the job list cannot be read, the
  trigger refuses rather than risking a duplicate.
- **Paused tasks.** A paused task blocks the trigger, so a drag is not a back door
  around the pause that stops automation everywhere else.
- **No undo.** Dragging `review` → `in_progress` re-opens implementation; it does
  not revert a delivered branch or close a PR. Undoing delivery stays a deliberate
  action.
- **Per project.** `autoStartOnMove` on the project (default **on**, including for
  projects that predate the field) turns the whole behaviour off for a manual board.
