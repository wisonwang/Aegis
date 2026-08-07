# ADR-0006: 身份 / 角色 / 权限治理体系重构

## Status
Accepted (Phase 1 implemented & verified on live `aegis` MySQL 2026-08-06) — Phase 2 still Proposed

## Context

用户要求重新梳理「数据源、用户、角色、数据集、权限」五大数据表与权限管理体系。审计当前 `internal/store` 后发现，体系能跑、但存在结构性债务，随着多租户（ADR-001）与企业版（数据集 / 指标）叠加，债务正在放大：

### 当前模型（as-is）

| 概念 | 表 | 关键形态 |
|------|----|----------|
| 主体 Principal | `users` (+ `api_keys`) | 服务账号也是 `users`（靠 `type` 区分）；`api_keys` 挂 `user_id` |
| 平台角色 | `roles` (id,name,is_system) + `user_roles`(user_id,role_id) | 全局治理角色：admin / analyst / 自定义 |
| 工作区角色 | `workspaces` + `workspace_members`(workspace_id,user_id,role VARCHAR(32)) | **角色是硬编码字符串** `ws_admin`/`member`/`viewer`，**不在 `roles` 表** |
| 数据源 | `datasources` | 无 `workspace_id`（全局共享，靠治理层 workspace 判别列隔离） |
| 表级治理 | `table_permissions` | `role_id`(TEXT)、`workspace_id`（由 ALTER 补列） |
| 行级治理 | `row_policies` | 同上 |
| 列级脱敏 | `column_masks` | `role_id`(VARCHAR(64))、**CREATE TABLE 无 workspace_id** |
| 目录元数据 | `schema_semantics` / `data_classifications` | 无 `workspace_id`、无角色维度 |
| 数据集（企业版） | `datasets` / `dataset_folders` | 自有 `workspace_id`；治理走另一套 |
| 横切 | `audit_logs` / `security_alerts` / `approval_requests` / `metric_definitions` | — |

### 核心问题

1. **两套正交的角色体系，无共享抽象。** 平台角色在 `roles`+`user_roles`；工作区角色是 `workspace_members.role` 里的硬编码字符串，二者**不在同一张表、无 `scope`、无外键**。桥接只发生在代码层 `ResolvePermissions`，schema 层面完全割裂（详见 P1b 历史任务）。
2. **没有真正的迁移框架，schema 漂移。** `migrate()` 把「过时的 `CREATE TABLE`（缺 `workspace_id`）」与一堆「无条件 `ALTER TABLE`（吞掉错误）」混在一起。权威 schema 被拆散、无版本号，`workspace_id` 这类关键列靠 ALTER 补，MySQL/SQLite 行为易分歧。
3. **零外键、零引用完整性。** 全是 `VARCHAR(64)` 字符串 id，没有任何 `REFERENCES`/`ON DELETE`。孤儿安全靠 handler 里的级联删除兜着（如删角色时手动清 `table_permissions` 等）。`role_id` 类型还不统一（`TEXT` vs `VARCHAR(64)`）。
4. **workspace 作用域不一致。** 表/行策略有 `workspace_id`（ALTER 补的）；列脱敏的 `workspace_id` 取决于 ALTER 循环是否覆盖；目录元数据（semantics/classifications）完全没有 `workspace_id`。多租户隔离在治理层是「隐式 + 代码内注入」，schema 不显式。
5. **三个「数据对象」概念（datasource / dataset / virtual_table）各自治理，无统一注册。** 数据集与数据源的组合关系藏在 `datasets.definition`（SQL 文本）里，没有 `governed_objects` 概念，治理策略无法统一挂载。
6. **命名不对称。** `user_roles`（平台赋值）与 `workspace_members`（租户赋值）本质都是「主体→角色」链接，却用两种完全不同的表建模。

> 正面点：治理授权表实际存的是 `roles.id`（不是角色名），所以改名角色不会静默破坏授权——这点**保持**。

## Decision

分两阶段重构，第一阶段低风险、可逆；第二阶段按需。

