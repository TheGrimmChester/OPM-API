# Changelog

## Unreleased

- Correction (2026-08-04): the Projects v2 entry below overstates item sync. Binding a project, creating draft
  items, and Status-on-move work, but **title refresh for an existing draft item does nothing** —
  `ORA-API/github_projects.go:294-302` returns `nil` without calling the API and the error is discarded at
  `ORA-API/peer_scm_pm.go:268`, so a renamed task never reaches the board and no failure is reported. Original
  entry kept below as written.
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
