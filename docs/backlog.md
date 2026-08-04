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
- **Containerized job spawn**: `opm-api` + `opm-orchestrator` ship docker CLI; compose mounts `docker.sock`; jobs `docker run` `opm-runner-task:nas` when `spawnReady` (builtin fallback)
- Shared artifact helpers write plan/spec/progress/review/changelog after spawn (or builtin-only when forced)
- **Stuck / recover**: `mark-stuck`, `recover-subtask`; cancel mid-run marks next subtask stuck
- **Pause / resume**: `pause-task` / `resume-task` gate automation until resumed
- Changelog generate + edit/save (`PUT …/changelog`)
- Orchestrator `/api/spawn-probe` with `spawnReady: true` when docker + runner image work

## Highest remaining gaps

| Area | Gap | Notes |
|------|-----|-------|
| **Delivery** | **No code delivery** | Largest gap. The job clone is created then discarded (`job_runner.go:45-50`, `_ = workDir`); only `git clone` exists (`workspace.go:57`, `:69`). No `git add`/`commit`/`push`, no branch, no pull request. `run-implementation` flips one plan subtask to `completed` (`job_runner.go:321-341`); `run-review` judges plan completeness, not a diff (`model_apply.go:146-152`) |
| Jobs | Model-backed agents in runner | **Shipped for three actions**: runner calls a chat-completions endpoint when `OPM_MODEL_API_KEY` is set (`cmd/opm-runner/main.go:69-100`) and the control plane applies it for planning, implementation, and review only (`model_apply.go:17-29`); builtin fallback otherwise |
| Roadmap / ideation | **Generation** | Manual create/edit/delete works. The three generation actions record an acknowledgement only: `job_runner.go:147-148` → `builtinMetaNote`, which appends one log line and returns `"builtin placeholder — agent prompts not wired yet"` (`job_runner.go:688-691`). Jobs still report `completed`, which reads as a silent failure. A model key does not help (`model_apply.go:27-29`). Generators exist on `origin/feature/agent-roadmap-ideation-skip` (open PR #18, unmerged) |
| Pipeline | Skip-to-phase | **Not implemented on `main`**: `skipToPhase` / `targetPhase` match no source file; the action hits `default:` and fails with `unsupported action` (`job_runner.go:149-151`). Implementation waits on `origin/feature/agent-roadmap-ideation-skip` |
| Orchestrator | Dispatch + reaping | `opm-orchestrator` is health + spawn-probe only and logs itself a "job scheduler stub" (`main.go:114`); spawn happens in-process in `opm-api` |
| Peer | Projects v2 draft title refresh | Silent no-op in ORA (`ORA-API/github_projects.go:294-302` returns nil; error discarded at `ORA-API/peer_scm_pm.go:268`) — task renames never reach the board |
| Docs / E2E | Automated suite | Manual curl + UI verification; no reproducible capability suite, which is why deployment claims were withdrawn from the family capability docs |

## Explicit non-goals (for now)

- Electron / local-folder projects (rejected by API)
- Duplicating ORA code-review / Repo Watch inside OPM
