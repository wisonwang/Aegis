# Aegis 支持策略

> 目标：明确 Community 与 Enterprise 的支持边界，统一对外支持承诺，降低 PoC、试用和商用交付中的预期偏差。

---

## 1. 支持原则

Aegis 采用双层支持模型：

- Community：社区自助支持
- Enterprise：商业支持与交付支持

支持策略的核心目标不是“无限兜底”，而是明确：

- 哪些问题可以被响应
- 哪些交付属于支持范围
- 哪些需求属于定制开发或路线图讨论

---

## 2. Community 支持范围

Community 版本默认提供以下支持方式：

- README、示例和公开文档
- GitHub Issues
- GitHub Discussions 或社区交流渠道
- 版本升级说明与公开变更日志

Community 支持更适合：

- 自助试用
- 技术评估
- 个人开发者或小团队接入
- 非关键生产环境验证

Community 默认**不承诺**：

- 响应时效 SLA
- 指定版本补丁交付
- 专项部署协助
- 私有环境排障
- 定制功能开发

---

## 3. Enterprise 支持范围

Enterprise 支持面向组织级客户，建议覆盖：

- 部署咨询
- 升级建议
- PoC 方案评估
- 故障排查
- 版本升级窗口协调
- 关键缺陷修复优先级提升

Enterprise 支持通常适合：

- 正式 PoC
- 受监管场景上线
- 需要多环境部署和版本治理的团队
- 需要组织级权限、审计、扩展治理能力的客户

---

## 4. 响应优先级建议

以下为推荐支持分级，可作为售前和交付协作基线：

| 优先级 | 典型问题 | 目标响应 |
|------|----------|----------|
| P1 | 生产不可用、核心治理失效、严重安全风险 | 4 个工作小时内响应 |
| P2 | 主要功能不可用、升级阻塞、PoC 关键路径中断 | 1 个工作日内响应 |
| P3 | 一般功能缺陷、兼容性问题、文档错误 | 3 个工作日内响应 |
| P4 | 使用咨询、增强建议、路线图讨论 | 纳入常规支持队列 |

说明：

- “响应”指确认问题、建立沟通和初步判断
- “修复”时间取决于问题复杂度、版本策略和变更风险

---

## 5. 支持渠道建议

### 5.1 Community

- GitHub Issues：缺陷、回归、文档问题
- GitHub Discussions：使用交流、方案讨论、FAQ 补充

### 5.2 Enterprise

- 商业支持邮箱或工单系统
- 约定的 IM / 会议沟通渠道
- 周期性版本与问题回顾机制

---

## 6. 支持范围边界

以下内容通常属于支持范围内：

- 安装与启动问题
- 配置项使用问题
- 与当前文档一致的行为偏差
- 已发布能力的缺陷与回归
- 安全基线与部署建议

以下内容通常不属于标准支持范围：

- 客户私有代码调试
- 业务 SQL 代写
- 非公开能力的提前承诺
- 长周期定制开发
- 与第三方系统深度定制集成但无通用价值的需求

这类需求更适合：

- 定制项目
- 专项咨询
- 路线图评估

---

## 7. 版本支持建议

支持范围建议与发布策略绑定：

- 只支持当前稳定版本
- 以及最近一个稳定小版本

不建议长期维护过多历史分支，否则会显著拉高交付成本。

参考：

- [release-policy.md](file:///Users/vincent/workspace/fosun/datahub/docs/release-policy.md)

---

## 8. 安全问题处理

涉及以下问题时，应按安全问题优先处理：

- 鉴权绕过
- 行级 / 列级治理失效
- 默认配置导致的敏感暴露
- 密钥处理缺陷
- 生产环境高危攻击面

安全问题建议优先通过私密渠道报告，而不是公开在 Issue 中直接披露细节。

参考：

- [SECURITY.md](file:///Users/vincent/workspace/fosun/datahub/SECURITY.md)

---

## 9. 推荐对外口径

可以直接对外使用的支持描述：

> Community 提供公开文档与社区支持，适合试用和自助评估；Enterprise 面向正式 PoC 和生产落地，提供更明确的响应机制、部署协助和版本支持。

不建议对外承诺：

- “7x24 全托管式支持”
- “任意版本长期维护”
- “所有客户环境问题都可远程排障”

除非这些能力已经具备对应团队与流程支撑。

---

## 10. 与其他文档的关系

- 商用包装与版本矩阵：[commercial-packaging-plan.md](file:///Users/vincent/workspace/fosun/datahub/docs/commercial-packaging-plan.md)
- 发布与版本策略：[release-policy.md](file:///Users/vincent/workspace/fosun/datahub/docs/release-policy.md)
- 生产部署说明：[deployment-production.md](file:///Users/vincent/workspace/fosun/datahub/docs/deployment-production.md)
