# Remaining backlog

Short gap list after containerized task spawn, stuck/recover, pause/resume, and board DnD.

Statuses below describe **`origin/main`**, re-checked 2026-08-04 by reading the cited code. This checkout often
sits on a feature branch, so "it works when I run it" is not evidence that something merged. Deployment
verification is unavailable this pass (the running image reports build id `opm-api-dev` with no commit identity
and capability routes need an operator token), so nothing here is claimed as verified on the deployed stack.

## Working today

- Hub-auth via dashboard `/hub-auth` proxy; JWT accepted by `opm-api`
- Hub org discovery + ORA GitHub connectors / repo list / project link
- Board task create / move / edit / delete; **drag-and-drop**; task action menu; require-review; approve-for-coding
- **Auto pipeline on create**: new tasks default to autopilot and enqueue `run-pipeline` through to `done` (PR after review PASS)
- Task detail drawer: plan / progress / spec / logs + run actions (incl. pause/resume/recover)
- Ideation create/edit/delete + promote; roadmap phase/feature create/edit/delete
- **Roadmap / ideation agents**: `run-roadmap-discovery`, `run-roadmap-features`, `run-ideation` prefer model apply with repo-aware context packs (`projectIndex`, ideation/discovery/features); builtins remain fallback
- **Skip-to-phase**: `skip-to-phase` with `targetPhase` completes earlier plan subtasks and jumps the pipeline
- **Containerized job spawn**: `opm-api` + `opm-orchestrator` ship docker CLI; compose mounts `docker.sock`; jobs `docker run` `opm-runner-task:nas` when `spawnReady` (builtin fallback)
- Shared artifact helpers write plan/spec/progress/review/changelog after spawn (or builtin-only when forced)
- **Stuck / recover**: `mark-stuck`, `recover-subtask`; cancel mid-run marks next subtask stuck
- **Pause / resume**: `pause-task` / `resume-task` gate automation until resumed
- Changelog generate + edit/save (`PUT …/changelog`)
- Orchestrator `/api/spawn-probe` with `spawnReady: true` when docker + runner image work
- **Repo mounted into the runner**: the prepared clone is bind-mounted read-only at `/repo`; the runner returns `files[] { path, contents | patch | delete }` + `commitMessage`
- **Code delivery**: `POST …/tasks/{specId}/deliver` applies the recorded change set, commits on `opm/<specId>`, pushes with an ORA `scm:pr` Contents-write credential, opens a pull request, and persists `deliveryBranch` / `deliveryCommitSha` / `deliveryFiles` / `prNumber` / `prUrl` / `prState` / `deliveryStatus` / `deliveryError`
- Delivery section in task detail (pending changes, changed-file summary, PR link) + **PR badge** on board cards

## Highest remaining gaps

| Area | Gap | Notes |
|------|-----|-------|
| Delivery | Code delivery | **Shipped**: the job clone is mounted read-only at `/repo`; `POST …/tasks/{specId}/deliver` applies the runner's change set, commits on a task branch, pushes via ORA `scm:pr`, and opens a pull request. Local `run-review` is retired — review column is human parking; deep review is ORA |
| Jobs | Model-backed agents in runner | **Shipped** for planning/implementation/ideation/roadmap when OAM resolve succeeds (`PEER_OAM_URL`); fail closed without OAM |
| Jobs | Higher-quality planning/impl | Model output persisted when key present; builtin heuristics remain fallback |
| Roadmap / ideation | Generation | **Shipped (model + builtin fallback)**: repo-aware context packs, projectIndex, implemented filters; OPM store is source of truth |
| Pipeline | Skip-to-phase | **Shipped**: `skip-to-phase` + `targetPhase` |
| Orchestrator | Dispatch + reaping | Health + spawn-probe only (scheduler stub); spawn in-process in `opm-api` |
| Peer | Projects v2 draft title refresh | **Shipped**: renames reach the board and every outcome is reported. Blocked in practice until the App installation is granted organization projects write — reported as `missing_organization_projects`, not silently skipped |
| Docs / E2E | Automated suite | Manual curl + UI verification |
| Delivery | **Builtin path produces no code** | **Not implemented, by design.** The builtin runner advances plan state only. Without OAM credentials no change set is recorded and `deliver` answers `no_changes_produced`. Code delivery requires a configured model runner. |
| Delivery | **PR state is a snapshot, not tracked** | **Not implemented.** `prState` is whatever the pull request was when it was opened. There is no webhook or poll, so a PR merged or closed on GitHub still shows `open` in OPM until the next delivery. |
| Delivery | **No delivery from the board** | **Not implemented.** Delivery is available in task detail only; the board shows the resulting PR badge but has no deliver action. |
| Delivery | **Force-push / branch reuse** | **Not implemented, deliberately.** A branch that exists on the remote with different commits yields `push_rejected`; the recovery is to re-deliver with an explicit `branch`. OPM never force-pushes. |
| Delivery | **No automatic column move** | **Not implemented.** A delivery does not move the task on the board; the operator decides. |
| Delivery | Multi-commit / stacked deliveries | One delivery is one commit on one branch. |
| Delivery | **Live-stack verification** | **Outstanding.** The chain is covered end to end by tests against a stub ORA and a local git remote; it has not been exercised against a real GitHub App on the deployed stack. |


## Explicit non-goals (for now)

- Electron / local-folder projects (rejected by API)
- Duplicating ORA code-review / Repo Watch inside OPM
