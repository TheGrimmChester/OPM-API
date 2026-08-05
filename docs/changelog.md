# Changelog

## Unreleased

- Feature: **auto pipeline from backlog to done** — `POST …/tasks` defaults to autopilot (`humanReviewRequired=false`) and always enqueues `run-pipeline`. The pipeline auto-approves require-review-before-coding, drains every coding subtask, opens a PR only after all coding subtasks complete, then moves to `done` (merging when a PR exists). Pass `humanReviewRequired: true` to stop at `human_review` instead.

- Feature: **board moves start jobs** — dragging a card to `queue` / `in_progress` / `review` starts `run-planning` / `run-implementation` / `run-review` as the user who moved it. `backlog`, `human_review` and `done` deliberately start nothing (`human_review` *is* the human gate; autopilot already moves cards to `done` itself). The move response gains a `trigger` object; task fields stay at the top level, so existing callers are unaffected. Guards: any active job on the task blocks a new one (a double drag cannot fan out two implementation jobs onto one shared workspace), a paused task blocks the trigger, backwards drags re-open work without reverting a delivered branch or PR, and `autoStartOnMove` on the project (default **on**) turns it off for a manual board. The trigger is wired at the HTTP move endpoint and **never** in `MoveTask`: jobs move cards themselves when they finish, so a store-level hook would make each job start the next in a loop — `TestTriggerIsReachableOnlyFromTheMoveHandler` scans the package and fails if any other file references it.

- Feature: **per-job credentials and models from OAM** — with `PEER_OAM_URL` set, each job resolves its model *and* API key from the account plane for its own agent phase, scoped to the acting user's org and any personal override, so "light model to plan, strong model to implement" is configurable per org or per user instead of per deployment. Jobs now carry `organizationId` / `actorUsername` / `agentKey` (stamped at enqueue, including from a board drag) and record the resolved provider/model/source. An autopilot chain inherits the actor unconditionally but only carries a pinned model *within one agent phase* — coding → review → qa_fix each resolve their own, while the coding subtasks of one task keep a single model so a config edit mid-task cannot switch models between subtask 2.1 and 2.2. A resolve failure fails the job rather than falling back to the deployment-wide key. Only the one resolved model reaches the runner (the six `OPM_MODEL_<PHASE>` variables no longer travel into every container). `OPM_MODEL*` / `CURSOR_API_KEY` are read only when `PEER_OAM_URL` is unset, and that legacy path keeps its per-phase overrides unchanged.

- Feature: **per-task human review gate** — task field `humanReviewRequired` (default **true**). When true, after coding and auto-deliver the bot chains automated review (+ qa-fix if needed) and stops at `human_review`; no auto-merge or auto-complete. When false (autopilot), the bot runs review → qa-fix loop as needed → merges the delivery PR via ORA `scm:pr` (when available) → moves the task to `done`. Autopilot keeps the task session workspace (`$OPM_JOB_TMP/tasks/{projectId}/{specId}/repo`) for implementation, qa-fix, and review until completion; the human-gate path releases it at review start after deliver.

- Feature: **task-scoped implementation session** — all coding subtasks for a spec share one read-write workspace under `$OPM_JOB_TMP/tasks/{projectId}/{specId}/repo`. Each subtask still runs in a new ephemeral runner container, but file changes persist across steps; the change set merges incrementally. Successful steps auto-chain to the next pending coding subtask (`OPM_IMPL_AUTO_CHAIN`, default on). When coding completes, auto-deliver from the session workspace is optional (`OPM_IMPL_AUTO_DELIVER`, default on). Pause retains the workspace; review releases it and uses a fresh clone.

- Config: all Cursor Agent CLI phase models (`OPM_MODEL` and `OPM_MODEL_*`) default to `auto` when unset — planning, coding, review, ideation, and roadmap jobs all inherit that default for now.

