# Remaining backlog

Short gap list after stuck/recover, pause/resume, board DnD, and orchestrator spawn probe.

## Working today (NAS)

- Hub-auth via dashboard `/hub-auth` proxy; JWT accepted by `opm-api`
- Hub org discovery + ORA GitHub connectors / repo list / project link
- Board task create / move / edit / delete; **drag-and-drop**; task action menu; require-review; approve-for-coding
- Task detail drawer: plan / progress / spec / logs + run actions (incl. pause/resume/recover)
- Ideation create/edit/delete + promote; roadmap phase/feature create/edit/delete
- **Builtin job runner** writes real artifacts: planning → implementation → review → changelog
- **Stuck / recover**: `mark-stuck`, `recover-subtask`; cancel mid-run marks next subtask stuck
- **Pause / resume**: `pause-task` / `resume-task` gate automation until resumed
- Changelog generate + edit/save (`PUT …/changelog`)
- Jobs enqueue/cancel with `execution: "builtin"` + operator `message`
- Orchestrator spawn probe (docker + runner image honesty; `spawnReady: false` until real spawn)

## Highest remaining gaps

| Area | Gap | Notes |
|------|-----|-------|
| Jobs | Agent-in-container spawn | Probe ready; real `docker run` of agent still missing |
| Jobs | Higher-quality planning/impl | Builtin is heuristic until model-backed runners land |
| Roadmap / ideation | Agent runs | Actions enqueue but only placeholder logs |
| Pipeline | Skip-to-phase | Pause/resume shipped; skip still missing |
| Docs / E2E | Automated NAS suite | Manual curl + UI verification |

## Explicit non-goals (for now)

- Electron / local-folder projects (rejected by API)
- Duplicating ORA code-review / Repo Watch inside OPM
