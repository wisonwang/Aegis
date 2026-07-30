# 数据集目录管理（D7）— 交付说明

## 问题：上一版「数据目录」为什么「没用」

上一版只是把**平铺**的数据集换了一种展示方式（平铺换皮），没有任何组织能力。
数据集一多就难管理：无法分层、无法归类、无法批量归并，消费端看到的仍是一长串平铺列表。

## 本轮交付：可管理的嵌套文件夹树

### 设计原则（关键不变量，保证可逆、零治理风险）
- `folder_id` 只是**组织元数据**。数据集现有 `name`（消费句柄、治理键）保持工作区内唯一不变
  → 加目录**不破坏**任何现有消费 / 治理链路，可逆性好。
- 自引用 `parent_id` + 前端建树，最小可行方案，无新权限模型。

### 管理端（`/admin/` 「数据集」tab）
- **左侧文件夹树**：任意层级「新建根目录 / 新建子目录 / 选择」，点击节点即筛选右侧表格。
- **目录栏 folderbar**：当前文件夹下「新建子文件夹 / 重命名 / 删除 / 移动数据集」。
- **数据集表单**：新增 `folder_id` 选择器（归属到某目录节点，或「未分类」）。

### 消费端（`/api/v1` + MCP + 「数据目录」tab）
- `GET /api/v1/dataset-folders` 返回目录树；`GET /api/v1/datasets?folder_id=&recursive=` 支持按目录过滤。
- 「数据目录」tab 渲染为**可折叠树**（展开/折叠 + 卡片详情 + 复制句柄）。
- 每个数据集携带 `folder_id`，下游消费与治理键不变。

### 后端 API（新增 / 改造）
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/admin/api/dataset-folders` | 文件夹列表（管理） |
| POST | `/admin/api/dataset-folders` | 新建文件夹 |
| PUT | `/admin/api/dataset-folders/:id` | 重命名 / 移动（防环、同级重名检测） |
| DELETE | `/admin/api/dataset-folders/:id` | 删除（**非空 409**） |
| POST | `/admin/api/datasets/:id/move` | 移动数据集到某目录 |
| GET | `/admin/api/datasets?folder_id=&recursive=` | 按目录过滤（支持递归） |
| GET | `/api/v1/dataset-folders` | 文件夹树（消费端） |

### 存储层
- 新增 `dataset_folders` 表：自引用 `parent_id`，`UNIQUE(workspace_id, parent_id, name)`。
- `datasets` 表加 `folder_id` 列（幂等迁移）。

### 关键文件
- 新增 `internal/store/dataset_folder.go`（文件夹 CRUD + 子树/递归辅助）
- 改 `internal/store/dataset.go`（`folder_id` + 迁移 + 空结果 `[]` 防 nil-slice）
- 改 `internal/enterprise/handlers/handlers.go`（folder CRUD / move + dataset 按 folder 过滤）
- 改 `internal/enterprise/enterprise.go`（新路由注册，均为新路径不冲突）
- 改 `internal/proxy/dataset.go`（`FolderID` 透传至消费端）
- 改 `internal/server/web/*`（`index.html` / `style.css` / `js/datasets.js` 目录树 UI）

## 验证（端到端冒烟全绿）
- 嵌套目录建 / 挂数据集 → 递归 / 非递归过滤结果正确
- 移动数据集 OK；删除非空文件夹返回 **409**；防环返回 **400**
- 消费端 tree + `folder_id` 透传；PUT 不改归属（哨兵 `nil` 语义）
- Go nil-slice → JSON `null` 崩溃已修复（空切片统一返回 `[]`）
- `go build` / 重启服务 OK；服务 `http://127.0.0.1:8080/admin/` 在线

## 待办 / 风险
- **提交前 `git checkout config.json`**（本地 `edition=enterprise` 仅验证用，勿提交）
- 所有改动**尚未 git commit**
- 字段级分类 / 脱敏联动（D6 蓝图）未实现
- 可选增强：拖拽排序、面包屑、文件夹级权限
