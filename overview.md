# 用户目录 + 数据集目录管理 — 交付概览

**日期**：2026-07-30 ｜ **关联 ADR**：ADR-0004（状态 Proposed → Accepted）

## 一、阻塞修复：启动 panic
- **根因**：`internal/server/server.go` 重复注册了 `/api/v1/datasets*` 三条消费侧路由，与 `internal/enterprise/enterprise.go:42-44`（`enterprise.Register`，置于 `CapDataProducts` gate 后）路径冲突 → gin 在重复路径注册时 **panic**，服务完全无法启动。
- **决策**：消费侧数据集路由**统一由 `enterprise.Register` 注册**，`server.go` 删除重复 3 行并加注释防回归（单一真相来源，避免两条注册路径行为不一致）。
- 重建 + 后台常驻启动通过。

## 二、用户目录（P1c / #158，统一视图增强）
后端：
- `AdminListUsers` 新增字段：`email` / `type`(human|service) / `source`(local|sso) / `status` / `last_login_at` / `external_id`；支持 `?workspace=` 过滤。
- `ListUsers(workspaceID)` 仓储层增加 `WHERE id IN (SELECT user_id FROM workspace_members WHERE workspace_id=?)`。
- `AdminCreateUser` 支持创建时加入指定工作区（`workspace` 参数，接受 id 或 slug）。

前端（`js/users.js`）：用户表格新增邮箱/类型/来源/状态/最后登录列；每用户 API Key 管理对话框；按工作区下拉过滤；创建表单含邮箱/类型/工作区。

**修复缺陷**：
1. `AdminCreateUser` 曾把 `req.Workspace`(slug/id) 直接传给 `AddWorkspaceMember(wsID,...)`（要真实 ID）→ 写出悬空 `workspace_members` 行、用户未正确归属。已改为先 `GetWorkspace`→`GetWorkspaceBySlug` 解析出 `ws.ID` 再链接。
2. 前端 `fillWSSelects` 用 `w.id`/`w.name` 读取工作区，但 `/admin/api/workspaces` 因 `store.Workspace` 无 JSON tag 返回**大写** `ID`/`Name`/`Slug` → 下拉 `value=undefined`、创建工作区用户失效。已改为 `w.ID`/`w.Name`（与 `loadWorkspaces` 一致）。全仓仅此一处误用。

## 三、数据集目录（P1c / #159 + #160）
管理侧增强：
- `AdminCreate/UpdateDataset` 的 `fields` 字段（JSON 数组 `{name,type,description}`）作为字段契约，管理侧与消费侧 `GET /api/v1/datasets/:id` 详情均携带。
- 前端 `datasets.js` 由裸文本框改为 `renderFields`/`collectFields` 可视化字段表格（增删行）。

消费侧目录（数据目录）：
- `GET /api/v1/datasets`（列表）、`GET /api/v1/datasets/:id`（含字段契约）、`POST /api/v1/datasets/:id/query`（消费查询），由 `enterprise.Register` 统一注册。
- 前端新增「数据目录」tab（`loadCatalog`）：浏览已发布数据集、字段契约、复制消费句柄。

## 四、API Key 管理
- 自管：`/api/v1/me/apikeys`（create/list/revoke）；代管：`/admin/api/users/:id/apikeys`。
- `MeCreateAPIKey` 响应字段为 `key`(一次性原始)/`prefix`/`id`；API Key 认证 `/api/v1/me` 返回 200，错误 Key 返回 401。

## 五、验证结果（本地 enterprise 版 :8080）
- 启动 panic 消除，服务正常监听。
- 数据集：创建带 fields → 往返一致 → publish → 目录可见 → query 对 `customers` 表真实返回行。
- 用户：新字段齐全；按工作区过滤正确（ws2 仅其成员，default 不含 ws2 专属用户）。
- 前端嵌入校验：`/admin/` 含「数据目录」tab；`users.js`/`datasets.js` 含全部新钩子。

## 六、待办（非阻塞）
- 提交前 `git checkout config.json`（本地 `edition=enterprise` 仅用于验证）。
- 数据集字段级分类/脱敏联动（D6 蓝图）尚未实现。
- P1b 桥接（D3）、P2 层级（D4）待排期。
- 当前改动均未提交 git。
