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
// @termsOfService https://github.com/wisonwang/Aegis
// @contact.name Aegis
// @contact.url https://github.com/wisonwang/Aegis
// @license.name MIT
// @host localhost:8080
// @BasePath /
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description "Bearer <JWT> — 通过 POST /api/v1/login 获取"
func main() {
	cfgPath := flag.String("config", "", "path to JSON config file (optional)")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if err := server.Run(cfg); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
