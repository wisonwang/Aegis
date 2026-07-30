# Aegis 产品设计文档（Product Design）

> **定位**：本文是 Aegis（AI Data Supply Gateway）的**产品设计单一事实来源**，与 `BLUEPRINT.md`（战略/路线图）、`README.md`（使用手册）、`AI-SCENARIO.md`（AI 场景）、`docs/adr/`（决策记录）配套。
> **版本**：v1.0 · 2026-07-30，基于"大厂数据权限治理分层 × Aegis 采用价值"分析提炼。
> **核心结论**：Aegis 的护城河在"**治理执行闭环**"（网关强制 + 默认拒绝 + 动态脱敏 + 全量审计），且比大厂内部平台更 AI 原生；采用价值集中在"给 LLM/Agent 受治理取数"这一新兴市场。

---

## 0. 文档说明

| 项 | 内容 |
|----|------|
| 产品名 | Aegis — AI Data Supply Gateway（AI 数据供给网关） |
| 形态 | 单 Go 二进制，自托管，零强制外部依赖 |
| 一句话 | 自托管、治理默认开启的 AI 数据网关：把内部数据库变成受表/行/列治理的 DataAPI + MCP，让 LLM/Agent 像调工具一样安全取数 |
| 设计原则 | 默认拒绝 · 治理不可绕过 · 凭据隔离 · 全量留痕 · AI 行为护栏 |

---

## 1. 产品定位与愿景

### 1.1 为什么是现在

- **Agent 取数治理缺口爆发**：企业把 LLM/Agent 接到内部数据库时，没有"网关"挡在中间——要么直连裸数据（合规/泄露风险），要么自建平台（Facebook 级投入）。
- **MCP 协议风口**：Model Context Protocol 让"把数据源封装成工具"成为标准动作，但**安全治理层缺位**——Aegis 正是补这一层。
- **大厂方案不对外**：Google/Meta/阿里/腾讯的内部数据平台不出售、不通用；Collibra/Alation 重且贵且非 LLM-native。

### 1.2 愿景与楔子

> **不做重型数据目录平台，做"最轻量的受治理取数网关"。**

- 楔子路线：**轻量单二进制自托管 + 治理默认开启 + AI 原生**，不打重型目录的功能广度战。
- 第一成功指标 = **OSS 采用率**（GitHub Stars / Clones / MCP 配置被复制次数），不是功能数。
- 商业化路径：OSS 积累采用率 → 企业版（多租户 + SIEM + HA + 数据产品增强）收费。

---

## 2. 目标用户与采用场景

### 2.1 用户画像（Persona）

| Persona | 角色 | 痛点 | Aegis 给什么 |
|---------|------|------|-------------|
| **AI/平台工程师** | 给团队 Agent 接内部数据 | 直连裸库怕泄露、自建网关太重 | 5 分钟起一个网关 + 复制 MCP 配置即可让 Agent 安全取数 |
| **数据治理/安全负责人** | 强监管集团（金融/医疗/多子公司） | 跨主体隔离、审批、审计对接 SIEM 是硬合规门槛 | 行/列治理 + 审批流 + 多租户(进行中) + SIEM(企业) |
| **独立开发者/小团队** | 想给自己的应用加"问数"能力 | 没有治理团队 | 单二进制自带默认拒绝治理，开箱即合规感 |

### 2.2 场景地图

1. **Agent 问数**：用户用自然语言提问 → Agent 调 `nl2sql` 生成 SQL → 经治理重写（行策略注入/列脱敏/限行）→ 返回脱敏结果。LLM 现场生成的 SQL **也绕不过治理**。
2. **受治理即席查询**：应用/BI 经 DataAPI 查询，统一过 `permission.Rewrite → proxy.Execute`。
3. **数据产品复用**：把治理过的虚拟表发布为 `dataset`，作为团队间"数据产品"共享，复用同一治理内核。
4. **语义指标服务**：业务方调 `run_metric` 取"口径一致"的指标，治理在指标层再次生效。
5. **失控查询风控**：`estimate_query` 预估返回行数/成本，超过阈值拦截——防 Agent 拖垮生产库。

