# Remaining backlog

Short gap list after AutoCursor parity pass (builtin runners + task detail + approve + changelog save).

## Working today (NAS)

- Hub-auth via dashboard `/hub-auth` proxy; JWT accepted by `opm-api`
- Hub org discovery + ORA GitHub connectors / repo list / project link
- Board task create / move / edit / delete; require-review checkbox; approve-for-coding
- Task detail drawer: plan / progress / spec / logs + run actions
- Ideation create/edit/delete + promote; roadmap phase/feature create/edit/delete
- **Builtin job runner** writes real artifacts: `run-planning` → `spec.md` + plan; `run-implementation` subtask loop; `run-review` / `run-qa-fix`; `generate-changelog`
- Changelog generate + edit/save (`PUT …/changelog`)
- Jobs enqueue/cancel with `execution: "builtin"` + operator `message`

## Highest remaining gaps

| Area | Gap | Notes |
|------|-----|-------|
| Jobs | Cursor-in-container spawn | Builtin is real artifact E2E; `opm-runner-task` still placeholder; orchestrator has docker.sock |
| Jobs | Model-quality planning/impl | Builtin is heuristic, not Cursor CLI |
| Board | Drag-and-drop | Button moves + action buttons; no DnD |
| Roadmap / ideation | Agent runs | Actions enqueue but only placeholder logs |
| Pipeline | Pause / resume / skip-to-phase | Missing |
| Follow-up planning | UI enqueue | API/builtin supports `run-followup-planning` |
| Docs / E2E | Automated NAS suite | Manual curl + UI verification |

## Explicit non-goals (for now)

- Electron / local-folder projects (rejected by API)
- Duplicating ORA code-review / Repo Watch inside OPM
