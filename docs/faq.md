# Aegis FAQ

> 目标：沉淀对外沟通中最常见的问题，统一 README、售前沟通、PoC 介绍和商用推广口径。

---

## 1. Aegis 是什么？

Aegis 是面向 AI Agent 的受治理数据网关，把内部数据库变成可安全调用的 `DataAPI + MCP` 工具。

它的重点不是“管理所有数据资产元数据”，而是解决一个更紧迫的问题：

- Agent / Copilot / ChatBI 需要访问数据库
- 但企业不能接受裸连数据库、无审计、无脱敏、无统一权限

Aegis 通过统一网关层，把这些治理能力强制收口在执行路径上。

---

## 2. Aegis 和普通 MCP Server 有什么区别？

普通 MCP Server 更像“把数据库能力包装成工具”。

Aegis 在此基础上额外提供：

- 表 / 行 / 列级治理
- 动态脱敏
- 执行前风险估算
- 审计留痕
- 管理端统一配置

一句话：

- MCP Server 解决“能不能让 Agent 调数据库”
- Aegis 解决“让 Agent 调数据库时，如何仍然安全、可控、可审计”

---

## 3. Aegis 和数据目录 / 元数据平台有什么区别？

不一样。

数据目录平台通常更关注：

- 元数据采集
- 血缘
- 资产发现
- 目录检索
- 治理流程编排

Aegis 当前更关注：

- 查询执行路径上的治理闭环
- LLM / Agent 场景下的受控取数
- DataAPI / MCP / NL2SQL 的统一供给

如果你的目标是“做完整企业数据目录”，Aegis 不是替代品。
如果你的目标是“让 Agent 安全访问数据库”，Aegis 更直接。

---

## 4. 为什么不让 Agent 直接连数据库？

因为 Agent 场景下的 SQL 往往是运行时生成的，问题包括：

- 不可预审
- 易被提示词注入影响
- 容易越权
- 容易拿到敏感数据
- 容易缺乏统一审计

一旦让 Agent 直接持有数据库凭据，治理就会退回到“相信提示词和应用代码”的层面，这在生产环境通常不可接受。

---

## 5. Aegis 是否支持私有化部署？

支持。

当前项目定位就是自托管、单二进制、轻量部署。

仓库已提供：

- 单二进制启动方式
- `docker-compose.prod.yml`
- `nginx` 反代示例
- `systemd` 单元文件

详见：

- [deployment-production.md](file:///Users/vincent/workspace/fosun/datahub/docs/deployment-production.md)

---

## 6. Community 和 Enterprise 有什么区别？

建议口径如下：

- Community：核心治理网关能力，单独即可成立
- Enterprise：组织级治理增强能力

当前推荐矩阵：

| 能力 | Community | Enterprise |
|------|-----------|------------|
| 表 / 行 / 列治理 | Yes | Yes |
| 动态脱敏 | Yes | Yes |
| DataAPI / MCP / NL2SQL | Yes | Yes |
| 基础审计 / 基础告警 | Yes | Yes |
| Datasets / Metrics / 审批流 | No | Yes |
| 多租户 / SIEM / HA | No | Yes |

完整说明见：

- [commercial-packaging-plan.md](file:///Users/vincent/workspace/fosun/datahub/docs/commercial-packaging-plan.md)

---

## 7. Aegis 是否已经是完整的数据平台？

不是。

当前不建议过度承诺以下能力已经成熟：

- 全量资产目录
- 全量数据血缘
- 自动 PII 扫描
- 企业级 HA 控制面
- 默认启用的完整多租户体系
- 标准化 SIEM 审计流转发

这些更适合作为路线图或企业版增强能力来讲，而不是首页主卖点。

---

## 8. Aegis 的典型使用场景有哪些？

当前最适合的三类场景：

1. 企业内部 Copilot / ChatBI 问数
2. 运营分析晨会备数
3. SaaS 产品内嵌 AI 数据助手

这些场景的共同特点是：

- 需要 Agent 访问数据库
- 对权限、脱敏、审计有要求
- 不想先上重型平台再做 AI 场景

---

## 9. Aegis 支持哪些数据库？

当前仓库已覆盖的主要方向包括：

- SQLite
- MySQL
- PostgreSQL
- MongoDB（以 NoSQL 网关方式集成）

建议在具体 PoC 中按真实数据源做验证。

---

## 10. Aegis 如何与现有身份体系集成？

当前已支持的方向包括：

- 本地账号
- OIDC
- LDAP

目标是把用户 / 角色 / 权限映射收口到统一治理面，而不是让每个 Agent 或应用自己处理身份逻辑。

---

## 11. Aegis 是否支持商用？

可以。

仓库采用 MIT License，允许商用、修改和再分发。
如果需要组织级治理增强能力、正式支持或企业交付能力，可在 Community 基础上扩展 Enterprise 版本。

注意：

- 开源许可允许商用
- 商业包装、支持承诺和版本边界需要额外文档明确

参考：

- [support-policy.md](file:///Users/vincent/workspace/fosun/datahub/docs/support-policy.md)
- [release-policy.md](file:///Users/vincent/workspace/fosun/datahub/docs/release-policy.md)

---

## 12. 上线前最重要的注意事项是什么？

生产环境至少需要做到：

- 不使用 `conf/config.demo.json`
- 不开启 `seed_demo`
- 配置强随机 `AEGIS_JWT_SECRET`
- 配置强随机 `AEGIS_MASK_SECRET`
- 配置强随机 `AEGIS_MCP_API_KEY`
- 前置 TLS
- 默认关闭 Swagger 文档暴露

完整检查表见：

- [production-readiness-checklist.md](file:///Users/vincent/workspace/fosun/datahub/docs/production-readiness-checklist.md)