---

## 3. 治理架构设计（核心）

### 3.1 设计原则（不可妥协）

1. **默认拒绝**：未显式授权的表即拒绝。
2. **治理不可绕过**：NL2SQL / 指标 / 估算 / 数据集全部复用同一 `Rewrite → Execute`，无 admin 旁路跳过脱敏。
3. **凭据隔离**：后端真实账号永不出平台；调用方只持 JWT / API Key。
4. **全量留痕**：成功/拒绝/失败三态均入审计；`admin` 为内置超级用户但受审计。
5. **AI 行为护栏**：行数上限 / 超时 / 限流 / 写防护在 proxy 层集中执行。

### 3.2 七层治理模型与大厂对齐

大厂通行做法是把"权限"拆成**不可绕过的强制层 + 默认拒绝**，核心是中央网关强制执行。Aegis 卡在最值钱的两层（策略中心 + 网关强制执行），且更 AI 原生。

| # | 治理层（大厂通行） | 大厂典型做法 | Aegis 覆盖 | 状态 |
|---|------------------|------------|-----------|------|
| 1 | 资产目录与发现 | DataHub/Amundsen 类：统一元数据/血缘/术语，先登记再暴露 | 数据源连接器 schema 发现 + 语义富化（`get_catalog`） | 🔴 缺口（企业版规划全量目录/血缘） |
| 2 | 分级分类 & PII | 自动打标 L1–L4、扫出手机号/身份证/银行卡 | 数据分级分类 + **自动推荐脱敏**（`bdc0820`） | ✅ 分级脱敏 / 全量自动 PII 扫描🔴 |
| 3 | 策略中心 | RBAC/ABAC + 行/列 + 目的限制，默认拒绝 | 角色×数据源×表、行谓词 `:attr` 注入派生表、列掩码 | ✅ 完整（等价大厂第 3 层） |
| 4 | 网关强制执行 | 中央网关：SQL 重写/行注入/列脱敏/限流/限行 | `permission.Rewrite → proxy.Execute` + **SQL 层 LIMIT 注入** + 限流/限行/限字节 | ✅ 强项（等价大厂第 4 层） |
| 5 | 审批与临时授权 | break-glass、时限令牌、用途绑定 | 权限审批流（申请-审批-生效-回收，`de3e097`） | ✅ 基础 / 增强归企业版 |
| 6 | 审计与异常 | 全量审计外发 SIEM + 异常检测 | 审计表（三态+会话串联）+ 告警 Webhook + **SIEM 转发(企业)** | ✅ 审计 / SIEM🔴企业版 |
| 7 | 多租户隔离 | 共享 schema + 租户判别列，仓储层强制作用域 | ADR-001 核心隔离代码已落地（`workspace.go`），尚未默认启用 | 🟡 进行中 |

### 3.3 治理内核数据流

```text
请求 (DataAPI / MCP / NL2SQL / Metric / Estimate)
   │
   ▼
① 认证         JWT / X-MCP-API-Key → 解析身份/角色/attrs
   │
   ▼
② 解析         Vitess sqlparser：解析 SQL → AST
   │
   ▼
③ 治理重写 Rewrite（默认拒绝内核）
   ├─ 表级：角色×数据源×表授权校验（拒绝即 403）
   ├─ 行级：多角色谓词 `:attr` 合并为派生表注入（AND）
   ├─ 列级：整列隐藏(allowed/denied_cols) + 动态掩码(tokenize/fpe/phone…)
   ├─ SQL 层 LIMIT 注入（无→注入；低于 max→保留；高于 max→替换为 max）
   └─ 写防护：无 WHERE 拦截 + 影响行数上限 + 嵌套子查询递归加固
   │
   ▼
④ 执行 Execute（数据源驱动：MySQL/PG/SQLite/NoSQL）
   │
   ▼
⑤ 审计         成功/拒绝/失败三态入审计 + 异常告警 Webhook/SIEM
   │
   ▼
返回（rewritten_sql 透明可查，便于调试与合规核对）
```

