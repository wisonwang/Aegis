# ADR-002: 开源核心与企业功能分层及隔离机制

## Status
Proposed

## Context

Aegis 的楔子策略是「自托管、治理默认开启的 AI 数据网关」，护城河 = **部署简单（单二进制、分钟级落地）+ 治理不可绕过**。

当前所有功能（含 OIDC / LDAP / 审计 / 异常告警 / 审批流 / `datasets` 数据产品子域）都**无条件编译进单一二进制**，代码库里**没有任何 edition / license / plan 概念**。config 中虽有零散的 `OIDC.Enabled` / `LDAP.Enabled` / `NL2SQL.Enabled` / `MCP.Enabled` 布尔开关，但彼此独立、不构成统一的「版本」概念。

要走向商业化、并让开源核心可被信任地独立分发，必须回答两件事：
1. **分层（Tiering）**：哪些功能属于开源免费（Community），哪些属于企业付费（Enterprise）？
2. **隔离（Isolation）**：技术上如何保证企业功能可清晰计费、可随许可证启停，同时**不牺牲「单二进制、分钟级部署」这一核心护城河**？

约束：
- 单人项目，仍处于验证 OSS 采用率阶段；
- 多租户工作区（ADR-001）仍 Proposed、尚未编码；
- 已落地的 SSO（OIDC/LDAP）、审批流、`datasets` 子域属于「已建功能」，其归类会直接决定本次是「增量式脚手架」还是「一次性大重构」。

## Decision

采用 **运行时能力门禁（runtime capability gate）** 作为主隔离机制，并遵循以下不变量：

### 不变量（Invariants）
1. **单一决策点**：所有企业能力判定集中到一个 `Capabilities` 服务（`Has(cap) bool`），禁止在 handler 里散落 `if cfg.OIDC.Enabled` 之类判断。
2. **依赖方向**：企业代码统一收纳于 `internal/enterprise/`，**只能 企业 → 核心**，核心（`internal/api`、`internal/proxy`、`internal/store`）**绝不反向 import 企业包**。核心通过接口调用企业能力。
3. **许可证可翻转**：Enterprise 能力由 `Edition`(community|enterprise) + 许可证（签名 JWT / license 文件）解锁；无许可证时返回 `402 Payment Required` + 结构化错误，且 **UI 隐藏对应 tab**（UX 层拦截，不止后端 403）。
4. **可逆性优先**：先用运行时门禁，企业代码放独立包——未来可**无痛苦升级**为编译期 build-tag（Option 2）或独立模块（Option 3）；反向迁移极痛。故从现在起就把企业代码隔离在 `internal/enterprise/`。

### 能力目录（Capability Catalog）
枚举式企业能力，作为 `Capabilities.Has` 的入参：

| 能力常量 | 含义 |
|---------|------|
| `CapMultiTenant` | 多租户工作区（ADR-001） |
| `CapSSOFederation` | OIDC / LDAP / AD 多 IdP 联邦 + SCIM 自动编排 |
| `CapApprovalWorkflow` | 数据访问审批流（申请/审批/驳回/撤销） |
| `CapSIEMExport` | 审计流转发至 Splunk / Datadog / 自定义 HTTP |
| `CapDataProducts` | `datasets` 数据产品 / 语义指标层 |
| `CapHAControlPlane` | 外部控制面（PostgreSQL）/ 集群部署 |

### 提案分层（Tiering，待用户确认）
**Community / 开源免费（护城河，必须单独有用）：**
- 三级治理代理（表 / 行 / 列）+ 动态脱敏（tokenize / fpe 等密钥化策略）
- DataAPI REST + MCP 网关（含 catalog / nl2sql 工具）
- 本地数据源：MySQL / PostgreSQL / SQLite
- 基础审计（本地视图）+ 基础异常告警
- 单组织 Users / Roles RBAC
- Schema 语义注入

**Enterprise / 企业付费：** 上表 6 项能力。

> **关键开放问题**：已落地的 OIDC / LDAP / 审批流 / `datasets` 子域当前在免费二进制中。是否把它们**重分类为企业**（搬入 `internal/enterprise/` 并加门禁），还是**维持免费**（作为采用率钩子），决定了本次实现是增量脚手架还是一次性大重构。见文末决策点。

## Consequences

**正向：**
- 单二进制保留，部署护城河不破。
- 许可证翻转即可 upsell，易测试、可逆。
- 企业代码物理隔离，未来可平滑升级为 build-tag / 独立模块。
- 统一 `Capabilities` 决策点，消除当前散落的 `*.Enabled` 判断。

**负向 / 代价：**
- 企业代码仍随二进制分发（许可不纯净）；对单人项目验证期足够——门禁是「减速带」而非「保险库」，执着于防破解为时过早。
- 若选择重分类已建 SSO / 审批 / datasets 为企业，需从 `internal/api` 搬移至 `internal/enterprise/` 并加门禁，一次性重构成本。

## Alternatives Considered

- **Option 2 — 编译期 build-tag（`//go:build enterprise`）**：企业文件仅在 `-tags enterprise` 时编译，免费二进制**物理上不含**企业代码，许可最纯净、可干净开源核心仓库。代价：双构建管线；无法按许可证**运行时**启停（需发两个二进制）；对采用期过早、且违背「单二进制」故事。
- **Option 3 — 独立模块 / 插件（open-core）**：企业作为 `github.com/wisonwang/aegis-enterprise` 独立模块，核心经接口/插件加载。许可最干净、可完全闭源。代价：插件 API 面需长期维护、版本耦合、构建编排最复杂——当前阶段过度设计。

**选择 Option 1 的核心理由**：可逆性。先用运行时门禁 + 独立包，把「未来升级为 Option 2/3」的迁移成本降到最低；反向（从 build-tag 退回门禁）几乎不可行。这符合「优先选易变的决策」原则。

## Decision Point（需用户拍板）
已建功能 OIDC / LDAP / 审批流 / `datasets` 的归类：
- **(A) 维持免费**（推荐）：它们是楔子的采用率钩子 / 桌腿功能，只对*未来*企业功能（多租户 / SIEM / 数据产品 / HA）做隔离。改动小、护城河稳。
- **(B) 重分类为企业**：把 OIDC / LDAP / 审批 / datasets 搬入 `internal/enterprise/` 并加门禁。改动大、可能影响采用率与既有部署。
