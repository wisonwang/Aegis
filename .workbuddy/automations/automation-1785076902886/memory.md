2026-07-26 22:43: 本次迭代完成 OIDC SSO 身份对接 + 健康探针增强。
- 新增 internal/auth/oidc.go：OIDC Provider 发现、PKCE 授权码流程、ID Token 验证、Claim→角色映射
- 新增 internal/api/oidc.go：/api/v1/auth/oidc/login + /api/v1/auth/oidc/callback，用户 auto-provisioning
- 修改 store.go：users 表新增 external_id 列（ALTER TABLE 向后兼容），GetUserByExternalID
- 修改 config.go：OIDCConfig 段 + 环境变量覆盖
- 修改 server.go：注册 OIDC 路由，增强 /api/v1/ready 就绪探针
- 更新 BLUEPRINT.html v0.3 + README.md
- 全部测试通过（含新增 oidc_test.go）

2026-07-26 23:39: 本轮迭代完成「结构化日志」+ 顺带修复首装种子致命 bug。
- 新增 internal/logging（标准库 log/slog，零外部依赖）：Init(Config{format,level}) 装全局 logger；Middleware 输出 HTTP 访问日志（req_id 关联、跳过 health/ready/metrics）；proxy.audit 对 denied/error 输出 `governance decision` 结构化事件，与访问日志共享 req_id。
- 新增 internal/logging/logging_test.go（Init JSON/Text、level 过滤、ctx 属性、Middleware、探针跳过）。
- 修改 config.go（LoggingConfig + AEGIS_LOG_FORMAT/LEVEL）、server.go（Init + 包裹 Middleware + 替换 log.Printf）、proxy.go（治理决策事件）。
- 修复首装 bug：users.external_id TEXT UNIQUE 下非 OIDC 用户默认空串导致第二个本地用户 UNIQUE 冲突被静默丢弃（analyst/mcp-agent 建不出）；改 User.ExternalID 为 sql.NullString（扫描 NULL 安全），CreateUser 空值绑 NULL。修复前此 bug 被 NULL 修复暴露出 SELECT 扫 NULL 进 string 报错。
- 更新 BLUEPRINT.html v0.3→v0.4（标记结构化日志已落地）+ README.md（结构化日志章节）。
- 验证：go build/vet 干净，go test ./... 全过；live 冒烟确认登录→受治理查询→越权拒绝全链路，结构化日志正确产出治理决策事件（WARN）且与访问日志 req_id 串联。

2026-07-27 01:33: 本轮迭代完成「脱敏算法库」（蓝图阶段3 脱敏算法库）。
- 新增 internal/proxy/mask.go 密钥化策略 tokenize（HMAC 确定性伪名）+ fpe（base-10 平衡 Feistel 格式保留加密，纯数字保长保型，可还原）。
- 新增 config.MaskSecret + env AEGIS_MASK_SECRET；server.go 启动注入，未设回退开发默认并 WARN；validMaskStrategy 接纳两种策略（数据源+数据集掩码共用）。
- 单测覆盖确定性/定长/保型/可还原；BLUEPRINT.html→v0.5、README 同步。
- 验证：go build/vet/test 全过；fresh DB live 冒烟 health/ready OK，正确输出 mask-secret 告警。
- 顺带清理占用 :8080 的残留 ./aegis 开发进程；提交 b1d8efe（含上轮未提交的 structured logging 迭代）。

2026-07-27 03:30: 本轮迭代完成「PostgreSQL 端到端治理验证」（蓝图阶段2 待办项）。
- 本地 Homebrew 启 Postgres 16，建 aegis_svc 角色 + aegis_demo 库（orders/customers 双租户 + 未授权 salary 表）。
- 新增 internal/proxy/pg_e2e_test.go（gated on AEGIS_TEST_PG_DSN）：默认拒绝 / 行策略(:attr) / 列脱敏 / admin 旁路 / ListTables 治理 6 项断言，对 live PG 全过。
- build/vet 干净；BLUEPRINT 三处标记 PostgreSQL 已验证；提交 8116abc。
- 下一步：蓝图剩余待办 = 数据库原生 RLS 双层加固、LDAP 接入。

