# Aegis 发布策略

> 目标：明确版本编号、发布节奏、兼容性与支持窗口，降低社区用户与商用客户对升级策略的不确定性。

---

## 1. 发布原则

Aegis 当前建议采用“快速迭代、有限支持窗口、明确升级路径”的发布策略。

核心原则：

- 优先保持主干可发布
- 优先维护当前稳定版本
- 不承诺过多历史分支长期维护
- 安全和治理回归优先于功能扩展

---

## 2. 版本编号建议

建议使用语义化版本：

- `MAJOR.MINOR.PATCH`

含义如下：

- `MAJOR`：存在不兼容的 API、配置、行为变化
- `MINOR`：新增能力、向后兼容的增强
- `PATCH`：缺陷修复、文档修复、安全补丁、小范围行为纠正

示例：

- `v0.7.0`：新增治理或 MCP 能力，但兼容现有用法
- `v0.7.1`：修复 bug、收口配置和部署问题
- `v1.0.0`：形成稳定外部接口和明确商用交付边界

---

## 3. 当前阶段建议

在 `1.0` 之前，建议这样理解版本：

- `0.x`：能力快速迭代期
- 允许在小版本中继续收口接口与默认值
- 但所有显著变化必须写入 `CHANGELOG.md`

在 `1.0` 之后，建议加强约束：

- 不轻易修改公开 API 路径
- 不轻易修改配置键名
- 不轻易改变默认安全策略的对外行为

---

## 4. 发布类型

建议分为三类：

### 4.1 正式发布

适合：

- 新能力可公开对外说明
- 文档已同步
- 构建、测试、MCP smoke 已通过

### 4.2 补丁发布

适合：

- 回归修复
- 安全修复
- 文档和配置纠偏

### 4.3 预发布

适合：

- 较大配置调整
- 商业能力边界调整
- 实验性能力验证

建议使用：

- `-rc.1`
- `-beta.1`

---

## 5. 发布前检查

每次正式版本发布前，建议至少完成：

- `go build ./...`
- `go vet ./...`
- `go test ./...`
- `make mcp-e2e`
- `make mcp-e2e-admin`
- README / 示例 / 变更日志同步
- 生产部署模板仍可用

如果涉及配置、安全、认证、代理、MCP 路径，建议额外检查：

- `make ci-smoke`
- `make security-scan`

---

## 6. 变更日志要求

每次发布都建议更新：

- `CHANGELOG.md`

建议记录的最小内容：

- 新增功能
- 不兼容变化
- 配置变更
- 安全修复
- 已知限制

对于商用客户，尤其要显式标记：

- 是否影响部署模板
- 是否影响鉴权与密钥配置
- 是否影响 Community / Enterprise 口径

---

## 7. 兼容性策略

### 7.1 API

建议保持以下接口稳定：

- `POST /api/v1/login`
- `POST /api/v1/query`
- `POST /mcp`

若必须调整：

- 先在文档与 changelog 标明
- 尽量保留一段兼容窗口
- 给出迁移示例

### 7.2 配置

建议尽量保持以下稳定性：

- 已公开的 JSON 配置键
- `AEGIS_*` 环境变量命名
- 生产配置模板的结构

若有重大调整：

- 必须在 release notes 中写迁移步骤

### 7.3 Demo 与示例

示例允许随着演示场景演进而变化，但要求：

- README 同步
- `examples/README.md` 同步
- MCP E2E 仍可跑通

---

## 8. 支持窗口建议

建议支持：

- 当前稳定版本
- 最近一个稳定小版本

不建议同时维护太多旧分支。

这样可以把工程资源集中在：

- 当前主线质量
- 生产安全
- 商用交付稳定性

参考：

- [support-policy.md](file:///Users/vincent/workspace/fosun/datahub/docs/support-policy.md)

---

## 9. 安全修复建议

安全问题发布建议：

- 优先发 `PATCH` 版本
- 在 changelog 中单独标记安全修复
- 如有需要，先通过私下渠道通知受影响客户

涉及以下情况时可考虑紧急补丁：

- 默认配置导致高危暴露
- 鉴权或治理绕过
- MCP / DataAPI 可直接被未授权调用
- 密钥或敏感数据处理存在严重缺陷

---

## 10. 推荐对外口径

可以直接用于外部说明：

> Aegis 采用语义化版本管理。当前以快速迭代为主，优先维护当前稳定版本和最近一个稳定小版本；安全与治理相关回归优先通过补丁版本修复。

---

## 11. 已完成的供应链增强

下列功能已经在当前版本中完成：

- ✅ **自动化 Release 流水线**：`.github/workflows/release.yml`
- ✅ **SBOM 产物**：使用 Syft 生成 SPDX 格式的 SBOM
- ✅ **漏洞扫描**：使用 Trivy/Grype 扫描
- ✅ **二进制签名**：使用 cosign 签名所有发布资产
- ✅ **发布说明模板**：完整的 `CHANGELOG.md` 与 `release-operations.md`
- ✅ **校验和**：SHA256 校验和

这些已经在 `release-operations.md` 中详细说明。

---

## 12. 关联文档

- 发布与供应链说明：[release-operations.md](file:///Users/vincent/workspace/fosun/datahub/docs/release-operations.md)
- 支持策略：[support-policy.md](file:///Users/vincent/workspace/fosun/datahub/docs/support-policy.md)
- 第三方许可说明：[third-party-notices.md](file:///Users/vincent/workspace/fosun/datahub/docs/third-party-notices.md)