- Feature: **repo-aware ideation and roadmap** — model-backed `run-ideation`, `run-roadmap-discovery`, and `run-roadmap-features` use a default-branch clone plus AutoCursor-near-parity context packs (`projectIndex`, `ideation_context`, discovery/features snapshots, implemented-item filters). The runner returns typed JSON to the control plane (OPM store is source of truth; no `.auto-cursor` writes into the customer repo). Phase model overrides: `OPM_MODEL_IDEATION`, `OPM_MODEL_ROADMAP_DISCOVERY`, `OPM_MODEL_ROADMAP_FEATURES`. Missing-clone honesty note when a connector+repo is linked but no `.git` mount is available. Builtins remain fallback only.

- Modules: drop the `../Open-*-Go` filesystem `replace` directives from `go.mod` and pin the family libraries at proxy-resolvable versions. The `OPA Checkup` `go-test` step failed on every commit — a CI runner checks out one repository, so `replacement directory ../Open-Auth-Go does not exist` aborted the build before a test ran, and a green tree was reported as a red check. `go test ./...` now passes in a bare checkout with a cold module cache. Local development against live sibling checkouts moves to a gitignored `go.work`, generated by `scripts/dev-go-work.sh`; see `docs/go-modules.md`. One version-to-version replace remains because `open-client-go`'s own published `go.mod` requires the untagged `open-auth-go v0.0.0` — it is removable once TheGrimmChester/Open-Client-Go#3 is merged.
- Feature: **code delivery** — `POST /api/projects/{id}/tasks/{specId}/deliver` applies the change set an implementation run recorded, commits it on a task branch, pushes it with a short-lived ORA `scm:pr` Contents-write credential, and opens a pull request. Returns `{branch, commitSha, prNumber, prUrl}`; persists `deliveryBranch` / `deliveryCommitSha` / `deliveryFiles` / `deliveredAt` / `prNumber` / `prUrl` / `prState` / `deliveryStatus` / `deliveryError` on the task. Failures are machine-readable (`no_changes_produced`, `push_rejected`, `unsafe_path`, `apply_failed`, `missing_contents_permission`, `missing_pull_requests_permission`, …), persisted on the task and appended to the task's `delivery` spec log. A run that produced no file changes never reports a delivery.
- Feature: the prepared clone is mounted **read-only at `/repo`** in `opm-runner-task`, so a task runs against real source. The runner returns `files[] { path, contents | patch | delete }` plus `commitMessage`; the control plane records that as the task's change set (`GET …/tasks/{specId}/changes`). The builtin path is unchanged and now says plainly that it writes no source code.
- Security: delivery credentials never touch disk, argv, remote URLs or logs; git output is redacted. `.git/`, `.github/workflows/`, absolute paths and `..` are refused; staging is explicit per applied path (never `git add -A`).

- Fixed (2026-08-04): the Projects v2 title refresh that previously did nothing now works. ORA resolves the
  board item to its backing draft issue and calls `updateProjectV2DraftIssue`, and OPM re-syncs on a title or
  description change — `PATCH …/tasks/{specId}` previously never touched the board at all. Every outcome is
  persisted on the task as `githubProjectSyncError` / `githubProjectSyncedAt`, appended to the
  `github-project-sync` spec log, and returned with a machine-readable `status`. A `200` from ORA whose
  `title_synced` is false is treated as a failure, not success, and an ORA that reports no `title_status` is
  treated as unknown rather than assumed good.
- Feature: unbind. `POST …/github/projects/unbind` clears the project-level Projects v2 bind and
  `POST …/github/projects/items/unbind` clears a single task's board-item bind; both leave GitHub untouched.
  `BindGitHubProject` now rejects a blank `projectId` instead of silently unbinding.
- Feature: milestone unassign. `POST …/github/milestones/unassign` `{ specId?|featureId?|phaseId? }` is the
  explicit inverse of `milestones/assign` and clears the bind on a task, a roadmap feature and/or a roadmap
  phase. The GitHub milestone is **neither closed nor deleted** — no peer call is made at all. Answers a
  machine-readable `status` (`ok`, `invalid_request`, `not_bound`, `task_not_found`, `feature_not_found`,
  `phase_not_found`, `roadmap_unreadable`) and appends the unassign to the `github-milestone-sync` spec log.
  Previously the only way to clear a bind was patching `githubMilestoneNumber` to `0`, which is silent, logs
  nothing and cannot reach a feature or phase.
