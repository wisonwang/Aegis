# ADR-0003: Security Hardening Baseline

## Status
Proposed

## Context

Aegis 是一个 AI 数据网关，其核心风险面是：AI agent 或人类用户通过网关对底层数据库进行大规模数据抽取（拖库）、越权写入、或通过高频探测绕过治理。

当前已有安全控制：

| 控制面 | 已实现 | 当前默认值 |
|--------|--------|-----------|
| 结果行数上限 (MaxRows) | ✅ | 1000 行/查询 |
| 查询超时 (QueryTimeout) | ✅ | 30s |
| 频率限制 (RatePerMin) | ✅ | 120 次/分钟/principal |
| 无 WHERE 写操作拦截 | ✅ | 拦截 (AllowNoWhere=false) |
| DDL 拦截 | ✅ | 默认拒绝 (非 SELECT/INSERT/UPDATE/DELETE) |
| 列级脱敏 | ✅ | 8 种策略 |
| 行级策略 | ✅ | JWT attrs 谓词注入 |
| 审计日志 | ✅ | 每次查询 |
| 异常检测 | ✅ | 探测/批量导出/非工作时间 |
| 认证 | ✅ | JWT/OIDC/LDAP/MCP APIKey |

**识别出的关键缺口：**

| # | 缺口 | 风险等级 | 说明 |
|---|------|---------|------|
| G1 | MaxAffectedRows=0 | 🔴 高 | 单次 UPDATE/DELETE 无上限，可误删全表 |
| G2 | 无累计行数上限 | 🟡 中 | 频率限制只看 per-minute，可跨小时缓慢拖库 |
| G3 | 无响应体大小限制 | 🟡 中 | 宽行/大文本列可绕过行数限制（1 行 10MB） |
| G4 | AdminExempt=true | 🟡 中 | admin 绕过所有限制，包括 MaxRows |
| G5 | 无并发查询限制 | 🟡 中 | 单 principal 可并发耗尽连接池 |
| G6 | JWT secret 弱默认值 | 🔴 高 | "please-change-me-in-production" 可被伪造 |
| G7 | MaskSecret 未配置 | 🟡 中 | tokenize/fpe 回退不安全默认值 |
| G8 | 无 TLS | 🟡 中 | 明文传输 JWT 和查询结果 |
| G9 | 无 per-datasource 限制覆盖 | 🟢 低 | 所有数据源共享同一套限制 |
| G10 | 无 IP 白名单 | 🟢 低 | 任意网络可达 |

## Decision

采用**分层加固（defense-in-depth）**策略，分三个优先级落地。

### P0 — 立即修复（配置层面，无需改代码）

```json
{
  "limits": {
    "max_rows": 1000,
    "query_timeout": "30s",
    "rate_per_min": 60,
    "admin_exempt": false,
    "max_affected_rows": 10000,
    "allow_no_where_writes": false
  },
  "jwt_secret": "<32-byte-random-secret>",
  "mask_secret": "<32-byte-random-secret>",
  "alerting": {
    "denied_count": 5,
    "denied_window_sec": 60,
    "bulk_rows": 3000,
    "off_hours_on": true,
    "off_hours_start": 22,
    "off_hours_end": 7,
    "cooldown_sec": 300,
    "webhook": ""
  }
}
```

**关键变更及理由：**

1. **MaxAffectedRows: 10000** — 单次写操作最多影响 1 万行。超过则预检拒绝。防止误删全表。
   - 取舍：大表批量更新需分批执行，增加操作复杂度，但防住了最危险的单次写入。

2. **AdminExempt: false** — admin 也受限。
   - 取舍：admin 调试时可能需要临时放开（可设 env `AEGIS_MAX_ROWS=0`），但日常默认安全。admin 的治理绕过（表/行/列权限）不受影响，只是行为限制（行数/超时/频率）同样适用。

3. **RatePerMin: 60** — 从 120 降到 60（1 次/秒）。
   - 取舍：AI agent 的典型查询频率远低于此；降低可减缓探测攻击节奏。

4. **BulkRows: 3000** — 从 5000 降到 3000，与 MaxRows 的 3 倍对齐。
   - 取舍：更早触发批量导出告警，但也可能对合法的大结果集查询产生误报。

5. **OffHoursOn: true** — 开启非工作时间告警（22:00-07:00）。
   - 取舍：对夜间 ETL 场景可能误报，需配合 webhook 调整。

6. **DeniedCount: 5** — 从 10 降到 5，更早检测探测行为。

### P1 — 代码增强（需开发）

| 增强项 | 描述 | 涉及模块 |
|--------|------|---------|
| 累计行数上限 | 按 principal 记录每日累计返回行数，超过阈值（如 10 万行/天）降级或告警 | `proxy/limits.go` + `store` |
| 响应体大小限制 | 在 `maskRaw` 中按字节累计，超过阈值截断 | `proxy/proxy.go` |
| 并发查询限制 | 每 principal 最多 N 个并发查询（信号量） | `proxy/limits.go` |
| per-datasource 限制覆盖 | `Limits` 支持按 datasource 覆盖全局默认 | `config` + `proxy` |

### P2 — 运维加固（部署层面）

| 加固项 | 描述 |
|--------|------|
| TLS | 前置 Nginx/Caddy 终止 TLS，或 Go 原生 `ListenAndServeTLS` |
| IP 白名单 | Nginx `allow/deny` 或云安全组 |
| 密钥管理 | JWT/Mask secret 从 KMS/Vault 读取，不落 config.json |
| 审计日志外发 | 结构化日志输出到 SIEM（ELK/Splunk） |
| 定期密钥轮换 | JWT secret 90 天轮换，Mask secret 180 天 |

## Consequences

### 变更容易
- P0 全部是配置变更，零代码改动，即时生效
- 所有 env 变量覆盖已存在（`AEGIS_MAX_ROWS` 等），运维可灵活调整

### 变更困难
- AdminExempt=false 后，admin 用户的大查询会被截断，需教育用户用 `LIMIT` 或调 env
- MaxAffectedRows 会阻断批量 ETL 写入，需提供 admin 临时放宽的文档
- per-datasource 限制覆盖需要改配置结构和 Guard 构建逻辑

### 风险
- 过严的限制可能促使用户绕过网关直连数据库（回到治理前状态）
- 需配套提供「临时提权」机制（env 覆盖 + 审计记录），否则用户会找 workaround
