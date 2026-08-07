# Aegis 演示 Walkthrough 与客户案例模板

> 用途：① 给销售/售前一套**可复现**的演示脚本（5–10 分钟跑完治理穿透）；② 给客户成功一套**案例模板**，把 PoC 沉淀成可对外讲的客户故事。
> 适用版本：Community（开源内核即可完整演示三级治理）+ Enterprise（数据产品 / 指标 / 审批流增强）。
> 演示数据：内置 `demo` 数据源（hotel_bookings / guest_profiles 经营表）+ 演示账号 `admin` / `analyst` / `mcp-agent`。

---

## 一、演示前置

```bash
# 1) 起服务（demo 配置会自动播种演示租户 + 演示账号 + mcp-demo-key）
make run                      # 等价于 go build -o aegis ./cmd/aegis && ./aegis -config conf/config.demo.json

# 2) 或只跑 MCP 端到端场景（自动覆盖 tools/resources/prompts/query/estimate/metrics/datasets）
make mcp-e2e                  # 分析师视角（受治理）
make mcp-e2e-admin            # 管理员视角（绕过治理，对比口径）
```

- 后台 UI：http://localhost:8080/admin/ （`admin` / `admin123`）
- DataAPI：http://localhost:8080/api/v1
- MCP：http://localhost:8080/mcp （`X-MCP-API-Key: mcp-demo-key`）

演示账号对照：

| 账号 | 角色 | 看点 |
|------|------|------|
| `admin` / `admin123` | admin | 绕过行级治理，看"全量"口径 |
| `analyst` / `analyst123` | analyst | 受表/行/列约束，属性 `tenant=acme` |
| `mcp-agent` / `mcp123` | analyst | MCP 静态 Key 服务账号，继承 analyst 治理 |

---

## 二、演示脚本（5–10 分钟，治理穿透主线）

### 步骤 1 · 一句话定位（30 秒）
"把内部数据库变成受控的 Agent 工具——LLM 生成的 SQL 也绕不过治理。"

### 步骤 2 · 治理后查询对比（核心，3 分钟）
用 `analyst` 与 `admin` 各查同一张表，突出**行级策略 + 列脱敏**：

```bash
TOKEN=$(curl -s -X POST localhost:8080/api/v1/login \
  -d '{"username":"analyst","password":"analyst123"}' | jq -r .token)

# analyst 视角：行策略自动注入 tenant_id='acme'，PII 列被脱敏
curl -s localhost:8080/api/v1/query -H "Authorization: Bearer $TOKEN" \
  -d '{"datasource":"demo","sql":"SELECT guest_name, phone, room_revenue FROM hotel_bookings"}'
```

返回里的 `rewritten_sql` 即实际下发 SQL，例如：
```sql
SELECT guest_name, phone, room_revenue
FROM (SELECT * FROM hotel_bookings WHERE tenant_id = 'acme') AS hotel_bookings
```
`phone` 值被脱敏为 `138****5678` 之类；`admin` 同条查询则看到全量与明文——**同一问题不同人得到各自权限内的答案**。

### 步骤 3 · MCP 让 Agent 安全取数（3 分钟）
把 MCP 配置交给 Claude Desktop / 任意客户端（无需数据库凭据）：
```json
{ "mcpServers": { "aegis": {
  "url": "http://localhost:8080/mcp",
  "headers": { "X-MCP-API-Key": "mcp-demo-key" } } } }
```
Agent 立刻获得 `query` / `estimate_query` / `nl2sql` / `list_datasets` 等工具，结果受同一治理约束。

### 步骤 4 · 执行前风险预估（2 分钟）
```bash
curl -s -X POST localhost:8080/api/v1/datasources/demo/query/estimate \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"sql":"SELECT * FROM guest_profiles"}'
# 返回 risk_level + 扫描行数 + 是否含 PII，防 Agent 拖库
```

### 步骤 5 · 审计与合规留痕（1 分钟）
后台"审计日志"页或 `GET /admin/api/audit?status=denied` 展示 ok/denied/error 三态 + 会话串联；一条被拒查询即证明"默认拒绝"生效。