> `rewritten_sql` 字段是实际执行的 SQL（含注入的派生表/投影与 LIMIT），让治理"可观察、可审计、可复核"，这是大厂网关的常见最佳实践，Aegis 已内置。

---

## 4. 产品功能与体验

### 4.1 三大产品面

| 产品面 | 受众 | 作用 |
|--------|------|------|
| **DataAPI**（REST） | 应用/BI/开发者 | `/api/v1/query`、`/nl2sql`、`/metrics`、`/datasets`、`/catalog` 等标准 HTTP 接口 |
| **MCP 服务** | LLM/Agent | `POST /mcp` 暴露 `query`/`nl2sql`/`estimate_query`/`list_*`/`run_metric`/`list_datasets` 等 11+ 工具 + `resources`(`aegis://<ds>/schema`) + `prompts`(nl2sql) |
| **管理后台**（`/admin`） | 治理管理员 | 用户/角色/数据源/权限/策略/审计/告警/审批/数据集 的可视化配置 |

### 4.2 MCP 工具目录（LLM 原生取数入口）

MCP 是 Aegis 的差异化核心——所有工具走同一套表/行/列三级治理，Agent 拿不到越权数据。

| 类型 | 名称 | 作用 |
|------|------|------|
| tools | `query` | 受治理执行 SQL |
| | `estimate_query` | 预估返回行数/成本，防失控查询 |
| | `nl2sql` | 自然语言 → 受治理 SQL |
| | `list_datasources` / `list_tables` / `describe_table` | 数据源/表发现 |
| | `run_metric` / `list_metrics` | 语义指标查询 |
| | `list_datasets` / `get_dataset_catalog` | 数据产品查询 |
| resources | `aegis://<数据源名>/schema` | 按调用者权限过滤的语义 Schema 卡片 |
| prompts | `nl2sql` | "如何安全查询"的提示词模板 |

> 实测（运行中的 `:8080/mcp`）：`list_datasources` 返回 demo/mysql-local/presto；`query SELECT 1` 被 `access denied to table "dual"` 拒绝（默认拒绝生效）；`query count(*) FROM orders` 成功且 `rewritten_sql` 含 `where tenant_id='acme'`（行级策略穿透 MCP 注入）。

### 4.3 治理配置体验

- **角色 × 数据源 × 表**：细粒度 SELECT/INSERT/UPDATE/DELETE 授权。
- **行策略**：谓词 `:attr` 由 JWT attrs 代入，多角色按 priority 合并（AND）注入为派生表。
- **列脱敏**：整列隐藏 与 动态值掩码两种；`tokenize`(确定性伪名) / `fpe`(格式保留加密) 为密钥相关，密钥 = `AEGIS_MASK_SECRET`。
- **审批流**：申请 → 审批 → 生效 → 回收（角色级闭环）。
- **数据产品**：`dataset` 虚拟表复用治理，作为团队间共享资产。

---

## 5. 差异化与护城河

### 5.1 对比矩阵

| 维度 | 大厂内部平台 | 数据目录厂商 (Collibra/Alation) | MCP 安全初创 | **Aegis** |
|------|------------|-------------------------------|------------|-----------|
| AI 原生（MCP/nl2sql） | 弱（pre-ChatGPT） | 弱 | 中 | **强** |
| 治理强制闭环 | 强 | 中（偏目录） | 中 | **强** |
| 部署轻量度 | 重 | 重 | 轻 | **最轻（单二进制）** |
| 开源可自托管 | 否 | 否 | 部分 | **是** |
| 多租户/SIEM/审批 | 强 | 强 | 弱 | 🟡 企业版/进行中 |

### 5.2 护城河

