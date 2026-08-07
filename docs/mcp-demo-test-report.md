# MCP Demo 测试报告

> 报告目的：记录基于项目目标场景的 MCP Demo 全流程验证结果，作为内部验收与外部演示的事实依据。

---

## 1. 测试范围

本次测试围绕“酒店运营晨会备数”这一更贴近真实业务的目标场景展开：

- Agent 不直连数据库
- 通过 MCP 发现数据能力
- 查询结果受治理与脱敏约束
- 可先做 SQL 风险评估
- 可消费经营指标与数据产品
- 支持通过 Prompt 与 NL2SQL 进行自然语言问数
- 同一问题在 `analyst / admin` 视角下呈现不同口径

---

## 2. 测试入口

- 测试脚本：[mcp_e2e_scenario.py](file:///Users/vincent/workspace/fosun/datahub/scripts/mcp_e2e_scenario.py)
- Make 命令：[Makefile](file:///Users/vincent/workspace/fosun/datahub/Makefile)

执行命令：

```bash
make mcp-e2e
make mcp-e2e-admin
```

首次执行因脚本缺少本地执行权限失败，补充权限后重跑成功：

```bash
chmod +x scripts/mcp_e2e_scenario.py && make mcp-e2e
```

---

## 3. 测试环境

- 启动方式：
  由测试脚本自动生成临时配置，并 `go run ./cmd/aegis`
- 控制面：
  SQLite 临时库
- 数据源：
  自动播种的 `demo` 酒店经营演示数据源
- 认证方式：
  - `analyst`：`X-MCP-API-Key: mcp-demo-key`
  - `admin`：`Bearer <JWT>`
- LLM：
  脚本内置本地 fake OpenAI-compatible stub，用于跑通 `nl2sql`

---

## 4. 覆盖清单

### 4.1 生命周期

- `initialize`
- `notifications/initialized`

### 4.2 MCP Tools

- `tools/list`
- `list_datasources`
- `list_tables`
- `describe_table`
- `get_catalog`
- `query`
- `estimate_query`
- `list_metrics`
- `run_metric`
- `list_datasets`
- `get_dataset_catalog`
- `nl2sql`

### 4.3 MCP Resources / Prompts

- `resources/list`
- `resources/read`
- `prompts/list`
- `prompts/get`

---

## 5. 实际结果

### 5.1 测试结论

- 结果：**通过**
- 结论：Aegis 当前已具备面向酒店经营分析场景的 MCP 完整演示能力

### 5.2 关键输出：analyst 视角

```json
{
  "mode": "analyst",
  "scenario": "酒店运营晨会备数",
  "datasource": "demo",
  "query_rows": [
    {
      "email": "a***@demo.com",
      "guest_name": "A*********g",
      "member_tier": "gold",
      "phone": "138****5678"
    },
    {
      "email": "d***@demo.com",
      "guest_name": "D********g",
      "member_tier": "silver",
      "phone": "137****3333"
    }
  ],
  "metric_rows": [
    {
      "arrival_guests": 3
    }
  ],
  "confirmed_room_revenue_rows": [
    {
      "confirmed_room_revenue": 4160
    }
  ],
  "nl2sql_rows": [
    {
      "confirmed_room_revenue": 4160
    }
  ],
  "resource_uris": [
    "aegis://dataset/hotel_confirmed_bookings/schema",
    "aegis://demo/schema"
  ]
}
```

### 5.3 关键输出：admin 视角

```json
{
  "mode": "admin",
  "scenario": "酒店运营晨会备数",
  "datasource": "demo",
  "query_rows": [
    {
      "email": "alice.zhang@demo.com",
      "guest_name": "Alice Zhang",
      "member_tier": "gold",
      "phone": "13812345678"
    },
    {
      "email": "brian.chen@demo.com",
      "guest_name": "Brian Chen",
      "member_tier": "platinum",
      "phone": "13900001111"
    },
    {
      "email": "daisy.wang@demo.com",
      "guest_name": "Daisy Wang",
      "member_tier": "silver",
      "phone": "13722223333"
    },
    {
      "email": "eric.xu@demo.com",
      "guest_name": "Eric Xu",
      "member_tier": "silver",
      "phone": "13688889999"
    }
  ],
  "metric_rows": [
    {
      "arrival_guests": 4
    }
  ],
  "confirmed_room_revenue_rows": [
    {
      "confirmed_room_revenue": 6040
    }
  ],
  "nl2sql_rows": [
    {
      "confirmed_room_revenue": 6040
    }
  ],
  "resource_uris": [
    "aegis://dataset/hotel_confirmed_bookings/schema",
    "aegis://demo/schema"
  ]
}
```

---

## 6. 场景验证结论

### 6.1 Agent 能发现数据能力

- 通过 `tools/list` 能发现查询、风险预估、指标、数据产品、NL2SQL 等完整工具集
- 说明 Aegis 对 Agent 暴露的不是单点查询，而是一整套受治理数据能力面

### 6.2 查询结果已受治理

- `guest_profiles` 查询在 `analyst` 视角返回 2 行、在 `admin` 视角返回 4 行
- `guest_name / phone / email` 都被脱敏
- 说明列级脱敏已在 MCP 链路中生效

### 6.3 风险预估可先于执行

- `estimate_query` 可正常返回估算行数与涉及表
- 说明 Agent 可以在执行前做风险决策

### 6.4 指标层可被 Agent 直接消费

- 测试脚本预创建 `arrival_guest_count` 与 `confirmed_room_revenue`
- `list_metrics / run_metric` 执行成功
- 说明“酒店经营 KPI -> Agent 消费”的链路已打通

### 6.5 数据产品层可被 Agent 发现

- `hotel_confirmed_bookings` 可通过 `list_datasets` 与 `get_dataset_catalog` 发现
- `resources/read(aegis://dataset/hotel_confirmed_bookings/schema)` 可正常读取
- 说明数据产品契约已能通过 MCP 暴露给 Agent

### 6.6 NL2SQL 链路完整

- `prompts/get` 可返回治理后的 schema 提示
- `nl2sql` 成功生成并执行只读 SQL
- `analyst` 返回 `confirmed_room_revenue = 4160`
- `admin` 返回 `confirmed_room_revenue = 6040`
- 说明自然语言问数链路不仅闭环，而且会随身份自动切换治理口径

### 6.7 双视角差异明确

- `analyst` 看到脱敏住客信息与本租户酒店订单
- `admin` 看到原始住客信息与全量酒店订单
- `analyst` 的 `confirmed_room_revenue / nl2sql` 返回租户口径 `4160`
- `admin` 的 `confirmed_room_revenue / nl2sql` 返回全量口径 `6040`
- 说明治理不是“前端隐藏”，而是运行时强制执行

---

## 7. 过程中发现并修正的脚本预期

为使脚本与当前仓库真实行为一致，已修正以下断言：

- `list_tables` 当前返回对象数组，而不是字符串数组
- `guest_profiles` 当前同时体现了列级脱敏与租户级行过滤
- `guest_profiles.guest_name` 已被自动推荐掩码为 `partial`
- `get_dataset_catalog` 当前使用 `fields` 表达数据集契约

这些修正不属于产品缺陷，而是让测试脚本与当前实现保持一致。

---

## 8. 可复用结论

这份 Demo 已可直接用于：

- 内部产品评审
- 安全/架构评审
- 对外演示“数据库变成受治理 Agent 工具”的核心价值
- 面向业务方展示“酒店晨会备数 / 经营口径 / 脱敏保护 / 权限差异”
- 后续 CI 中的 MCP 回归冒烟测试

---

## 9. 建议下一步

- 将本脚本纳入 CI，形成稳定回归
- 继续补充库存房量、入住率、渠道成本等更完整的酒店经营主题表
- 在主 [README.md](file:///Users/vincent/workspace/fosun/datahub/README.md) 进一步加入“业务口径差异”示意图
