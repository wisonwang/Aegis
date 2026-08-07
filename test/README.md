# Aegis 端到端测试套件 (`test/`)

本目录是一个**自包含、可复现**的 pytest 全流程测试套件。每个测试会话会：

1. 在临时目录启动一个**全新的 Aegis 实例**（`go run ./cmd/aegis`，配置 `seed_demo: true`），
   用 SQLite 控制面 + 演示数据源，**完全不污染**你正在运行的开发实例（如 `:8080`）。
2. 同时启动一个进程内的 **FakeLLM**（`/chat/completions`），让 `nl2sql` 路径也能端到端跑通，无需真实 LLM。
3. 围绕 seed 进来的「酒店运营」真实场景数据展开断言：多租户 `hotel_bookings` / `guest_profiles`、
   `analyst`（限定 `acme` 租户，列脱敏）与 `admin`（绕过治理）两种身份、已发布的
   `hotel_confirmed_bookings` 数据集、两类指标。

## 运行

```bash
cd test
python3 -m venv .venv && . .venv/bin/activate
pip install -r requirements.txt
pytest -q            # 或：pytest -v 看每个用例
```

> 需要 Go 工具链在 PATH（默认使用 `go`，可用 `GO_BIN` 环境变量覆盖）。
> 首次运行会编译 Aegis（约 30–60s），之后实例在会话内复用。

### 通过 make / CI 运行

- 本地一条命令：`make test-py`（等价于 `cd test && python3 -m pytest -q`）。
- CI：`.github/workflows/ci.yml` 的 `build-test` job 已内置该步骤，每次 push/PR 到 `main` 都会自动跑 pytest。
- `make ci-smoke`（本地 CI 等价物）也已纳入 pytest，且被 `.github/workflows/release.yml` 在打 tag 发布时调用 → 发布前回归保护。
- 无论本地还是 CI，`go run ./cmd/aegis` 的 cwd 都由 conftest 锁定为仓库根（见 `conftest.py` 的 `ROOT`），不受 pytest 启动目录影响；CI 额外用 `GO_BIN` 指向 runner 的 Go。

## 覆盖矩阵

| 模块 | 验证点 |
|------|--------|
| `test_auth.py` | 登录发 JWT、错误密码 401、服务账号禁止口令登录、analyst 越权访问 admin API 被拒、/me 返回角色 |
| `test_governance.py` | 行策略租户隔离（analyst 3 行 / admin 5 行）、列脱敏（姓名/手机/邮箱）、admin 见原始 PII、`rewritten_sql` 注入租户条件、DDL 被 403 拦截 |
| `test_datasource.py` | 数据源列表、表列表、列描述、catalog |
| `test_dataset.py` | 数据集列表、schema 契约、数据集查询的租户隔离（analyst 2 行 / admin 3 行） |
| `test_folders.py` | (D7) 嵌套目录树 CRUD、数据集归属、递归/非递归过滤、移动到未分类、非空删除 409、目录移动防环 400 |
| `test_metrics.py` | 指标 seed、`run_metric` 租户隔离（analyst 3 / admin 4 入住人数） |
| `test_mcp.py` | MCP 握手、11 工具清单、resources/prompts、analyst 查询脱敏、nl2sql（FakeLLM）端到端、指标、admin 原始数据 |

## 设计原则

- **默认拒绝**是断言对象：治理由单一引擎重写每条 SQL，测试直接验证「analyst 看不到别的租户、拿不到明文 PII、写不了 DDL」。
- **可逆性**：目录（`folder_id`）只是组织元数据，测试创建的资源全部落在临时实例，随进程退出消失。
- `conftest.py` 中的 `http_json` / `list_of` / `rows_of` 适配器对响应形态（扁平 `QueryResult` 或嵌套 `query_result`）做了兼容，避免脆弱的字段假设。

## 接口契约要点（写测试前必读）

实测后端与直觉不同之处，避免重复踩坑：

1. **数据集路由按 UUID `id`，不是 name。** `GET/POST /api/v1/datasets/:id` 与 `:id/query` 的 `:id`
   是数据集的 UUID（`DatasetInfo.id`），传 name 会返回 `403 dataset not found`。消费路径应为：
   `GET /api/v1/datasets` 列表 → 按 `name` 取 `id` → 再用 `id` 调 detail/query。
2. **建数据集按数据源 UUID `id` 查。** `POST /admin/api/datasets` 的 `datasource_id` 必须传
   `GET /api/v1/datasources` 列表里的 `id`，传 `"demo"`（name）会返回 `400 datasource not found`。
   （注：DataAPI 的 `/api/v1/datasources/demo/tables` 路径形参接受 name，但 admin 写接口要 id，两者不一致。）
3. **创建类接口返回 201。** 目录文件夹、数据集的创建返回 `201 Created`（响应体含 `{"id": ...}`），
   而非 200。测试断言用 `st in (200, 201)`。创建响应只回 `id`，要验证 `folder_id` 需再 `GET` 一次。
4. **MCP 工具响应形态不统一：**
   - `list_datasources` 直接返回**裸 list**（`[{name, ...}]`）；
   - `list_tables` / `list_metrics` 包在 `{"tables":[...]}` / `{"metrics":[...]}`；
   - `query` / `nl2sql` / `run_metric` 包在 `{"queryResult": {"rows":[...], ...}}`。
   测试里用 `test_mcp._rows()` / 对 list 与 dict 双形态做兼容。
5. **NL2SQL `base_url` 不要带后缀。** `internal/nl2sql/llm.go` 会自动在 `BaseURL` 后拼
   `/chat/completions`，所以 FakeLLM 只填根地址（如 `http://127.0.0.1:PORT`），否则双重拼接 → 404。
6. **MCP 协议顺序：** 必须先 `initialize`，再发 `notifications/initialized`（202 通知），之后才能
   `tools/list` / `tools/call`，响应带 `Mcp-Session-Id`。
