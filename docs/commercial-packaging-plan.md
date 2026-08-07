# Aegis 商用包装方案

> 目标：把当前项目从“技术上成立”进一步包装成“客户能理解、销售能讲、版本能卖”的产品形态。
> 适用范围：官网首页、README 首页、售前介绍、一页纸、客户 PoC 沟通、版本说明。

---

## 1. 包装目标

当前 Aegis 的核心优势已经比较清晰：

- 自托管、单二进制、部署轻
- 治理默认开启且不可绕过
- 面向 Agent / MCP / NL2SQL 的 AI 原生数据入口
- 支持 DataAPI、Metrics、Datasets、审计、审批、脱敏

但外部客户从“知道这是什么”到“愿意试用/采购”，还需要补齐三层包装：

1. **价值包装**：一句话说清楚产品价值
2. **版本包装**：Community / Enterprise 边界清楚
3. **销售包装**：客户什么时候该用、为什么现在就该用

---

## 2. 一句话定位

### 2.1 推荐主定位

> **Aegis 是面向 AI Agent 的受治理数据网关，把内部数据库变成可安全调用的 DataAPI 和 MCP 工具。**

### 2.2 推荐辅助描述

- 给 Agent 用的数据访问层，而不是又一个重型数据目录
- 让 LLM 生成的 SQL 也绕不过权限、脱敏和审计
- 用最轻量的方式，把“数据库直连”升级成“治理后供给”

### 2.3 不建议的表述

- 不建议说“统一数据平台”
- 不建议说“企业级数据中台”
- 不建议说“替代 DataHub / Collibra / Alation”

原因：

- 当前真正强的是**治理执行闭环**，不是全量资产目录与血缘管理
- 一旦表述过重，客户会拿目录、血缘、质量、主数据、数据开发平台来对标，容易失焦

---

## 3. 目标客户画像

### 3.1 第一优先客户

| 客户画像 | 典型痛点 | Aegis 切入点 |
|----|----|----|
| AI / 平台工程团队 | 想把 Agent 接数据库，但不敢裸连 | 5 分钟起网关，直接提供 MCP + 治理 |
| 中型企业数据负责人 | 没有大厂级治理平台，但有合规压力 | 默认拒绝、动态脱敏、统一审计 |
| 强监管行业创新团队 | 想做问数、Copilot、Agent，但安全过不了 | 用 Aegis 作为前置治理层 |

### 3.2 第二优先客户

| 客户画像 | 典型痛点 | Aegis 切入点 |
|----|----|----|
| 酒旅 / 零售 / 医疗数据团队 | 经营问数场景多，PII 风险高 | 用数据产品 + 指标 + 脱敏满足业务分析 |
| 软件服务商 / ISV | 想给产品补 AI 问数能力 | 把数据库能力包装成 API / MCP 交付给 Agent |

---

## 4. 价值主张

### 4.1 客户价值

| 价值点 | 客户能听懂的话 |
|----|----|
| 安全 | Agent 不再直连数据库，SQL 也不会绕过治理 |
| 速度 | 单二进制、自托管、5 分钟起服务 |
| 可控 | 表、行、列、脱敏、审计统一在网关层 |
| AI 原生 | MCP、NL2SQL、Metrics、Datasets 一次到位 |
| 可商用扩展 | 可先开源试用，再升级企业能力 |

### 4.2 推荐销售话术

- “Aegis 不是帮你再造一个数据平台，而是先把 Agent 连数据库这件事变安全。”
- “你不用先做完整的数据目录建设，也能先把问数和 Copilot 场景跑起来。”
- “真正的价值不在模型会不会写 SQL，而在模型写出来的 SQL 是否还受治理。”

---

## 5. 版本分层建议

### 5.1 推荐版本矩阵

| 能力 | Community | Enterprise | 说明 |
|----|----|----|----|
| 表 / 行 / 列治理 | Yes | Yes | 核心开源能力，必须单独有用 |
| 动态脱敏 | Yes | Yes | Community 护城河桌腿 |
| DataAPI | Yes | Yes | 基础入口 |
| MCP 服务 | Yes | Yes | Aegis 关键差异化入口 |
| NL2SQL | Yes | Yes | 形成 AI 原生叙事 |
| OIDC / LDAP | Yes | Yes | 建议继续保留免费，降低采用门槛 |
| 基础审计 / 基础告警 | Yes | Yes | 保留最小可信闭环 |
| Datasets | No | Yes | 企业版价值增强 |
| Metrics | No | Yes | 企业版价值增强 |
| 审批流 | No | Yes | 企业版合规增强 |
| 多租户工作区 | No | Yes | 企业级隔离能力 |
| SIEM 转发 | No | Yes | 强监管客户关键需求 |
| HA 控制面 | No | Yes | 企业交付稳定性能力 |

