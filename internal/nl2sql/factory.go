package nl2sql

import (
	"fmt"
	"time"

	"github.com/wisonwang/aegis/internal/config"
)

// NewGenerator builds the configured generator. It returns (nil, nil) when
// NL2SQL is disabled, and (nil, err) on a misconfiguration (e.g. enabled
// without an API key). Callers should treat a nil generator as "feature off".
func NewGenerator(cfg config.NL2SQLConfig) (Generator, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("nl2sql is enabled but no api_key is configured")
	}
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	return NewLLMGenerator(LLMConfig{
		BaseURL:    cfg.BaseURL,
		APIKey:     cfg.APIKey,
		Model:      cfg.Model,
		Timeout:    timeout,
		MaxRetries: cfg.MaxRetries,
	}), nil
}
