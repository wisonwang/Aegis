# ADR-0004: 用户与角色体系完善

## Status
Accepted

> 实现进度（2026-07-30）：P0 (D1) 已完成；P1a (D2) 已完成；P1c (D5 用户目录 + D6 数据集目录) 已完成并通过冒烟验证；P1b (D3 桥接) 与 P2 (D4 层级) 待实施。

## Context
Aegis 当前已有可用的用户/角色/权限 API，但模型本身存在"未完成"项与一处架构隐患，经代码核查确认：

1. **两套角色体系正交、互不连通**（最关键隐患）
   - 平台角色（`admin`/`analyst`/自定义）携带治理权限 `TablePermission`/`RowPolicy`/`ColumnMask`；
   - 工作区角色（`ws_admin`/`member`/`viewer`）仅控制租户成员与可见性，**不携带任何数据治理**；
   - 两者无桥接：`ws_admin` 无法管理本工作区内用户的平台角色，平台角色也无工作区作用域。
2. **角色模型扁平**：`Role{ID,Name,Description}` 无层级继承、无 `system` 标记、无 `Update`；`AdminDeleteRole` 可删掉 `admin` 角色，而 `admin` 是 superuser（绕过治理），属删库级 footgun。
3. **用户生命周期字段缺失**：无 `email`/`last_login`/MFA，无服务账号与每用户 API Key（MCP 现仅一个全局静态 key）；`Status=disabled` 需确认是否在全链路真正拦截。
4. **审计与恢复缺口**：角色授予、用户禁用、改密等变更未纳入审计；`admin` 角色误删后无恢复路径。

**关键约束（已核实）**：所有角色合并成"按表有效视图"只发生在 `Store.ResolvePermissions`（store.go:785），之后才进 SQL 改写引擎。因此**角色模型任何演进（继承/桥接/统一）都只改这一处，改写引擎不动**——这是低风险演进的天然接缝。

用户已确认范围：角色补全、用户补全+服务账号、两套体系桥接、角色层级/继承、用户目录；架构取向为 **保持两套体系分离 + 桥接，不统一为单一模型**。

## Decision

### D0. 架构取向：分离 + 桥接（不统一）
平台角色与工作区角色保持为两个独立概念。新增"工作区维度的角色分配授权"作为桥接层，**不**把平台角色改造成带工作区作用域的单一模型。理由：避免存量权限数据迁移、避免改写引擎与聚合层重构，改动可逆、风险低。

### D1. 角色补全（P0）
- 新增 `UpdateRole`（改名/改描述）。
- `Role` 增加 `system bool`（DB 加列），seed 时将 `admin`/`analyst` 标记为 system。
- `AdminDeleteRole` 拒绝删除 system 角色；`AddUserRole`/`RemoveUserRole` 禁止把 `admin` system 角色授予/回收给非全局 admin（防提权）。
- 用户 `Status=disabled` 在全链路生效：登录拒绝、token 签发拒绝、请求中间件 fail-closed 拦截。
- 管理界面：角色 tab 补全 列表/创建/编辑/删除（带 system 守卫）；用户 tab 补全 禁用/启用、改密、角色分配。

### D2. 用户补全 + 服务账号（P1a）
- `User` 增加 `Email`、`LastLoginAt`、`Type`（`human`/`service`）。
- 新增 `api_keys` 表（`id,user_id,name,key_hash,created_at,last_used_at,expires_at`）；服务账号无密码、凭 API Key 认证。
- 新增 API Key 认证中间件（Header `Authorization: Bearer <key>` 或 `X-API-Key`），DataAPI 与 MCP 共用；解析为 user 上下文。
- `LastLoginAt` 登录成功时更新；禁用拦截覆盖服务账号。

### D3. 两套体系桥接（P1b）
- 新增 `CanManageUserRoles(caller, targetUserID)` 判定：caller 为全局 admin → 允许；或 caller 为某工作区 `ws_admin` 且 target 为该工作区成员 → 允许（仅限**非 system** 平台角色）。
- 在 `AddUserRole`/`RemoveUserRole` 入口应用该判定；`ws_admin` 不能操作系统角色、不能影响本工作区外用户。
- 权限规则的**编写**（TablePermission 等）仍仅限全局 admin，桥接只下放"分配"，不下放"定义治理"。

### D4. 角色层级 / 继承（P2）
- 新增角色父级关系（`role_parents` 表或 `parent_id`）；有效权限 = 自身 ∪ 所有祖先的治理规则。
- 直接复用 `ResolvePermissions` 已有的多角色合并逻辑：聚合前把祖先角色一并展开即可；掩码仍"首胜"（定义祖先优先或自身优先的明确语义）。
- 需做环检测；"角色模板"= 仅作基类、自身无直接权限的角色。

