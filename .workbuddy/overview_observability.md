# 可观测性（Observability）— Aegis 迭代交付

## 做了什么

为 Aegis 网关新增 `GET /metrics` 端点，暴露 Prometheus 格式指标，可直接被 Prometheus / Grafana 抓取，覆盖治理流量、延迟、数据供给量与构建信息。

## 核心指标

| 指标 | 类型 | 标签 | 说明 |
| --- | --- | --- | --- |
| `aegis_queries_total` | Counter | `channel`(dataapi/mcp), `status`(ok/denied/error) | 受治理查询总数（DataAPI / MCP / 数据集查询统一计入） |
| `aegis_query_duration_seconds` | Histogram | `channel`, `status` | 查询延迟分布 |
| `aegis_rows_returned_total` | Counter | `channel`, `status` | 返回给调用方的数据行累计数 |
| `aegis_datasources_total` | Gauge | — | 已配置数据源数 |
| `aegis_datasets_published_total` | Gauge | — | 已发布数据集数 |
| `aegis_build_info` | Gauge | `version`, `commit` | 构建版本/提交号（值恒 1） |

## 设计要点

- **零新权限/治理模型**：埋点落在 `proxy.audit` 与 `proxy.auditDataset` 两个审计汇聚点——所有 SQL / NoSQL / 数据集查询的 ok/denied/error 结果都从这里经过，一行 `metrics.RecordQuery(...) + RecordRows(...)` 即统一覆盖，包括 `channel`（dataapi/mcp）。
- **独立 registry**：用专属 `prometheus.Registry`（非全局默认），仅暴露 Aegis 指标 + Go/进程级 runtime 指标，干净无噪声。
- **构建身份**：新增 `internal/version` 包，`Version`/`Commit` 由 `go build -ldflags` 注入（默认 `dev`/`unknown`），启动时写入 `aegis_build_info`。
- 启动后统计数据源与已发布数据集数量写入 Gauge。

## 验证

- `go build ./...` / `go vet` / `go test ./...` 全绿
- 临时实例（带版本 ldflags `0.9.0`/`abc1234`）`/metrics` 正确暴露：`aegis_build_info{version="0.9.0",commit="abc1234"}`、datasources=1、datasets_published=1
- 跑 analyst 查询：customers(ok,3行) + 不存在表(403 denied) + paid_orders 数据集(ok,1行)
  → 计数器 `ok=2 / denied=1 / rows_returned=4`，channel 均为 dataapi（MCP 路径代码一致，共用同一 counter）

## 文件

- 新增 `internal/metrics/metrics.go`、`internal/version/version.go`
- 修改 `internal/proxy/proxy.go`、`internal/proxy/dataset.go`（审计汇聚点埋点）
- 修改 `internal/server/server.go`（启动写入 gauge + 挂载 `/metrics`）
- 新增依赖 `github.com/prometheus/client_golang v1.24.0`
- 文档：`README.md`（可观测性整节）、`BLUEPRINT.html`（AI 功能扩充表加行，已落地）
