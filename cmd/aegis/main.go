package main

import (
	"flag"
	"log"

	"github.com/wisonwang/aegis/internal/config"
	"github.com/wisonwang/aegis/internal/server"
)

// @title Aegis DataAPI
// @version 1.0
// @description Aegis — AI Data Supply Gateway. 受治理的 DataAPI + 面向 AI Agent 的 MCP 服务。默认拒绝：每条查询都经过权限引擎重写与脱敏。
// @description
// @description ## 认证与接入（如何获取凭证）
// @description
// @description ### 1. DataAPI / 管理 API（浏览器、脚本、curl）— JWT
// @description 1. 调用 `POST /api/v1/login`，请求体 `{"username":"admin","password":"***"}`，响应里的 `token` 字段即 JWT。
// @description 2. 之后所有请求带请求头：`Authorization: Bearer <token>`。
// @description 3. 默认有效期 24h（服务端 `jwt_expiry` 可配），过期后重新登录换取。
// @description 4. 在线调试：点本页右上方 **Authorize** 按钮，粘贴 `Bearer <token>` 即可。
// @description
// @description 本地演示账号：`admin` / `admin123`（由 `seed_demo` 种入，生产请立即修改）。
// @description
// @description ### 2. MCP（AI Agent 接入）— 静态 API Key
// @description AI Agent 不走本页 REST 接口，而是连接 `POST /mcp`（JSON-RPC 2.0 over HTTP），用静态 API Key 认证：
// @description 请求头 `X-MCP-API-Key: <key>`。该 key 由管理员在服务端配置 `mcp.api_key` 下发（本地演示为 `mcp-demo-key`），不经过登录接口。
// @description
// @description ### 3. 多租户
// @description 非 admin 角色的请求会被强制限定在其所属工作区；admin 可用请求头 `X-Workspace-Id: <id>` 指定目标工作区（缺省为跨工作区视图）。
// @termsOfService https://github.com/wisonwang/Aegis
// @contact.name Aegis
// @contact.url https://github.com/wisonwang/Aegis
// @license.name MIT
// @host localhost:8080
// @BasePath /
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description "Bearer <JWT> — 通过 POST /api/v1/login 用用户名密码换取，见上方「认证与接入」"
// @securityDefinitions.apikey MCPApiKey
// @in header
// @name X-MCP-API-Key
// @description "MCP 静态 API Key（仅 /mcp 端点），由管理员在服务端 mcp.api_key 配置"
func main() {
	cfgPath := flag.String("config", "config.json", "path to JSON config file (default: config.json)")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if err := server.Run(cfg); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
