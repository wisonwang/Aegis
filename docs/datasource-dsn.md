# 数据源 DSN 配置说明

Aegis 通过 **DSN（数据源名称 / 连接字符串）** 连接后端数据库。本文说明每种受支持数据源的 DSN 格式、必填参数与示例。

> **安全提示**：Aegis 在 `GET /admin/api/datasources` 与 MCP `list_datasources` 的列表响应中，**始终对 DSN 里的密码做脱敏**（形如 `user:****@tcp(...)`），原始密码不会离开服务端。编辑数据源时若留空 DSN，则保留既有连接串、不会用脱敏占位符覆盖真实密码。

## 支持的数据源类型

`mysql` · `postgres` · `sqlite` · `starrocks` · `clickhouse` · `trino` · `presto` · `mongo` · `es`

---

## 关系型 / SQL 类

### MySQL
驱动：`go-sql-driver/mysql`。两种写法均可。

```text
# 驱动原生写法（推荐，参数更直接）
user:password@tcp(host:port)/dbname?parseTime=true&charset=utf8mb4

# 标准 URL 写法
mysql://user:password@host:3306/dbname?parseTime=true
```

| 参数 | 说明 |
|------|------|
| `user` / `password` | 数据库账号与密码 |
| `host:port` | 地址与端口，如 `127.0.0.1:3306` |
| `dbname` | 默认库名 |
| `parseTime=true` | 让 `DATETIME` 映射为 Go `time.Time`（强烈建议） |
| `charset` | 字符集，默认 `utf8mb4` |

### PostgreSQL
标准 URL 写法：

```text
postgres://user:password@host:5432/dbname?sslmode=disable
```

| 参数 | 说明 |
|------|------|
| `sslmode` | `disable` / `require` / `verify-full`，按部署环境选择 |

### SQLite
DSN 为**文件路径**（或 `:memory:`），**不含密码**，因此无需脱敏。

```text
/path/to/aegis_demo.db
:memory:
```

### StarRocks / ClickHouse
均兼容 MySQL 协议，DSN 写法同 MySQL：

```text
user:password@tcp(host:9030)/dbname          # StarRocks（默认查询端口 9030）
user:password@tcp(host:9000)/default        # ClickHouse
```

---

## 查询引擎类（HTTP）

### Trino / Presto
通过 `/v1/statement` REST API 访问，DSN 为 URL：

```text
https://user:password@coordinator:8443?catalog=hive&schema=default
```

| 参数 | 说明 |
|------|------|
| `catalog` / `schema` | 默认 catalog 与 schema（可省略，查询时指定） |
| `user` / `password` | 协调者认证信息 |

---

## 文档 / 搜索类（NoSQL）

### MongoDB
```text
mongodb://user:password@host:27017/dbname?authSource=admin
```

### Elasticsearch
```text
http://user:password@host:9200
```

---

## 校验规则（创建 / 更新时）

- `sqlite`：接受任意非空文件路径或 `:memory:`，可为空仅当类型确为 sqlite。
- `mysql`：接受 `user:pass@tcp(host:port)/db` 或 `mysql://...` URL。
- 其余类型（postgres / starrocks / clickhouse / trino / presto / mongo / es）：必须是带 `host` 的合法 URL（`scheme://user:pass@host[:port]/db`）。
- 格式校验失败的错误信息会直接给出本文档链接，便于自助修正。

## 常见问题

- **密码里含 `@` 或 `:`**：MySQL 驱动写法下，密码中的特殊字符需按 URL 编码（如 `@` → `%40`）；或改用 `mysql://` URL 写法，由驱动负责转义。
- **列表里看到 `****`**：这是脱敏占位符，不是真实密码。编辑时留空 DSN 即可保留原连接。
- **生产环境**：建议为 Aegis 使用**专属只读账号**，并将 DSN 中的密码通过环境变量 / 密钥管理注入，避免明文写入配置文件。
