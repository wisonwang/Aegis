# Contributing to Aegis

Thanks for considering a contribution! Aegis is a single-binary Go project with
a hard rule: **governance must be unavoidable**. Any change that lets an Agent
or app reach a database *around* Aegis is a regression.

## Prerequisites

- Go 1.25+ (the repo's `go.mod` is `1.25.0`, and the Dockerfile builds with `golang:1.25.4`).
- `make` 可选，仓库提供了本地调试目标；你也可以继续直接用 `go` 命令。

## Build & test

```bash
# from the repo root
GOPROXY=https://goproxy.cn GOSUMDB=sum.golang.google.cn GOFLAGS=-mod=mod \
  go build ./...

GOPROXY=https://goproxy.cn GOSUMDB=sum.golang.google.cn GOFLAGS=-mod=mod \
  go vet ./...

GOPROXY=https://goproxy.cn GOSUMDB=sum.golang.google.cn GOFLAGS=-mod=mod \
  go test ./...
```

> **Note for contributors behind restrictive networks:** the default `GOPROXY`
> may be overridden by an OS environment variable that breaks `go get`. The
> commands above pin a reachable proxy/checksum db. Adjust if your region needs
> a different mirror.

## Run locally (seeded demo)

```bash
make dev
# or
go run ./cmd/aegis -config conf/config.demo.json
# first boot seeds an admin/analyst/mcp-agent tenant + a demo SQLite datasource
```

Then exercise the gateway:

```bash
bash examples/dataapi/curl.sh
python3 examples/mcp/client.py
```

## Live end-to-end testing tips

- On **macOS** the `timeout` command is unavailable — start the server with
  `nohup … &`, `sleep` a few seconds, then `kill` the PID.
- The control-plane SQLite DB's **parent directory must exist** before boot, or
  you'll get `error 14`. `conf/config.demo.json` already points `data_dir` at `./data`,
  which the seed step creates; create it manually if you change the path.

## Where things live

| Path | Responsibility |
|------|----------------|
| `internal/config` | configuration + env overrides |
| `internal/store` | control plane (SQLite) + permission aggregates |
| `internal/auth` | JWT, bcrypt, OIDC, LDAP |
| `internal/permission` | SQL rewrite engine (table/row/column) |
| `internal/proxy` | execution, masking, metrics, **estimate** |
| `internal/api` | DataAPI + admin API |
| `internal/mcp` | MCP (Streamable HTTP) service |
| `internal/server` | route assembly + embedded web UI + seed |

## Pull requests

1. Keep the **governance-in-the-middle** invariant intact — new query paths must
   go through `permission.Rewrite` + `proxy.Execute`.
2. Add a test (unit preferred; live e2e for new transport/feature paths).
3. Update `README.md` / `CHANGELOG.md` if behavior or the public surface changes.
4. Run `go build ./... && go vet ./... && go test ./...` green before pushing.
5. Prefer `make mcp-e2e` as a smoke test when touching MCP, auth, proxy, seed, or config paths.
6. GitHub Actions runs build / vet / test / MCP E2E smoke, plus advisory `govulncheck` / `gosec`; keep local commands aligned with CI.
