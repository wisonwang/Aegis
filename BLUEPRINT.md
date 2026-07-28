# Aegis 项目蓝图（重新规划 · v1.0）

> **本文是项目蓝图的单一事实来源（single source of truth）。**
> 生成于 2026-07-28，是对 v0.8 的复盘重排：以 **代码实际状态** 为据纠正文档漂移，把最新落地的 **开源内核能力门禁（ADR-002）**、**多租户工作区设计（ADR-001）**、**OSS 采用加速策略（launch.md）** 纳入主线，并重排"采用率优先"的演进路线。
> 配套文档：`README.md`（使用手册）、`AI-SCENARIO.md`（AI 场景专项，状态已同步）、`docs/adr/`（ADR-001/002）、`docs/launch.md`（发布就绪）、`CHANGELOG.md`（能力时间线）、`SECURITY.md`（部署加固）。

---

## 0. 一句话定位

**Aegis = 自托管、治理默认开启的 AI 数据供给网关（AI Data Supply Gateway）。**
把内部 MySQL / PostgreSQL / NoSQL 变成受表/行/列治理的 **DataAPI + MCP 服务**，让 LLM/Agent 像调工具一样安全取数——**LLM 现场生成的 SQL 也绕不过治理**。单 Go 二进制、分钟级落地、护城河 = 部署简单 + 治理不可绕过。

---

## 1. 战略：走「楔子」而非「平台」

### 1.1 竞争象限（二维定位）

- **横轴**：部署形态（轻量自托管 ↔ 重型平台）
- **纵轴**：治理范式（AI Agent 原生 ↔ 传统面向人/BI）

| 象限 | 代表 | Aegis 位置 |
|------|------|-----------|
| 轻量 + AI 原生 | **Aegis（蓝海，相对空旷）** | ✅ 当前所在 |
| 重型 + 传统 | Collibra / Alation / Atlan（数据目录） | — |
| 轻量 + 传统 | ShardingSphere / ProxySQL（SQL 代理，非 AI 原生） | — |
| 重型/初创 + AI 原生 | MCP 安全 / LLM 防火墙初创、dbt / Cube 语义层 | 邻接升温 |

### 1.2 护城河与风险（须持续正视）

- **顺风**：Agent 接数据的治理缺口、MCP 风口、单二进制易落地 Pilot。
- **护城河**：部署简单 + 治理不可绕过（同一 `permission.Rewrite → proxy.Execute` 内核覆盖 DataAPI / MCP / NL2SQL / 指标 / 估算）。
- **风险 ①（价值被吞）**：PII 识别 / NL2SQL / 语义描述正被 LLM 能力本身吸收——价值在**治理执行闭环**，不在识别算法。
- **风险 ②（单人天花板）**：企业买家要 SSO / 审计对接 SIEM / 合规 / 多租户，缺这些难进大客户。
- **风险 ③（邻接升温）**：数据目录厂商下沉、云厂数据库上绑、MCP 安全初创抢同一批用户。

### 1.3 第一成功指标 = **采用率**，不是功能数

商业化路径：**OSS 积累采用率 → 企业版（SSO + 多租户 + SIEM + HA）收费**。
开源内核必须"单独有用且是楔子的桌腿"；任何把核心能力锁进企业版而伤及采用率的做法都违背楔子策略。度量方法见 `docs/launch.md` 第三节。

---

## 2. 当前状态盘点（代码为据，截至 2026-07-28）

> 下列状态以 `git log`、`internal/` 实现、`ADR` 与功能文档交叉核对为准。

### 2.1 能力矩阵

