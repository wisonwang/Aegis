# Aegis MCP Skill 包

这个目录是 `datahub` 项目的 **TRAE Skill 源包**，用于把 Aegis 的 MCP 能力封装成可被 TRAE 按需加载的项目技能。

## 目录说明

```text
aegis-mcp/
├── evals/
│   └── trigger-cases.md
├── SKILL.md
└── references/
    └── mcp-tools.md
```

- `SKILL.md`
  Skill 入口文件，定义何时触发、如何调用 Aegis、输出时遵循什么流程。
- `references/mcp-tools.md`
  Aegis MCP 工具目录与原始 HTTP 参考，方便 Skill 编写和联调。
- `evals/trigger-cases.md`
  触发评测样例与验收清单，用于回归 Skill 命中率和推荐调用路径。
- `evals/cases.json`
  结构化评测样例，便于后续脚本化校验和自动回归。

## 使用方式

- 源包目录：`aegis-mcp/`
- 本地导入目标：`.trae/skills/aegis-mcp/`
- 打包命令：`make skill-pack`
- 全局安装命令：`make skill-install-global`
- 评测校验命令：`make skill-evals-check`
- 详细开发与导入规范：`docs/skill-config.md`

## 维护原则

- 仓库内只维护这一份 Skill 源包。
- `.trae/skills/` 视为本地导入产物，不纳入 git。
- 历史 `.workbuddy/skills/` 仅保留兼容，不再作为主维护目录。
