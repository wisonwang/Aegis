# Aegis 第三方许可说明

> 目标：为商用沟通和法务检查提供一份仓库内可直接查看的第三方依赖许可说明。
> 说明：本文件基于当前 `go.mod` 中的主要直接依赖整理，适合作为“初始版 notices”。正式商用前，建议结合自动化工具生成更完整的依赖许可证清单。

---

## 1. 项目自身许可

Aegis 当前采用：

- MIT License

详见：

- [LICENSE](file:///Users/vincent/workspace/fosun/datahub/LICENSE)

---

## 2. 主要直接依赖

以下清单基于当前 `go.mod` 中的主要直接依赖整理：

| 依赖 | 用途 | 常见许可类型 |
|------|------|--------------|
| `github.com/gin-gonic/gin` | Web 框架 | MIT |
| `github.com/coreos/go-oidc/v3` | OIDC 集成 | Apache-2.0 |
| `github.com/go-ldap/ldap/v3` | LDAP 集成 | MIT |
| `github.com/go-sql-driver/mysql` | MySQL 驱动 | MPL-2.0 |
| `github.com/lib/pq` | PostgreSQL 驱动 | MIT |
| `github.com/golang-jwt/jwt/v5` | JWT 处理 | MIT |
| `github.com/google/uuid` | UUID | BSD-3-Clause |
| `github.com/prometheus/client_golang` | Prometheus 指标 | Apache-2.0 |
| `github.com/swaggo/files` | Swagger 静态文件 | MIT |
| `github.com/swaggo/gin-swagger` | Swagger UI 集成 | MIT |
| `github.com/swaggo/swag` | Swagger 文档生成 | MIT |
| `github.com/xwb1989/sqlparser` | SQL 解析 | Apache-2.0 |
| `go.mongodb.org/mongo-driver` | MongoDB 驱动 | Apache-2.0 |
| `golang.org/x/crypto` | 加密工具 | BSD-3-Clause |
| `golang.org/x/oauth2` | OAuth2 能力 | BSD-3-Clause |
| `modernc.org/sqlite` | SQLite 驱动 | BSD-3-Clause 风格许可 |

说明：

- 上表中的许可类型是基于这些项目的常见公开许可整理
- 在正式商用交付前，建议通过自动化工具重新扫描并固化精确版本对应的许可证结果

---

## 3. 为什么这里先列“主要直接依赖”

因为从商用准备角度，第一步通常先回答两个问题：

1. 项目是否依赖了明显不适合商用分发的许可证？
2. 是否存在需要额外关注或披露的驱动、框架、文档生成组件？

当前这份文件优先覆盖直接依赖，是为了先建立一份“可被审阅”的基础材料。

后续若进入正式商用交付，建议再补：

- 完整传递依赖许可证清单
- 依赖版本锁定对应的许可证快照
- NOTICE / attribution 打包策略

---

## 4. 推荐生成方式

建议在后续供应链阶段引入自动化工具，例如：

- `go list -m all`
- `go mod graph`
- `syft`
- 许可证扫描工具或 SBOM 工具链

推荐目标：

- 每次 release 自动产出依赖清单
- 每次 release 自动产出 SBOM
- 将第三方许可证结果与发布产物一起归档

---

## 5. 当前使用建议

这份文件当前适合用于：

- 售前回答“是否能商用”
- 初步法务沟通
- 客户安全问卷中的许可证说明
- 内部发布准备检查

这份文件当前**不应被视为**：

- 最终法律意见
- 全量传递依赖许可证声明
- 可替代自动化合规扫描的最终产物

---

## 6. 后续建议

进入正式商用阶段前，建议至少补齐：

- `NOTICE` 文件
- 自动化依赖许可证清单
- SBOM
- 发布产物归档策略

参考：

- [production-readiness-checklist.md](file:///Users/vincent/workspace/fosun/datahub/docs/production-readiness-checklist.md)
- [release-policy.md](file:///Users/vincent/workspace/fosun/datahub/docs/release-policy.md)
