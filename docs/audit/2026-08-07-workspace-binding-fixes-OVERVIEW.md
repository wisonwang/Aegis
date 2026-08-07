# 概览：多租户作用域缺口修复（ADR-0007 收尾）

## 做了什么
审计（#146）暴露 ADR-0007 之后仍有 **6 个静默错配租户缺口**：写路径把裸 `r.Context()` 交给 store，admin 默认 `*` 跨工作区视图下 `WriteWorkspace(ctx)` 解析为 `default`，导致掩码/审批/指标/文件夹被静默盖到 `default` 工作区、且指标可跨租户删除。本次按 P0→P1→P2 全部修复并验证。

## 关键改动
- **GAP-1~5（写绑定）**：掩码 / 审批申请 / 审批授予权限 / 文件夹 / 指标，全部改用作用域 ctx（数据源对象用 `ds.BoundContext`/`resolveDSBound`，文件夹加 `workspace_id` 请求字段）；指标创建响应回显 `workspace_id`。
- **GAP-6（删除越界）**：`DeleteMetric(id)` → `DeleteMetric(ctx,id)`，走 `deleteWorkspaceScoped`；enterprise handler 改用 `writeMutationError`，跨区删除返回 **404** 而非 500。
- **可观测性**：`MetricDefinition` / `ApprovalRequest` 补 `WorkspaceID` 字段并回读。

## 验证
- `go build ./...` / `go vet ./internal/...` / `go test ./internal/...` 全绿。
- 新增单测 `internal/enterprise/handlers/workspace_binding_test.go`（`TestWorkspaceBinding`，`*` 视图下断言非 default 数据源写入对象正确绑定工作区）。
- **Live E2E（`:8080` enterprise + MySQL）**：建临时工作区→数据源绑定 ws→指标响应与列表 `workspace_id` 均正确→从 `default` 删指标返回 404（拦截）→清理成功。
- 线上二进制已重建并重启（持久后台 task `J7GkfS`）。

## 任务
#210–#215 全部 completed。

## 遗留（非本次范围）
`schema_semantics` / `data_classifications` 写路径 ctx 来源未确认（疑似由 catalog/LLM 流程产生），建议后续核查。