### D5. 用户目录（P1c，含一处待确认假设）
- 统一用户视图 + 工作区维度用户列表（`ListUsers` 增加 workspace 过滤）。
- 目录同步：周期性 LDAP/OIDC 组→角色/工作区成员映射同步，使权限随企业目录变化自动收敛（复用现有 `claim_mappings`/`provisionOrLinkExternalUser`）。
- 管理界面"用户目录"整合视图。
- **假设**：此处"用户目录"指"目录同步 + 统一视图"。若你指的是别的（如外部 IdP 直连查询），请纠偏。
- **2026-07-30 交付**：统一视图已落地——`AdminListUsers` 新增 `email`/`type`(human|service)/`source`(local|sso)/`status`/`last_login_at`/`external_id` 字段；`ListUsers(workspaceID)` 支持按工作区过滤（仓储层 `WHERE id IN (SELECT user_id FROM workspace_members WHERE workspace_id=?)`）；前端"用户"tab 增加邮箱/类型/来源/状态/最后登录列、每用户 API Key 管理对话框、按工作区下拉过滤；`AdminCreateUser` 支持创建时加入指定工作区。验证见文末"实现记录"。

### D6. 数据集目录（P1c，本次新增）
- 管理侧增强：`AdminCreateDataset`/`AdminUpdateDataset` 的 `fields` 字段支持字段契约（JSON 数组 `{name,type,description}`），前端数据集 tab 提供可视化字段表格增删（非裸 JSON 文本框）。
- 消费侧目录：只读 `GET /api/v1/datasets`（列表）、`GET /api/v1/datasets/:id`（详情含字段契约）、`POST /api/v1/datasets/:id/query`（消费查询）作为"数据目录"对外提供，前端新增"数据目录"tab 浏览已发布数据集与字段契约。
- **路由归属决策（重要）**：`/api/v1/datasets*` 三条消费侧路由**仅由 `enterprise.Register`（`internal/enterprise/enterprise.go:42-44`）注册一次**，置于 `CapDataProducts` gate 之后。`internal/server/server.go` 的 `registerRoutes` **不得重复注册**——gin 在重复路径注册时会 panic（本次即因此 panic 阻塞启动）。`server.go` 已加注释说明，避免回归。
- **分类分级/治理联动**：字段契约与既有 `datasources/:id/classifications`、`masks` 共享同一数据源表，后续可在字段级挂分类与脱敏（蓝图待补）。

## Consequences

| 决策 | 变得更容易 | 变得更难 / 代价 |
|------|-----------|----------------|
| D0 分离+桥接 | 不迁数据、改写引擎不动、可逆 | 两套概念并存，需桥接层维护 |
| D1 角色补全 | 防误删 admin、禁用真正生效 | 删除角色增加守卫分支 |
| D2 服务账号 | 非人身份安全接入、可独立吊销 | 新增 api_keys 表与认证中间件 |
| D3 桥接 | ws_admin 可自助管理本区角色 | 引入"谁能分配谁的角色"判定，需防提权 |
| D4 层级 | 减少权限重复配置 | 环检测、掩码合并语义需定义 |
| D5 目录 | 权限随企业目录自动收敛 | 同步任务、冲突解决策略 |
| D7 目录管理 | 数据集可按业务域分层组织、导航、按目录视图；folder_id 纯组织元数据、可逆、零治理风险 | 需维护树结构（防环/删除保护）、前端树组件 |

**不做的代价**：若放任两套体系割裂，`ws_admin` 永远无法管理数据权限，多租户下的自助治理无法落地；`admin` 角色可被误删导致系统失去 superuser。

## 决策 D7（2026-07-30 追加）：数据集目录管理（层级文件夹）

### Context
用户反馈此前"数据目录"只是把**平铺**数据集换一种展示，无法组织，数据集一多就难管理。真正需要的是**目录管理**：在管理端建任意层级的文件夹树，把数据集挂到节点下；消费端也按树浏览。

### Decision
- 新增 `dataset_folders` 表（`id` / `parent_id`（自引用，`''`=根）/ `workspace_id` / `name` / `sort_order`），`UNIQUE(ws, parent_id, name)` 约束同级同名。
- `datasets` 增加 `folder_id`（可空，`''`=未分类/根）。**folder 为纯组织元数据**：移动数据集只改 `folder_id`，不动 `name`（消费句柄）也不动任何治理行（按 `table_name=dataset.Name` 关联）——可逆、零治理风险。
- 管理端：`/admin/api/dataset-folders` 提供 CRUD；`POST /admin/api/datasets/:id/move` 移动；`GET /admin/api/datasets?folder_id=&recursive=` 按目录过滤（子树展开）。
- 消费端：`GET /api/v1/dataset-folders` 提供树（新路径，不冲突）；`GET /api/v1/datasets` 每个数据集带 `folder_id`；前端渲染**可折叠目录树**。
- 防御：删除非空文件夹（含子目录或数据集）返回 `409`；移动文件夹做**防环校验**（不能落入自身后代）。

### Consequences
数据集从平铺变得可分层治理与导航；`folder_id` 与消费/治理完全解耦，改动可逆。代价是需维护树结构（防环、删除保护）与前端树组件——这是"管理"诉求的固有成本，用最小可行树（自引用 + 前端构建）控制复杂度。

