# Aegis v0.6.0 — Release Notes（草稿）

> **用途**：本文件是 GitHub Release 正文草稿，打 tag 时直接粘贴到 `gh release create` 的 `--notes-file`。
> **版本号**：以你实际打的 tag 为准（本文按 `release-policy.md` 语义化建议暂用 **v0.6.0**）。
> **前置**：打 tag 前请先提交当前工作区改动（含本次 `test/` pytest 套件、`ci.yml`/`Makefile` 的 CI 接入），否则 release 流水线跑不到最新代码。

---

## Summary

Aegis 是一个**自托管、治理默认开启**的 AI 数据网关（AI Data Supply Gateway）。单个二进制把你的内部数据库变成**受控的 Agent 工具**：LLM/Agent 永远看不到数据库凭据，每一条查询都被强制穿过表 / 行 / 列三级治理、脱敏、行为限制与审计链路。

v0.6.0 是「楔子战略」的又一次推进：在**同一个治理内核**之上，补齐了数据集目录管理、多租户工作区隔离、数据库级防拖库（SQL 层 LIMIT 注入），并把端到端测试接进 CI —— 让「主干可发布」从口号变成流水线绿标。

---

## Highlights

### 治理 / 安全
- **SQL 层 LIMIT 注入（数据库级防拖库）**：`permission.Engine` 在数据库引擎层面注入/替换 `LIMIT max_rows`，无 LIMIT 则补、低于上限保留用户值、超出则钳制为上限；即便应用层被绕过，也拖不走全表（`ebd7cc5`）。
- **统一行为限制，admin 不再豁免**：所有执行路径统一 `max_rows / max_affected_rows / max_bytes / rate_per_min`，`admin_exempt` 默认 `false`；admin 仍绕过*权限*，但不再绕过*行数/超时/频率*（`3a72ecf`）。
- **多租户工作区隔离 GA（ADR-001）**：共享 schema + `workspace_id` 判别列 + 仓储层强制作用域，HTTP 与 MCP 共用同一决策矩阵，fail-closed（`44a3dc0`，`f932afd` 支持 OIDC/LDAP group→workspace 映射）。
- **纵深防御基线（ADR-0003）**：认证 → 频率 → 权限 → 写保护 → 超时 → 截断 → 脱敏 → 审计 → SQL 层 LIMIT，共 9 层；DDL（CREATE/DROP/ALTER/TRUNCATE）默认拒绝，写操作无 WHERE 且非 admin+Guard 直接 403。
- **open-core 能力门控**：Community/Enterprise 能力通过 `capability` gate 隔离（`32c9b8f`）。

### MCP / Agent 体验
- **MCP 工具完备性验证**：11 个工具全注册可用，治理穿透生效（`query` 的 `rewritten_sql` 含行策略派生表注入 + 列脱敏 + LIMIT 注入，与 DataAPI 共用治理引擎，不可绕过）。
- **数据集目录管理（D7）**：嵌套文件夹树 + 消费端可折叠树；数据集归属目录，组织元数据不影响治理键。
- **NL2SQL 安全网关 + 语义指标层**：自然语言生成的 SQL 回灌同一 `Proxy.Execute` 路径；管理员预定义受控指标模板，Agent 按名消费，杜绝指标漂移。
- **API 文档自动化**：路由层迁移到 GIN + gin-swagger，自动萃取 60+ 端点；后台「API 接入」tab 内嵌 Swagger UI（`121d254`，`6099b94`）。

### 部署 / 运维
- **控制面支持 MySQL 后端**：除 SQLite 外，控制面 store 现已支持 MySQL + API-key 认证（`4aa5d44`），满足生产高可用诉求。
- **发布流水线加固**：`release.yml` 产出 SBOM（Syft/Trivy）+ cosign 签名，Release 自动生成发行说明与制品校验和。
- **端到端测试接进 CI**：`test/` 下 29 个 pytest 用例（DataAPI + MCP 全流程，含多租户治理、脱敏、数据集、指标）已接入 `.github/workflows/ci.yml` 与 `make ci-smoke`，每次 push/PR 自动回归，打 tag 前亦回归。
- **管理界面编辑能力补齐**：数据源/用户编辑、治理三格、指标管理、工作区管理（`eaa8439`）。

### 文档 / 示例
- **商业化物料就绪**：一页式介绍（`docs/one-pager.md`）、可复现演示 walkthrough（`docs/demo-walkthrough.md`）、竞品分析、MCP 演示案例、GitHub 社交预览图（`docs/pics/aegis-social-preview.png`）。
- **OSS 采用加速清单**（`docs/launch.md`）、README 徽章、`CONTRIBUTING` / `SECURITY` / `CHANGELOG` 齐备。

---

## Upgrade Notes

- **配置变更**
  - `limits` 默认值已改为安全值（`max_rows=10000`、`max_affected_rows=10000`、`max_bytes=4MB`、`rate_per_min=60`、`admin_exempt=false`）。
  - **生产必须**设置 `AEGIS_JWT_SECRET` 与 `AEGIS_MASK_SECRET`（脱敏 `tokenize`/`fpe` 依赖该密钥，未配会回退不安全默认值并告警）。
- **API / 行为变更**
  - 数据集 / 数据源的查询键为 **UUID**（`/api/v1/datasets/:id` 的 `:id` 是 `DatasetInfo.id`，非 name）；如需按名查找请先 list 取 id。
  - DDL 被 `permission.Rewrite` 默认拒绝；写操作无 WHERE 且非 admin+Guard → 403；UPDATE/DELETE 超过 `max_affected_rows` 预检拒绝。
- **部署模板变更**
  - `docker-compose.yml` 已精简；release 制品新增 SBOM（`aegis-sbom.spdx.json`）与 cosign 签名（`.sig`/`.pem`）。
- **Community / Enterprise 边界**
  - **免费（Community）**：三级治理、MCP、`nl2sql`、审计、SQLite 控制面。
  - **商用（Enterprise）**：Datasets / Metrics / 审批流 / 多租户工作区 / SIEM 外发 / HA。

---

## Verification

打 tag 前建议在本地跑通（CI 也会自动跑）：

- [ ] `go build ./...`
- [ ] `go vet ./...`
- [ ] `go test ./...`
- [ ] `make mcp-e2e`
- [ ] `make mcp-e2e-admin`
- [ ] `make test-py`  ← 新增：pytest 端到端（DataAPI + MCP 全流程，29 用例）
- [ ] `make release-artifacts VERSION=v0.6.0`
- [ ] `make release-sbom`

---

## Artifacts

- Release archives（`aegis_v0.6.0_*.tar.gz`）
- `checksums.txt`
- `aegis-sbom.spdx.json`（SBOM）
- `aegis-vulns-trivy.json`（漏洞扫描）
- cosign 签名 `*.sig` / `*.pem`

---

## 已知限制 / 下一步

- 度量埋点（utm + Star/Clone 跟踪）仍处设计待定（见 `BLUEPRINT.md` 阶段 A 行动 #5）。
- 数据集/数据源按 UUID 查的取舍仅文档化；未来可考虑让 `/api/v1/datasets/:id` 兼融 name 查找以提升 Agent 消费体验。
- 分类分级自动推荐脱敏、权限审批流 UI、DB 原生 RLS 双层加固为 Enterprise 后续门槛。
