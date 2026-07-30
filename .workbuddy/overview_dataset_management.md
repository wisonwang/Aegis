# 数据集管理（Data Products）— 迭代交付说明

在「数据库连接（数据源）」之上，新增 **数据集（Dataset）** 治理层：把一条查询固化为
**可发布、带字段契约、按名称供 Agent 消费**的受治理数据产品。 Agents 消费稳定的数据集，
而非底层物理表。

## 核心设计
- **复用既有治理引擎，零新权限模型**：数据集的表/行/列权限、动态脱敏、语义均以
  `table_name = 数据集名` 写入现有治理表（`table_permissions` / `row_policies` /
  `column_masks` / `schema_semantics`）。无需新建权限模型。
- **数据集即虚拟表**：权限引擎把数据集定义包成派生表
  `SELECT * FROM (<definition>) AS <数据集名>`，按别名做 SELECT 校验、把行策略注入
  **定义体内**、列脱敏按结果列名施加。
- **只读数据产品**：数据集是经策展的视图；Agent 拿到受治理（行策略 + 列裁剪 + PII 脱敏）
  的结果。写操作仍走底层数据源表。

## 新增 / 修改文件
- `internal/store/dataset.go` — `datasets` 表、CRUD、`DeleteDataset`（级联清理治理行）
- `internal/permission/engine.go` — `RewriteVirtual(...)` SQL 虚拟表治理
- `internal/permission/nosql.go` — `GovernNoSQLVirtual(...)` NoSQL 虚拟表治理
- `internal/proxy/dataset.go` — `ExecuteDataset` / `ListDatasets` / `DatasetCatalog` /
  `ValidateDatasetDefinition` / `aegis://dataset/<名称>/schema`
- `internal/api/dataset.go` — admin CRUD、发布/下架、数据集级 permissions·policies·masks·
  semantics；Agent 侧 list/get/query
- `internal/server/server.go` — 路由注册
- `internal/mcp/*.go` — `list_datasets` / `get_dataset_catalog` 工具 + 数据集资源
- `internal/server/seed.go` — 演示数据集 `paid_orders`（已支付订单，按租户隔离）
- `internal/permission/dataset_test.go` — 虚拟表治理单元测试
- `README.md` / `BLUEPRINT.html` — 文档同步

## API 速览
```
Admin:  GET/POST /admin/api/datasets · GET/PUT/DELETE /admin/api/datasets/{id}
        POST .../{id}/publish · /unpublish
        GET/POST .../{id}/permissions | /policies | /masks | /semantics
Agent:  GET /api/v1/datasets · GET /api/v1/datasets/{id} · POST /api/v1/datasets/{id}/query
MCP:    list_datasets · get_dataset_catalog · aegis://dataset/<名称>/schema
```

## 验证
- `go build ./...` / `go vet ./...` / `go test ./...` 全绿（新增 `dataset_test.go`）。
- 临时实例端到端 14/14 通过：admin/analyst 列表可见性、catalog 5 字段、analyst 查询仅见
  `tenant_id='acme'` 已支付单、admin 绕过见全部、未授权 403、数据集级 `amount` 哈希脱敏、
  MCP 工具/资源可读、删除级联。

## 已知边界
- 数据集当前为**只读**数据产品（不承接写）。
- NoSQL（mongo/es）数据集治理路径已实现但未做真机端到端验证（环境无可用 Mongo/ES）。
- 数据集查询暂不支持 Agent 自定义 WHERE/排序（数据集即固化视图；按需由策展方调整 definition）。
