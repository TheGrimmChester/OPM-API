# Remaining backlog

Short gap list after the PM CRUD dashboard ship (ideation/roadmap write UI, board edit/delete, jobs specId + cancel). Update as items land.

## Working today (NAS)

- Hub-auth via dashboard `/hub-auth` proxy; JWT accepted by `opm-api`
- Hub org discovery + ORA GitHub connectors / repo list / project link
- Board task create / move / edit / delete
- Ideation create + promote-to-task; roadmap phase/feature create
- Jobs enqueue (optional `specId`), cancel active runs; `run-planning` completes after git + clone-credentials fixes
- Changelog read; project status read

## Highest remaining gaps

| Area | Gap | Notes |
|------|-----|-------|
| Jobs | Real runner spawn | `stubRunJob` prepares workspace + Docker argv but does not exec the container; planning “completes” without model output |
| Plans | Plan / progress UI | API has `GET …/tasks/{id}/plan` and `…/progress`; dashboard does not surface them |
| Roadmap / ideation | Edit & delete | Create + promote only; no inline edit/delete of phases, features, or ideas |
| Board | Drag-and-drop | Column moves are button-based; no DnD |
| Changelog | Write / generate UX | `generate-changelog` job action exists; changelog page is read-only markdown |
| Multi-tenant headers | Project header | Org id now attached from active project; `X-Project-ID` not yet set on all calls |
| Orchestrator | Scheduler | `opm-orchestrator` health only — no reaper / spawn loop yet |
| Docs / E2E | Automated NAS suite | Manual curl + UI verification; no committed family harness for OPM CRUD |

## Explicit non-goals (for now)

- Electron / local-folder projects (rejected by API)
- Duplicating ORA code-review / Repo Watch inside OPM