- **部署简单 + 治理不可绕过**：同一 `permission.Rewrite → proxy.Execute` 内核覆盖 DataAPI / MCP / NL2SQL / 指标 / 估算，无旁路。
- **AI 原生程度领先**：大厂内部平台是 pre-ChatGPT 时代建的，Aegis 从第一天就是给 Agent 用的。
- **价值在闭环不在算法**：PII 识别 / NL2SQL / 语义描述正被 LLM 能力本身吸收，而"网关强制 + 默认拒绝 + 动态脱敏 + 全量审计"这一强制闭环是 LLM 替代不了的。

---

## 6. 商业化与采用策略

### 6.1 Open-Core 分层（ADR-002 · Accepted「折中」）

- **Community（免费，护城河桌腿，必须单独有用）**：三级治理 + 动态脱敏、DataAPI + MCP 网关、本地数据源、基础审计 + 基础告警、单组织 RBAC、Schema 语义注入、**SSO（OIDC/LDAP）免费**。
- **Enterprise（付费）**：`datasets` 数据产品 + 语义指标层、审批流、未来多租户 / SIEM 转发 / HA 控制面。
- 机制：`Capabilities.Has` 单一决策点 + `internal/enterprise` 物理隔离（单向依赖核心）+ 缺失能力返回 402 + UI 按 `data-cap` 隐藏。

### 6.2 采用率优先

- **北极星**：GitHub Stars + Clones + MCP 配置片段被复制次数（而非功能数）。
- **楔子不打功能广度战**：先用"5 分钟起网关 + 复制 MCP 配置"占领采用，再用企业版变现。
- **价值靶点**：无巨型内部平台却需 LLM 受治理取数的团队；强监管多子公司集团（金融/医疗）。

### 6.3 采用前需补的硬门槛（诚实清单）

1. **目录 / 血缘 / 自动分级**是当前最大缺口——若客户已有 DataHub/Amundsen，Aegis 应定位为"网关 + AI 层"去对接而非替代。
2. **审批流 / 多租户 / SIEM** 仍属企业版或落地中，决定能否进大客户生产。

---

## 7. 路线图对齐（阶段 A/B/C）

```text
阶段 A · 采用率验证        阶段 B · 企业门槛           阶段 C · 平台化
─────────────────        ──────────────────        ──────────────
发布就绪 + 叙事打磨         多租户 Phase 0/1            Helm / 云原生
度量埋点 + 反馈闭环         SIEM 审计转发             读写分离路由
门禁分级策略 (ADR-002)      HA 控制面(PG)              低代码 DataAPI
文档漂移清零                审批流增强                治理即代码
SIEM webhook 完善           数据产品企业增强           插件市场/向量检索
```

详见 `BLUEPRINT.md` §4。

---

## 8. 已知缺口与边界（诚实清单）

| 缺口 | 影响 | 规划 |
|------|------|------|
| 全量资产目录 / 血缘 | 大客户"先登记再暴露"缺失 | 企业版 / 对接 DataHub |
| 自动 PII 扫描 | 依赖人工分级，可能漏标 | 企业版规划 |
| 多租户默认启用 | 尚未完全接线 | ADR-001 Phase 0/1（进行中） |
| SIEM 转发 | 仅告警 Webhook，未扩审计流 | 企业版（阶段 B） |
| HA 控制面 | SQLite 单点 | 阶段 B 迁 PG |

---

## 附录 · 关联文档

- `BLUEPRINT.md` — 战略与路线图（v1.1）
- `README.md` — 使用手册
- `AI-SCENARIO.md` — AI 场景专项
- `docs/adr/0001-multi-tenant-workspace.md` — 多租户隔离模型
- `docs/adr/0002-open-core-tiering.md` — open-core 分层
- `docs/launch.md` — OSS 发布与采用加速
- `docs/competitive_analysis.md` — 竞争对比矩阵
- `SECURITY.md` — 部署加固与信任模型
- `.workbuddy/skills/aegis-mcp/` — Aegis MCP 调用技能（含 `references/mcp-tools.md` 完整工具 schema）

*本文随代码与战略演进维护；任何定位/治理模型变更须同步本文件与 `BLUEPRINT.md`。*
