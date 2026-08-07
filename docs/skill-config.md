# 本地 Skill 开发配置

> 本文定义 `datahub` 项目的本地 TRAE Skill 开发与调试规范，目标是把 **Aegis MCP 能力** 以可复用 Skill 的方式稳定接入 TRAE，同时避免 `.trae/skills/`、根目录包、历史 `.workbuddy/skills/` 三套目录长期漂移。

---

## 1. 目标与原则

### 1.1 目标

- 让 Aegis 既是 **治理网关产品**，也是 **TRAE 可调用的数据能力模块**。
- 让本地开发、团队共享、后续对外分发三条路径使用同一份 Skill 源文件。
- 让 Skill 开发与 MCP 配置有清晰边界：**Skill 负责告诉 TRAE 何时/如何调用；MCP Server 负责提供实际工具能力**。

### 1.2 设计原则

- **单一事实来源**：Skill 源码只维护一份。
- **项目内可复现**：新同事拿到仓库后，按文档即可本地接好 Skill。
- **本地优先**：优先满足 TRAE IDE / Work 桌面版本地开发调试，不依赖云端环境。
- **兼容但不扩散**：保留历史 `.workbuddy/skills/` 作为兼容遗留，不再作为主维护目录。

---

## 2. 官方约束

依据 TRAE 官方文档：

- 项目级 Skill 应放在项目根目录下的 `.trae/skills/`。
- 全局 Skill 应放在 macOS/Linux 的 `~/.trae-cn/skills`。
- Skill 以 `SKILL.md` 为入口，可带 `references/`、`templates/`、`resources/` 等辅助文件。
- MCP Server 由 TRAE 单独配置，Skill 与 MCP 是协作关系，不应混为一种配置。

参考官方文档：

