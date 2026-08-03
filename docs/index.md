# Open Project Manager API docs

| Doc | Description |
|-----|-------------|
| [architecture.md](architecture.md) | Components, data layout, job flow |
| [install.md](install.md) | Local and container install |
| [configuration.md](configuration.md) | Environment variables |
| [api.md](api.md) | HTTP control-plane surface |
| [interop.md](interop.md) | Peer URLs and service auth |
| [operations.md](operations.md) | Health, logs, upgrades |
| [security.md](security.md) | Auth, tenancy, sandbox boundaries |
| [changelog.md](changelog.md) | Release notes |

OPM owns GitHub-linked project work management: kanban, roadmaps, ideation, task specs/plans, and task-automation jobs (ephemeral clones). Discovery uses OPA-Hub (identity/orgs) and ORA (GitHub connectors). The operator UI is the **OPM-Dashboard** web dashboard (browser URL; smoke: http://127.0.0.1:8098). Code-review / Repo Watch remains with ORA — deep-link; do not duplicate review.