- Note: Projects v2 needs **Organization permissions › Projects: Read and write** on the GitHub App
  installation, which the current installation does not have. Until it is granted every board call returns
  `missing_organization_projects` with the permission named; milestone and Issue routes are unaffected.
- Column moves no longer discard their sync error: a failed Status update after a board move is persisted on the
  task and logged, rather than dropped in a detached goroutine.
- Feature: Roadmap/ideation agent generators — `run-roadmap-discovery`, `run-roadmap-features`, and `run-ideation` write vision/phases/features/ideas (builtin helpers after optional container spawn); optional `ideationType` on enqueue.
- Feature: Skip-to-phase — `skip-to-phase` job with `targetPhase` marks earlier plan subtasks complete and advances progress; exposed on task actions.

- Feature: two-way GitHub Issue sync for tasks (`…/github/issues/{link,unlink,push,pull}` via ORA peer `scm:pm`). Push sends task title/description and the board column's issue state; pull mirrors issue state, assignee, labels and milestone, moving the task on the `done`/reopen boundary only. Title divergence is reported rather than resolved (`adoptTitle` opts in). Failures persist on the task as `githubIssueSyncError` and in the `github-issue-sync` spec log with a machine-readable `status`.
- Feature: GitHub Milestones + Projects v2 bind/sync via ORA peer `scm:pm` (`…/github/milestones`, `…/github/projects`, task/roadmap fields, Status sync on board move).

- Feature: Model-backed planning/implementation/review inside `opm-runner-task` (OpenAI-compatible `OPM_MODEL_*` env from compose; honest fallback + builtin artifacts when key missing).


- Feature: Containerized task spawn — docker CLI in `opm-api` image; jobs `docker run` `opm-runner-task` when spawnReady; builtin fallback via `OPM_FORCE_BUILTIN` or spawn failure; `/api/spawn-probe` returns `spawnReady: true` when docker + image work.
- Auth: adopt Open-Auth-Go per-user project ACLs (`project_ids` / `EnforceProjectACL` on Gate middleware). Restricted JWTs get **403** on non-member `X-Project-ID`; role `admin` stays unrestricted. No second membership store — hub-minted claims only.
- Builtin job runner writes `spec.md`, plan, progress, review/QA artifacts, changelog; `POST …/approve`, `GET …/spec|logs|actions`; `PUT …/changelog`.
- Default runner tag `nas`.
- Bump `open-tenant-go` to v0.2.2 so auth-enforced list scope matches `WriteTenant` defaults.
- Docs: tenant header defaults under auth; NAS curl examples in interop/security/api.
- Auth via Open-Auth-Go `Gate` (delete local `auth.go` duplicate; validate-only, no local login routes).

## 0.2.0

- Projects are GitHub repositories only (`owner/repo` + ORA `connectorId`).
- Board/task state moves under `$OPM_DATA_DIR/projects/<id>/` (no durable local clone registry).
- Hub linkage (`PEER_OPA_URL`): org discovery; ORA linkage (`PEER_ORA_URL`): connectors, repos, tmp-clone credentials.
- Job workspaces: ephemeral clone under `OPM_JOB_TMP`, cleaned up after the run.
- Removed local-folder / `path` project create.

## 0.1.1

- Document that the companion UI is the OPM-Dashboard web dashboard (browser:
  http://127.0.0.1:8098 on smoke compose).

## 0.1.0

- Initial Open Project Manager API: multi-project registry, kanban, tasks/specs/plans, roadmap, ideation, changelog, job stub with `Open-Job-Go` runner wiring.
- Images: `opm-api`, `opm-orchestrator` command, `opm-runner-task`.
