# OPM-API

Go API for **Open Project Manager** — GitHub-linked projects, kanban boards, roadmaps, ideation, task specs/plans, and task-automation jobs.

Projects are GitHub repositories discovered via **OPA-Hub** (identity / orgs) and **ORA** (GitHub App / PAT connectors). Job workspaces are ephemeral tmp clones.

The companion UI is the **OPM-Dashboard** web dashboard (browser only), typically
http://127.0.0.1:8098 on smoke compose.

## Documentation

See [docs/index.md](docs/index.md).

## Quick start

```bash
export PEER_OPA_URL=http://127.0.0.1:8080
export PEER_ORA_URL=http://127.0.0.1:8091
export OPEN_SERVICE_JWT_SECRET=…   # shared with hub/ora
go run .
# GET http://127.0.0.1:8096/api/health
# GET http://127.0.0.1:8096/api/hub/status
```

## License

EUPL-1.2 — see [LICENSE](LICENSE).