| 能力域 | 能力 | 状态 | 关键实现 / 提交 |
|--------|------|------|----------------|
| **治理内核** | 表/行/列三级治理 + 默认拒绝 | ✅ 已落地 | `permission.Rewrite` + `proxy.Execute` |
| | 动态脱敏（phone/email/card/partial/hash/redact/tokenize/fpe） | ✅ | 密钥化 tokenize/fpe 经 `AEGIS_MASK_SECRET`（`mask.go`） |
| | 行策略双层加固（嵌套子查询递归注入） | ✅ | `0e2f33e` |
| | 写操作防护（无 WHERE 拦截 + 影响行数上限） | ✅ | `overview_write_protection.md`，`max_affected_rows` |
| | 数据分级分类 + 自动推荐脱敏 | ✅ | `bdc0820`，`POST .../masks/recommend` |
| **审计与告警** | 全量审计（ok/denied/error + 会话串联 + 渠道） | ✅ | `audit_logs` |
| | 异常行为告警（高频 denied / 批量导出 / 非工作时段 + Webhook） | ✅ | `security_alerts` |
| **协议供给** | REST DataAPI | ✅ | login/query/list/describe/catalog/nl2sql/metrics/datasets |
| | MCP Streamable HTTP（含 resources/prompts + 11+ 工具） | ✅ | `list_datasources`/`query`/`get_catalog`/`estimate_query`/`nl2sql`/`run_metric`/`list_datasets`… |
| | 管理后台 Web UI | ✅ | 用户/角色/数据源/权限/策略/审计/告警/审批/数据集 |
| **身份** | SSO/OIDC（Auth Code + PKCE + nonce + auto-provisioning） | ✅ | `auth/oidc.go` |
| | LDAP / AD（三步绑定 + 组→角色映射） | ✅ | `auth/ldap.go` |
| | 权限审批流（申请-审批-生效-回收，角色级闭环） | ✅ | `de3e097` |
| **可观测性** | 健康探针（liveness/readiness） | ✅ | `/api/v1/health`、`/api/v1/ready` |
| | 结构化日志（slog，JSON/text，req_id + 治理事件） | ✅ | `logging` |
| | Prometheus 指标（`/metrics`） | ✅ | `aegis_queries_total` 等 |
| **数据源** | MySQL / PostgreSQL / SQLite 端到端 | ✅ | PG live 验证 `8116abc` |
| | NoSQL 适配器（Mongo / ES / Trino） | ✅ | 治理与 SQL 对齐 |
| **AI 增强** | NL2SQL 安全网关（只读强制 + 列约束） | ✅ | `3193f38` |
| | 语义指标层（模板 + 类型化参数 + 血缘） | ✅ | `1e0923c` |
| | 查询血缘成本（EXPLAIN 风险预估） | ✅ | `0571258` |
| | 数据集 / 数据产品（Data Products） | ✅ | 虚拟表复用治理 |
| **商业化基座** | 开源内核能力门禁（运行时判定 + 企业包隔离） | ✅ 已落地 | `internal/capabilities` + `internal/enterprise`（`32c9b8f`）；分层策略 ADR-002 **已 Accepted（折中）**；Phase 2 拟把 datasets/approvals handler 体迁入 `internal/enterprise` |

### 2.2 治理内核不变式（不可妥协）

1. **默认拒绝**：未显式授权的表即拒绝。
2. **治理不可绕过**：NL2SQL / 指标 / 估算 / 数据集全部复用同一 `Rewrite → Execute`，无 admin 旁路跳过脱敏。
3. **凭据隔离**：后端真实账号永不出平台；调用方只持 JWT / API Key。
4. **全量留痕**：成功/拒绝/失败三态均入审计；`admin` 为内置超级用户但受审计。
5. **AI 行为护栏**：行数上限 / 超时 / 限流 / 写防护在 proxy 层集中执行。

### 2.3 文档漂移（本次已纠正）

| 偏差位置 | 问题 | 处理 |
|----------|------|------|
| `README.md`「已知边界（MVP）」第 1 条 | 称"嵌套子查询行策略未递归注入"，与 RLS 双层加固（`0e2f33e`，已落地）矛盾 | ✅ 已改写为准确表述 |
| `AI-SCENARIO.md` 状态列 | 大量"规划"，实际多为"已落地" | ✅ 状态列已同步 |
| `BLUEPRINT.md` v0.8 | 未含 open-core 门禁 / launch 策略 / ADR-001·002 | ✅ 本次重排补齐 |
| `README.md` 写防护章节 | 与 `overview_write_protection.md` 一致，已对齐 | ✅ |

---

## 3. 开放决策与待办

### 3.1 开源内核 vs 企业版分级（ADR-002 · 已 Accepted「折中」）

