# 工作区 / 多租户作用域审计（ADR-0007 收尾）— 2026-08-06

## 0. 范围与方法

ADR-0007 已把 **数据源 / 权限 / 数据目录 / 数据集** 绑定到工作区。本审计核查「是否还有别的数据对象类型漏掉了工作区作用域」，分三层：

1. **Store 层**：每个含 `workspace_id` 的实体是否在 list / read / write / delete 处强制作用域。
2. **Handler 层（core `internal/api` + enterprise `internal/enterprise/handlers`）**：读路径是否走中间件注入的 `r.Context()`；**写路径**是否把工作区从请求体（或所属数据源）显式绑定进作用域 ctx，而不是把裸 `r.Context()`（在 `*` 跨工作区视图下解析为 `default`）直接交给 store。
3. **Store 删除护栏**：`delete` 是否按工作区过滤（防越界删除 / 枚举）。

> 关键不变量（ADR-0007）：store 的 CREATE 一律用 `WriteWorkspace(ctx)` 盖章。该值**当且仅当 ctx 已被显式作用域化**时才正确；若 handler 把裸 `r.Context()`（admin 默认 `*` 视图）传进去，对象会被静默盖到 `default` 工作区——这正是 dataset 此前塌进 `default` 的同类 bug。

---

## 1. 实体 × 层 合规矩阵

| 实体 | Store 写盖章 | 读/列表作用域 | 删除护栏 | Handler 写绑定 | 结论 |
|---|---|---|---|---|---|
| datasource | `WriteWorkspace(ctx)` | ✅ | `deleteWorkspaceScoped`（非 `*` 视图过滤） | core `datasourceWriteCtx`（payload `workspace_id`，`*` 视图**必填**）✅ | **合规** |
| dataset | `WriteWorkspace(ctx)` | ✅ | `GetDataset` 作用域 + 级联 ✅ | core+enterprise `writeCtx`（payload `workspace_id`）✅ | **合规**（ADR-0007 已修） |
| table_permission | `WriteWorkspace(ctx)` | ✅ | `deleteWorkspaceScoped` ✅ | core+enterprise 均 `resolveDSBound`/`ds.BoundContext` ✅ | **合规** |
| row_policy | `WriteWorkspace(ctx)` | ✅ | `deleteWorkspaceScoped` ✅ | core+enterprise 均 `resolveDSBound`/`ds.BoundContext` ✅ | **合规** |
| column_mask | `WriteWorkspace(ctx)` | ✅ | `deleteWorkspaceScoped` ✅ | ❌ core `UpsertColumnMask(r.Context())` | **GAP-1** |
| approval_request | `WriteWorkspace(ctx)` | ✅ | 仅 admin 视图 | ❌ core+enterprise `CreateApprovalRequest(r.Context())` | **GAP-2** |
| approval→permission 授予 | `WriteWorkspace(ctx)` | — | `deleteWorkspaceScoped` ✅ | ❌ core+enterprise `CreateTablePermission(r.Context())` | **GAP-3** |
| dataset_folder | `WriteWorkspace(ctx)` | ✅ | `GetFolder` 作用域 ✅ | ❌ enterprise `CreateFolder(r.Context())`（无 `workspace_id`） | **GAP-4** |
| metric_definition | `WriteWorkspace(ctx)` | ✅ | ❌ **`DeleteMetric(id)` 无 ctx / 无工作区过滤** | ❌ enterprise `UpsertMetric(r.Context())` | **GAP-5 + GAP-6** |
| schema_semantics | `WriteWorkspace(ctx)` | ✅ | `DeleteSemantic` 走 `ds.BoundContext` ✅ | core `AdminUpsertSemantic`(`resolveDSBound`) + ent `AdminUpsertDatasetSemantic`(`ds.BoundContext`) ✅ | **合规**（2026-08-07 复核：此前「无独立写路径」判断有误，写路径存在且已正确绑定） |
| data_classifications | `WriteWorkspace(ctx)` | ✅ | `DeleteClassification` 走 `ds.BoundContext` ✅ | core `AdminUpsertClassification`(`resolveDSBound`) ✅（enterprise 未覆盖，走 core） | **合规**（2026-08-07 复核） |
| api_keys / security_alerts / audit_logs / users / roles | 部分无 `workspace_id` 或全局 | 各异 | — | 身份/运维类，非「数据对象」租户隔离重点 | 不在本次范围 |

---

## 2. 确认的缺口（含文件:行号）

### GAP-1 — 列脱敏掩码写错工作区
- `internal/api/masks.go:238` `h.Store.UpsertColumnMask(r.Context(), m)`
- 掩码已带 `DataSourceID: dsID`，且本函数已通过 `h.resolveDS(r.Context(), ...)` 解析出 `dsID`（line 175）。
- **修复**：改用 `ds.BoundContext(r.Context())`。最省事的做法是把 line 175 换成 `h.resolveDSBound(r.Context(), ...)`，拿到已绑定的 `ctx`，并用于 `ListClassifications` 与 `UpsertColumnMask`。
- 影响：admin 在 `*` 视图（UI 默认）点「应用推荐脱敏」时，掩码被盖到 `default`，治理对该数据源（属其他工作区）实际不生效。