### 阶段 0：引入版本化迁移框架（零数据风险，先做）
- 新增 `schema_migrations(version PK, applied_at)`。
- 用编号迁移（`migrate/0001_init.up.sql` 等，Go 驱动）取代 `CREATE TABLE IF NOT EXISTS` + 散落 `ALTER` 的组合。每次启动按版本顺序执行未应用的迁移，幂等。
- 现有所有建表 + ALTER 收敛进 `0001`，行为不变。

### 阶段 1：统一角色与赋值（低风险、可逆）
- `roles` 增加 `scope` 列：`platform | workspace`，默认 `platform`；把 `ws_admin`/`member`/`viewer` **作为 scope='workspace' 的种子行**写入 `roles`。
- 新增 `role_assignments(principal_id, role_id, workspace_id NULL, granted_by, created_at)`，主键 `(principal_id, role_id, workspace_id)`：
  - `workspace_id IS NULL` = 平台全局赋值（替代 `user_roles`）；
  - `workspace_id` 有值 = 工作区赋值（替代 `workspace_members`，其 `role` 字符串改为引用 `roles.id` 的 workspace 角色行）。
  - 单一查询即可解析「某主体在任意上下文下的有效角色」，两套体系在 schema 层合一。
- 加真实外键 + 统一类型：`role_id VARCHAR(64) REFERENCES roles(id) ON DELETE CASCADE`、`principal_id REFERENCES users(id)`、`workspace_id`/`datasource_id` 同理。
- 补齐 `workspace_id`：给 `column_masks`、目录元数据表加 `workspace_id`（与表/行策略一致）。
- **过渡期**：保留 `user_roles`/`workspace_members` 为只读兼容，代码双写或加适配层；验证通过后删除旧表。

### 阶段 1 实施中发现的问题与修正（迁移 0003 / 0004）

统一到单表后暴露了两个原设计未预见的问题，均已在实施中修正：

**（1）`role_assignments.created_at` 为 NULL（迁移 0003）**
0002 的回填只搬了 `principal_id/role_id/workspace_id/is_default`，历史行 `created_at` 为 NULL，`ListWorkspaceMembers` 扫描 `time.Time` 直接失败，整个成员列表接口 500。
修正：0003 从 `workspace_members.created_at` 相关子查询恢复真实加入时间，剩余（来自 `user_roles`，本就无时间戳）用当前时间兜底；同时读取端改 `sql.NullTime` 防御。
**纪律**：不修改已发布的 0002，用后续迁移修数据——这是版本化迁移框架存在的意义。

**（2）工作区角色名占用了平台角色命名空间（迁移 0004）**
`roles.name` 有 `UNIQUE` 约束。0002 把工作区角色以 `viewer`/`member`/`workspace_admin` 之名种进 `roles`，导致 IdP 组映射出的**平台**角色 `viewer` 无法创建/解析——`GetRole("viewer")` 命中工作区行，赋权被 scope 护栏拒绝，SSO 用户静默变成零角色（LDAP 测试捕获）。
修正：工作区角色改为**按 id 寻址**（`ws-role:<level>`），`roles.name` 加 `ws:` 前缀仅为满足唯一索引；对外暴露的成员级别一律从 id 前缀派生，不再读 `name`。0004 执行重命名——因为 `role_assignments` 引用的是 `id` 而非 `name`，重命名不破坏任何既有授权。
**权衡**：`roles.name` 对工作区角色退化为装饰字段（轻微不一致），换来两套命名空间彻底解耦。替代方案是把 `UNIQUE(name)` 改为 `UNIQUE(name, scope)`，语义更纯，但 SQLite 无法在线改唯一索引（须重建表），风险显著更高，故不采用。

**（3）工作区成员级别可叠加（迁移 0005）**
旧 `workspace_members` 主键 `(workspace_id, user_id)` **隐式**保证「一个用户在一个工作区只有一个级别」。`role_assignments` 主键含 `role_id`，于是「改成员角色」从**替换**退化为**追加**——线上出现同一用户在同一工作区既是 `workspace_admin` 又是 `member`，并导致该工作区在用户的工作区列表里重复出现。
修正：`AddWorkspaceMember` 改为事务内「先删后插」的 set 语义（并保留最早加入时间，避免改级别把加入时间刷新）；`UserWorkspaces` 加 `DISTINCT` 防御；0005 清理已产生的重复行，**保留最高级别**（admin > member > viewer）、保留 default 标记与最早加入时间——静默降权比多留一个级别危险得多。
**教训**：把两张表合并到一张时，必须清点旧主键**隐式承载**的不变量。旧 schema 的约束就是需求文档。

