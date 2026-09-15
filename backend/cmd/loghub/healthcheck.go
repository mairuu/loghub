package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/mairuu/loghub/backend/internal/platform/config"
)

type healthCheckConfig struct {
	ListenAddr string `env:"LISTEN_ADDR" default:":8080"`
}

func healthCheck(ctx context.Context) error {
	cfg, err := config.Load[healthCheckConfig]()
	if err != nil {
		return err
	}

	_, port, err := net.SplitHostPort(cfg.ListenAddr)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/api/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %s", resp.Status)
	}

	return nil
}