### 5.2 分层原则

- Community 必须单独成立，不能成为“没法用的阉割版”
- Enterprise 不卖基础网关，卖的是**组织级治理增强**
- 收费点应聚焦在“大客户明确愿意为之付费的门槛能力”

---

## 6. 不同渠道的话术版本

### 6.1 README 首页

适合强调：

- 一句话定位
- 30 秒上手
- 3 到 5 个核心卖点
- Community / Enterprise 能力矩阵
- Demo 与接入示例

### 6.2 售前一页纸

适合强调：

- 客户痛点
- 典型架构图
- 3 个典型场景
- 安全闭环
- 版本升级路径

### 6.3 客户 PoC 沟通

适合强调：

- 先解决 Agent 直连裸库的风险
- 先跑单一问数场景，而不是全平台替换
- PoC 成功标准要聚焦

推荐 PoC 成功标准：

- Agent 可通过 MCP 调用受治理数据
- 至少 1 个真实数据源接通
- 至少 1 条行级策略和 1 组脱敏规则生效
- 审计日志能完整留痕

---

## 7. 典型场景模板

### 场景 1：企业内部 Copilot 问数

- 用户问题：销售额、客户数、订单趋势
- 核心风险：Agent 直接看到敏感客户信息
- Aegis 方案：NL2SQL + 行列治理 + 动态脱敏 + 审计

### 场景 2：运营分析晨会备数

- 用户问题：经营指标、数据产品复用、权限口径差异
- 核心风险：分析师与管理员看到同一份不该一致的数据
- Aegis 方案：Metrics + Datasets + 基于角色的治理

### 场景 3：面向客户的 AI 数据助手

- 用户问题：SaaS 产品内嵌问数助手
- 核心风险：租户数据隔离、查询失控
- Aegis 方案：多租户工作区 + 限流 + 估算 + 审计

---

## 8. 销售与推广资料建议

### 8.1 必备资料

- 一页式产品介绍
- Community / Enterprise 能力矩阵
- 典型架构图
- 3 个客户场景案例
- FAQ
- 支持策略
- 发布策略
- 第三方许可说明

### 8.2 FAQ 建议题目

- Aegis 和 MCP Server 的区别是什么？
- Aegis 和传统数据目录有什么不同？
- 为什么不让 Agent 直连数据库？
- 如果我已经有 DataHub / Collibra，还需要 Aegis 吗？
- Community 和 Enterprise 有什么区别？
- 是否支持私有化部署？

### 8.3 官网或 README 首屏建议结构

1. 一句话定位
2. 30 秒上手
3. 核心能力
4. 典型架构图
5. 能力矩阵
6. Demo / 示例入口
7. 深入文档链接

---

## 9. 当前不宜过度承诺的点

以下能力可以讲路线图，但不建议在商用宣传中当作已成熟卖点：

- 全量资产目录与数据血缘
- 自动 PII 扫描
- 默认启用的完整多租户体系
- SIEM 审计流标准化转发
- HA 控制面与企业级高可用

建议表述方式：

- “已纳入企业版路线图”
- “适合在需要这些能力的场景中联合现有目录平台使用”
- “当前优势在治理执行闭环，而不是目录广度”

---

## 10. 推荐推进节奏

### 第一步：先统一对外口径

- 统一 README、BLUEPRINT、产品设计、发布文档
- 固定一句话定位
- 固定能力矩阵

### 第二步：再收口生产化能力

- 修正默认配置
- 补部署模板
- 补发布与供应链流程

### 第三步：最后做推广物料

- 一页式介绍
- FAQ
- 客户案例模板
- 演示脚本与截图

---

## 11. 关联文档

- 生产上线检查表：[production-readiness-checklist.md](file:///Users/vincent/workspace/fosun/datahub/docs/production-readiness-checklist.md)
- FAQ：[faq.md](file:///Users/vincent/workspace/fosun/datahub/docs/faq.md)
- 支持策略：[support-policy.md](file:///Users/vincent/workspace/fosun/datahub/docs/support-policy.md)
- 发布策略：[release-policy.md](file:///Users/vincent/workspace/fosun/datahub/docs/release-policy.md)
- 第三方许可说明：[third-party-notices.md](file:///Users/vincent/workspace/fosun/datahub/docs/third-party-notices.md)
- 产品设计：[product-design.md](file:///Users/vincent/workspace/fosun/datahub/docs/product-design.md)
- 开源发布与采用加速：[launch.md](file:///Users/vincent/workspace/fosun/datahub/docs/launch.md)
- 项目蓝图：[BLUEPRINT.md](file:///Users/vincent/workspace/fosun/datahub/BLUEPRINT.md)
