# Aegis 开源发布与采用加速清单

> 战略第一成功指标 = **采用率**，不是功能数。
> 本文是两步可交付物：① GitHub 仓库发布动作清单（你或我执行，全部可逆）；② 落地页文案母本（直接用作 README 英雄区 / Release notes / 对外介绍）。
> 楔子定位：自托管、治理默认开启的 AI 数据网关——把内部库变成受控 Agent 工具，不打重型数据目录的功能广度战。
> 若准备进入正式商用推进，请同时参考：
> - [生产上线检查表](file:///Users/vincent/workspace/fosun/datahub/docs/production-readiness-checklist.md)
> - [商用包装方案](file:///Users/vincent/workspace/fosun/datahub/docs/commercial-packaging-plan.md)
> - [发布与供应链说明](file:///Users/vincent/workspace/fosun/datahub/docs/release-operations.md)

## 一、GitHub 仓库发布动作清单

### 1.1 仓库元数据（可逆，建议先做）
- [ ] **仓库描述 (About)**：`AI-native data gateway — turn internal databases into governed Agent tools in minutes. Self-hosted, governance that can't be bypassed.`
- [ ] **Topics**（影响被发现性）：`data-governance` `llm` `ai-agents` `mcp` `model-context-protocol` `database-proxy` `nl2sql` `data-security` `self-hosted` `golang`
- [ ] **Website**（可选）：留空，或指向 README 锚点
- [ ] **徽章**：README 顶部已含 license / go / mcp / deploy 徽章 ✅

### 1.2 发布物料
- [ ] **Social preview / OG 图**（1280×640）：文案建议 `Aegis — AI Data Supply Gateway · 把内部库变成受控 Agent 工具`。需人工在 GitHub UI 上传（API 不支持图上传）；我可生成一张 SVG/PNG 供你上传。
- [ ] **Release v0.6.0 + tag**：notes 直接复用 `CHANGELOG.md` 的「楔子能力时间线」一节。若无 tag，先打 `v0.6.0`。
- [ ] **Pinned issue / Discussions**：开一个 *"Show & tell：你用 Aegis 接了什么 Agent？"* 收集采用案例，反哺优先级。
- [ ] **README 英雄区**：用下方「落地页文案」替换/强化现有英雄区。

### 1.3 可我来执行的部分（需你确认 `gh` 已登录）
`gh repo edit --description "..." --add-topic ...` 可逆、无损；**tag / release 我先不动**，等你拍板（属对外里程碑动作）。

## 二、落地页文案（canonical）

### Hero
- **标题**：把内部数据库，变成受控的 Agent 工具
- **副标**：Aegis 是一个自托管的 AI 数据网关。一分钟落地，把你的 MySQL / PostgreSQL 封装成带表/行/列治理的 DataAPI 与 MCP 服务——LLM 生成的 SQL 也**绕不过治理**。
- **CTA**：`docker compose up` 跑起来 · 5 分钟接进 Claude Desktop

### 为什么需要 Aegis（楔子叙事）
- 你给 Agent 直连数据库 = 把全部数据权限一次性交出去。
- 传统数据目录面向「人 / BI」，对 Agent 的 NL2SQL、动态脱敏、执行前风险预估无能为力。
- Aegis 占据「轻量自托管 + AI 原生治理」蓝海：单二进制、分钟级落地、治理默认开启且不可绕过。

### 30 秒上手
```bash
docker compose up -d
```
把下面这段放进任意 MCP 客户端配置，**无需任何数据库凭据**：
```json
{
  "mcpServers": {
    "aegis": {
      "url": "http://localhost:8080/mcp",
      "headers": { "X-MCP-API-Key": "mcp-demo-key" }
    }
  }
}
```
Agent 立刻获得 `query` / `estimate_query` / `nl2sql` 三个受治理工具。详见 `examples/mcp/claude_desktop_config.json`。

### 核心能力
| 能力 | 说明 |
|------|------|
| 三级治理 | 表 / 行 / 列权限，默认拒绝 |
| 动态脱敏 | `tokenize` / `fpe` / 部分掩码，密钥化、可还原 |
| NL2SQL 网关 | 接 LLM 生成 SQL，仍走同一治理内核 |
| 语义指标层 | 管理员定义口径，Agent 复用，注入-proof |
| 查询血缘成本 | 执行前 EXPLAIN 预估扫描行数 + 敏感度风险 |
| 全量审计 | 留痕可上 SIEM，关联 AI 会话 |
| 企业身份 | OIDC + LDAP 对接，自动供给与角色映射 |

### 与其他方案
- **vs 重型数据目录**：不拼功能广度，拼「部署简单 + 治理不可绕过」。
- **vs 直连数据库**：治理、脱敏、审计、限流一手包办，Agent 不持凭据。

### 安全模型
默认拒绝；`admin` 为内置超级用户。生产务必改种子凭据、设 `AEGIS_JWT_SECRET` / `AEGIS_MASK_SECRET` / `AEGIS_MCP_API_KEY`，前置 TLS。详见 `SECURITY.md`。

## 三、采用率怎么验证（第一指标）
- **北极星**：GitHub Stars + Clones；MCP 配置片段被复制次数（可在文档链接埋 `?utm=`）。
- **领先指标**：Issues / Discussions 数、示例 Agent 仓库出现、`examples/` 被 fork。
- **反馈闭环**：Pinned *"Show & tell"* issue 收集真实场景，反哺多租户 / 审批流等工作优先级——用采用反馈定重编码的先后，而非拍脑袋。

## 四、与剩余待办的衔接
- 多租户工作区（企业版前置、最后企业门槛）属重编码，优先级应**由采用反馈驱动**。
- 本清单是「采用率验证」的第一步（叙事 + 发布就绪 + 度量方法）；真正闭环在仓库设置动作落地后看指标。

---

## 附录：GitHub 仓库设置命令（需安装 gh CLI 并登录）

当前环境未检测到 `gh`，下列命令已按仓库 `wisonwang/aegis` 准备，复制到本地运行即可（全部可逆）：

```bash
# 1. 安装 gh 并登录（若尚未完成）
brew install gh      # macOS
gh auth login

# 2. 设置仓库描述与 Topics（可重复执行，覆盖更新）
gh repo edit wisonwang/aegis \
  --description "AI-native data gateway — turn internal databases into governed Agent tools in minutes. Self-hosted, governance that can't be bypassed." \
  --add-topic "data-governance,llm,ai-agents,mcp,model-context-protocol,database-proxy,nl2sql,data-security,self-hosted,golang"

# 3. 上传社交预览图
# 已在 docs/pics/aegis-social-preview.png 生成 1280×640 PNG。
# 请到 GitHub 仓库 Settings → General → Social preview 手动上传（GitHub API 不支持图片上传）。

# 4. 打 tag 并发布 Release（里程碑动作，请确认后再执行）
git tag v0.6.0
git push origin v0.6.0
gh release create v0.6.0 --title "Aegis v0.6.0 — AI Data Supply Gateway" --notes-file CHANGELOG.md
```

> **注意**：当前沙箱未安装 `gh`，以上命令未实际执行。运行前请确认已登录 GitHub、目标仓库路径正确，且 Release/tag 动作符合你的发布节奏。
