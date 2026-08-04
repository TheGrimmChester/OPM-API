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
- Task detail drawer: plan / progress / spec / logs + run actions (incl. pause/resume/recover)
- Ideation create/edit/delete + promote; roadmap phase/feature create/edit/delete
- **Roadmap / ideation agents**: `run-roadmap-discovery`, `run-roadmap-features`, `run-ideation` write real roadmap/ideation artifacts (builtin; container spawn then helpers)
- **Skip-to-phase**: `skip-to-phase` with `targetPhase` completes earlier plan subtasks and jumps the pipeline
- **Containerized job spawn**: `opm-api` + `opm-orchestrator` ship docker CLI; compose mounts `docker.sock`; jobs `docker run` `opm-runner-task:nas` when `spawnReady` (builtin fallback)
- Shared artifact helpers write plan/spec/progress/review/changelog after spawn (or builtin-only when forced)
- **Stuck / recover**: `mark-stuck`, `recover-subtask`; cancel mid-run marks next subtask stuck
- **Pause / resume**: `pause-task` / `resume-task` gate automation until resumed
- Changelog generate + edit/save (`PUT …/changelog`)
- Orchestrator `/api/spawn-probe` with `spawnReady: true` when docker + runner image work

## Highest remaining gaps

| Area | Gap | Notes |
|------|-----|-------|
| **Delivery** | **No code delivery** | Largest gap. Job clone is discarded; only `git clone` exists. No commit/push/PR. `run-implementation` advances the plan; `run-review` judges plan completeness, not a diff |
| Jobs | Model-backed agents in runner | **Shipped for planning/implementation/review**: OpenAI-compatible API when `OPM_MODEL_API_KEY` set; fallback + builtin otherwise |
| Jobs | Higher-quality planning/impl | Model output persisted when key present; builtin heuristics remain fallback |
| Roadmap / ideation | Generation | **Shipped (builtin)**: discovery/features/ideation write real artifacts; model-backed path for these still follow-on |
| Pipeline | Skip-to-phase | **Shipped**: `skip-to-phase` + `targetPhase` |
| Orchestrator | Dispatch + reaping | Health + spawn-probe only (scheduler stub); spawn in-process in `opm-api` |
| Peer | Projects v2 draft title refresh | **Shipped**: renames reach the board and every outcome is reported. Blocked in practice until the App installation is granted organization projects write — reported as `missing_organization_projects`, not silently skipped |
| Docs / E2E | Automated suite | Manual curl + UI verification |


## Explicit non-goals (for now)

- Electron / local-folder projects (rejected by API)
- Duplicating ORA code-review / Repo Watch inside OPM
