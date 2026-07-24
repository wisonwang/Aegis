# Aegis · 数据库代理治理平台

> 使用 Golang 构建的**数据库代理 / 数据服务治理平台**：集中管理数据库访问权限、封装表/行/列级数据权限，并以 **DataAPI** 与 **MCP 服务** 的形式统一对外提供数据能力，供业务系统与 AI Agent 使用。

> **命名（Rebrand）：** 项目已由 **DataHub** 更名为 **Aegis**（副标题 *AI Data Supply Gateway*），以规避与 LinkedIn / Acryl 同名数据目录产品（metadata catalog）的品牌与 SEO 混淆。代码级标识符已同步更名完成：模块路径 `github.com/fosun/aegis`、`aegis://` URI 方案、`aegis_svc` 受限账号、`AEGIS_*` 环境变量、`cmd/aegis` 入口目录。

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

AI 应用（ChatBI / Agent 工作流 / RAG / Copilot）的 SQL 由 LLM 现场生成——不可评审、不可穷举、可被提示词注入操纵，「应用层代码内控权限」在 AI 场景下失效。Aegis 把治理下沉到数据访问层：凭据隔离、三级权限、解析级 SQL 强制校验、全量审计、统一供给，五大风险逐一兜底。完整论证与面向 AI 的功能扩充规划（MCP resources / NL2SQL 网关 / 行数上限 / 限流 / 动态脱敏等）见 **[BLUEPRINT.html](BLUEPRINT.html)**（项目 PRD 蓝图 v0.2，含平台架构图）。

---

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

## 权限模型

### 表级（Table Permission）

为「角色 × 数据源 × 表」授予操作集合：`SELECT / INSERT / UPDATE / DELETE`。  
**默认拒绝**：未在权限表中显式授权的表，任何查询都会被拒绝。

### 行级（Row Policy）

为「角色 × 数据源 × 表」配置一条 SQL 谓词，支持 `:attr` 占位符，由调用者属性（JWT 中的 `attrs`）代入。  
例如谓词 `tenant_id = :tenant`，当用户属性为 `{"tenant":"acme"}` 时，自动改写为  
`(SELECT * FROM orders WHERE tenant_id = 'acme')`。多个角色的策略按 `priority` 合并（AND）。

### 列级（Column Masking）

表权限中可配置 `allowed_cols`（白名单）与 `denied_cols`（黑名单，优先）。  
对于 `SELECT` 结果，平台在返回前剔除无权列——即使原始 SQL 写了 `SELECT *`。

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
  auth/                      # JWT / bcrypt / 身份声明
  datasource/                # 数据源连接池(MySQL/PostgreSQL/SQLite)
  permission/                # 权限引擎：SQL 解析 + 表/行/列治理重写
  proxy/                     # 代理执行层：经权限引擎执行并脱敏
  api/                       # DataAPI(REST) + 后台管理 API
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

## 已知边界（MVP）

- 行级策略目前作用于顶层 `FROM/JOIN` 表；嵌套子查询内部的表暂未递归注入（多表 JOIN 顶层表已覆盖常见场景）。
- 列级治理在结果层按列名执行（单表查询精确；多表同名列取并集白名单）。
- `INSERT` 行级策略未注入（仅校验表级 INSERT 权限）。

这些边界在生产化时可基于具体数据库的原生 Row-Level Security (RLS) 进一步加固。
