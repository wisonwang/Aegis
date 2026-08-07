# Aegis 安全配置加固指南

> 配套文档：[ADR-0003 安全加固基线](../adr/0003-security-hardening-baseline.md)

## 快速加固（5 分钟）

将以下配置应用到 `conf/config.local.json`，或通过环境变量覆盖：

### 配置文件方式

```json
{
  "jwt_secret": "<用 `openssl rand -hex 32` 生成>",
  "mask_secret": "<用 `openssl rand -hex 32` 生成>",
  "limits": {
    "max_rows": 1000,
    "query_timeout": "30s",
    "rate_per_min": 60,
    "admin_exempt": false,
    "max_affected_rows": 10000,
    "allow_no_where_writes": false
  },
  "alerting": {
    "denied_count": 5,
    "denied_window_sec": 60,
    "bulk_rows": 3000,
    "off_hours_on": true,
    "off_hours_start": 22,
    "off_hours_end": 7,
    "cooldown_sec": 300,
    "webhook": "https://your-siem.example.com/aegis-alerts"
  }
}
```

### 环境变量方式（推荐生产环境）

```bash
# 密钥（必须更改）
export AEGIS_JWT_SECRET="$(openssl rand -hex 32)"
export AEGIS_MASK_SECRET="$(openssl rand -hex 32)"

# 查询限制
export AEGIS_MAX_ROWS=1000
export AEGIS_QUERY_TIMEOUT=30s
export AEGIS_RATE_PER_MIN=60
export AEGIS_MAX_AFFECTED_ROWS=10000

# 写操作安全
# AEGIS_ALLOW_NO_WHERE_WRITES 不设置 = 默认 false = 拦截无 WHERE 写操作

# 告警
export AEGIS_ALERT_DENIED_COUNT=5
export AEGIS_ALERT_BULK_ROWS=3000
export AEGIS_ALERT_OFFHOURS=true
export AEGIS_ALERT_OFFHOURS_START=22
export AEGIS_ALERT_OFFHOURS_END=7
export AEGIS_ALERT_WEBHOOK="https://your-siem.example.com/aegis-alerts"
```

## 配置项详解

### 查询限制 (`limits`)

| 配置项 | 推荐值 | 默认值 | 说明 |
|--------|--------|--------|------|
| `max_rows` | 1000 | 1000 | 单次查询最大返回行数。防止 AI agent 拖库。设为 0 则不限制（不推荐）。 |
| `query_timeout` | 30s | 30s | 单次查询超时。防止 runaway SQL 拖垮数据源。 |
| `rate_per_min` | 60 | 120 | 每 principal 每分钟最大查询数。滑动窗口算法。 |
| `admin_exempt` | false | true | admin 是否绕过行为限制。**建议 false**——admin 的权限绕过（表/行/列）不受影响，但行数/超时/频率同样适用。 |
| `max_affected_rows` | 10000 | 0 | 单次 UPDATE/DELETE 最大影响行数。0=不限制。**必须设置**，否则误操作可删全表。 |
| `allow_no_where_writes` | false | false | 是否允许无 WHERE 的 UPDATE/DELETE。**必须 false**。 |

### 告警 (`alerting`)

| 配置项 | 推荐值 | 默认值 | 说明 |
|--------|--------|--------|------|
| `denied_count` | 5 | 10 | 滑动窗口内被拒绝查询次数，超过则告警。检测探测行为。 |
| `denied_window_sec` | 60 | 60 | 滑动窗口大小（秒）。 |
| `bulk_rows` | 3000 | 5000 | 单次查询返回行数超过此值触发批量导出告警。 |
| `off_hours_on` | true | false | 开启非工作时间访问告警。 |
| `off_hours_start` | 22 | 0 | 非工作时间起始小时（含）。 |
| `off_hours_end` | 7 | 6 | 非工作时间结束小时（不含）。 |
| `cooldown_sec` | 300 | 300 | 同一规则告警冷却时间（秒），防止告警风暴。 |
| `webhook` | SIEM URL | "" | 告警 webhook，POST JSON 到指定 URL。 |

### 密钥

| 配置项 | 环境变量 | 说明 |
|--------|---------|------|
| `jwt_secret` | `AEGIS_JWT_SECRET` | JWT 签名密钥。**必须更改**，否则可伪造任意身份 token。 |
| `mask_secret` | `AEGIS_MASK_SECRET` | 列脱敏 tokenize/fpe 密钥。未设置则回退不安全默认值并告警。 |

生成密钥：
```bash
openssl rand -hex 32
```

## 防御层次总览

```
请求 → [1] 认证 (JWT/OIDC/LDAP/APIKey)
     → [2] 频率限制 (RatePerMin)
     → [3] 权限治理 (表/行/列)
     → [4] 写操作保护 (NoWHERE + MaxAffectedRows)
     → [5] 查询超时 (QueryTimeout)
     → [6] 结果截断 (MaxRows)
     → [7] 列脱敏 (mask/tokenize/fpe)
     → [8] 审计 + 异常检测
     → 响应
```

每一层都是 fail-closed（默认拒绝）。

## 临时提权

当 admin 需要执行大查询或批量写入时，可通过环境变量临时放宽：

```bash
# 临时放宽行数限制（仅本次启动）
AEGIS_MAX_ROWS=100000 ./aegis -config conf/config.local.json

# 临时放宽写操作影响行数
AEGIS_MAX_AFFECTED_ROWS=1000000 ./aegis -config conf/config.local.json

# 临时允许无 WHERE 写操作（危险！仅维护窗口使用）
AEGIS_ALLOW_NO_WHERE_WRITES=true ./aegis -config conf/config.local.json
```

> **注意**：所有临时提权仍会记录在审计日志中。建议在维护窗口结束后立即恢复。

## 已知限制

1. **无累计行数上限**：当前只有 per-query 和 per-minute 限制，没有 per-day 累计。一个 agent 可以在频率限制内缓慢拖库。P1 计划修复。
2. **无响应体大小限制**：宽行（大文本/BLOB 列）可绕过行数限制。P1 计划修复。
3. **无并发查询限制**：单 principal 可发起多个并发查询耗尽连接池。P1 计划修复。
4. **无 per-datasource 限制覆盖**：所有数据源共享同一套限制。P1 计划修复。

## 验证清单

- [ ] `jwt_secret` 不是默认值
- [ ] `mask_secret` 已设置
- [ ] `max_affected_rows` > 0
- [ ] `allow_no_where_writes` = false
- [ ] `admin_exempt` = false
- [ ] `off_hours_on` = true
- [ ] `webhook` 指向 SIEM
- [ ] 审计日志可查询
- [ ] 异常告警可收到
