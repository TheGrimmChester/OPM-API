# Remaining backlog

Short gap list after containerized task spawn, stuck/recover, pause/resume, and board DnD.

## Working today (NAS)

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
| Jobs | Model-backed agents in runner | Container spawn works; prompts/agent CLI inside `opm-runner-task` still follow-on |
| Jobs | Higher-quality planning/impl | Helpers are heuristic until model-backed runners land |
| Roadmap / ideation | Agent runs | Actions enqueue but only placeholder logs |
| Pipeline | Skip-to-phase | Pause/resume shipped; skip still missing |
| Docs / E2E | Automated NAS suite | Manual curl + UI verification |

## Explicit non-goals (for now)

- Electron / local-folder projects (rejected by API)
- Duplicating ORA code-review / Repo Watch inside OPM
