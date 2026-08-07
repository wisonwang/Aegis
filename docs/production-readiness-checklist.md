# Aegis 生产上线检查表

> 目标：把当前仓库从“可演示、可试用”收口到“可进客户 PoC / 可控上线”的状态。
> 使用方式：按 `P0 -> P1 -> P2` 推进；每项都要求有明确负责人、完成证据和回归结果。

---

## 1. 总体判断

当前项目已经具备较完整的核心产品能力：

- 治理网关：表 / 行 / 列级治理、动态脱敏、默认拒绝
- AI 入口：DataAPI、MCP、NL2SQL、Metrics、Datasets
- 管理能力：RBAC、审批、审计、告警、OIDC、LDAP

但要进入正式商用推广，仍有四类关键缺口需要先收口：

1. **默认配置仍偏 demo，不适合作为生产入口**
2. **安全基线尚未升级为启动强约束**
3. **发布与交付链路缺少标准化生产形态**
4. **文档和版本分层口径尚未完全统一**

---

## 2. 上线门槛

### 2.1 P0：上线前必须完成

| 检查项 | 当前风险 | 建议动作 | 完成证据 |
|----|----|----|----|
| 默认密钥清理 | `jwt_secret`、`mcp-demo-key`、默认 root 数据库凭据可能误入生产 | 删除镜像内 demo 配置；生产强制通过环境变量或 Secret 注入密钥 | 生产镜像不再包含 `conf/config.local.json`；启动校验通过 |
| demo 播种关闭 | `seed_demo=true` 会创建固定 admin / analyst 账号与演示数据 | 生产默认关闭；首次启动改为初始化管理员流程 | 生产配置模板中 `seed_demo=false`；首次启动文档可复现 |
| 掩码密钥强制 | 未配置 `AEGIS_MASK_SECRET` 时回退开发默认值 | 在生产模式下未设置即拒绝启动 | 启动日志与回归测试证明 fail-fast |
| edition 默认值 | 仓库默认是 `enterprise`，容易造成能力边界误判 | 默认改为 `community`；企业能力只通过 license 解锁 | 默认配置、README、示例全部统一 |
| Swagger / Docs 暴露 | `/admin/api/docs` 默认暴露管理 API 结构 | 生产默认关闭或要求管理员认证 | 生产配置模板和回归测试 |
| Gin / 调试模式 | 调试模式可能暴露多余日志和路由信息 | 默认 release mode | 启动日志显示 release；无 debug 路由输出 |
| TLS 前置要求 | 若直接明文监听，JWT / API Key 易泄漏 | 提供反向代理模板，并对公网明文监听强告警 | nginx / Caddy 模板与安全部署文档 |

### 2.2 P1：进入 PoC 前建议完成

| 检查项 | 当前风险 | 建议动作 | 完成证据 |
|----|----|----|----|
| CI 守门 | 目前主要依赖手工 `go test` 和 `make mcp-e2e` | 增加 CI：build / test / smoke / govulncheck / gosec | 仓库内工作流与运行记录 |
| 供应链安全 | 缺 SBOM、镜像扫描、签名发布 | 加入 syft / grype / Trivy / cosign | Release 产物含 SBOM 和签名 |
| 生产部署模板 | 缺少 `compose.prod`、反代、systemd、K8s 最小模板 | 补标准部署模板 | 文档与样例目录可直接运行 |
| 日志与告警接入 | 有审计与 webhook，但交付路径未标准化 | 补 Prometheus、Webhook、SIEM 对接说明 | 文档和最小配置示例 |
| 版本兼容声明 | Go 版本、发布版本、支持策略未统一 | 明确最低支持版本和升级策略 | README / release policy / support policy |

### 2.3 P2：面向正式商用推广完善

| 检查项 | 当前风险 | 建议动作 | 完成证据 |
|----|----|----|----|
| 商业授权硬隔离 | 企业能力仍随单二进制分发，门禁偏软 | 分离 license 签名体系；考虑私有模块或编译期隔离 | 架构方案与实施结果 |
| 多租户 / SIEM / HA | 大客户关键能力仍在进行中 | 形成企业版里程碑路线图 | ROADMAP 与能力矩阵 |
| 法务与合规材料 | 缺第三方许可清单、商标与支持策略 | 补 `NOTICE`、依赖许可证清单、支持政策 | 仓库新增法务文档 |
| 售前资料包 | 技术叙事强，但销售物料不足 | 补一页纸、FAQ、客户案例模板、架构图 | docs/ 或官网资料包 |

