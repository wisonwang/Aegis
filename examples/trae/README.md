# TRAE 本地接入示例

这个目录提供把本地 `Aegis MCP` 接入 TRAE 的最小示例，面向本地开发与 Skill 联调。

## 文件

- `mcp_http_config.json`
  TRAE 手动配置 HTTP MCP Server 时可直接参考的 JSON 示例。

## 使用方式

1. 先启动本地 Aegis 服务：

```bash
make dev
```

2. 在 TRAE 中进入“设置 → MCP → 创建 → 手动配置”。

3. 将 `mcp_http_config.json` 中的内容粘贴进去，并按实际情况调整请求头。

4. 在 Skill 面板导入 `aegis-mcp/SKILL.md` 或 `dist/aegis-mcp-skill.zip`。

## 说明

- 本地开发默认使用 `X-MCP-API-Key: mcp-demo-key`。
- 若要验证管理员视角，把请求头改成 `Authorization: Bearer <JWT>`。
- 若需要项目级 Skill，导入后 TRAE 会在当前项目生成 `.trae/skills/aegis-mcp/`。

完整规范见 `docs/skill-config.md`。