配套护栏（防止同类问题复发）：
- `ListRoles()` 只返回非 workspace scope——工作区角色是成员级别，不是可授予的平台角色，不得出现在角色管理、权限授予、脱敏/数据集角色选择器里。
- `AddUserRole()` 拒绝 workspace scope 角色——否则等于「在所有工作区都是 member」，绕过成员关系。
- `GetRole(name)` 排除 workspace scope。
- SQLite 跳过 `addForeignKey`（不支持 `ALTER ADD CONSTRAINT`，且默认不强制 FK），避免每次启动刷 5 条 WARN。

### 阶段 2（可选，较高风险）：统一治理对象
- 引入 `governed_objects(id, kind['datasource'|'dataset'|'virtual_table'], ref, workspace_id)`。
- 表/行/列策略统一以 `(role_id FK, workspace_id, object_ref)` 为键挂载，数据集治理与主数据治理共用一套。
- **权衡**：减少按种类复制的治理代码，但增加抽象层；仅当数据集/虚拟表治理确实需要与主数据治理对齐时才做。可逆性低，故列为可选。

### 关于「合并为单一 policies 表」
不在本次范围。表/行/列三层策略**形状差异大**（ops vs predicate vs mask strategy），保留三张表、仅统一键与外键，比合成一张 EAV 式 `policies(layer, spec_json)` 更安全、查询更直白。合并的代价（失去类型化列、查询变 JSON 提取）大于收益。

## Consequences

**变容易：**
- 角色体系可演进（增删 workspace 角色、组合角色）只动 `roles` + `role_assignments`，不再依赖 `ResolvePermissions` 里的隐式桥接。
- schema 有版本、可审计、MySQL/SQLite 一致；新同事能看懂权威结构。
- 引用完整性由 DB 保证，删除主体/角色不再产生孤儿授权。
- 多租户作用域在 schema 显式化，治理层注入与存储一致，降低「漏隔离」风险。

**变困难 / 代价：**
- 阶段 1 是一次 schema 变更，需在**用户确认 + 备份**后执行（尤其生产 MySQL `aegis` 库）。
- 过渡期双写/适配层增加少量复杂度，属暂时性债务。
- 外键在 MySQL 上对 `VARCHAR` 主键有长度/编码约束，需统一字符集（utf8mb4）。
- 阶段 2 抽象层若过早引入会违背「无架构宇航员」原则，故标为可选、延后。

## 迁移策略（可逆性优先）
1. 阶段 0 纯框架，不改数据 → 随时可回退。
2. 阶段 1 先做「加列 + 新建 `role_assignments` + 回填」，旧表保留只读；切到新表查询并跑回归测试（含多租户隔离测试）通过后，再删旧表。
3. 阶段 2 仅在数据集/虚拟表治理需求明确时启动，单独 ADR。

## 待确认（Open Questions）
- ~~是否现在执行阶段 1（触及线上 `aegis` MySQL 库，需先备份）？~~ → **已确认执行**。备份 `data/backups/aegis_pre_adr0006_20260806.sql`，迁移 0001–0004 已在线上 `aegis` MySQL 应用。
- ~~工作区角色是否仍固化为三种，还是允许自建？~~ → **固化三种**：`workspace_admin` / `member` / `viewer`，不支持自建 workspace 角色。
- 阶段 2 是否在本次范围内？ → **否**，延后，需要时另开 ADR。
- 旧表 `user_roles` / `workspace_members` 的删除时机：当前保留只读用于回滚。原计划「0005 删除」已被去重占用，故删除顺延为 **0006+** 迁移（建议观察一个发布周期、确认无回滚需求后再删；删除前需先停掉任何仍读取旧表的代码路径）。