能力门禁**机制已落地**（`Capabilities.Has` 单一决策点 + `internal/enterprise` 物理隔离 + 缺失能力返回 402 + UI 按 `data-cap` 隐藏 tab）。**分层策略已拍板（折中）**，不再阻塞：

- **Community / 免费（护城河桌腿，必须单独有用）**：三级治理 + 动态脱敏（tokenize/fpe 等）、DataAPI + MCP 网关、本地数据源 MySQL/PostgreSQL/SQLite、基础审计 + 基础异常告警、单组织 RBAC、Schema 语义注入、**SSO（OIDC/LDAP）维持免费**。
- **Enterprise / 企业付费**：`datasets` 数据产品 + 语义指标层、审批流、未来多租户 / SIEM 转发 / HA 控制面。

> **剩余工程（非决策）**：Phase 2 把 `datasets`/审批流的 handler 体从 `internal/api` 物理迁入 `internal/enterprise`（当前 Phase 1 仅迁路由所有权 + 门禁，handler 暂留原处）。可逆性不变：未来可平滑升级为编译期 build-tag 或独立模块。

### 3.2 多租户工作区时机（ADR-001 · Proposed）

设计已就绪（方案 A：共享表 + `workspace_id` 判别列 + 仓储层强制作用域），**尚未编码**。属"最后一个企业门槛"，优先级**由采用反馈驱动**，非技术阻塞。先落 Phase 0（默认单工作区 + 判别列，现网零破坏迁移），再按需 Phase 1/2。

---

## 4. 演进路线图（按"采用率优先"重排）

```text
阶段 A · 采用率验证（现在 → 数周）   阶段 B · 企业门槛（采用反馈驱动）   阶段 C · 平台化（远期）
─────────────────────────          ────────────────────────────     ───────────────────
发布就绪 + 叙事打磨                   多租户 Phase 0/1                   Helm / 云原生
度量埋点 + 社区反馈闭环               SIEM 审计转发                     读写分离路由
门禁分级策略落地 (ADR-002 A)          HA 控制面（PostgreSQL）           低代码 DataAPI
文档漂移清零                          审批流增强（分组/时限/自动撤回）    治理即代码
SIEM webhook 完善                     数据产品企业增强                  插件市场 / 向量检索
```

### 阶段 A · 采用率验证（最高优先级）

目标：让陌生人 5 分钟跑起来、复制 MCP 配置、在 GitHub 上给 Star。

- 落地 `docs/launch.md` 的 GitHub 发布动作（仓库描述 / Topics / Release v0.6 / Show&tell issue）。
- 打磨 README 英雄区与落地页叙事（见 launch.md 第二节）。
- 门禁分级策略落地为 (A)：把 `Capabilities.Has` 接到未来企业能力，免费能力不受影响。
- 度量埋点：README 文档链接 `?utm=`、Star/Clone 跟踪方法。
- 社区反馈闭环：用 Show&tell 收集真实场景，反哺阶段 B 优先级。

### 阶段 B · 企业门槛（采用反馈驱动）

- **多租户工作区**（ADR-001）：Phase 0 默认工作区 + 判别列 + 仓储作用域；Phase 1 显式多工作区 CRUD + 成员邀请 + OIDC/LDAP 组→工作区映射。
- **SIEM 审计转发**：把现有告警 Webhook 扩为审计流转发（Splunk / Datadog / 自定义 HTTP），作为企业能力门禁纳入。
- **HA 控制面**：控制面从 SQLite 平滑迁 PostgreSQL，支撑多副本。
- **审批流增强**：审批人分组 / 时限 / 自动撤回。

### 阶段 C · 平台化（远期）

- 云原生部署（K8s Helm Chart、多副本）。
- 读写分离路由（AI 查询默认只读副本）。
- 低代码 DataAPI（SQL 模板 + 参数一键发布）。
- 治理即代码（声明式策略纳入版本管理）。
- 插件市场 / 向量检索集成。

---

## 5. 近期行动清单（Next 30 Days）

