# MCP Demo 案例设计

> 目标：用一个面向 AI Agent 的完整演示案例，直观证明 Aegis 不只是“能查数据库”，而是“把数据库变成受治理的 Agent 工具”。

---

## 1. Demo 定位

### 1.1 一句话故事

让一个没有数据库凭据的 AI 酒店运营分析师助手，在晨会前通过 Aegis 的 MCP 服务完成“发现酒店经营数据 -> 理解酒店业务口径 -> 安全评估 -> 受治理查询 -> 运行经营指标 -> 消费数据产品 -> 自然语言追问”的完整链路。

### 1.2 观众视角

本案例面向三类观众：

- **产品/业务方**
  看到 Agent 可以直接问数据，但结果仍然受权限与脱敏控制。
- **架构/安全方**
  看到数据库凭据没有暴露给 Agent，治理在网关强制执行。
- **研发/平台方**
  看到 MCP、Resources、Prompts、NL2SQL、指标、数据集已经形成可接入的完整接口层。

---

## 2. Demo 场景主线

### 2.1 角色设定

- **业务角色**：酒店运营分析师助手
- **调用主体**：`mcp-agent`
- **身份方式**：`X-MCP-API-Key: mcp-demo-key`
- **角色语义**：分析师视角，受表/行/列治理与脱敏约束，只能看自己租户口径

### 2.2 业务背景

假设每天早上 9:00 前，酒店运营分析师需要准备晨会口径，回答三个高频问题：

1. 已确认及在住订单的房费收入是多少？
2. 当日到店客人数是多少？
3. 如果要抽查 VIP 住客联系方式，系统是否会自动脱敏？

系统启动后自动播种演示数据：

- 数据源：`demo`
- 物理表：
  - `hotel_bookings`
  - `guest_profiles`
- 数据产品：
  - `hotel_confirmed_bookings`
- 指标：
  - 测试时动态创建 `arrival_guest_count`
  - 测试时动态创建 `confirmed_room_revenue`

种子数据覆盖的经营维度包括：

- 酒店 / 门店：三亚海棠湾度假酒店、杭州西湖度假酒店、上海外滩城市酒店、厦门海景酒店
- 城市：三亚、杭州、上海、厦门
- 渠道：`OTA`、`Direct`、`Member`、`Corporate`
- 房型：海景套房、行政套房、园景大床房、亲子房、标准房
- 订单状态：`confirmed`、`checked_in`、`pending`、`cancelled`
- 经营指标字段：`guest_count`、`room_nights`、`room_revenue`、`fnb_revenue`、`total_revenue`
- 住客画像字段：`member_tier`、`preferred_channel`、`phone`、`email`

### 2.3 业务问题

围绕“晨会备数”展开，依次回答：

1. 当前接了哪些数据源？
2. `hotel_bookings` / `guest_profiles` 表能访问什么字段？
3. 查询住客联系方式时，系统是否自动脱敏？
4. 执行酒店订单查询前，能否先看风险与扫描规模？
5. 有没有现成指标可以直接给出“房费收入”和“到店客人数”？
6. 有没有已发布的数据产品可以直接消费，而不必自己拼 SQL？
7. 是否支持用自然语言直接问“已确认订单的房费收入是多少”？
8. 同一个问题在 `analyst` 和 `admin` 视角下，结果是否会自动分口径？

---

## 3. 演示流程

### Step 1 初始化 MCP 会话

目的：

- 证明 Aegis 支持标准 MCP 生命周期

动作：

- `initialize`
- `notifications/initialized`

预期：

- 返回 `aegis-mcp`
- 后续请求带上 `Mcp-Session-Id`

### Step 2 工具发现

目的：

- 证明 Agent 可发现一整套受治理数据能力

动作：

- `tools/list`

重点看：

- `query`
- `estimate_query`
- `nl2sql`
- `list_metrics`
- `run_metric`
- `list_datasets`
- `get_dataset_catalog`

### Step 3 数据发现

目的：

- 证明 Agent 不需要预先记住库表结构

动作：

- `list_datasources`
- `list_tables`
- `describe_table`
- `get_catalog`

预期：

- 能看到 `demo`
- 能看到 `hotel_bookings`、`guest_profiles`
- 语义目录包含中文业务含义、同义词、示例值

