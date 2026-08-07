# aegis-mcp 技能

> 兼容遗留目录。当前主维护入口已切换到 `aegis-mcp/`，本地 TRAE Skill 开发配置见 `docs/skill-config.md`。除兼容存量环境外，不再以本目录作为单一事实来源。

调用本地 **Aegis 受治理数据网关** MCP 服务的 WorkBuddy 技能(项目级,纳入 git 跟踪)。

## 用途
让 WorkBuddy 在 `datahub` 工作区自动获得「安全查询数据库」能力:通过 Aegis 的 MCP 工具(`query` / `nl2sql` / `estimate_query` / `list_datasources` / `list_tables` / `describe_table` / `get_catalog` / `list_datasets` / `get_dataset_catalog` / `list_metrics` / `run_metric`)回答问题,且所有调用都经过表/行/列三级治理与脱敏,agent 拿不到越权数据。

## 维护
- 源码位于本目录(`.workbuddy/skills/aegis-mcp/`),由团队共享、git 跟踪。
- 修改技能请直接编辑 `SKILL.md`(调用手册)与 `references/mcp-tools.md`(工具详细 schema + 原始 HTTP 示例),提交即可生效。
- **不要**把它放回用户级 `~/.workbuddy/skills/`——保持本目录为单一来源。

## 配套:启用 MCP 连接器
技能依赖 WorkBuddy 已连接的 `aegis` MCP 服务:
1. 在 `~/.workbuddy/mcp.json` 中配置(已配):`url: http://localhost:8080/mcp`,`headers` 用 `X-MCP-API-Key: mcp-demo-key` 或 `Authorization: Bearer <JWT>`。
2. 在 WorkBuddy **连接器管理页**找到 `aegis` 点 **Trust** 启用。
3. Aegis 服务需运行(默认 `:8080`):`curl -s -o /dev/null -w '%{http_code}' localhost:8080/metrics` 应返回 200。

## 本地验证
```bash
# 端点存活 + 工具列表
curl -s -X POST localhost:8080/mcp \
  -H 'Content-Type: application/json' -H 'X-MCP-API-Key: mcp-demo-key' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'

# 实查(治理生效:analyst 默认拒绝越权表,行级策略穿透注入)
curl -s -X POST localhost:8080/mcp \
  -H 'Content-Type: application/json' -H 'X-MCP-API-Key: mcp-demo-key' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"query","arguments":{"datasource":"demo","sql":"SELECT count(*) AS n FROM orders"}}}'
```

## 重新打包分发(可选)
需用 `skill-creator` 重新打包时:
```bash
python3 <skill-creator>/scripts/package_skill.py .workbuddy/skills/aegis-mcp
```
产物 `.zip` 是**派生产物,请勿提交进 git**(分享用即可)。