- [技能（Skill）](https://docs.trae.cn/ide/skills)
- [如何写好一个 Skill：从创建到迭代的最佳实践](https://docs.trae.cn/ide/best-practice-for-how-to-write-a-good-skill)
- [MCP 概览](https://docs.trae.cn/ide/model-context-protocol)
- [添加 MCP Server](https://docs.trae.cn/work/remote-mcp-server)

---

## 3. 推荐目录策略

### 3.1 单一事实来源

仓库内以 `aegis-mcp/` 作为 **Skill 源包目录**：

```text
datahub/
├── aegis-mcp/
│   ├── SKILL.md
│   └── references/
│       └── mcp-tools.md
├── docs/
│   └── skill-config.md
└── .trae/               # 本地导入产物，不纳入 git
```

### 3.2 各目录职责

- `aegis-mcp/`
  受 git 管理，作为 Skill 源包与对外分发包。
- `.trae/skills/aegis-mcp/`
  本地 TRAE 项目技能目录，由导入或手工同步产生；**不纳入 git**。
- `~/.trae-cn/skills/aegis-mcp/`
  跨项目复用时使用的全局技能目录；适合个人长期使用。
- `.workbuddy/skills/aegis-mcp/`
  历史兼容目录；不再作为主开发入口。

### 3.3 为什么不直接把 `.trae/skills/` 纳入 git

- `.trae/` 同时承载本地客户端生成的配置和禁用状态，适合视为本地环境目录。
- 若把 `.trae/skills/` 直接当作主开发目录，会让“源文件”和“导入产物”混在一起，后续难以维护。
- 当前更适合采用“**仓库源包 + 本地导入目录**”模式。

---

## 4. 本地开发标准配置

### 4.1 启动 Aegis 服务

本地 Skill 调试依赖 Aegis MCP 端点可用：

```bash
go run ./cmd/aegis
```

默认端点：

- MCP: `http://localhost:8080/mcp`
- Metrics: `http://localhost:8080/metrics`

健康检查：

```bash
curl -s -o /dev/null -w '%{http_code}' http://localhost:8080/metrics
```

期望返回 `200`。

### 4.2 在 TRAE 中添加本地 MCP Server

在 TRAE 中进入“设置 → MCP → 创建 → 手动配置”，填入 HTTP MCP 配置。

推荐本地开发配置示例：

```json
{
  "mcpServers": {
    "aegis": {
      "url": "http://localhost:8080/mcp",
      "headers": {
        "X-MCP-API-Key": "mcp-demo-key",
        "START_MCP_TIMEOUT_MS": "60000",
        "RUN_MCP_TIMEOUT_MS": "60000"
      }
    }
  }
}
```

补充说明：

- 本地调试优先使用 `X-MCP-API-Key: mcp-demo-key`，便于快速验证 analyst 视角。
- 需要管理员视角时，改为 `Authorization: Bearer <JWT>`。
- 若要验证多工作区视角，可附加 `X-Workspace-Id` 请求头。

### 4.3 在 TRAE 中导入项目 Skill

推荐流程：

1. 在 TRAE 中打开本项目。
2. 进入 Skill 面板，选择“导入已有 Skill”。
3. 选择仓库内 `aegis-mcp/SKILL.md`，或上传包含 `SKILL.md` 与 `references/` 的 zip 包。
4. 让 TRAE 在当前项目下生成 `.trae/skills/aegis-mcp/`。

这样做的好处：

- 仓库保留源包；
- TRAE 使用项目技能目录；
- 两边职责清晰，不需要手工长期维护双份文案。

### 4.4 安装为全局 Skill

如果希望在多个项目中复用本 Skill，可执行：

```bash
make skill-install-global
```

默认会把 `aegis-mcp/` 安装到：

```text
~/.trae-cn/skills/aegis-mcp/
```

如需自定义目标根目录，可设置环境变量：

```bash
TRAE_SKILLS_HOME=/custom/path make skill-install-global
```

---

## 5. Skill 开发工作流

### 5.1 日常迭代流程

1. 修改仓库源包：`aegis-mcp/SKILL.md`、`aegis-mcp/references/*`
2. 如需导入包，执行 `make skill-pack`
3. 重启或确认本地 Aegis 服务可用
4. 在 TRAE 中重新导入 Skill，或手动覆盖 `.trae/skills/aegis-mcp/`
5. 开启新对话验证触发效果
6. 根据失败样例继续收敛 `name`、`description`、触发边界和步骤说明

### 5.2 验证场景

- “查一下有哪些数据源”
- “订单表有哪些字段”
- “上个月 GMV 是多少”
- “先评估这条 SQL 风险，再执行”
- “有没有现成指标可以直接跑”

推荐把回归样例维护在：

- `aegis-mcp/evals/trigger-cases.md`
- `aegis-mcp/evals/cases.json`

### 5.3 验证标准

- Skill 能被正确触发，而不是被普通自由对话替代。
- 能优先走 `get_catalog` / `list_metrics` / `estimate_query` 等安全路径。
- 输出会明确说明结果来自 Aegis 受治理 MCP，而不是直接数据库裸连。
- 没有继续出现 `WorkBuddy`、`~/.workbuddy/mcp.json` 等旧命名。

---

## 6. 版本与发布策略

### 6.1 当前阶段

当前采用：

- **项目内源包**：`aegis-mcp/`
- **本地运行时导入**：`.trae/skills/aegis-mcp/`
- **本地 MCP 配置**：TRAE 设置中心手动配置

### 6.2 后续升级路线

- **Phase S1：本地可用**
  先把单技能调通，满足当前项目内使用。
- **Phase S2：团队复用**
  增加 zip 打包脚本与安装说明，支持同团队一键导入。
- **Phase S3：对外分发**
  形成公开示例仓库或 Skill 市场分发包，把 Aegis 作为“治理数据访问 Skill”输出。

---

## 7. 维护约束

- `aegis-mcp/` 为主维护目录，改 Skill 先改这里。
- `.trae/` 一律视为本地产物，不作为评审依据。
- `.workbuddy/skills/` 仅保留兼容，不再继续扩写新特性。
- 若 MCP 工具集新增/变更，必须同步：
  - `aegis-mcp/SKILL.md`
  - `aegis-mcp/references/mcp-tools.md`
  - `BLUEPRINT.md` 中 Skill / Agent 交付层描述

---

## 8. 建议的下一步

- 补一个 `make skill-pack`，把 `aegis-mcp/` 打成可导入 zip。
- 为 `aegis-mcp` 增加 5~8 条触发评测样例，建立失败优先的迭代闭环。
- 后续如要做团队统一交付，再补 `~/.trae-cn/skills` 的全局安装脚本。

## 9. 已落地产物

- `make skill-pack`
  将 `aegis-mcp/` 打包为 `dist/aegis-mcp-skill.zip`
- `make skill-install-global`
  将 `aegis-mcp/` 安装到 `~/.trae-cn/skills/aegis-mcp/`
- `make skill-evals-check`
  校验 `aegis-mcp/evals/cases.json` 的结构完整性
- `examples/trae/mcp_http_config.json`
  TRAE 本地 HTTP MCP 手动配置示例
- `aegis-mcp/evals/trigger-cases.md`
  Skill 触发评测样例与验收清单
- `aegis-mcp/evals/cases.json`
  结构化评测样例