### Step 4 受治理查询：抽查住客联系方式

目的：

- 证明业务人员可以看到会员等级与住客画像，但 PII 不会原样泄露

动作：

- `query(datasource=demo, sql=SELECT guest_name, member_tier, phone, email FROM guest_profiles ORDER BY id)`

预期：

- 返回的 `guest_name`、`phone`、`email` 均为脱敏值
- Agent 没有直接连接数据库，也没有原始凭据

### Step 5 风险预估：先评估，再查酒店订单

目的：

- 证明 Agent 在执行酒店经营分析前可先做风险评估，而不是直接扫全表

动作：

- `estimate_query(datasource=demo, sql=SELECT hotel_name, room_revenue FROM hotel_bookings)`

预期：

- 返回估算行数
- 返回涉及表
- 返回风险提示信息

### Step 6 指标消费：晨会先跑酒店经营 KPI

目的：

- 证明晨会备数可以优先走“策展指标”而不是每次临时写 SQL

动作：

- 测试前通过管理 API 创建 `arrival_guest_count`
- 测试前通过管理 API 创建 `confirmed_room_revenue`
- `list_metrics`
- `run_metric(metric=arrival_guest_count)`
- `run_metric(metric=confirmed_room_revenue)`

预期：

- 返回指标定义
- 返回到店客人数结果
- 返回房费收入结果

### Step 7 数据产品消费：优先消费已发布数据产品

目的：

- 证明业务分析优先消费平台发布的数据产品，而不是直接摸底表

动作：

- `list_datasets`
- `get_dataset_catalog(name=hotel_confirmed_bookings)`
- `resources/read(uri=aegis://dataset/hotel_confirmed_bookings/schema)`

预期：

- 能发现 `hotel_confirmed_bookings`
- 能拿到稳定字段契约

### Step 8 Prompts 与 NL2SQL：业务追问

目的：

- 证明 Aegis 不只提供“工具”，还提供“业务语义 + 安全上下文”

动作：

- `prompts/list`
- `prompts/get(name=nl2sql, ...)`
- `nl2sql(question=已确认订单的房费收入是多少？)`

预期：

- Prompt 中自动注入治理后的 schema
- `nl2sql` 返回只读 SQL
- 最终结果正确，且仍走治理执行链

### Step 9 双视角对照：运营分析师 vs 管理员

目的：

- 证明同一 MCP 接口会随身份自动切换治理口径，而不是靠前端写死分支

动作：

- `make mcp-e2e`
- `make mcp-e2e-admin`

预期：

- `analyst` 看到脱敏住客信息，房费收入为租户口径
- `admin` 看到原始住客信息，房费收入为全量口径

---

## 4. 成功标准

- Agent 全程只使用 MCP，不持有数据库账号密码
- 工具发现、资源、Prompt、查询、指标、数据产品、NL2SQL 全部可用
- 住客字段返回结果可见脱敏效果
- SQL 风险评估可先于执行
- `confirmed_room_revenue` 与 `nl2sql` 的房费收入结果一致
- `analyst / admin` 对同一问题可自动呈现不同治理口径

---

## 5. 推荐演示话术

- “现在不是让 Agent 直连数据库，而是让它像调工具一样调用 Aegis，拿到的是治理后的经营数据能力。”
- “晨会备数不再靠人手写 SQL，平台已经把房费收入、到店客数、数据产品和酒店语义目录都准备好了。”
- “你看到的住客姓名、手机号和邮箱已经自动脱敏，说明治理是在网关层强制生效，而不是靠应用自己约束。”
- “同一问题在 analyst 和 admin 视角下口径不同，证明权限是运行时生效的。”
- “即使是 NL2SQL，模型生成的 SQL 也没有绕过治理链路。”

---

## 6. 推荐配套材料

- 测试脚本：[mcp_e2e_scenario.py](file:///Users/vincent/workspace/fosun/datahub/scripts/mcp_e2e_scenario.py)
- 测试结果报告：[mcp-demo-test-report.md](file:///Users/vincent/workspace/fosun/datahub/docs/mcp-demo-test-report.md)
- 示例入口：[examples/README.md](file:///Users/vincent/workspace/fosun/datahub/examples/README.md)