### GAP-2 — 权限审批申请写错工作区
- core `internal/api/approval.go:108`；enterprise `internal/enterprise/handlers/handlers.go:994`：`h.Store.CreateApprovalRequest(r.Context(), ar)`
- `UserSubmitApproval` 已解析 `ds`（approval.go:71-84）。
- **修复**：`h.Store.CreateApprovalRequest(ds.BoundContext(r.Context()), ar)`。两处 handler 都要改。

### GAP-3 — 审批通过后授予的表权限写错工作区
- core `internal/api/approval.go:198`；enterprise `internal/enterprise/handlers/handlers.go:1060`：`h.Store.CreateTablePermission(r.Context(), perm)`
- `AdminApproveApproval` 已解析 `ds`（approval.go:183-191）。
- **修复**：`h.Store.CreateTablePermission(ds.BoundContext(r.Context()), perm)`。两处都要改。
- 注意：直接走 `AdminCreateTablePermission` 的路径（adminapi.go / dataset.go / enterprise handlers.go 的 500/578）**已正确**用 `resolveDSBound`/`ds.BoundContext`，仅「审批流授予」这条路径漏了。

### GAP-4 — 数据目录文件夹写错工作区
- enterprise `internal/enterprise/handlers/handlers.go:398` `h.Store.CreateFolder(r.Context(), f)`
- 文件夹**没有**数据源引用，`f` 也未带 `WorkspaceID`，store 的 `CreateFolder` 用 `WriteWorkspace(ctx)` → `*` 视图下塌 `default`。
- **修复**：在 `folderRequest` 增加 `workspace_id` 字段，handler 内镜像 datasource/dataset 的模式——校验后 `store.WithWorkspace(r.Context(), ws.ID)`；`*` 视图下**必填**（与 datasourceWriteCtx 一致）。或退而求其次：若有 `ParentID`，继承父文件夹的 `workspace_id`。推荐显式 `workspace_id`，与既有约定统一。

### GAP-5 — 指标定义写错工作区
- enterprise `internal/enterprise/handlers/handlers.go:893` `h.Store.UpsertMetric(r.Context(), m)`
- `AdminUpsertMetric` 已 `resolveDS`（line 869）得到 `dsID`。
- **修复**：改用 `resolveDSBound` 拿绑定 ctx，传给 `UpsertMetric`。（core 无指标写路径；指标为 enterprise 能力。）

### GAP-6 — `DeleteMetric` 完全没有工作区过滤（store 层）
- `internal/store/metric.go:124` `func (s *Store) DeleteMetric(id string) error` —— **无 `ctx` 参数，DELETE 仅 `WHERE id=?`**。
- 后果：任何已认证 admin（无论作用域视图）只要知道 id 即可删除**任意**工作区的指标；与其他治理删除（`deleteWorkspaceScoped`）不一致，是防御性缺失。
- **修复**：改为 `DeleteMetric(ctx context.Context, id string)`，内部走 `s.deleteWorkspaceScoped(ctx, "metric_definitions", id)`。需同步更新调用方（enterprise `AdminDeleteMetric` 等）与测试。

---

## 3. 删除路径专项结论

- `deleteWorkspaceScoped`（store.go:1042）在**非 `*` 视图**用 `COALESCE(NULLIF(workspace_id,''),'default')=WorkspaceID(ctx)` 过滤；在 `*` 视图（平台 admin）不过滤 → 允许跨工作区删除（属预期）。table_permission / row_policy / column_mask 删除均走此路径，**合规**。
- `DeleteDataset`（dataset.go:247）先 `GetDataset(ctx,id)`（非 `*` 视图已作用域），再按 id 删 → 非 `*` 视图安全；`*` 视图平台 admin 可删任意 → 与上面一致，可接受。
- **唯一例外 = `DeleteMetric`（GAP-6）**：完全无作用域。需修。

---

## 4. 风险定级与紧迫性

**当前线上（4 个工作区：acme-corp / default / globex / 复游会 已存在）仍是 latent（潜伏）风险**：现有 2 数据源 + 2 数据集均在 `default`，所以即便 mask/approval/metric/folder 被错误盖到 `default` 也「看起来正常」。但**一旦在非 `default` 工作区使用这些能力（现在工作区已存在，很可能发生），admin 在默认 `*` 视图下创建的掩码/审批/指标/文件夹会被静默错配到 `default`，治理实际失效且跨租户泄漏**。

