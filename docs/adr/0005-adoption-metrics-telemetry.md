# ADR-0005: 采用度量与匿名遥测（Adoption Metrics & Anonymous Telemetry）

## Status
Accepted — Phase 1 implemented 2026-08-05; Phase 2 deferred (needs collector endpoint + `docs/privacy.md`).

## Context

项目战略（`Aegis` 长期记忆）明确：**第一成功指标是 OSS 采用率，而非功能数**。楔子路线靠"部署简单 + 治理不可绕过"取胜，靠 Star/Clone/活跃实例数证明牵引力，再向企业版转化。

但现状是：**代码里没有任何度量/遥测设施**（探查 `internal/**` 无 `telemetry`/`heartbeat`/`ping` 相关实现）。我们当前对"采用率"的判断完全依赖 GitHub 公开指标（Stars/Clones），对**自托管实例的真实使用情况一无所知**：

- 有多少个实例在跑？版本分布如何？
- 接了哪些类型的数据源（MySQL/PG/ES）？接了几个？
- 治理引擎到底拦了多少次越权查询？（这是产品核心价值主张，却无法量化）
- 数据集 / 语义层 / MCP 这些能力的实际采用率如何？

没有这些数据，我们无法用证据驱动路线图，也无法向企业客户证明"治理确实在生效"。

**约束（不可让步）**：
- Aegis 的卖点是"自托管 + 隐私 + 治理默认开启"。任何遥测都**绝不能**触碰 PII、SQL 文本、租户数据、主机名、IP——否则直接摧毁价值主张。
- 必须是 **opt-in、默认关闭**，且开关透明、可在文档与部署清单中审查。

## Decision

分两阶段落地，先吃低风险果实，再上远程遥测。两阶段都保持"零 PII"与"可一键关闭"。

### Phase 1 — 本地指标 + GitHub 侧追踪（无需自建采集服务，本 ADR 立即可做）

1. **本地实例统计端点**：新增 `GET /admin/api/stats`（仅 admin / `WorkspaceAll` 可读），返回本实例的聚合计数：
   ```json
   {
     "version": "0.6.0",
     "edition": "enterprise|community",
     "uptime_seconds": 123456,
     "counts": {
       "datasources": 3,
       "datasource_types": ["mysql","postgres"],
       "datasets": 5,
       "workspaces": 2,
       "users": 8,
       "queries_served": 1423,
       "queries_denied": 17,
       "mcp_sessions": 64
     }
   }
   ```
   计数全部来自仓储层聚合查询，**不含任何行级内容**。这同时让自托管用户"看到自己用得好不好"，形成产品价值闭环。

2. **GitHub 侧采用度追踪（纯 workflow，零后端）**：新增 `.github/workflows/adoption.yml`（cron 每日），用 `gh api` 拉取 Stars / Clones / Forks / Referrers，写入仓库 `docs/adoption/<date>.json` 并提交。这是 GitHub 原生指标，不依赖任何自托管实例，零隐私风险。

### Phase 2 — Opt-in 远程心跳（默认关闭，需自建/接入轻量采集器）

仅在 Phase 1 验证"本地统计可用"后实施。

3. **配置块**：
   ```json
   {
     "telemetry": {
       "enabled": false,
       "endpoint": "https://metrics.aegis.dev/v1/ping",
       "interval_hours": 24
     }
   }
   ```
   `enabled` 默认 `false`；任何非空 `endpoint` 在文档与 `aegis doctor` 自检中明确提示。

4. **心跳载荷**（严格白名单，缺省不报）：
   ```json
   {
     "instance_id": "<随机 UUID，首次启动生成并持久化>",
     "version": "0.6.0",
     "edition": "enterprise",
     "datasource_types": ["mysql","postgres"],
     "counts": { "datasources": 3, "datasets": 5,
                 "queries_served": 1423, "queries_denied": 17 },
     "ts": 1690000000
   }
   ```
   - `instance_id` 为本地生成、与任何用户/租户无关的随机 UUID，仅用于去重与活跃度。
   - **绝不**包含：SQL、表名、列名、tenant、username、hostname、IP、任何行数据。
   - 采集端只做计数聚合，原始 ping 日志保留期 ≤ 30 天。

## Consequences

**变得更容易**：
- 用真实证据驱动路线图（例如"PG 采用率最高 → 优先修 PG 方言 bug"）。
- 量化治理价值（`queries_denied` 直接证明"不可绕过的治理"在生效），可用于企业版销售叙事。
- 自托管用户通过 `/admin/api/stats` 获得使用反馈，提升留存。

**变得更难 / 需承担**：
- Phase 2 需要运维一个采集端点（或复用 Plausible/PostHog 自托管实例），并写一份公开隐私说明。
- 必须持续保证"零 PII"不退化——采集字段白名单需代码评审守护，新增字段走本 ADR 修订。
- opt-in 默认关闭意味着初期样本小；但这是产品定位的代价，值得。

**可逆性**：
- Phase 1 纯只读端点 + 外部 workflow，删除零副作用。
- Phase 2 默认 `enabled:false`，且 `instance_id` 随机无关联——用户随时关、随时删本地 UUID 即彻底断联。整体可逆、低锁定。

## 备选方案（已否决）

| 方案 | 否决理由 |
|------|----------|
| 全量查询遥测（每次查询上报） | 触碰 SQL/租户数据，摧毁隐私卖点；用户必关闭。 |
| 强制开启的匿名遥测 | 违背"opt-in 默认关"约束，且对 OSS 口碑是净负。 |
| 仅依赖 GitHub Stars | 对**自托管**实例完全失明，而自托管正是楔子路线的主战场。 |

## 后续
- Phase 1 实现后，把 `/admin/api/stats` 接入 `aegis doctor` 自检输出。
- Phase 2 实施前需先确定采集端点归属（自建 vs 第三方），并补 `docs/privacy.md`。

## 实现记录（Phase 1，2026-08-05）

- `internal/metrics/metrics.go`：在既有 Prometheus 指标基础上新增原子镜像（`queriesServed`/`queriesDenied`/`datasourcesCnt`/`datasetsCnt`/`mcpSessionsCnt`/`startTime`/`buildVersion`/`buildCommit`）与 `SetMCPSessions` + 一组 `Snapshot` getter（`UptimeSeconds`/`BuildVersion`/`QueriesServed`/`QueriesDenied`/`Datasources`/`DatasetsPublished`/`MCPSessions`），供 `/admin/api/stats` 直接读取，无需解析 exposition。
- `internal/api/stats.go`：新增 `AdminStats`（仅 admin），返回固定白名单 schema（version/commit/edition/uptime_seconds/counts），**绝不**含 PII/SQL/租户/表名列名/主机/IP；datasource_types 为去重后的类型列表。
- `internal/server/server.go`：注册 `GET /admin/api/stats`（经 `a()` 包裹器 = Authenticate+WorkspaceResolver+RequireAdmin）。
- `internal/mcp/server.go`：`initialize` 建立会话后 `metrics.SetMCPSessions(len(s.sessions))`（在 `mu` 锁内）。
- `.github/workflows/adoption.yml`：每日 cron 拉取 Stars/Forks/Clones/Views 并提交 `docs/adoption/<date>.json`，零后端、零隐私风险。
- `test/test_stats.py`：3 个用例（admin 门禁 + schema 白名单 + 查询后 `queries_served>=1`）。全量 pytest **32 passed**。
- **未做（本 ADR 范围外）**：`aegis doctor` 接入、`/metrics` 之外无新增；Phase 2 远程心跳未实施。
