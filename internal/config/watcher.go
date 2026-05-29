package config

import (
	"context"
	"io/fs"
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
				continue
			}
		}
	}
}

func (w *Watcher) reload(store *Store, onReload func(*GatewayConfig)) error {
	info, err := os.Stat(w.path)
	if err != nil {
		return err
	}

	if !info.ModTime().After(w.lastMod) {
		return nil
	}

	cfg, err := Load(w.path)
	if err != nil {
		return err
	}

	if err := store.Store(cfg); err != nil {
		return err
	}

	w.lastMod = info.ModTime()
	if onReload != nil {
		onReload(cfg)
	}

	return nil
}

func validateConfig(cfg *GatewayConfig) error {
	if cfg == nil {
		return fs.ErrInvalid
	}
	if len(cfg.Tenants) == 0 {
		return fs.ErrInvalid
	}
	for _, tenant := range cfg.Tenants {
		if tenant.ID == "" || len(tenant.Upstreams) == 0 {
			return fs.ErrInvalid
		}
		if tenant.RatePerSec <= 0 || tenant.Burst <= 0 {
			return fs.ErrInvalid
		}
	}
	return nil
}