- **P0（静默错配，需尽快）**：GAP-1 / GAP-2 / GAP-3 / GAP-5 —— 均为「把裸 `r.Context()` 换成 `ds.BoundContext(r.Context())` / `resolveDSBound`」，改动小、风险低、与既有权限/策略路径完全同构。
- **P1（需补请求字段）**：GAP-4 文件夹创建 —— 增加 `workspace_id` 请求字段 + 绑定逻辑（含 `*` 视图必填）。涉及 handler + 前端表单。
- **P2（防御一致性）**：GAP-6 `DeleteMetric` 加 `ctx` + 工作区过滤。

---

## 5. 建议落地方式（待用户确认范围后再改代码）

1. 先修 P0 四条（GAP-1/2/3/5），每处都是 1-2 行 ctx 替换；补单测（在 `*` 视图下断言写入对象的 `workspace_id` 等于所属数据源工作区，而非 `default`）。
2. 再修 P1 文件夹（GAP-4），同步前端 `datasources.js`/`datasets.js` 已有 `workspace_id` 下拉的写法。
3. 最后收尾 P2 `DeleteMetric`。
4. ~~同步核查 `schema_semantics` / `data_classifications` 的写路径~~ — 2026-08-07 复核：两者均有独立 admin 写入口（`AdminUpsertSemantic`/`AdminUpsertClassification`/`AdminUpsertDatasetSemantic`），均通过 `resolveDSBound` / `ds.BoundContext` 正确绑定工作区，**合规，无缺口**。

> 不擅自大规模改代码：以上为审计产出与修复提案，待确认是否执行、以及优先级。

---

## 6. 待办（task 跟踪）

- #206 枚举数据对象与 workspace_id 现状 —— ✅
- #207 审计 store 层作用域强制 —— ✅
- #208 审计 handler 层（含 enterprise 覆盖）作用域一致性 —— ✅
- #209 产出审计结果与缺口修复 ADR —— ✅（本报告）
- 后续：依确认范围落地 P0→P1→P2 修复（新任务）

---

## 7. 修复状态（2026-08-07）— 全部 GAP 已修复并验证

用户确认「继续」后按 P0→P1→P2 顺序落地。改动均为同构 ctx 绑定 + 1 处 store 签名 + 2 处响应/错误码一致性。

| GAP | 修复点 | 改动 | 验证 |
|---|---|---|---|
| GAP-1 列脱敏掩码 | `internal/api/masks.go` | `resolveDS` → `resolveDSBound` 拿绑定 ctx，用于 `UpsertColumnMask` | 单测 + E2E |
| GAP-2 审批申请 | core `approval.go:108` + ent `handlers.go:994` | `CreateApprovalRequest(r.Context())` → `ds.BoundContext(r.Context())` | 单测 `TestWorkspaceBinding` |
| GAP-3 审批授予权限 | core `approval.go:198` + ent `handlers.go:1060` | `CreateTablePermission(r.Context())` → 绑定 ctx | 单测 `TestWorkspaceBinding` |
| GAP-4 数据目录文件夹 | ent `handlers.go` `AdminCreateFolder` | 请求结构加 `workspace_id`；`*` 视图必填并构建 scoped `writeCtx` | 单测 `TestWorkspaceBinding` |
| GAP-5 指标定义 | ent `handlers.go:893` `UpsertMetric` | `r.Context()` → `ds.BoundContext(r.Context())`；**响应回显 `workspace_id`** | E2E（响应带 ws_id） |
| GAP-6 指标删除越界 | store `metric.go` + ent `handlers.go:923` | `DeleteMetric(id)` → `DeleteMetric(ctx,id)` 走 `deleteWorkspaceScoped`；handler 改用 `writeMutationError`（404 而非 500） | E2E（跨区删 404） |

**附带可观测性增强**：`MetricDefinition`、`ApprovalRequest` 结构体补 `WorkspaceID` 字段并回读（此前绑定已写入但 API 不可见）。

**测试**：
- 新增 `internal/enterprise/handlers/workspace_binding_test.go`（Go 单测），在 `*` 视图下为「非 default 工作区数据源」写指标/文件夹/审批/权限，断言 `workspace_id` 绑定到数据源工作区、且跨工作区删除被拦截。
- `go build ./...` / `go vet ./internal/...` / `go test ./internal/...` 全绿（无 FAIL）。
- **Live E2E（`:8080` enterprise + MySQL）**：建临时工作区→建数据源（绑定 ws）→建指标（响应 `workspace_id`=ws 且列表一致）→从 `default` 视图删除指标返回 **404**（拦截）→ 清理全部成功。

**结论**：ADR-0007 收尾的 6 个静默错配租户缺口已全部闭合；线上二进制已重建并重启验证。`schema_semantics` / `data_classifications` 经 2026-08-07 二次复核，确认均有正确绑定工作区的 admin 写路径，**无遗留缺口**。审计到此完整闭环。
