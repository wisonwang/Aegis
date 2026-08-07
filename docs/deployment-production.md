# Aegis 生产部署说明

> 目标：提供一套最小可用、适合自托管客户快速落地的生产部署参考。
> 原则：Aegis 只提供 HTTP 服务，生产必须前置 TLS 反向代理；配置与密钥不内嵌在镜像中。

---

## 1. 推荐部署形态

建议优先采用以下组合：

1. `aegis` 进程或容器，仅监听内网 `127.0.0.1:8080`
2. `nginx` / `caddy` / Ingress 负责 TLS 终止
3. `conf/config.local.json` 作为生产配置模板
4. `AEGIS_JWT_SECRET`、`AEGIS_MASK_SECRET`、`AEGIS_MCP_API_KEY` 通过环境变量或 Secret 注入

仓库已提供的生产模板：

- `docker-compose.prod.yml`
- `deploy/nginx/aegis.conf`
- `deploy/systemd/aegis.service`

---

## 2. 配置文件说明

### 2.1 生产模板

生产环境请使用：

- [conf/config.local.json](file:///Users/vincent/workspace/fosun/datahub/conf/config.local.json)

该文件已经收口为更安全的默认值：

- `environment=production`
- `edition=community`
- `seed_demo=false`
- `docs_enabled=false`
- `mcp.api_key=""`
- `jwt_secret=""`
- `mask_secret=""`

这些值在生产态下**必须由环境变量或 Secret 覆盖**，否则服务会拒绝启动。

### 2.2 Demo 配置

仅开发 / 演示使用：

- [conf/config.demo.json](file:///Users/vincent/workspace/fosun/datahub/conf/config.demo.json)

不要把 demo 配置带入生产。

---

## 3. Docker Compose 生产部署

### 3.1 准备环境变量

```bash
export AEGIS_JWT_SECRET='replace-with-a-strong-random-secret'
export AEGIS_MASK_SECRET='replace-with-a-second-strong-random-secret'
export AEGIS_MCP_API_KEY='replace-with-a-strong-agent-api-key'
```

### 3.2 启动服务

```bash
docker compose -f docker-compose.prod.yml up -d --build
```

特点：

- 镜像内不再打包 demo 配置
- 服务仅绑定 `127.0.0.1:8080`
- 配置文件通过挂载方式注入
- 关键密钥由环境变量传入

---

## 4. Nginx 反向代理

参考配置：

- [deploy/nginx/aegis.conf](file:///Users/vincent/workspace/fosun/datahub/deploy/nginx/aegis.conf)

说明：

- `listen 443 ssl http2`
- `/` 全路径反代到 `127.0.0.1:8080`
- `80 -> 443` 强制跳转
- MCP 的 `POST /mcp` 与普通 API 共用同一反代入口

部署时需要替换：

- `server_name`
- 证书路径
- 反代上游地址

---

## 5. Systemd 部署

参考单元文件：

- [deploy/systemd/aegis.service](file:///Users/vincent/workspace/fosun/datahub/deploy/systemd/aegis.service)

推荐目录结构：

```text
/usr/local/bin/aegis
/etc/aegis/config.local.json
/etc/aegis/aegis.env
/var/lib/aegis
/var/log/aegis
```

环境变量文件示例：

```bash
AEGIS_ENV=production
AEGIS_JWT_SECRET=replace-with-a-strong-random-secret
AEGIS_MASK_SECRET=replace-with-a-second-strong-random-secret
AEGIS_MCP_API_KEY=replace-with-a-strong-agent-api-key
AEGIS_LOG_FORMAT=json
AEGIS_LOG_LEVEL=info
```

启用方式：

```bash
sudo cp deploy/systemd/aegis.service /etc/systemd/system/aegis.service
sudo systemctl daemon-reload
sudo systemctl enable --now aegis
sudo systemctl status aegis
```

---

## 6. 生产上线前检查

至少确认以下内容：

- 未使用 `conf/config.demo.json`
- 未开启 `seed_demo`
- 已设置 `AEGIS_JWT_SECRET`
- 已设置 `AEGIS_MASK_SECRET`
- 已设置 `AEGIS_MCP_API_KEY`
- 未开启 `docs_enabled`
- LDAP 未启用 `skip_tls_verify`
- 服务前置了 TLS
- 监听地址未直接暴露在公网

更完整的上线清单见：

- [production-readiness-checklist.md](file:///Users/vincent/workspace/fosun/datahub/docs/production-readiness-checklist.md)

---

## 7. 本地验证命令

部署相关改动建议至少本地执行：

```bash
go build ./...
go vet ./...
go test ./...
make mcp-e2e
make mcp-e2e-admin
```

CI 也已覆盖：

- build
- vet
- test
- MCP E2E smoke
- `govulncheck`

---

## 8. 关联文档

- [README.md](file:///Users/vincent/workspace/fosun/datahub/README.md)
- [SECURITY.md](file:///Users/vincent/workspace/fosun/datahub/SECURITY.md)
- [production-readiness-checklist.md](file:///Users/vincent/workspace/fosun/datahub/docs/production-readiness-checklist.md)
