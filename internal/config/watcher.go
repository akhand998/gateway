package config

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"time"
)

type Watcher struct {
	path     string
	interval time.Duration
	lastMod  time.Time
}

func NewWatcher(path string, interval time.Duration) *Watcher {
	return &Watcher{path: path, interval: interval}
}

func (w *Watcher) Start(ctx context.Context, store *Store, onReload func(*GatewayConfig)) error {
	if err := w.reload(store, onReload); err != nil {
		return err
	}

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := w.reload(store, onReload); err != nil {
				log.Printf("[config-watcher] reload failed for %s: %v", w.path, err)
				continue
			}
		}
	}
}

func (w *Watcher) reload(store *Store, onReload func(*GatewayConfig)) error {
	info, err := os.Stat(w.path)
	if err != nil {
		return fmt.Errorf("stat config file: %w", err)
	}

	if !info.ModTime().After(w.lastMod) {
		return nil
	}

	cfg, err := Load(w.path)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := store.Store(cfg); err != nil {
		return fmt.Errorf("store config: %w", err)
	}

	w.lastMod = info.ModTime()
	if onReload != nil {
		onReload(cfg)
	}

	return nil
}

func validateConfig(cfg *GatewayConfig) error {
	if cfg == nil {
		return fmt.Errorf("config is nil: %w", fs.ErrInvalid)
	}
	if len(cfg.Tenants) == 0 {
		return fmt.Errorf("no tenants configured: %w", fs.ErrInvalid)
	}
	for _, tenant := range cfg.Tenants {
		if tenant.ID == "" || len(tenant.Upstreams) == 0 {
			return fmt.Errorf("tenant %q has empty id or no upstreams: %w", tenant.ID, fs.ErrInvalid)
		}
		if tenant.RatePerSec <= 0 || tenant.Burst <= 0 {
			return fmt.Errorf("tenant %q has invalid rate (%v) or burst (%v): %w",
				tenant.ID, tenant.RatePerSec, tenant.Burst, fs.ErrInvalid)
		}
	}
	return nil
}
