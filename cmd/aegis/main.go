package main

import (
	"flag"
	"log"

	"github.com/fosun/aegis/internal/config"
	"github.com/fosun/aegis/internal/server"
)

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
