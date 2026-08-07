# Aegis · 一页式产品介绍（Sales One-Pager）

> **Aegis = 面向 AI Agent 的受治理数据网关（AI Data Supply Gateway）。**
> 把内部 MySQL / PostgreSQL / NoSQL 变成受**表/行/列治理**的 DataAPI + MCP 工具——让 LLM / Agent 像调工具一样安全取数，**LLM 现场生成的 SQL 也绕不过治理**。
> 单 Go 二进制、分钟级落地、治理默认开启、护城河 = 部署简单 + 治理不可绕过 + Agent 交付简单。

---

## 客户痛点：Agent 直连数据库 = 把全部数据权限一次性交出去

| 痛点 | 后果 |
|------|------|
| AI 应用的 SQL 由 LLM 现场生成，不可评审、可被提示词注入操纵 | 传统"应用层代码内控权限"在 AI 场景完全失效 |
| 每个 AI 项目各自接数据、各写权限 | 重复建设、权限散落、无法审计与回收 |
| 裸连生产库 + 无脱敏 + 无审计 | 数安法/个保法/等保合规风险、PII 泄露 |
| 直连库即交出整库 | Agent 失控循环可能拖垮生产数据库 |

**一句话必要性**：没有治理代理层的 AI 应用，等价于给一个"不可控的实习生"发了生产库 root 密码。Aegis 把它换成"带工牌、有权限边界、全程录像"的受控访问。

---

## 典型架构

```text
┌────────────┐    JWT / MCP     ┌──────────────────┐   服务账号(单一)  ┌────────────┐
│ 应用 / AI  │ ───────────────▶ │    Aegis 代理    │ ───────────────▶ │  MySQL /   │
│   Agent    │ ◀─────────────── │ 权限引擎+连接池  │ ◀─────────────── │ PostgreSQL │
└────────────┘  治理后结果(脱敏) └──────────────────┘                  │  SQLite/   │
                                                                       │  NoSQL     │
```

后端数据库真实账号被隔离在平台内部：平台用单一服务账号连接，所有访问控制在代理层完成。

---

## 3 个典型场景

1. **企业内部 Copilot 问数** — NL2SQL + 行列治理 + 动态脱敏 + 审计；分析师与管理员看到各自权限内的数据。
2. **运营分析晨会备数** — Metrics（口径一致指标）+ Datasets（数据产品）+ 基于角色的治理，复用语义、避免口径漂移。
3. **面向客户的 AI 数据助手** — Enterprise 多租户工作区 + 限流 + 估算 + 审计，隔离租户数据、防查询失控。

---

## 核心能力矩阵

| 能力 | 说明 |
|------|------|
| **三级治理** | 表 / 行 / 列权限，**默认拒绝**；行谓词 `:attr` 注入派生表，多角色策略合并 |
| **动态脱敏** | `tokenize`(确定性假名) / `fpe`(格式保留) / `phone` / `email` / `card` / `partial` / `hash` / `redact`，密钥化、可还原 |
| **NL2SQL 安全网关** | 接 LLM 生成 SQL，仍走同一治理内核；只放宽"谁能问"，绝不放宽"能看到什么" |
| **语义指标层** | 管理员定义口径，Agent 用 `run_metric` 复用，注入-proof 参数渲染 |
| **查询血缘成本** | 执行前 EXPLAIN 预估扫描行数 + 敏感度风险，防 Agent 拖库 |
| **全量审计** | ok / denied / error 三态留痕 + 会话串联，可上 SIEM |
| **企业身份** | OIDC（Auth Code + PKCE + auto-provisioning）+ LDAP，组→角色映射 |
| **AI 行为护栏** | 行数上限 / 超时熔断 / 限流 / 无 WHERE 写拦截 / 影响行数上限 |

---

## 安全闭环（5 条不可妥协原则）

1. **默认拒绝** — 未显式授权的表即拒绝。
2. **治理不可绕过** — DataAPI / MCP / NL2SQL / 指标 / 估算全部复用同一 `Rewrite → Execute`，无 admin 旁路跳过脱敏。
3. **凭据隔离** — 后端真实账号永不出平台；调用方只持 JWT / API Key。
4. **全量留痕** — 成功/拒绝/失败三态均入审计；`admin` 受审计。
5. **AI 行为护栏** — 行数上限 / 超时 / 限流 / 写防护在代理层集中执行。

---

## 版本路径（Community / Enterprise）

| 能力 | Community | Enterprise |
|------|-----------|------------|
| 表/行/列治理 · 动态脱敏 | ✅ | ✅ |
| DataAPI · MCP · NL2SQL · 基础审计/告警 | ✅ | ✅ |
| OIDC / LDAP 身份对接 | ✅ | ✅ |
| Datasets · Metrics · 审批流 | — | ✅ |
| 多租户工作区 · SIEM 转发 · HA 控制面 | — | ✅ |

> 开源内核必须"单独有用且是楔子的桌腿"；任何把核心能力锁进企业版而伤及采用率的做法都违背楔子策略。

---

## PoC 成功标准（建议）

- [ ] Agent 可通过 MCP 调用受治理数据（无需任何数据库凭据）
- [ ] 至少 1 个真实数据源接通
- [ ] 至少 1 条行级策略 + 1 组脱敏规则生效（可对比 admin / analyst 视图差异）
- [ ] 审计日志完整留痕（含 denied 记录）
- [ ] 跑通 NL2SQL 自然语言问数 + estimate_query 风险预估

---

## 30 秒上手

```bash
docker compose up -d
```

把下面这段放进任意 MCP 客户端配置（**无需任何数据库凭据**）：

```json
{
  "mcpServers": {
    "aegis": {
      "url": "http://localhost:8080/mcp",
      "headers": { "X-MCP-API-Key": "mcp-demo-key" }
    }
  }
}
```

---

## 资源

- 使用手册：[README.md](file:///Users/vincent/workspace/fosun/datahub/README.md)
- 项目蓝图：[BLUEPRINT.md](file:///Users/vincent/workspace/fosun/datahub/BLUEPRINT.md)
- 竞品对比：[competitive_analysis.md](file:///Users/vincent/workspace/fosun/datahub/docs/competitive_analysis.md)
- 商用包装方案：[commercial-packaging-plan.md](file:///Users/vincent/workspace/fosun/datahub/docs/commercial-packaging-plan.md)
- 发布与采用加速：[launch.md](file:///Users/vincent/workspace/fosun/datahub/docs/launch.md)
- 演示 walkthrough：[demo-walkthrough.md](file:///Users/vincent/workspace/fosun/datahub/docs/demo-walkthrough.md)

*MIT License · 单二进制自托管 · 生产环境务必改种子凭据并设置 `AEGIS_JWT_SECRET` / `AEGIS_MASK_SECRET` / `AEGIS_MCP_API_KEY`，前置 TLS。*
