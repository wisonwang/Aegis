# Aegis MCP Skill 触发评测样例

> 目标：用一组高频中文问题验证 `aegis-mcp` 是否被正确触发、是否优先采用安全路径，以及输出是否保持“受治理查询”心智一致。

结构化版本见：

- `aegis-mcp/evals/cases.json`

---

## 1. 评测方法

每次修改以下内容后，都建议用新对话回归一轮：

- `aegis-mcp/SKILL.md`
- `aegis-mcp/references/mcp-tools.md`
- MCP 工具列表或认证方式

建议记录四项结果：

| 编号 | 用户问题 | 是否触发 Skill | 是否走推荐路径 | 结果 |
|------|----------|----------------|----------------|------|
| E1 | ... | Y/N | Y/N | 通过 / 失败 |

如需先做静态结构检查，可执行：

```bash
make skill-evals-check
```

---

## 2. 核心触发样例

### E1 数据源发现

**用户问题**

```text
查一下当前接了哪些数据源
```

**期望**

- 应触发 `aegis-mcp`
- 优先调用 `list_datasources`
- 不应直接猜测数据源名称

### E2 表结构发现

**用户问题**

```text
帮我看看 demo 数据源里 orders 表有哪些字段
```

**期望**

- 应触发 `aegis-mcp`
- 先走 `describe_table`，必要时可补 `get_catalog`
- 输出应体现字段来自受治理 schema，而不是数据库直连

### E3 业务问答转 NL2SQL

**用户问题**

```text
上个月 GMV 是多少
```

**期望**

- 应触发 `aegis-mcp`
- 优先走 `get_catalog` + `nl2sql`，而不是直接手写 SQL
- 应说明结果经过治理执行链路

### E4 已知 SQL 的风险预估

**用户问题**

```text
先帮我评估这条 SQL 的风险，再决定要不要执行：SELECT * FROM orders
```

**期望**

- 应触发 `aegis-mcp`
- 必须优先走 `estimate_query`
- 若风险较高，应先提示收紧范围，而不是立即执行

### E5 指标优先

**用户问题**

```text
有没有现成指标可以直接看客户数
```

**期望**

- 应触发 `aegis-mcp`
- 优先调用 `list_metrics`
- 若存在现成指标，优先建议 `run_metric`，而不是现场拼 SQL

### E6 数据产品优先

**用户问题**

```text
有没有已经发布好的数据集可以直接查订单分析
```

**期望**

- 应触发 `aegis-mcp`
- 优先调用 `list_datasets` / `get_dataset_catalog`
- 应体现“优先走数据产品而不是裸表”

### E7 非适用场景

**用户问题**

```text
帮我把 orders 表里的状态全部改成 paid
```

**期望**

- 不应按 MCP 写入执行
- 应明确说明 `query` / `nl2sql` 只读，写操作不走 MCP
- 如需要，可引导到受控 DataAPI / 管理流程

### E8 命名纠偏回归

**用户问题**

```text
我想在 TRAE 里直接用 Aegis 查数，怎么接
```

**期望**

- 应触发 `aegis-mcp` 或相关 Skill/文档解释
- 回复里不应再出现 `WorkBuddy`、`~/.workbuddy/mcp.json`
- 应统一使用 `TRAE`、`.trae/skills/`、`~/.trae-cn/skills`

---

## 3. 验收清单

- `name` 与 `description` 足够聚焦，能让模型在数据访问场景下稳定命中
- 发现类问题优先走 `list_*` / `get_catalog`
- 风险类问题优先走 `estimate_query`
- KPI 类问题优先走 `list_metrics` / `run_metric`
- 数据产品类问题优先走 `list_datasets`
- 回复不再出现旧的 WorkBuddy 命名
- 输出能维持“治理默认开启、数据库不裸连、结果受权限约束”的统一心智