2026-07-27 04:39: 本轮迭代完成「LDAP / Active Directory 身份对接」（蓝图阶段3 企业治理，原「短期」待办项）。
- 新增 internal/auth/ldap.go：Directory 接口（可 mock 单测）+ go-ldap/ldap/v3 实现，三步绑定校验（服务账号检索→解析用户 DN→用户 DN 绑定），可选组检索驱动 claim_mappings 组→角色映射。
- 新增 internal/api/ldap.go（POST /api/v1/auth/ldap/login）+ internal/api/sso.go（抽取 OIDC/LDAP 共用 provision+JWT 签发，OIDC 回调重构复用）。
- config.go 新增 LDAPConfig + AEGIS_LDAP_* 覆盖；server.go 注册路由；config.json 增禁用示例块；新增依赖 go-ldap/ldap/v3。
- 单测覆盖角色映射/供给+令牌/默认角色/缺失角色自动创建/无效凭证401/缺失body400；go build/vet/gofmt/test 全过；live 冒烟确认禁用不可达、启用拨号失败返回401。
- BLUEPRINT.html→v0.6、README 新增 LDAP 章节；提交 2ed0500。
- 蓝图剩余企业门槛待办：权限审批流、数据分类分级自动推荐脱敏、多租户工作区；阶段2 残留：数据库原生 RLS 双层加固（嵌套子查询边界）。

2026-07-27 09:06: 本轮完成「数据分类分级自动推荐默认脱敏策略」（蓝图阶段3 企业治理，原「短期」②）+ 顺带补交此前未提交的工作树改动。
- 补交 0e2f33e：RLS 双层加固（行策略基于 sqlparser.Walk 递归注入嵌套子查询，engine.go+engine_test.go 182 行单测，此前改动留工作树未提交）。
- 新增 bdc0820：internal/proxy/maskrec.go（RecommendMask 纯函数，精确标签>级别+列名>级别兜底，public/internal 不脱敏）；POST /admin/api/datasources/{id}/masks/recommend（默认 dry-run 预览，apply=true 按角色或全量非 admin 落地，安全默认要求显式指定目标角色）；seed.go 全新安装自动为 analyst 套用；单测 20+ 场景；go build/vet/test 全过，live 冒烟 dry-run+apply 通过。
- BLUEPRINT.html→v0.8（自动推荐标记已完成、能力矩阵分类分级✅、P1③ 新增行）；README 新增「脱敏策略自动推荐」小节。
- 蓝图剩余企业门槛待办：权限审批流、多租户工作区；阶段4 残留：NL2SQL 安全网关、语义指标层。

2026-07-27 09:58: 本轮完成「权限审批流」（蓝图阶段3 企业治理，原剩余企业门槛待办①）。
- 新增 internal/store/approval.go：approval_requests 表迁移 + ApprovalRequest 模型 + Create/Get/List/Resolve 方法；审批生效联动创建 table_permissions 授权、回收按 granted_perm_id 删除，闭环可逆。
- 新增 internal/api/approval.go：POST /admin/api/approvals（任意登录用户提交）、GET /api/v1/me/approvals（本人申请）、GET/POST /admin/api/approvals(+approve/reject/revoke，admin）；校验数据源/角色/ops，重复批准 409 防护。
- server.go 注册路由（提交用 Authenticate、审批用 RequireAdmin）；前端 web 新增「审批流」tab（申请表单+我的申请+审批台双视图）。
- 顺带修复潜伏缺陷：ListTablePermissions / ListRowPolicies 硬编码 `WHERE role_id=?`，roleID="" 时静默返回空（治理页「加载治理」权限/策略列表一直为空）；改为 "" 表示不过滤角色，与 ListColumnMasks 语义一致；新增 permissions_list_test.go 回归测试。
- 单测（store+api 闭环 13 断言）+ 全量 go build/vet/test 全过；live 冒烟（隔离 DB:8099）端到端验证 提交→pending→approve 落授权→revoke 精确删授权→非 admin 403。
- BLUEPRINT.html→v0.9（审批流标记已完成、补 ADR 说明、阶段3 转进行中、tag 行加标签）；README 新增「权限审批流」章节。提交 de3e097。
- 蓝图剩余企业门槛待办：多租户工作区；阶段4 残留：NL2SQL 安全网关、语义指标层。
