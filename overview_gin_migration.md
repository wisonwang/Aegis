# Aegis 路由层迁移到 GIN + gin-swagger 自动萃取 API 文档

**Commit**: `121d254` · 19 files changed, +9093 / −448

## 做了什么
按用户决策，将 HTTP 路由层从标准库 `net/http`(`http.ServeMux`)整体迁移到 **GIN**，并用 **gin-swagger** 从代码注解自动萃取 OpenAPI 文档（不再单独维护手写接口文档）。

## 核心策略（治理逻辑零改写）
- **路由层重写**：`internal/server/server.go` 的 `registerRoutes` 改写为 `gin.Engine`；`internal/enterprise/enterprise.go` 的 `Register` 签名改为收 `*gin.Engine`。
- **中间件复用**：`Authenticate` / `WorkspaceResolver` / `RequireAdmin` / `logging.Middleware` 全部是 `http.HandlerFunc` 组合器，仅通过 `gin.WrapF` 接入 GIN 链，**鉴权与默认拒绝逻辑一字未动**。
- **路径参数桥接**：新增 `internal/api/ginparam.go`，`withPathParams` 中间件把 GIN 的 `:id` 等参数镜像进 request context；handler 用 `pathParam(r, "id")` 读取（先试 `r.PathValue` 再回退 context）。70 处 `r.PathValue(...)` 批量替换为 `pathParam(r, ...)`，治理代码路由无关。

## 关键设计决策
- **静态 UI 用 fall-through 中间件**（`staticWebMiddleware`）而非 catch-all，否则会遮蔽 `/admin/api/*` 与 Swagger 路由。
- **Swagger 挂载** `/admin/api/docs/*any` → `ginSwagger.WrapHandler(swaggerFiles.Handler)`；`/admin/api/docs` 与带斜杠的 `/admin/api/docs/` 都 302 到 `index.html`。
- **后台「API 接入」tab**：原 `index.html` 第 302–517 行的手写文档替换为嵌入 `/admin/api/docs/` 的 Swagger UI iframe，落实「不单独维护接口文档」。

## 文档自动萃取
- 为全部 DataAPI（11）+ 管理接口（53）补充 swag 注解；注解由脚本从 `server.go`/`enterprise.go` 的路由注册反查 method/path/param 自动注入。
- `swag init -g cmd/aegis/main.go -o internal/apidoc` 生成 `internal/apidoc`（**64 个端点**）。
- `go.mod`：`gin-gonic/gin`、`swaggo/gin-swagger`、`swaggo/files` 升直接依赖；移除 `http-swagger`。

## 验证（:8099 烟测，全部通过）
| 项 | 结果 |
|---|---|
| `go build ./...` / `go vet` / `go test`(store/api/enterprise) | 绿 |
| `/api/v1/health` | 200 |
| Swagger UI `/admin/api/docs/index.html` + `doc.json` | 200 |
| `/admin/`（嵌入 UI） | 返回真实 HTML，且不被静态中间件遮蔽 `/admin/api/*` |
| 路径参数链路 | 带真 admin token 访问 `/api/v1/datasources/abc/tables` → `datasource not found: abc`（证 GIN→context→pathParam 通） |
| `/mcp` (POST) | 返回 JSON-RPC initialize 结果 |

## 已知点
- `queryRequest` 含 `json.RawMessage`，swag 跳过该 body schema（路由仍登记）；不影响文档可用性。
- 后续新增接口时，在 handler 上方补 swag 注解后重跑 `swag init` 即可，文档与代码同源、永不失同步。
