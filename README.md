# Aegis · 数据库代理治理平台

![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)
![Go](https://img.shields.io/badge/Go-1.26-blue.svg)
![MCP](https://img.shields.io/badge/MCP-Streamable%20HTTP-purple.svg)
![Deploy](https://img.shields.io/badge/deploy-single%20binary-orange.svg)

> **把内部数据库变成受治理的 AI Agent 工具**——单二进制、默认开启治理、LLM 生成的 SQL 也绕不过权限。详见 [CHANGELOG](CHANGELOG.md) · [SECURITY](SECURITY.md) · [examples](examples/)。

> 使用 Golang 构建的**数据库代理 / 数据服务治理平台**：集中管理数据库访问权限、封装表/行/列级数据权限，并以 **DataAPI** 与 **MCP 服务** 的形式统一对外提供数据能力，供业务系统与 AI Agent 使用。

> **命名（Rebrand）：** 项目已由 **DataHub** 更名为 **Aegis**（副标题 *AI Data Supply Gateway*），以规避与 LinkedIn / Acryl 同名数据目录产品（metadata catalog）的品牌与 SEO 混淆。代码级标识符已同步更名完成：模块路径 `github.com/wisonwang/aegis`、`aegis://` URI 方案、`aegis_svc` 受限账号、`AEGIS_*` 环境变量、`cmd/aegis` 入口目录。

---

## 核心理念：应用不再直连数据库

传统模式下，每个应用都持有数据库的账号与密码，权限散落在各处的连接串中，难以审计与回收。

Aegis 在应用（或 AI Agent）与后端数据库之间插入一个**代理层**：

```
┌────────────┐      JWT / MCP       ┌──────────────────┐   服务账号(单一)   ┌────────────┐
│ 应用 / AI  │ ───────────────────▶ │    Aegis 代理   │ ────────────────▶ │  MySQL /   │
│   Agent    │ ◀─────────────────── │ 权限引擎 + 连接池 │ ◀──────────────── │ PostgreSQL │
└────────────┘   治理后的结果(脱敏) └──────────────────┘                   │  SQLite    │
                                                                          └────────────┘
```

后端数据库的**真实账号权限被隔离**在平台内部：平台用单一服务账号连接数据库，所有访问控制都在代理层完成。

---

## 三大能力映射需求

| 需求                                  | 实现                                                                                                                                                            |
| ----------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1. 集中权限管理，隔离数据库自身账号                 | 平台持有后端 DB 服务账号；应用/ Agent 仅持平台 JWT。后端账号凭据不离开平台。                                                                                                                |
| 2. 封装表 / 行权限，统一后台管理                 | 权限引擎解析 SQL，校验**表级**操作权限、注入**行级**策略（派生子查询）、并在结果层做**列级**脱敏；全部通过后台 API / Web UI 集中配置。                                                                            |
| 3. 提供 DataAPI 与 MCP 服务供 AI Agent 使用 | `POST /api/v1/query` 等 REST 接口；以及 `POST /mcp` 的 MCP（Model Context Protocol）JSON-RPC 端点，暴露 `list_datasources` / `list_tables` / `describe_table` / `query` 工具。 |

### 为什么 AI 场景必须有这一层

AI 应用（ChatBI / Agent 工作流 / RAG / Copilot）的 SQL 由 LLM 现场生成——不可评审、不可穷举、可被提示词注入操纵，「应用层代码内控权限」在 AI 场景下失效。Aegis 把治理下沉到数据访问层：凭据隔离、三级权限、解析级 SQL 强制校验、全量审计、统一供给，五大风险逐一兜底。完整论证与面向 AI 的功能扩充规划（MCP resources / NL2SQL 网关 / 行数上限 / 限流 / 动态脱敏等）见 **[BLUEPRINT.md](BLUEPRINT.md)**（项目 PRD 蓝图 v0.6）。

---

> **30 秒上手**：`docker compose up -d` 启动后，把 Aegis 注册为 Claude Desktop 的 MCP 服务器（配置见 [`examples/mcp/claude_desktop_config.json`](examples/mcp/claude_desktop_config.json)），Agent 立刻获得受治理的 `query` / `estimate_query` / `nl2sql` 工具——全程无需任何数据库凭据。更多接入示例见 [`examples/`](examples/)。

**推荐仓库 Topics**：`ai-agent` · `mcp` · `data-governance` · `llm` · `text-to-sql` · `data-security` · `sql-proxy` · `rag`

## 快速开始

```bash
# 1. 构建
go build -o aegis ./cmd/aegis

# 2. 运行（首次启动会自动写入演示租户：用户/角色/数据源/权限）
./aegis -config config.json
```

启动后访问：

- 后台管理 UI： <http://localhost:8080/admin/>
- DataAPI 基址： `http://localhost:8080/api/v1`
- MCP 端点： `http://localhost:8080/mcp`

演示账号：

| 用户名         | 密码           | 角色      | 说明                          |
| ----------- | ------------ | ------- | --------------------------- |
| `admin`     | `admin123`   | admin   | 超级用户，绕过行级治理，用于管理            |
| `analyst`   | `analyst123` | analyst | 受表/行/列权限约束，属性 `tenant=acme` |
| `mcp-agent` | `mcp123`     | analyst | 供 MCP 静态 API Key 使用的服务账号    |

---

## DataAPI 示例

```bash
# 登录拿到 token
TOKEN=$(curl -s -X POST localhost:8080/api/v1/login \
  -d '{"username":"analyst","password":"analyst123"}' | jq -r .token)

# 列出可访问的表
curl -s localhost:8080/api/v1/datasources -H "Authorization: Bearer $TOKEN"

# 执行受治理的查询（行级策略自动注入 tenant_id = 'acme'）
curl -s localhost:8080/api/v1/query -H "Authorization: Bearer $TOKEN" \
  -d '{"datasource":"demo","sql":"SELECT * FROM orders"}'
```

返回中 `rewritten_sql` 即为实际下发到后端、已注入行级策略的 SQL，例如：

```sql
SELECT * FROM (SELECT * FROM orders WHERE tenant_id = 'acme') AS orders
```

`analyst` 只能看到 `tenant_id='acme'` 的订单，而 `admin` 能看到全部。

---

## MCP 服务（AI Agent 接入）

端点：`POST /mcp`（Streamable HTTP 传输）。Agent 通过两种方式鉴权：

- **Bearer JWT**：使用 `/api/v1/login` 获取的同一 Token；
- **静态 API Key**：请求头 `X-MCP-API-Key: mcp-demo-key`（映射到 `mcp-agent` 服务账号，继承 analyst 治理）。

### 初始化

```bash
curl -s -X POST localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "X-MCP-API-Key: mcp-demo-key" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
```

### 列出工具

```bash
curl -s -X POST localhost:8080/mcp -H "Content-Type: application/json" \
  -H "X-MCP-API-Key: mcp-demo-key" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
```

### 通过 Agent 查询（同样受行级治理约束）

```bash
curl -s -X POST localhost:8080/mcp -H "Content-Type: application/json" \
  -H "X-MCP-API-Key: mcp-demo-key" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call",
       "params":{"name":"query",
                 "arguments":{"datasource":"demo","sql":"SELECT * FROM orders"}}}'
```

可用工具：

- `list_datasources` —— 列出已注册数据源
- `list_tables` —— 列出当前调用者可访问的表（受表权限约束）
- `describe_table` —— 描述表结构（已剔除拒绝列/非授权列）
- `get_catalog` —— 返回**治理后的语义 Schema**（可访问表/列 + 业务含义 + 同义词 + 示例值），供 Agent 生成 SQL 前理解字段语义
- `query` —— 执行受治理的 SQL 查询

### Resources：语义 Schema 卡片（提升 NL2SQL 准确率）

MCP 声明 `resources` 与 `prompts` 能力。每个可访问数据源暴露一个 `aegis://<数据源名>/schema` 资源，读取后返回**按调用者治理过滤**的语义 Schema（markdown + JSON 双格式）：表/列的业务含义、同义词、示例值，且拒绝列不会出现。

```bash
# 列出资源
curl -s -X POST localhost:8080/mcp -H "Content-Type: application/json" \
  -H "X-MCP-API-Key: mcp-demo-key" \
  -d '{"jsonrpc":"2.0","id":4,"method":"resources/list"}'

# 读取语义 Schema 卡片
curl -s -X POST localhost:8080/mcp -H "Content-Type: application/json" \
  -H "X-MCP-API-Key: mcp-demo-key" \
  -d '{"jsonrpc":"2.0","id":5,"method":"resources/read",
       "params":{"uri":"aegis://demo/schema"}}'
```

### Prompts：内置 NL2SQL 模板

`prompts/get` 提供 `nl2sql` 模板：给定 `datasource` 与自然语言 `question`，Aegis 自动把治理后的语义 Schema 注入 system 提示、附带安全规则（仅用现有列、行级过滤由平台自动完成、禁止写操作），返回可直接喂给 LLM 的 messages。

```bash
curl -s -X POST localhost:8080/mcp -H "Content-Type: application/json" \
  -H "X-MCP-API-Key: mcp-demo-key" \
  -d '{"jsonrpc":"2.0","id":6,"method":"prompts/get",
       "params":{"name":"nl2sql",
                 "arguments":{"datasource":"demo","question":"每个状态的订单总金额是多少？"}}}'
```

> 语义描述通过后台 API 维护：`GET/POST /admin/api/datasources/{id}/semantics`、`DELETE .../semantics/{sem}`（仅 admin）。演示数据源已内置中文业务语义。

> 在 Claude Desktop / 任意 MCP 客户端中，将 transport 设为 `streamable-http`，URL 设为 `http://localhost:8080/mcp`，并配置 `Authorization: Bearer <token>` 或 `X-MCP-API-Key` 头即可。

---

## NL2SQL 安全网关（自然语言 → 受治理 SQL）

把「用大白话问数据」变成受治理的查询接口。**关键设计**：LLM 生成的 SQL 不会直接执行，而是被原样送回 `Proxy.Execute` —— 与手写 SQL 走完全相同的表/行/列治理、脱敏、行为限流与审计链路。NL2SQL 只放宽「谁能问」，绝不放宽「能看到什么」。

- LLM 端点兼容 OpenAI `/chat/completions`（OpenAI / Volcengine Ark·doubao / Azure OpenAI / 本地 vLLM 均可）。
- 生成阶段强制只读：模型只产出 `SELECT` / `WITH`，`INSERT/UPDATE/DELETE/DDL` 等会被拒绝；且只能引用治理后 Schema 中**已授权**的表与列。
- 支持 `sql_hint`：可直接传入已知 SQL 让平台代为执行（同样受治理）。

### 配置（config.json 或环境变量）

```jsonc
"nl2sql": {
  "enabled": true,
  "provider": "openai",
  "base_url": "https://api.openai.com/v1",   // 或 https://ark.cn-beijing.volces.com/api/v3
  "api_key": "sk-...",                        // 生产环境从密钥管理注入
  "model": "gpt-4o-mini",
  "timeout_sec": 30,
  "max_retries": 2
}
```

环境变量：`AEGIS_NL2SQL_ENABLED` / `AEGIS_NL2SQL_BASE_URL` / `AEGIS_NL2SQL_API_KEY` / `AEGIS_NL2SQL_MODEL` / `AEGIS_NL2SQL_TIMEOUT_SEC` / `AEGIS_NL2SQL_MAX_RETRIES`。未配置时接口返回明确的「未启用」错误。

### DataAPI 调用

```bash
curl -s -X POST localhost:8080/api/v1/datasources/demo/nl2sql \
  -H "Authorization: Bearer <token>" -H "Content-Type: application/json" \
  -d '{"question":"查看上海客户的姓名和手机号"}'
# 返回 { generated_sql, explanation, query_result(已脱敏), session_id }
```

### MCP 工具 `nl2sql`

```bash
curl -s -X POST localhost:8080/mcp -H "Authorization: Bearer <token>" \
  -d '{"jsonrpc":"2.0","id":7,"method":"tools/call",
       "params":{"name":"nl2sql",
                 "arguments":{"datasource":"demo","question":"查看上海客户的姓名和手机号"}}}'
```

### 后台查看模型可见 Schema

`GET /api/v1/datasources/{id}/catalog` 返回治理后的语义 Schema（即喂给模型的上下文，脱敏列已标注），便于排查 NL2SQL 准确率。

---

## 语义指标层（Curated Metrics）

把常用的业务指标（如「各地区客户数」「月度 GMV」）预定义为**受治理的模板**，让 Agent 用 `run_metric("monthly_revenue")` 而非每次手写 SQL。指标只放宽「怎么问」，治理从不绕过。

- 指标由管理员在后台或 `POST /admin/api/datasources/{id}/metrics` 定义：`sql_template` 中用 `:param` 占位，并声明每个参数的类型（`string`/`number`/`date`/`bool`/`enum`）、是否必填、枚举值。
- 运行阶段：调用方传入的参数被**类型校验 + 枚举白名单 + 单引号转义**后，安全地渲染进 SQL 模板（注入-proof），再原样送回 `Proxy.Execute` —— 表/行/列治理、脱敏、行为限流、审计全部生效。
- **血缘（Lineage）**：每次运行都返回该指标涉及哪些表、哪些敏感列、最高敏感度（public/internal/confidential/restricted/pii）和是否含 PII，帮助 Agent 对敏感结果谨慎处理、不回显原始 PII。

### DataAPI 调用

```bash
# 列出某数据源的可用指标
curl -s localhost:8080/api/v1/datasources/demo/metrics -H "Authorization: Bearer <token>"

# 运行指标（参数通过 params 传入；返回治理后的结果与血缘）
curl -s -X POST localhost:8080/api/v1/datasources/demo/metrics/customer_count/run \
  -H "Authorization: Bearer <token>" -H "Content-Type: application/json" \
  -d '{"params":{}}'
# 返回 { sql, lineage:{tables,columns,max_sensitivity,has_pii}, query_result(已脱敏), session_id }
```

### MCP 工具 `list_metrics` / `run_metric`

```bash
curl -s -X POST localhost:8080/mcp -H "Authorization: Bearer <token>" \
  -d '{"jsonrpc":"2.0","id":8,"method":"tools/call",
       "params":{"name":"run_metric",
                 "arguments":{"datasource":"demo","metric":"customer_count","params":{}}}}'
```

---

## 查询血缘成本（Query Lineage & Cost）

在 Agent **真正执行**一条 SQL 之前，先用 `EXPLAIN` 给出**成本/风险预览**——扫描行数估算、涉及表与敏感列、最高敏感度，以及 `low`/`medium`/`high` 风险等级与可读告警。仍复用同一条治理 `Rewrite` 链路（行/列策略、脱敏已反映到被 `EXPLAIN` 的 SQL 上），但**只读、零变更**：读操作 `EXPLAIN` 治理后的 SELECT；写操作复用权限引擎已生成的 `SELECT COUNT(*)` 预检来估影响行数。

- **方言无关解析**：MySQL/StarRocks/ClickHouse 取数值列 `rows`；PostgreSQL / SQLite 正则匹配 `rows=N`；SQLite 在 `EXPLAIN QUERY PLAN` 不吐行数时**回退到只读 `COUNT(*)`** 给出精确行数（演示/开发默认 sqlite，故该功能在开发态也开箱可用）。
- **风险合成**：结合数据敏感度（public→pii）与扫描行数（≥100 万判 `high`、≥5 万判 `medium`）给出 `risk_level`，并附带可读告警（如「扫描约 1,200,000 行，建议加 WHERE 过滤」「结果含 PII 列，输出前请确认已脱敏」）。
- **治理拒绝即最有用的估计**：若 SQL 触及未授权表，`Estimate` 直接返回拒绝错误，让 Agent 在发出真实查询前就学到会被拦。

### DataAPI 调用

```bash
# 运行前预估一条 SQL 的成本/风险（不执行）
curl -s -X POST localhost:8080/api/v1/datasources/demo/query/estimate \
  -H "Authorization: Bearer <token>" -H "Content-Type: application/json" \
  -d '{"sql":"SELECT * FROM customers"}'
# 返回 { governed_sql, read_only, estimated_rows, tables, columns,
#         max_sensitivity, has_pii, risk_level, warnings }
```

### MCP 工具 `estimate_query`

```bash
curl -s -X POST localhost:8080/mcp -H "Authorization: Bearer <token>" \
  -d '{"jsonrpc":"2.0","id":9,"method":"tools/call",
       "params":{"name":"estimate_query",
                 "arguments":{"datasource":"demo","sql":"SELECT * FROM customers"}}}'
```

Web 控制台「数据查询」tab 也提供「评估成本」按钮，实时展示风险等级与告警。

---

## 数据集管理（Data Products）

在「数据库连接（数据源）」之上，Aegis 提供**数据集（Dataset）**这一受治理的「数据产品」层：把一条查询固化成稳定的、可发布的、带契约的数据集，供 AI Agent 按名称消费，而无需接触底层物理表。

- **数据集是虚拟表**：其治理（表/行/列权限、动态脱敏、语义）复用同一套权限引擎，治理行以 `table_name = 数据集名` 写入现有权限表，无需新建权限模型。
- **生命周期**：`draft → published`；仅 `published` 且已授权的数据集对 Agent 可见、可查。
- **只读数据产品**：数据集是经策展的视图，Agent 消费受治理的结果（行策略自动注入、列按契约裁剪、PII 按角色脱敏），写操作仍走底层数据源表。

### 后台 API（仅 admin）

```
GET    /admin/api/datasets                      列出数据集
POST   /admin/api/datasets                      创建（name, datasource_id, definition, ...）
GET/PUT/DELETE /admin/api/datasets/{id}        查看 / 更新 / 删除（级联清理治理）
POST   /admin/api/datasets/{id}/publish         发布
POST   /admin/api/datasets/{id}/unpublish       下架
# 数据集级治理（table_name 自动设为数据集名）
GET/POST /admin/api/datasets/{id}/permissions   角色 × 数据集 表权限
GET/POST /admin/api/datasets/{id}/policies      行级策略
GET/POST/DELETE /admin/api/datasets/{id}/masks   列动态脱敏
GET/POST/DELETE /admin/api/datasets/{id}/semantics 业务语义
```

### Agent 消费

```
GET  /api/v1/datasets                 列出当前主体可消费的数据集
GET  /api/v1/datasets/{id}            获取数据集治理后的字段契约（catalog）
POST /api/v1/datasets/{id}/query      执行受治理的数据集查询
```

MCP 侧同步暴露：`list_datasets` / `get_dataset_catalog` 工具，以及 `aegis://dataset/<名称>/schema` 资源。

示例：演示租户已内置数据集 `paid_orders`（已支付订单，按租户隔离），`analyst` 仅能看到 `tenant_id='acme'` 的已支付订单；`admin` 绕过治理可见全部。

```bash
# 列出可消费的数据集
curl -s localhost:8080/api/v1/datasets -H "Authorization: Bearer $TOKEN"

# 查询数据集（行策略 + 列脱敏由平台自动施加）
curl -s localhost:8080/api/v1/datasets/<dataset-id>/query -H "Authorization: Bearer $TOKEN"
```

---

## 权限模型

### 表级（Table Permission）

为「角色 × 数据源 × 表」授予操作集合：`SELECT / INSERT / UPDATE / DELETE`。  
**默认拒绝**：未在权限表中显式授权的表，任何查询都会被拒绝。

### 行级（Row Policy）

为「角色 × 数据源 × 表」配置一条 SQL 谓词，支持 `:attr` 占位符，由调用者属性（JWT 中的 `attrs`）代入。  
例如谓词 `tenant_id = :tenant`，当用户属性为 `{"tenant":"acme"}` 时，自动改写为  
`(SELECT * FROM orders WHERE tenant_id = 'acme')`。多个角色的策略按 `priority` 合并（AND）。

### 列级（Column Masking）

表权限中可配置 `allowed_cols`（白名单）与 `denied_cols`（黑名单，优先）——平台在返回前剔除无权列，即使原始 SQL 写了 `SELECT *`。

**动态脱敏（Dynamic Masking）**：除了整列隐藏，Aegis 还支持对列值做**变换掩码**——列仍返回，但敏感值被脱敏，让 AI 既能拿到有用数据又不暴露 PII。规则按「角色 × 数据源 × 表 × 列」配置，内置 6 种策略：

| 策略 | 效果 | 示例 |
| --- | --- | --- |
| `phone` | 保留前 3 + 后 4 位，中间 `*` | `13812345678` → `138****5678` |
| `email` | 保留首字符 + 完整域名 | `ops@acme.com` → `o***@acme.com` |
| `card` | 仅保留后 4 位 | `4111111111111111` → `************1111` |
| `partial` | 保留首尾各 N 位（`keep`，默认 2） | `Acme Corp` → `Ac*****rp` |
| `hash` | SHA-256 截断 16 位（不可逆） | `secret` → `2bb80d537b1da3e3` |
| `redact` | 常量 `***` | 任意值 → `***` |
| `tokenize` | **确定性 HMAC 假名**（密钥相关，可跨表关联/聚合） | `alice@example.com` → `aZ3k...`（24 位不透明令牌，相同输入稳定同值） |
| `fpe` | **格式保留加密**（仅纯数字 PII，保长保型，密钥可还原） | `4111111111111111` → `7f2c...`（仍是 16 位数字，非数字回退 `tokenize`） |

掩码在结果层对单元格执行，`admin` 角色与无掩码规则的角色不受影响；MCP `get_catalog` 的语义卡片会标注被掩码列（`masked: <策略>`），让 Agent 知道自己拿到的字段是脱敏值。

> **密钥策略（`tokenize` / `fpe`）**：这两种策略是**确定性、密钥相关**的——相同的输入在相同的 `AEGIS_MASK_SECRET` 下总是得到相同输出，因此脱敏后的值仍能做 `JOIN` / 去重 / 聚合，而原始 PII 不会离开平台。`fpe` 还可用 `proxy.FpeDecrypt`（或未来管理端「再识别」工具）在持有密钥时还原。生产环境**务必通过 `AEGIS_MASK_SECRET` 注入密钥**（建议来自 KMS / Vault），未配置时回退到不安全的开发默认值并在启动日志告警；密钥一旦轮换，已落库的脱敏值需按新密钥重算。

<!-- 管理 API（仅管理员）：
GET  /admin/api/datasources/{id}/masks
POST /admin/api/datasources/{id}/masks   {role, table, column, strategy, keep?}
DELETE /admin/api/datasources/{id}/masks/{mask}
POST /admin/api/datasources/{id}/masks/recommend   {role?, table?, apply_to_all_roles?, apply?}  # 按分类推荐脱敏，apply=true 落地
-->

### 数据分级分类（Data Classification）

分级分类是贴在「表 / 列」上的**数据敏感度标签**，与按角色下发的脱敏规则相互独立：它描述的是数据资产本身有多敏感，主要作用是让语义目录与 AI Agent 知道哪些列需要谨慎处理。

- **分级（Level）**：`public` / `internal` / `confidential` / `restricted` / `pii`
- **标签（Tags）**：自由标签，如 `["pii:phone","contact"]`、`["financial","money"]`
- **粒度**：`column_name` 为空表示整表级别标签；列级标签对表级做细化。

标签随语义目录自动透出：

- MCP `get_catalog` / `resources/read` 的语义卡片会标注 `[class: <level>] [tags: ...]`；
- `nl2sql` 提示词据此注入敏感列处理规则（优先聚合而非逐条列举个人记录、不回显原始 PII 值、保留平台已施加的掩码）。

### 脱敏策略自动推荐（Auto-Recommend）

配置逐列脱敏很繁琐。**自动推荐**把「分类一次」变成「掩码自动就位」：基于列的分类（`level` + `tags` + 列名）推导默认脱敏策略，无需手工逐列指定。

推荐优先级（精确标签 > 级别 + 列名启发 > 级别兜底）：

| 输入 | 推荐策略 |
| --- | --- |
| 标签 `pii:phone` / `pii:mobile` | `phone` |
| 标签 `pii:email` | `email` |
| 标签 `pii:card` / `pii:bank` / `pii:account` | `card` |
| 标签 `pii:idcard` / `pii:ssn` | `fpe` |
| 标签 `pii:name` | `partial`（`keep=1`） |
| 级别 `pii`/`restricted` + 列名含 phone/email/card/idcard/name | 对应策略（证件号 → `fpe`） |
| 级别 `pii`/`restricted` 且无更精确特征 | `tokenize`（确定性假名，可关联） |
| 级别 `confidential` 或标签含 `financial`/`money` | `partial`（`keep=2`，保留量级） |
| 级别 `public`/`internal` | 无需脱敏 |

管理端点（仅管理员）：

```http
# 预览：返回每个分类列的推荐策略，不落库
POST /admin/api/datasources/{id}/masks/recommend
{}

# 落地：为指定角色应用推荐脱敏（也可 apply_to_all_roles 应用到全部非 admin 角色）
POST /admin/api/datasources/{id}/masks/recommend
{"apply": true, "role": "analyst"}
```

`admin` 角色绕过掩码，因此落地默认排除 `admin`。全新安装会自动为 `analyst` 角色套用演示数据的推荐脱敏，列治理开箱可见。

> 分级分类通过后台 API 维护（仅 admin）：`GET/POST /admin/api/datasources/{id}/classifications`、`DELETE .../classifications/{cls}`。演示数据源已内置示例：`customers.name/phone/email = pii`、`orders.amount = confidential`。

权限在「角色」维度定义，用户通过「用户-角色」关联获得权限；`admin` 角色为内置超级用户。

### 审计日志（Audit Log）

每一条经过代理的查询——成功、被拒绝或执行失败——都会写入审计流水，记录：  
调用者、渠道（`dataapi` / `mcp`）、数据源、原始 SQL、治理重写后的 SQL、状态（`ok` / `denied` / `error`）、返回行数与耗时。

```bash
# 查询审计流水（管理员，支持 user / datasource / status / channel / limit / offset 过滤）
curl -s "http://localhost:8080/admin/api/audit?status=denied&limit=20" \
  -H "Authorization: Bearer $ADMIN_TOKEN"

# 聚合计数（total / ok / denied / error）
curl -s http://localhost:8080/admin/api/audit/stats -H "Authorization: Bearer $ADMIN_TOKEN"
```

后台管理 UI 的「审计日志」标签页提供同等能力（统计卡片 + 过滤 + 分页）。

### 安全告警（异常行为检测）

Aegis 在审计汇聚点（每一次受治理查询）之外，额外运行一个轻量**异常检测引擎**，对可疑的 Agent / 用户行为自动生成安全告警，把「治理执行闭环」补上「发现」这一环。当前内置三条规则：

- **高频拒绝（repeated_denied）**：同一主体在滑动窗口内被拒绝 N 次（默认 60s 内 10 次），疑似探测 / 越权 / 配置错误。
- **批量导出（bulk_export）**：单次查询返回行数超过阈值（默认 5000 行），疑似拖库。
- **非工作时段（off_hours）**：在配置的非工作时段（默认 `00:00–06:00`）发生访问，排除 `admin`（运维访问属正常）。

命中后写入控制面 `security_alerts` 表，并可在冷却期（默认 5 分钟）内对同一「主体 + 规则」去重，避免刷屏；若配置了 `webhook`，还会把告警 JSON POST 到外部系统（Slack / 飞书 / SIEM 等）。

> 阈值与开关在 `config.json` 的 `alerting` 段，或环境变量 `AEGIS_ALERT_*` 覆盖：`AEGIS_ALERT_DENIED_COUNT` / `AEGIS_ALERT_DENIED_WINDOW_SEC` / `AEGIS_ALERT_BULK_ROWS` / `AEGIS_ALERT_OFFHOURS` / `AEGIS_ALERT_OFFHOURS_START` / `AEGIS_ALERT_OFFHOURS_END` / `AEGIS_ALERT_COOLDOWN_SEC` / `AEGIS_ALERT_WEBHOOK`。

告警在管理后台「安全告警」标签页查看（统计卡片 + 级别 / 状态过滤 + 标记已处理），或经 API：

```bash
# 列出告警（支持 level / resolved=open|resolved 过滤）
curl -s "http://localhost:8080/admin/api/alerts" -H "Authorization: Bearer $ADMIN_TOKEN"
# 聚合计数（total / warning / critical / open / resolved）
curl -s "http://localhost:8080/admin/api/alerts/stats" -H "Authorization: Bearer $ADMIN_TOKEN"
# 标记已处理
curl -X POST "http://localhost:8080/admin/api/alerts/<id>/resolve" -H "Authorization: Bearer $ADMIN_TOKEN"
```

### AI 行为治理（Limits）

针对 AI Agent「概率性调用方」的行为约束，在权限治理之外再加一层运行时防线（`config.json` 的 `limits` 段，亦可用环境变量覆盖）：

| 参数                   | 默认   | 作用                                                                 |
| -------------------- | ---- | ------------------------------------------------------------------ |
| `max_rows`           | 1000 | 单次查询返回行数上限，超出即截断并在响应里带 `truncated: true`（防 Agent 拖库）             |
| `query_timeout`      | 30s  | 单查询执行超时，超时熔断取消后端查询（防失控 SQL 占死连接）                                  |
| `rate_per_min`       | 120  | 按主体（用户/Agent）滑动窗口每分钟限流，超额直接拒绝并审计留痕                                  |
| `max_affected_rows`  | 0（关闭） | 单条 UPDATE/DELETE 影响行数上限；执行前先 `SELECT COUNT(*)` 预检，超阈值直接拒绝（防批量误改/误删） |
| `allow_no_where_writes` | false | 为 `false` 时，无 WHERE 且无行策略兜底的 UPDATE/DELETE 直接拒绝（硬安全网，可显式开启放宽）     |
| `admin_exempt`       | true | `admin` 角色豁免以上限制                                                       |

环境变量覆盖：`AEGIS_MAX_ROWS` / `AEGIS_QUERY_TIMEOUT` / `AEGIS_RATE_PER_MIN` / `AEGIS_MAX_AFFECTED_ROWS`（如 `1`）/ `AEGIS_ALLOW_NO_WHERE_WRITES`（如 `true`）。

限流拒绝记为审计 `denied`，超时记为 `error`，截断在 `ok` 记录中备注 `result truncated at max_rows=N`——行为全部可追溯。无 WHERE 拦截与影响行数超限同样记为审计 `denied`。

---

## 项目结构

```
cmd/aegis/main.go          # 入口
internal/
  config/                    # 配置加载
  store/                     # 控制面存储(SQLite) + 权限聚合
  auth/                      # JWT / bcrypt / OIDC / 身份声明
  datasource/                # 数据源连接池(MySQL/PostgreSQL/SQLite)
  permission/                # 权限引擎：SQL 解析 + 表/行/列治理重写
  proxy/                     # 代理执行层：经权限引擎执行并脱敏
  api/                       # DataAPI(REST) + 后台管理 API + OIDC 回调
  mcp/                       # MCP JSON-RPC 服务
  server/                    # 路由装配 + 演示租户播种 + 内嵌 Web UI
internal/server/web/         # 后台管理前端(原生 HTML/JS)
```

## 支持的数据源

- **MySQL**（`type: mysql`，DSN `user:pass@tcp(host:3306)/db?parseTime=true&charset=utf8mb4`）
- **PostgreSQL**（`type: postgres`，DSN `postgres://user:pass@host:5432/db?sslmode=disable`）
- **SQLite**（`type: sqlite`，DSN 为文件路径）—— 同时用于平台控制面与演示数据源

### 接入真实 MySQL 示例（已验证）

最佳实践：**不要用 root**，为平台创建专用受限账号，体现「隔离数据库自身账号」：

```sql
CREATE USER 'aegis_svc'@'%' IDENTIFIED BY '<strong-password>';
GRANT SELECT, INSERT, UPDATE, DELETE ON your_db.* TO 'aegis_svc'@'%';
```

通过管理 API 在线注册（无需重启）：

```bash
curl -X POST localhost:8080/admin/api/datasources -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"name":"mysql-local","type":"mysql",
       "dsn":"aegis_svc:<password>@tcp(127.0.0.1:3306)/your_db?parseTime=true&charset=utf8mb4"}'
```

之后按「角色 × 数据源 × 表」配置表权限 / 行策略 / 列脱敏即可，治理语义与 SQLite/PostgreSQL 一致（行策略注入的派生表会自动补别名，兼容 MySQL 的 `Every derived table must have its own alias` 约束）。

> 说明：权限引擎基于 MySQL/SQLite 语法解析，对标准 PostgreSQL 查询同样适用；PostgreSQL 专有语法（如 `RETURNING`）在行级重写场景下可能受限。

### NoSQL 数据源（MongoDB / Elasticsearch / Trino）

Aegis 同样能代理文档/搜索类与联邦查询引擎，治理语义与 SQL 完全一致（表权限、行策略、列 allow/deny、值脱敏、写入护栏）：

- **MongoDB**（`type: mongo`，DSN `mongodb://user:pass@host:27017/db`）—— 集合即「表」，文档字段即「列」。
- **Elasticsearch**（`type: es`，DSN `http://host:9200`，可选的 `user:pass@`）—— 索引即「表」，字段即「列」。
- **Trino / Presto**（`type: trino` / `presto`，DSN `http://host:8080?catalog=x&schema=y`）—— 走标准 ANSI-SQL，复用 `permission.Rewrite` 治理管线（无需额外依赖，纯 `/v1/statement` REST 轮询）。

**查询格式（DataAPI `POST /api/v1/query` 的 `query` 字段为后端原生 JSON）**

Mongo 读（find）：

```json
{"collection":"orders","filter":{"status":"open"},"projection":{"status":1,"amount":1},"sort":{"amount":-1},"limit":10}
```

Mongo 写（insert / update / delete，按 `op` 区分）：

```json
{"op":"insert","collection":"orders","document":{"status":"open","amount":99,"customer":"acme"}}
{"op":"update","collection":"orders","filter":{"_id":"..."},"update":{"$set":{"amount":10}}}
{"op":"delete","collection":"orders","filter":{"_id":"..."}}
```

Elasticsearch 读（search）/ 写（index / updateByQuery / deleteByQuery）同理，字段为 `index` / `query` / `_source` / `document` / `script` / `op`。

**治理如何作用于 NoSQL**

- **表权限**：集合/索引名即「表」，未授权的集合直接拒绝；`INSERT/UPDATE/DELETE` 需对应授权。
- **行策略**：作为 Mongo 的 `$and` 过滤、ES 的 `bool.must` 查询自动注入（与 SQL 派生表等价）。
- **列权限**：Mongo `projection`、ES `_source` 的 `includes/excludes` 在查询层执行（所以连接的是真实后端）；**写入**时若触碰 `denied` 列直接拒绝，非授权列自动剔除。
- **值脱敏**：结果层与 SQL 走同一套 `applyMask`，列保留但 PII 被变换。
- **写入护栏**：无 `WHERE/过滤` 的 update/delete 默认拒绝（由 `allow_no_where_writes` 放宽）；当 `max_affected_rows>0` 时，写前会先 `Count` 预检影响行数，超额拒绝。

> 管理端注册 NoSQL 数据源同样在线生效；`type` 必须是 `mongo`/`es`/`trino`/`presto` 之一，非法类型会被拒绝。

## 已知边界（MVP）

- 行级策略目前作用于顶层 `FROM/JOIN` 表；嵌套子查询内部的表暂未递归注入（多表 JOIN 顶层表已覆盖常见场景）。
- NoSQL（Mongo/ES）写入已支持 `insert/update/delete`，治理与 SQL 对齐；**数据集（Dataset）对 NoSQL 目前为只读视图**（写操作仍走底层集合）。
- ES 写入的字段级治理仅覆盖顶层字段（嵌套对象字段暂不在列权限范围内）。
- 列级治理在结果层按列名执行（单表查询精确；多表同名列取并集白名单）。
- `INSERT` 行级策略未注入（仅校验表级 INSERT 权限）。

这些边界在生产化时可基于具体数据库的原生 Row-Level Security (RLS) 进一步加固。

---

## SSO / OIDC 身份对接

Aegis 支持通过 **OpenID Connect** 接入外部身份提供商（IdP），实现单点登录与企业身份统一管理。启用后，平台不再自行管理密码，而是把认证委托给 OIDC IdP（如 Google Workspace、Azure AD、Okta、Keycloak、Authing 等）。

### 配置

在 `config.json` 的 `oidc` 段填写 IdP 信息（全部支持环境变量覆盖）：

```json
{
  "oidc": {
    "enabled": true,
    "issuer": "https://accounts.google.com",
    "client_id": "<your-client-id>",
    "client_secret": "<your-client-secret>",
    "redirect_url": "http://localhost:8080/api/v1/auth/oidc/callback",
    "scopes": ["profile", "email"],
    "claim_mappings": {
      "admins": "admin",
      "analysts": "analyst"
    }
  }
}
```

| 配置项 | 环境变量 | 说明 |
| --- | --- | --- |
| `enabled` | `AEGIS_OIDC_ENABLED` | 是否启用 OIDC |
| `issuer` | `AEGIS_OIDC_ISSUER` | IdP issuer URL，自动发现 `.well-known/openid-configuration` |
| `client_id` | `AEGIS_OIDC_CLIENT_ID` | OAuth2 client id |
| `client_secret` | `AEGIS_OIDC_CLIENT_SECRET` | OAuth2 client secret |
| `redirect_url` | `AEGIS_OIDC_REDIRECT_URL` | 回调地址，必须在 IdP 中注册 |
| `scopes` | — | 额外 scope，默认含 `openid` + `profile` + `email` |
| `claim_mappings` | — | 将 IdP claim 值映射到平台角色，如 `{"admins":"admin"}` |

### 登录流程

```bash
# 1. 浏览器访问登录入口，自动重定向到 IdP
open http://localhost:8080/api/v1/auth/oidc/login

# 2. 用户在 IdP 完成认证后，IdP 重定向回 callback
#    平台验证 ID Token，自动创建或关联用户，返回平台 JWT
```

**用户生命周期**：
- **首次登录（auto-provisioning）**：平台根据 `sub` 自动创建用户，用户名取 `email`，display_name 取 `name`；`claim_mappings` 自动分配角色（若角色不存在则自动创建）。
- **再次登录**：根据 `sub` 关联已有用户，同步 display_name。
- **密码字段为空**：OIDC 用户无本地密码，只能通过 IdP 登录。

### 安全细节

- **PKCE**：授权码流程强制使用 PKCE（S256），防 authorization code 拦截攻击。
- **Nonce**：强制验证 ID Token 的 `nonce`，防重放。
- **State**：10 分钟有效期的 cookie 携带 state，回调时严格校验，防 CSRF。
- **Cookie**：`HttpOnly` + `SameSite=Lax`，生产环境建议开启 HTTPS 并将 `Secure` 设为 `true`。

---

## 目录（LDAP / Active Directory）身份对接

Aegis 支持通过 **LDAP / Active Directory** 做基于密码的目录单点登录。启用后，平台用户的认证由企业目录（如 OpenLDAP、FreeIPA、Microsoft AD）完成，登录成功后平台按目录中的**组（group）成员关系**自动映射平台角色并签发 JWT。适合尚未部署 OIDC、或以 AD 为主身份源的企业。

### 配置

在 `config.json` 的 `ldap` 段填写目录信息（全部支持环境变量覆盖，前缀 `AEGIS_LDAP_*`）：

```json
{
  "ldap": {
    "enabled": true,
    "url": "ldap://dc1.example.com:389",
    "bind_dn": "cn=aegis-svc,ou=service,dc=example,dc=com",
    "bind_password": "<service-account-password>",
    "base_dn": "dc=example,dc=com",
    "user_filter": "(uid=%s)",
    "user_attr": "uid",
    "display_attr": "displayName",
    "email_attr": "mail",
    "group_base_dn": "ou=groups,dc=example,dc=com",
    "group_filter": "(member=%d)",
    "group_name_attr": "cn",
    "claim_mappings": {
      "aegis-admins": "admin",
      "aegis-analysts": "analyst"
    },
    "default_roles": ["analyst"],
    "skip_tls_verify": false
  }
}
```

| 配置项 | 环境变量 | 说明 |
| --- | --- | --- |
| `enabled` | `AEGIS_LDAP_ENABLED` | 是否启用 LDAP 登录 |
| `url` | `AEGIS_LDAP_URL` | 目录地址，如 `ldap://host:389` 或 `ldaps://host:636` |
| `bind_dn` / `bind_password` | `AEGIS_LDAP_BIND_DN` / `AEGIS_LDAP_BIND_PASSWORD` | 用于检索用户的服务账号（匿名绑定可留空） |
| `base_dn` | `AEGIS_LDAP_BASE_DN` | 用户检索基，如 `dc=example,dc=com` |
| `user_filter` | `AEGIS_LDAP_USER_FILTER` | 用户检索过滤器，`%s` 为登录名，如 `(uid=%s)` 或 `(sAMAccountName=%s)` |
| `user_attr` | `AEGIS_LDAP_USER_ATTR` | 用作平台用户名的属性（缺省回退为用户 DN） |
| `display_attr` | `AEGIS_LDAP_DISPLAY_ATTR` | 展示名属性，如 `displayName` |
| `email_attr` | `AEGIS_LDAP_EMAIL_ATTR` | 邮箱属性，如 `mail` |
| `group_base_dn` | `AEGIS_LDAP_GROUP_BASE_DN` | 组检索基 |
| `group_filter` | `AEGIS_LDAP_GROUP_FILTER` | 组过滤器，`%d` 为用户 DN，如 `(member=%d)` 或 `(memberOf=%d)` |
| `group_name_attr` | `AEGIS_LDAP_GROUP_NAME_ATTR` | 组名属性，如 `cn` |
| `claim_mappings` | — | 将目录组值映射到平台角色，如 `{"aegis-admins":"admin"}` |
| `default_roles` | — | 所有 LDAP 登录用户都授予的角色 |
| `skip_tls_verify` | `AEGIS_LDAP_SKIP_TLS_VERIFY` | 跳过 TLS 证书校验（仅开发环境） |

### 登录流程

```bash
# 应用 / Agent 用目录凭据直接换取平台 JWT
curl -X POST http://localhost:8080/api/v1/auth/ldap/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"<directory-password>"}'
# => { "token": "<aegis-jwt>", "user": { "username":"alice", "roles":["analyst"], ... } }
```

**认证过程**（三步绑定）：
1. （可选）用服务账号 `bind_dn` 绑定目录，以便检索用户；
2. 按 `user_filter` 检索用户条目、解析其 DN；
3. **以用户 DN + 输入密码绑定目录**——这是真正的凭证校验，失败即返回 401。

随后按 `claim_mappings` 把组映射为平台角色（缺失的角色会自动创建），自动创建或关联用户，签发 JWT。

**用户生命周期**：与 OIDC 一致——首次登录根据目录 DN 自动建号（external_id 为 `ldap:<DN>`），再次登录按 DN 关联、同步展示名；LDAP 用户无本地密码。

---

## 权限审批流（Access Approval Workflow）

Aegis 的授权按**角色**聚合，因此一次审批 = 为某角色申请对某数据源某表的访问。闭环：**申请 → 审批 → 生效 → 回收**。

### 端点

| 方法 & 路径 | 权限 | 说明 |
| --- | --- | --- |
| `POST /admin/api/approvals` | 登录用户 | 提交申请：`datasource_id`、`table_name`、`role`、`ops`、`justification`；`ops` 限定为 `SELECT,INSERT,UPDATE,DELETE` 子集 |
| `GET /admin/api/approvals` | 管理员 | 列出申请，支持 `status` / `datasource_id` 过滤 |
| `GET /api/v1/me/approvals` | 登录用户 | 查看本人提交与状态 |
| `POST /admin/api/approvals/{id}/approve` | 管理员 | 批准：自动创建角色级 `table_permissions` 授权，记录 `granted_perm_id` |
| `POST /admin/api/approvals/{id}/reject` | 管理员 | 拒绝：不创建授权 |
| `POST /admin/api/approvals/{id}/revoke` | 管理员 | 撤回已批准项：按 `granted_perm_id` 精确删除授权，闭环可逆 |

> 已批准项不可重复批准（返回 409）；仅 `pending` 可审批，`approved` 可撤回。

### 示例

```bash
# 1. 普通用户提交申请（为 analyst 角色申请 demo 数据源 orders 表的只读）
curl -X POST http://localhost:8080/admin/api/approvals \
  -H 'Authorization: Bearer <user-token>' -H 'Content-Type: application/json' \
  -d '{"datasource_id":"<ds-id>","table_name":"orders","role":"analyst","ops":"SELECT","justification":"报表需要"}'

# 2. 管理员批准 -> 自动落授权
curl -X POST http://localhost:8080/admin/api/approvals/<req-id>/approve \
  -H 'Authorization: Bearer <admin-token>'

# 3. 撤回 -> 精确删除该授权
curl -X POST http://localhost:8080/admin/api/approvals/<req-id>/revoke \
  -H 'Authorization: Bearer <admin-token>'
```

### 设计要点（ADR）

授权模型以**角色**为自然键，审批目标是角色而非个人——申请人仅作留痕，避免牵动用户↔角色成员图；回收缩小到「删除本审批创建的授权行」，保证治理闭环可审计、可还原。后台「审批流」页提供**申请视图**（提交 + 我的申请）与**审批台**（待审批 + 历史 + 通过/拒绝/撤回）双视图。

---

## 可观测性（Observability）

### 健康探针

Aegis 提供两个标准探针端点，便于 K8s / 负载均衡器做健康检查：

| 端点 | 用途 | 响应 |
| --- | --- | --- |
| `GET /api/v1/health` | **Liveness**（存活） | 始终返回 `{"status":"ok"}`（200），进程活着即通过 |
| `GET /api/v1/ready` | **Readiness**（就绪） | 检查控制面 store 是否可达；就绪返回 `{"status":"ready"}`（200），否则 503 |

K8s 探针配置示例：

```yaml
livenessProbe:
  httpGet:
    path: /api/v1/health
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 10
readinessProbe:
  httpGet:
    path: /api/v1/ready
    port: 8080
  initialDelaySeconds: 3
  periodSeconds: 5
```

### 结构化日志（Structured Logging）

Aegis 用标准库 `log/slog` 输出结构化日志（**零额外依赖**，保持单二进制），便于接入 Loki / ELK / 云日志服务做检索与告警。格式与级别在 `config.json` 的 `logging` 段配置：

```json
{
  "logging": {
    "format": "json",   // "json"（默认）或 "text"
    "level":  "info"     // "debug" | "info"（默认） | "warn" | "error"
  }
}
```

| 配置项 | 环境变量 | 说明 |
| --- | --- | --- |
| `format` | `AEGIS_LOG_FORMAT` | 输出格式：`json` 或 `text`（`text` 便于本地肉眼看） |
| `level` | `AEGIS_LOG_LEVEL` | 最低输出级别：低于该级别的记录被丢弃 |

### 脱敏密钥（`tokenize` / `fpe`）

| 配置项 | 环境变量 | 说明 |
| --- | --- | --- |
| `mask_secret` | `AEGIS_MASK_SECRET` | 密钥化脱敏策略（`tokenize` 确定性假名、`fpe` 格式保留加密）的服务器密钥；生产环境务必注入（建议来自 KMS / Vault），未配置回退不安全开发默认值并告警 |

日志覆盖三类高价值信号：

- **HTTP 访问日志**：每个请求一行，携带 `req_id`（从 `X-Request-Id` 头透传，缺失则自动生成 UUID）、`method` / `path` / `status` / `duration_ms` / `bytes` / `remote`；级别随状态码升降（5xx→`ERROR`、4xx→`WARN`、其余→`INFO`）。`/api/v1/health`、`/api/v1/ready`、`/metrics` 探针路径默认跳过，避免刷屏。
- **治理决策事件**：每次被拒绝（`denied`）或执行失败（`error`）的受治理查询，额外输出一条 `msg="governance decision"` 结构化事件，含 `user` / `channel` / `datasource` / `decision` / `reason` 以及（截断至 2000 字符的）原始与重写 SQL——安全团队无需扫审计表即可对「探测 / 越权」直接告警。
- **启动与运行时事件**：启动监听、演示租户播种失败、OIDC 初始化失败等均结构化输出。

JSON 示例（一行一事件）：

```json
{"time":"2026-07-26T23:00:00+08:00","level":"WARN","msg":"governance decision","req_id":"c1f0...","user":"analyst","channel":"dataapi","datasource":"mysql-local","decision":"denied","reason":"table 'secret' not authorized for role analyst","sql_original":"SELECT * FROM secret","sql_rewritten":""}
{"time":"2026-07-26T23:00:01+08:00","level":"INFO","msg":"http request","req_id":"c1f0...","method":"POST","path":"/api/v1/query","status":403,"duration_ms":2,"bytes":98,"remote":"127.0.0.1:51234"}
```

### Prometheus 指标

Aegis 在 `GET /metrics` 暴露 Prometheus 格式指标，可直接被 Prometheus / Grafana 抓取，用于监控网关的治理流量、延迟与数据供给量。

> 指标走独立的 Prometheus registry，仅暴露 Aegis 相关指标（含 Go runtime / 进程级指标），不含默认全局注册表的其他内容。

核心指标：

| 指标 | 类型 | 标签 | 说明 |
| --- | --- | --- | --- |
| `aegis_queries_total` | Counter | `channel`(dataapi/mcp), `status`(ok/denied/error) | 受治理查询总数（DataAPI、MCP、数据集查询均计入） |
| `aegis_query_duration_seconds` | Histogram | `channel`, `status` | 受治理查询延迟分布 |
| `aegis_rows_returned_total` | Counter | `channel`, `status` | 返回给调用方的数据行累计数 |
| `aegis_datasources_total` | Gauge | — | 已配置数据源数量 |
| `aegis_datasets_published_total` | Gauge | — | 已发布数据集数量 |
| `aegis_build_info` | Gauge | `version`, `commit` | 构建版本与提交号（值恒为 1） |

抓取配置示例（`prometheus.yml`）：

```yaml
scrape_configs:
  - job_name: aegis
    static_configs:
      - targets: ["localhost:8080"]
```

构建时通过 ldflags 注入版本与提交号，使 `aegis_build_info` 准确：

```bash
go build -ldflags "\
  -X github.com/wisonwang/aegis/internal/version.Version=1.2.3 \
  -X github.com/wisonwang/aegis/internal/version.Commit=$(git rev-parse --short HEAD)" \
  -o aegis ./cmd/aegis
```

---

## License

本项目以 [MIT License](./LICENSE) 开源。

你可以自由地用于学习、商用、修改与再分发，只需在副本中保留版权声明与许可声明即可；软件按「原样」提供，不附带任何担保。

> 若需将版权持有人（当前为 `wisonwang`）改为贵司或具体作者，直接替换 `LICENSE` 文件首行的 `Copyright (c) 2026 wisonwang` 即可。