| # | 行动 | 归属 | 状态 / 阻塞 |
|---|------|------|------------|
| 1 | 落地 GitHub 发布动作（描述/Topics/Release/Show&tell） | 阶段 A | 需确认 `gh` 已登录 |
| 2 | 完成 ADR-002 Phase 2：把 datasets/审批流 handler 体迁入 `internal/enterprise`（门禁策略已 Accepted·折中） | 阶段 A | 待排期 |
| 3 | 修正文档漂移（README MVP、AI-SCENARIO 状态） | 阶段 A | ✅ 本次已完成 |
| 4 | README 英雄区 + 落地页叙事替换 | 阶段 A | 待审阅 |
| 5 | 度量埋点（utm + Star/Clone 跟踪） | 阶段 A | 设计待定 |
| 6 | SIEM 审计转发能力（webhook 由告警扩到审计流） | 阶段 B | 待排期 |
| 7 | 多租户 ADR-001 Phase 0 设计评审 + 实施 | 阶段 B | 采用反馈驱动 |

---

## 6. 技术与度量

### 6.1 技术栈

- **语言/运行时**：Golang 单二进制，零强制外部依赖。
- **控制面存储**：SQLite（modernc，纯 Go）；可平滑迁 PostgreSQL（HA 档）。
- **SQL 解析**：Vitess sqlparser 分支（解析 → 重写 → 行策略注入）。
- **数据源驱动**：`go-sql-driver/mysql`、`lib/pq`、`modernc/sqlite`，NoSQL 走原生客户端 / REST。
- **能力门禁**：`internal/capabilities`（运行时判定）+ `internal/enterprise`（企业代码物理隔离，单向依赖核心）。

### 6.2 关键度量

- **战略北极星**：GitHub Stars + Clones；MCP 配置片段被复制次数。
- **治理健康度**：默认拒绝覆盖率（100%）、治理粒度（3 级）、审计三态覆盖率、AI 渠道（mcp）查询占比与截断率。
- **运行时**：各数据源查询 P95 延迟、平均治理重写耗时、被拒绝查询占比、策略命中率。
- 上述指标经 `/metrics`（Prometheus）+ 结构化日志 + 审计表可观测。

---

## 7. 风险与边界（更新）

- **治理完整性**：行策略双层加固已递归覆盖嵌套子查询，平台层治理不可绕过；非 SELECT 写操作行约束由无 WHERE 拦截 + 影响行数上限 + 嵌套子查询递归加固兜底。
- **可用性与性能**：每次查询经解析与重写有少量开销；单实例 SQLite 控制面是单点（阶段 B 迁 PG 解决）。
- **凭证与密钥**：JWT 签名密钥、数据源密码、`AEGIS_MASK_SECRET` 需纳入密钥管理（KMS / Vault），避免明文落盘；未设 `MASK_SECRET` 启动告警。
- **AI 特有风险**：Agent 失控循环拖垮生产库、全表结果进入 LLM 上下文造成二次泄露——需 P0-② 行为治理（限流 / 行数上限 / 超时 / 写防护）落地后方可上生产（均已落地）。
- **商业化风险**：避免把核心能力过早锁进企业版而伤采用率（见 §3.1）。

---

## 附录 · 文档与 ADR 索引

- `README.md` — 使用手册（DataAPI / MCP / NL2SQL / 指标 / 估算 / 数据集 / 权限 / 审计 / 告警 / 限流 / SSO / LDAP）。
- `AI-SCENARIO.md` — 企业 AI 应用建设场景专项（五大风险 × 对策、场景映射、功能规划，状态已同步）。
- `docs/adr/0001-multi-tenant-workspace.md` — 多租户工作区隔离模型（Proposed）。
- `docs/adr/0002-open-core-tiering.md` — 开源内核与企业功能分层及隔离（Proposed，含 A/B 决策点）。
- `docs/launch.md` — OSS 发布与采用加速清单。
- `docs/competitive_analysis.md` — 竞争对比矩阵。
- `CHANGELOG.md` — 能力时间线（楔子策略里程碑）。
- `SECURITY.md` — 部署加固清单与信任模型。
- `overview_write_protection.md` — 写操作防护设计说明。

*本蓝图随代码演进维护；任何状态变更须同步 §2.1 矩阵与对应功能文档。*
