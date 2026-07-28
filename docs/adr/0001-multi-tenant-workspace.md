# ADR-001: 多租户工作区隔离模型

## Status

**Accepted** — 已采纳并实现（Phase 0 + Task #112 + Phase 1 / Task #113）。共享 schema + `workspace_id` 判别列 + 仓储层强制作用域的模型已落地并通过跨租户隔离测试；OIDC/LDAP 组→工作区映射（Phase 1）已实现：复用并扩展 `claim_mappings`，SSO 登录即按组归属落入对应租户。

## Context

Aegis 当前是**单租户**形态：一个部署实例服务一个组织，控制面（`internal/store`，SQLite）里的 `users` / `roles` / `datasources` / 权限 / 掩码 / 语义 / 指标 / 审计 全部无租户边界。身份侧已支持 OIDC / LDAP / 本地 JWT（auto-provisioning），但"用户属于哪个组织/工作区"这一概念不存在。

**为什么现在要做**：多租户是楔子策略里**最后一个企业门槛**（见 `BLUEPRINT.md` 路线图）。两类真实需求倒逼它：

1. **大客户内部分单元隔离**：一个企业想把 Aegis 同时给多个 BU / 客户用，彼此的数据源、权限、审计互不可见，却不愿运维 N 个独立实例。
2. **Aegis-as-a-Service（未来）**：单一部署服务多个组织。

**约束（来自产品定位）**：
- 仍是**自托管、单 Go 二进制、分钟级落地**——不能引入额外的编排/分库中间件作为前置条件。
- 治理内核不变式必须保持：租户隔离是**叠加在**表/行/列治理之上的维度，二者正交，任一都不能被绕过。
- 现有单实例部署**不能因为加了多租户而需要重写或破坏性迁移**（演进策略原则）。

## Decision Drivers

- 隔离强度 vs 运维复杂度（自托管单二进制前提下）
- 现有数据零破坏迁移
- 与既有治理/身份/审计链路自然融合
- 实现可逆、可渐进交付（先单默认工作区，后按需增强）

## Considered Options（隔离策略）

| 方案 | 做法 | 优点 | 代价 / 风险 |
|---|---|---|---|
| **A. 共享库共享表 + 判别列** | 所有控制面表加 `workspace_id` 列；仓储层强制 `WHERE workspace_id=?` | 零额外运维；单二进制即可；迁移平滑（默认工作区） | 隔离强度依赖"作用域不被忘记"；一处漏写=越权泄漏，**爆炸半径大** |
| **B. 每租户独立 Schema** | 同库内按 workspace 切 schema（SQLite `ATTACH` / PG `search_path`） | 物理隔离，泄漏面小 | SQLite 多 schema 支持弱；路由与迁移复杂；自托管运维负担上升 |
| **C. 每租户独立数据库** | 一个 workspace 一个 DB 文件/连接 | 最强隔离，可独立备份/配额 | 运维最重；与"单二进制分钟级"定位冲突；连接管理复杂 |
| **D. 独立部署（现状延伸）** | 不引入租户，靠多实例 | 最简单 | 不满足"单实例服务多组织"需求；资源浪费 |

## Decision

**采用方案 A 为主（共享表 + `workspace_id` 判别列 + 仓储层强制作用域），并预留方案 C 作为企业增强档。**

具体设计：

### 1. 新增实体与关系
- 新表 `workspaces(id, name, slug UNIQUE, settings JSON, created_at)`。
- 新表 `workspace_members(workspace_id, user_id, role, created_at)`，PK=`(workspace_id, user_id)`。`role` 为**工作区级**角色（`workspace_admin` / `member` / `viewer`）。
- 现有治理/数据对象表增加 `workspace_id TEXT NOT NULL DEFAULT 'default'`：
  `datasources` / `table_permissions` / `row_policies` / `column_masks` / `schema_semantics` / `metrics` / `audit_logs`。
- `users` 保持**全局身份**（一个自然人/服务账号跨工作区），工作区归属走 `workspace_members`。

### 2. 工作区解析流（WorkspaceResolver）
每次受治理操作前，解析"有效工作区"：
1. 显式选择器：`X-Workspace-Id` 请求头 / API Key 绑定的 workspace / 请求体 `workspace` 字段；
2. 否则取该 principal 的**默认工作区**（成员关系中标记 `is_default` 或首个）；
3. 校验 principal 是成员，**失败即拒绝**（fail-closed）。
- MCP / DataAPI 统一走此解析；解析结果注入 `context`，下游所有 store 查询自动带上 `workspace_id`。

### 3. 作用域强制（防御纵深）
- **单一注入点**：仓储层（`internal/store`）的查询构造统一追加 `WHERE workspace_id = ?`，handler 无法直接绕过。
- **测试护城河**：新增跨工作区访问用例，断言返回空/拒绝。
- **未来可选第二层**：若控制面改用 Postgres，可用原生 RLS（行级安全）作为数据库层兜底（方案 A→C 的增强，不影响应用层）。

### 4. 管理员模型
- **平台管理员**：现有 `admin` 角色，跨工作区（支持/审计用），保留治理绕过能力。
- **工作区管理员**：`workspace_admin`，仅在本工作区内管成员/数据源/权限；数据面**不绕过治理**（除非同时是平台 admin）。
- 二者解耦，避免"给 BU 管理员就等于给了全平台绕过权"。

