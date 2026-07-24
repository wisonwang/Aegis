# Aegis 写操作防护（P0-② 补全）

> 日期：2026-07-24 · 在已有「权限治理 + 行数上限/超时/限流」基础上，补齐写操作（UPDATE/DELETE）的硬安全网。

## 解决的问题
风险章节点名的治理盲区：此前**行级治理不作用于写操作**、且写操作**无影响行数上限**——一个被注入或幻觉的 Agent 可能 `UPDATE/DELETE` 整张表。DDL 已被语句白名单拦截，但 DML 写缺少同等级防护。

## 改动
- **`internal/permission/engine.go`**：`Rewrite` 为 UPDATE/DELETE 产出两个事实——
  - `WriteHasWhere`：经行策略注入后若仍无 WHERE（用户没写 WHERE 且没有任何行策略兜底），记为 `false`；
  - `CountCheckSQL`：单表写生成可移植的 `SELECT COUNT(*) FROM <表> <where>`（含注入的行策略），供执行前预检影响行数。
  - INSERT/REPLACE 标记 `WriteHasWhere=true`（自带 VALUES，不触发无 WHERE 拦截）；superuser 快速路径同样为 `true`。
- **`internal/config/config.go` + `internal/proxy/limits.go`**：`Limits` 新增 `max_affected_rows`（默认 0=关闭）、`allow_no_where_writes`（默认 false）；`Guard` 持有二者，`NewGuard` 装入。环境变量 `AEGIS_MAX_AFFECTED_ROWS` / `AEGIS_ALLOW_NO_WHERE_WRITES` 可覆盖。
- **`internal/proxy/proxy.go`**：`Execute` 在 Guard 激活且非 admin 时，写语句先校验——无 WHERE 且无放行 → 403 denied（审计）；`max_affected_rows>0` 且为单表写时，先 `countAffected` 预检，超阈值 → 403 denied。新增 `countAffected` 辅助。

## 行为语义
- 无 WHERE 的 UPDATE/DELETE：**默认拒绝**（硬安全网）；若表配置了行策略，注入后等效有 WHERE → 放行；设 `allow_no_where_writes=true` 可放宽。
- 影响行数上限：执行前 `SELECT COUNT(*)` 预检，超 `max_affected_rows` 直接拒绝——**执行前拦截，避免不可逆损伤**；多表写因 COUNT 歧义不预检。
- admin 经 `AdminExempt` 整体豁免，与限流/超时/行数上限一致。
- 两处拒绝均写入审计（`denied` 状态）。

## 验证
- `internal/permission/engine_test.go`（新增）：no-WHERE 检测、行策略注入后视为有 WHERE、INSERT 安全、superuser 豁免 —— `go test` 通过。
- `go build ./...` 通过。
- 端到端双实例 live（:8080 默认限额 + :8090 `max_affected_rows=1`）：脚本 `verify_writes.py` **6/6 通过** —— 无 WHERE 的 UPDATE/DELETE 被拒；带 WHERE 允许；影响 2 行的更新被 cap 拒绝；审计含 denied 记录。

## 配置示例
```json
"limits": {
  "max_rows": 1000, "query_timeout": "30s", "rate_per_min": 120,
  "admin_exempt": true,
  "max_affected_rows": 1000,        // 单条写最多影响 1000 行
  "allow_no_where_writes": false   // 无 WHERE 写默认拒绝
}
```

## 文档同步
BLUEPRINT.html（P0-② 四项标「已落地」、P0-① MCP/Schema 标「已落地」、第五章边界更新、第十二章下一步更新）；README.md「AI 行为治理」表补两行 + env 覆盖说明。

## 下一步建议
PostgreSQL 端到端验证；可观测性（Prometheus 指标/结构化日志/探针）；动态脱敏与分级分类；NL2SQL 安全网关。
