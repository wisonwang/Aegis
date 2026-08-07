# ADR-0007: 受治理对象与工作区绑定模型

## Status
Accepted

## Context

ADR-001 确立了「共享 schema + `workspace_id` 判别列 + 仓储层强制作用域」的多租户骨架，ADR-0006 把角色体系统一到了 `role_assignments`。但实机验证发现：虽然 `datasources` / `datasets` / `table_permissions` / `row_policies` / `column_masks` / `schema_semantics` / `data_classifications` 等表都带有 `workspace_id` 列，这些列在本地开发里**始终是 `default` 一个值**——对象从未被真正绑定到工作区。

根因不在 schema，而在「绑定动作缺失」：

- 后台 UI 从不发送 `X-Workspace-Id`（core.js 的 `api()` 助手漏了该头），于是 `WorkspaceResolver` 对管理员解析为 `*`（跨工作区视图），而 `WriteWorkspace(ctx)` 把每次创建都坍缩成 `default`。
- `datasource/manager.go` 的连接池用 `context.Background()` 去取数据源，强制 `workspace_id='default'`，导致非 `default` 工作区的数据源「not found」、无法连接。
- `ResolvePermissions` 在管理员 `*` 上下文下会把**所有租户**的授权聚合成一条策略（跨租户 bleed）。
- `listPermView` 等列表用 `context.Background()`，永远只显示 `default` 的授权。
- 多个删除端点（权限/目录）只按 `id` 删，无工作区护栏（IDOR 隐患）。

用户确认的三项决策（2026-08-06）：
1. **数据源按工作区归属**（datasource-per-workspace）。
2. 后台用**全局工作区切换器**（顶栏下拉设置 `X-Workspace-Id`），复用 users.js 既有模式。
3. **管理员保留显式跨工作区视图**，但**查询时权限解析绝不跨租户聚合**。

## Decision

### 1. 数据源归属单一工作区
每个数据源属于一个工作区。`CreateDataSource` 已用 `WriteWorkspace(ctx)` 盖章；连接池改为**按 `id` 取数据源（不带 workspace 过滤，因 `id` 全局唯一）**，从而非 `default` 工作区的数据源也可连接。列表/删除按上下文工作区过滤（管理员 `*` 可见全部）。

### 2. 权限 / 目录 / 数据集治理「写时继承数据源工作区」
表/行/列权限、业务语义、数据分类，都挂在「某个数据源的某张表」上，因此它们**不**使用调用者切换器里的工作区，而是**继承其所属数据源的 `workspace_id`**：

```
wctx = WithWorkspace(ctx, datasource.WorkspaceID)
CreateTablePermission(wctx, p) / CreateRowPolicy(wctx, p) / UpsertColumnMask(wctx, m)
UpsertSemantic(wctx, s) / UpsertClassification(wctx, c)
```

理由：数据源是工作区边界的锚点。让授权随数据源走，保证「A 工作区的数据源只可能被 A 工作区的授权治理」，即使管理员当前停在「全部工作区」视图创建授权，也不会落到 `default`。数据集治理同理（数据集通过 `DataSourceID` 追溯到数据源工作区）。

### 3. 查询时权限解析永不跨租户
`ResolvePermissions` 在上下文 `CrossesWorkspaces(ctx)`（即管理员 `*`）时，回落到调用者的默认工作区（`DefaultWorkspaceForUser`）再解析——单工作区、确定性的有效策略，绝不聚合多租户。

### 4. 列表 ctx 修正 + 输出 workspace_id
`listPermView` 等业务列表改用 `r.Context()`（不再 `context.Background()`），使管理员在某一工作区上下文时只看到该区的授权；`ListTablePermissions` / `ListRowPolicies` / `ListColumnMasks` 的 SELECT 增加 `workspace_id` 并在输出中返回，便于「全部工作区」视图标注每行归属。

### 5. 删除护栏（纵深防御）
`DeleteTablePermission` / `DeleteRowPolicy` / `DeleteColumnMask` / `DeleteSemantic` / `DeleteClassification` / `DeleteClassificationsByTable` / `DeleteDataset` / `DeleteDataSource` 增加 `ctx` 参数与工作区护栏：仅当 `CrossesWorkspaces(ctx)`（管理员全局视图）或 `对象.workspace_id == WorkspaceID(ctx)` 时才允许删除，否则返回 403。管理端点虽已 `RequireAdmin`，此护栏让模型自洽，并为将来向 workspace_admin 开放自服务铺路。

### 6. 前端切换器
- `core.js`：`api()` 在 `currentWorkspace` 非空时发送 `X-Workspace-Id`；新增 `setWorkspace(id)`（持久化到 localStorage + 派发 `workspace-changed` 事件）。
- `index.html` 顶栏增加 `#wsSwitcher`；`showApp` 填充（管理员含「全部工作区」`*` 选项）。
- `datasources.js` / `governance.js` / `datasets.js` 监听 `workspace-changed` 刷新，并在列表增加工作区列。创建表单无需工作区字段（数据源按切换器推导；权限/目录按所属数据源推导）。

## Consequences

**变容易：**
- 多租户隔离在「数据源 + 权限 + 目录 + 数据集」全链路真正生效，本地开发所见即生产行为。
- 治理对象与工作区的关系单一、可解释：数据源是锚，权限/目录/数据集都挂在它下面。
- 管理员仍可一键横向管理所有工作区；查询解析确定性、不泄漏。

**代价 / 约束：**
- 数据源**不可跨工作区共享**：同一物理库若需服务多个工作区，须在各工作区各注册一个数据源（治理策略各自独立）。若未来需要「共享数据源 + 按工作区分治」，需引入 ADR-0006 阶段 2 的 `governed_objects`，属更高风险变更，本次不做。
- 在「全部工作区」视图下新建数据源会落到 `default` 工作区（由 `WriteWorkspace` 语义决定）；管理员应切到具体工作区再创建。权限/目录创建不受此影响（继承数据源工作区）。
- 后台管理端点仍 `RequireAdmin`；workspace_admin 自助管理本区对象为后续增强，不在本次范围。

## 验证
- `go test ./internal/...` 全绿；pytest 通过。
- live MySQL：非 `default` 工作区数据源可连接；权限按工作区隔离；`*` 查询解析单工作区；删除跨工作区被 403。