### 5. 配额（per-workspace）
- 现有 `config.Limits`（max_rows / rate_per_min / max_affected_rows / allow_no_where_writes）扩展为可按工作区覆盖：新表 `workspace_limits(workspace_id, ...)`，缺省回落全局配置。

### 6. 迁移（零破坏）
- 启动时若 `workspaces` 表不存在，执行迁移：
  1. 建 `workspaces`，种子 `default` 工作区；
  2. 为所有现存 `users` 建 `workspace_members`（原 `admin`→`default` 的 `workspace_admin`，其余→`member`）；
  3. 为各治理表 `ALTER TABLE ... ADD COLUMN workspace_id TEXT NOT NULL DEFAULT 'default'`。
- **结果**：每一个既有单实例部署自动成为"单工作区"部署，API/行为完全不变。

## Consequences

### 变得更容易
- 单实例即可服务多组织，打开大客户与 SaaS 形态。
- 治理与租户正交叠加，内核不变式不受影响。
- 现网部署无感升级（默认工作区）。

### 变得更难 / 代价
- 隔离强度依赖应用层作用域正确——**一处漏写即越权**，故必须把作用域收敛到仓储单一入口 + 测试覆盖（已列入设计）。
- 所有列表/查询 API 需显式或隐式携带工作区上下文，调用面变宽。
- 跨工作区全局视图（平台 admin 看全量审计）需单独聚合路径，不能简单 `SELECT *`。

## 演进策略（不重写）

1. **Phase 0（本 ADR 若采纳）**：单默认工作区 + 判别列 + 仓储作用域。现有部署零变化。✅ 已落地（含 Task #112 的 WorkspaceResolver + 工作区 CRUD）。
2. **Phase 1**：显式多工作区 CRUD + 成员邀请 + OIDC/LDAP 组→工作区映射（复用并扩展现有 `claim_mappings`）。✅ 已落地（Task #113）。
3. **Phase 2（企业增强，可选）**：对高隔离需求客户提供**每租户数据库**（方案 C）作为部署档位，应用层接口不变，仅仓储后端切换。
4. 每个 Phase 独立可交付、可逆。

### Phase 1 实现说明（Task #113）

**目标**：SSO（OIDC / LDAP）用户登录时，依据 IdP 组归属自动加入对应工作区，无需手动邀请。

**决策：扩展 `claim_mappings` 而非新增配置块。**
- `claim_mappings` 的值类型由 `string` 升级为 `config.ClaimMapping`，并配自定义 `UnmarshalJSON`：
  - 遗留字符串形式 `"admins":"admin"` **仍合法**（向后兼容，现有配置零改动）；
  - 新结构形式 `"admins":{"role":"admin","workspaces":[{"slug":"acme","role":"workspace_admin"}]}` 同时授予平台角色 + 工作区成员关系。
- 新增 `ResolveWorkspaces(mappings)`：从 IdP 组解析出 `[]WorkspaceBinding`（按 `slug/role` 去重）。
- `provisionOrLinkExternalUser` 在**每次登录**幂等应用：平台角色（`AddUserRole`）+ 工作区成员（`EnsureDefaultMembership` + 按 slug 查 `GetWorkspaceBySlug` 后 `AddWorkspaceMember`）。组变更可随下次登录动态生效。

**关键权衡（fail-safe 默认）**：
- **不自动创建工作区**：绑定目标 slug 不存在时跳过该绑定，而非凭登录副作用新建租户（避免组名拼错就炸出空租户）。管理员需先建好工作区再配映射——符合 ADR 的"治理默认开启、无惊喜副作用"基调。
- 工作区角色（`workspace_admin` / `member` / `viewer`）与平台角色（如 `admin`）解耦；空 `role` 回落 `member`。

**验证**：`internal/auth/*_test.go`（`ResolveWorkspaces` 解析 + `ClaimMapping` 双形态反序列化）、`internal/api/ldap_test.go`（`TestLDAPLogin_ProvisionsWorkspaceMembership` 端到端断言 SSO 用户落入映射工作区且仍在 `default`）。

## Open Questions / Risks

- **Q1**：工作区数量上限与单库性能——SQLite 下单库多工作区是否够？若客户量级大，Phase 2 的每租户库档位即为答案。
- **Q2**：跨工作区审计/报表是否需要？平台 admin 聚合路径的查询形态待定。
- **R1（主要风险）**：作用域遗漏导致泄漏。缓释=仓储单一入口 + 强制 code review 检查 + 跨租户测试。
- **R2**：现有 `admin` 绕过治理的语义在"工作区 admin"下需明确文档，避免误用。

## 参考

- `BLUEPRINT.md` — 路线图将"多租户工作区"列为企业版前置的最后企业门槛。
- `docs/launch.md` / `docs/github-setup.md` — 当前发布/采用就绪文档（多租户属采用后的企业门槛，优先级由采用反馈驱动）。
- `internal/store` 控制面、`internal/auth`（OIDC/LDAP）、`internal/permission.Rewrite`（治理内核不变式）。
