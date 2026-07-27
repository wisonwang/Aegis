# GitHub 仓库发布设置 · 可直接复制粘贴

> 配合 `docs/launch.md`（发布策略与落地页母本）使用。本文件是**执行层**——下面每块都是你打开 GitHub 仓库 Settings / Releases 页面后，可以直接 Ctrl+C / Ctrl+V 的文本。
> 全部动作可逆；除 Release/Tag 外均随时可改。涉及里程碑（打 tag、发 Release）建议先看 `docs/launch.md` 的"拍板项"。

---

## 1. 仓库 Description（Settings → General → Description）

```
Aegis — self-hosted AI Data Supply Gateway. Turn internal databases into governed Agent tools via MCP + DataAPI, with table/row/column governance, dynamic masking, and full audit. No DB credentials ever reach the Agent.
```

（约 250 字符，GitHub 上限 350，留有余量。）

---

## 2. Topics（Settings → General → Topics，逐行添加）

```
data-governance
mcp
model-context-protocol
ai-agents
database-proxy
llm
nl2sql
data-security
golang
self-hosted
```

> 添加方式：在 Topics 输入框粘贴一个回车一个，或一次性粘贴逗号分隔串（GitHub 会自动拆分）。

---

## 3. About 侧栏一句话（可放进 Description 上方或仓库 README 顶部）

```
把内部数据库，变成受控的 Agent 工具 —— 单二进制、治理默认开启、AI 原生。
```

---

## 4. Release v0.6.0（Releases → Draft a new release）

**Tag:** `v0.6.0`
**Target:** `main`
**Release title:** `Aegis v0.6.0 — AI Data Supply Gateway`

**Body（直接粘贴）：**

~~~
## Aegis v0.6.0

把内部数据库变成 **受控的 AI Agent 工具** —— 单 Go 二进制，分钟级自托管，治理默认开启。

### 它解决什么
AI Agent 直连数据库 = 凭证泄露 + 越权访问 + 无审计。Aegis 在 Agent 与 DB 之间架一道**治理网关**：
代理持有连接池（Agent 永远不碰 DB 密码），表/行/列三级管控默认拒绝，结果层动态脱敏，全量审计留痕。

### 两种供给形态
- **MCP（面向 AI Agent）**：`query` / `estimate_query` / `nl2sql` 工具 + `resources` 语义卡片 + `prompts` 安全模板。Streamable HTTP，零数据库凭据即可接入 Claude Desktop 等任意 MCP 客户端。
- **DataAPI（面向传统应用）**：REST 查询 / 指标 / 血缘成本预估。

### 核心能力
- 三级治理：表权限（默认拒绝）× 行策略（`:attr` 属性代入派生表）× 列脱敏（phone/email/fpe/tokenize…）
- 执行前成本/风险预估：`estimate_query` 复用治理内核，EXPLAIN 扫描行数 + 敏感度合成 risk_level
- 语义指标层：把口径沉淀为受治理的 metrics，Agent 调指标而非裸 SQL
- 身份对接：OIDC / LDAP / 本地 JWT，auto-provisioning
- 审计 + 结构化日志 + 健康检查（liveness/readiness）

### 30 秒上手
```bash
docker run -p 8080:8080 ghcr.io/wisonwang/aegis:latest
```
MCP 客户端配置（无需任何 DB 凭据）：
```json
{ "mcpServers": { "aegis": { "url": "http://localhost:8080/mcp",
  "headers": { "X-MCP-API-Key": "mcp-demo-key" } } } }
```

### 文档
- [README](README.md) · [BLUEPRINT](BLUEPRINT.md) · [AI 应用场景](AI-SCENARIO.md) · [竞品对比](docs/competitive_analysis.md) · [发布与采用加速](docs/launch.md)

> 默认演示账号 admin/admin123（**生产务必改密并设 `AEGIS_JWT_SECRET` / `AEGIS_MASK_SECRET` / `AEGIS_MCP_API_KEY`**）。
~~~

> ⚠️ 发布前确认：Docker 镜像是否已推到 `ghcr.io/wisonwang/aegis`（Release 里 `docker run` 才能拉到）。若镜像尚未发布，先把该段换成「从源码 `go build` / 用仓库内 `docker-compose.yml`」。

---

## 5. Social Preview（Settings → General → Social preview）

GitHub 推荐 **1280×640 PNG**。建议文案构图：

- 主标题：**Aegis** · AI Data Supply Gateway
- 副标题：**把内部数据库，变成受控的 Agent 工具**
- 三栏小标：数据库 MCP Server ｜ 数据访问代理与治理 ｜ 语义层供给（呼应 `docs/competitive_analysis.md` 的交集定位图）
- 底部一行：self-hosted · single Go binary · governance by default

> 图片文件放哪都行（GitHub 直接上传），无需进仓库。需要我出一张 SVG/PNG 母版可另说。

---

## 6. 发布后（采用率验证，见 `docs/launch.md`）

- 北极星：Stars + Clones（Insights → Traffic）
- 领先指标：Issues / Discussions / fork
- 在仓库开一个 **Show & tell** 类 Issue 或用 GitHub Discussions 收集「你的 Agent 接 Aegis 的故事」
- 反馈反哺优先级：多租户工作区是否现在做，由采用信号决定