### 截图建议（放客户 deck）
1. 后台"数据集"tab 左侧目录树（D7 管理能力）。
2. `analyst` vs `admin` 同一查询的结果差异（行/列治理肉眼可见）。
3. MCP 客户端里 Agent 调用 `query` 拿到脱敏结果。
4. 审计页 `denied` 记录 + `rewritten_sql` 字段。

---

## 三、客户案例模板（沉淀 PoC 成故事）

> 复制以下模板，按客户真实场景填空，即形成可对外讲的案例。敏感数据用脱敏占位。

### 模板 A · 企业内部 Copilot 问数

- **客户画像**：______（行业 / 规模 / 角色：AI 平台团队 or 数据负责人）
- **背景**：团队想给内部人员做"自然语言问数" Copilot，但不敢让 LLM 直连生产库。
- **痛点**：① SQL 由 LLM 现场生成不可评审；② 不同人应看到不同数据范围；③ 客户 PII 不能进对话上下文。
- **Aegis 方案**：NL2SQL 网关 + 行级策略 `:attr` 注入 + 列动态脱敏 + 全量审计。
- **验证点**：行策略自动注入 `tenant_id=:tenant`；`phone`/`email` 自动脱敏；审计三态留痕。
- **成效指标**：问数场景上线周期 ______ 天；PII 泄露风险降至 ______；审计覆盖率 100%。

### 模板 B · 运营分析晨会备数

- **客户画像**：______（酒旅 / 零售 / 医疗 经营分析团队）
- **背景**：晨会前需备数，多个分析师口径不一致、重复取数。
- **痛点**：① 指标口径漂移；② 分析师与管理员看到不该一致的数据；③ 数据产品无复用。
- **Aegis 方案**：语义指标层（Metrics，口径一致）+ 数据产品（Datasets，复用治理）+ 基于角色的治理。
- **验证点**：`run_metric` 返回治理后结果与血缘；`dataset` 发布后 Agent 按名消费。
- **成效指标**：备数耗时从 ______ 降至 ______；口径一致率 ______%。

### 模板 C · 面向客户的 AI 数据助手

- **客户画像**：______（SaaS / 多租户产品，需内嵌问数助手）
- **背景**：产品想给终端客户做 AI 问数，但租户数据必须隔离、查询不能失控。
- **痛点**：① 多租户数据隔离；② Agent 失控循环拖垮生产库；③ 合规需审计。
- **Aegis 方案**：Enterprise 多租户工作区 + 限流 + estimate_query 风险预估 + 审计。
- **验证点**：跨租户查询被隔离；超行数/超时查询被拦截；审计可上 SIEM。
- **成效指标**：租户隔离合规通过 ______；查询 P95 延迟 ______；失控查询拦截率 ______。

---

## 四、PoC 成功标准（Checklist）

- [ ] Agent 可通过 MCP 调用受治理数据（无需数据库凭据）
- [ ] 至少 1 个真实数据源接通（MySQL / PostgreSQL）
- [ ] 至少 1 条行级策略 + 1 组脱敏规则生效（admin / analyst 视图差异可演示）
- [ ] 审计日志完整留痕（含 denied）
- [ ] 跑通 NL2SQL 自然语言问数 + estimate_query 风险预估

---

## 五、关联资源

- 演示案例设计：[mcp-demo-case.md](file:///Users/vincent/workspace/fosun/datahub/docs/mcp-demo-case.md)
- 演示测试报告：[mcp-demo-test-report.md](file:///Users/vincent/workspace/fosun/datahub/docs/mcp-demo-test-report.md)
- 一页式介绍：[one-pager.md](file:///Users/vincent/workspace/fosun/datahub/docs/one-pager.md)
- 架构图：[pics/aegis-architecture.svg](file:///Users/vincent/workspace/fosun/datahub/docs/pics/aegis-architecture.svg)
- 竞争版图：[pics/competitive-landscape.svg](file:///Users/vincent/workspace/fosun/datahub/docs/pics/competitive-landscape.svg)