## 演进接缝（实现要点）
所有角色相关改动最终都收敛到 `Store.ResolvePermissions`（store.go:785）与角色分配 API（`AddUserRole`/`RemoveUserRole`）。D3/D4 只扩展"喂给 ResolvePermissions 的角色集合"与"谁有权喂"，不动 SQL 改写引擎（`internal/permission/engine.go`）。

## 路线图
- **P0**：D1 角色补全（UpdateRole + system 守卫 + 禁用全链路 + UI）—— **已完成**。
- **P1a**：D2 用户补全+服务账号+API Key —— **已完成**（含 API Key 认证中间件、每用户 Key 管理）。
- **P1b**：D3 两套体系桥接 —— 待实施。
- **P1c**：D5 用户目录（统一视图+工作区过滤+API Key 管理 UI）+ D6 数据集目录（字段契约+消费侧目录）+ D7 数据集目录管理（层级文件夹） —— **已完成并通过冒烟验证**。
- **P2**：D4 角色层级/继承 —— 待实施。

## 实现记录（2026-07-30）

### 验证结果（本地 enterprise 版，:8080）
- 启动 panic 已消除：`enterprise.Register` 与 `server.go` 不再重复注册 `/api/v1/datasets*`。
- 用户目录：登录→`GET /admin/api/users` 返回 `email`/`type`/`source`/`status`/`last_login_at`/`external_id`；按工作区过滤生效（ws2 仅含其成员，default 不含 ws2 专属用户）；每用户 API Key 创建/列举/吊销、`/api/v1/me/apikeys` 自管、API Key 认证 `200`、错误 Key `401` 均通过。
- 数据集目录：`POST /admin/api/datasets` 带 `fields` 字段契约往返一致；`publish` 后 `GET /api/v1/datasets` 目录可见；`POST /api/v1/datasets/:id/query` 对 `customers` 表真实返回 3 行。
- 前端嵌入校验：`/admin/` 提供 `数据目录` tab；`users.js` 含 `fillWSSelects`/`openAPIKeyDialog`/`apiKeyDialog`；`datasets.js` 含 `renderFields`/`collectFields`/`loadCatalog`。

### 修复的两个实现缺陷（同批发现）
1. **`AdminCreateUser` 工作区解析 bug**：原代码把 `req.Workspace`（slug 或 id）直接传给 `AddWorkspaceMember(wsID,...)`，而该仓储方法要求真实 workspace ID。传 slug 会写出悬空 `workspace_members` 行、用户实际未被正确归属。已改为先 `GetWorkspace`→`GetWorkspaceBySlug` 解析出 `ws.ID` 再链接，错误显式返回（不再被 `_=` 吞掉）。
2. **前端 `fillWSSelects` 字段大小写 bug**：`/admin/api/workspaces` 因 `store.Workspace` 无 JSON tag 返回大写 `ID`/`Name`/`Slug`（项目既有约定），但 `fillWSSelects` 用 `w.id`/`w.name` 读取 → 用户表单工作区下拉 `value=undefined`，创建工作区用户失效。已改为 `w.ID`/`w.Name`（与 `loadWorkspaces` 保持一致）。
   - 同步排查：`loadWorkspaces`/`loadUsersIntoWS` 已正确使用大写/小写对应字段，无其他同类误用。

### 数据集目录管理（D7，2026-07-30）
- **后端**：新增 `internal/store/dataset_folder.go`（Folder 模型 + CRUD + 防环校验 + 删除保护 + 子树/过滤辅助）；`datasets` 增加 `folder_id` 列（`migrateDatasets` 内用 `columnExists` 幂等 ALTER）；`Dataset`/`DatasetInfo`/`DatasetSchema` 补 `folder_id`；enterprise handlers 新增 `AdminListFolders`/`AdminCreateFolder`/`AdminUpdateFolder`/`AdminDeleteFolder`/`AdminMoveDataset` + 消费 `ListFolders`；`enterprise.go` 注册新路由（均为新路径，不与现有 `/api/v1/datasets*` 重复注册冲突）。
- **前端**：`datasets.js` 管理端左侧文件夹树 + 目录栏（新建根/子目录、重命名、删除、移动数据集）+ 表单 folder 选择器；消费端 `catalog` tab 改为**可折叠目录树**（搜索时退化为平铺卡片）。`index.html` 加 `catalog-layout`/`folderDialog`(`<dialog>`)/`catTree`，`style.css` 加目录树样式。
- **验证（全绿）**：建嵌套目录→挂数据集→递归/非递归过滤正确→移动到根→非空删除 `409`→消费端 `GET /api/v1/dataset-folders` 与 dataset `folder_id` 正确→PUT 不带 `folder_id` 不改变归属→防环校验拒绝。
- **实现缺陷修复**：Go 的 nil slice 会序列化为 `null`——`ListDatasets`/`ListFolders`/`ListDatasetsByFolder` 的空结果改为返回非空 `[]`，避免前端把 `datasets`/`folders` 收到 `null`。

### 待办（非阻塞）
- `config.json` 本地 `edition=enterprise` 为本地验证用，提交前需 `git checkout config.json` 还原。
- 数据集字段级分类/脱敏联动（D6 蓝图）尚未实现。
- P1b 桥接、P2 层级待排期。