---

## 3. 整改任务表

### 3.1 安全与默认配置

| 优先级 | 任务 | 影响 | 工作量 |
|----|----|----|----|
| P0 | 删除镜像内 demo 配置 | 避免开发配置进生产 | 0.5 天 |
| P0 | 增加启动时密钥校验 | 避免默认密钥上线 | 0.5 天 |
| P0 | 关闭生产默认 `seed_demo` | 避免固定账号密码暴露 | 0.5 天 |
| P0 | 默认 release mode | 降低调试信息暴露 | 0.5 天 |
| P0 | Swagger 生产默认关闭 | 降低管理面暴露 | 0.5 天 |

### 3.2 工程与发布

| 优先级 | 任务 | 影响 | 工作量 |
|----|----|----|----|
| P1 | 增加 CI 工作流 | 防止回归与质量漂移 | 1 天 |
| P1 | 接入 `govulncheck` / `gosec` | 补静态安全守门 | 0.5 天 |
| P1 | 增加 SBOM 与镜像扫描 | 提升供应链可信度 | 1 天 |
| P1 | 发布产物签名 | 支撑企业交付可信链路 | 1 天 |

### 3.3 交付与文档

| 优先级 | 任务 | 影响 | 工作量 |
|----|----|----|----|
| P1 | 重构 README 首页 | 提升推广转化与认知清晰度 | 1 天 |
| P1 | 提供 `docker compose.prod` / nginx / systemd 模板 | 降低部署门槛 | 1 天 |
| P1 | 统一接口示例路径 | 避免用户照抄失败 | 0.5 天 |
| P2 | 增加支持策略与 release policy | 提升客户信任 | 0.5 天 |

---

## 4. 上线验收标准

满足以下条件后，项目可进入“对外 PoC 推广”阶段：

- 生产镜像不再携带 demo 配置和默认凭据
- 未配置关键密钥时服务拒绝启动
- 默认 edition、README、能力矩阵三者口径一致
- 至少具备一条自动化 CI 流水线
- 至少具备一套标准生产部署模板
- MCP Demo、基础单测、构建流程在 CI 中可跑通

满足以下条件后，项目可进入“正式商用推广”阶段：

- 发布链路具备镜像扫描、SBOM、签名
- 许可与企业能力隔离机制明确
- 支持策略、版本策略、第三方许可清单齐备
- 售前资料包已形成标准模板

---

## 5. 推荐推进顺序

### 第一周：先收口安全与默认值

- 清理 demo 配置进入生产的问题
- 增加启动强校验
- 关闭不必要的默认暴露面
- 统一 README / 配置 / 分层口径

### 第二周：补工程化与交付面

- 上 CI
- 补部署模板
- 增加扫描、SBOM、签名
- 输出正式发布说明

### 第三周：补商用资料包

- 一页式价值主张
- 版本能力矩阵
- FAQ
- 典型客户场景与架构图

---

## 6. 关联文档

- 产品设计：[product-design.md](file:///Users/vincent/workspace/fosun/datahub/docs/product-design.md)
- 开源发布与采用加速：[launch.md](file:///Users/vincent/workspace/fosun/datahub/docs/launch.md)
- 安全配置指南：[security-config-guide.md](file:///Users/vincent/workspace/fosun/datahub/docs/security-config-guide.md)
- 生产部署说明：[deployment-production.md](file:///Users/vincent/workspace/fosun/datahub/docs/deployment-production.md)
- FAQ：[faq.md](file:///Users/vincent/workspace/fosun/datahub/docs/faq.md)
- 支持策略：[support-policy.md](file:///Users/vincent/workspace/fosun/datahub/docs/support-policy.md)
- 发布策略：[release-policy.md](file:///Users/vincent/workspace/fosun/datahub/docs/release-policy.md)
- 第三方许可说明：[third-party-notices.md](file:///Users/vincent/workspace/fosun/datahub/docs/third-party-notices.md)
- 项目蓝图：[BLUEPRINT.md](file:///Users/vincent/workspace/fosun/datahub/BLUEPRINT.md)
