package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcher_ReloadsOnFileChange(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "tenants.yaml")

	initial := `tenants:
  - id: tenant-a
    upstreams:
      - http://localhost:9001
    rate_per_second: 5
    burst: 10
`
	if err := os.WriteFile(configFile, []byte(initial), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(configFile)
	if err != nil {
		t.Fatalf("failed to load initial config: %v", err)
	}
	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	reloadCh := make(chan *GatewayConfig, 2)
	watcher := NewWatcher(configFile, 100*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = watcher.Start(ctx, store, func(newCfg *GatewayConfig) {
			reloadCh <- newCfg
		})
	}()

	// Drain the initial reload callback that fires on Start.
	select {
	case <-reloadCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial reload")
	}

	// Small delay to ensure the watcher has settled before we modify the file.
	time.Sleep(200 * time.Millisecond)

	updated := `tenants:
  - id: tenant-a
    upstreams:
      - http://localhost:9001
    rate_per_second: 50
    burst: 100
`
	if err := os.WriteFile(configFile, []byte(updated), 0644); err != nil {
		t.Fatalf("failed to write updated config: %v", err)
	}

	select {
	case newCfg := <-reloadCh:
		if newCfg.Tenants[0].RatePerSec != 50 {
			t.Fatalf("expected rate=50 after reload, got %v", newCfg.Tenants[0].RatePerSec)
		}
		if newCfg.Tenants[0].Burst != 100 {
			t.Fatalf("expected burst=100 after reload, got %v", newCfg.Tenants[0].Burst)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for config reload")
	}
}

func TestWatcher_InvalidYAMLDoesNotCorruptStore(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "tenants.yaml")

	initial := `tenants:
  - id: tenant-a
    upstreams:
      - http://localhost:9001
    rate_per_second: 5
    burst: 10
`
	if err := os.WriteFile(configFile, []byte(initial), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(configFile)
	if err != nil {
		t.Fatalf("failed to load initial config: %v", err)
	}
	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	watcher := NewWatcher(configFile, 100*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = watcher.Start(ctx, store, nil)
	}()

	// Wait for initial load then write invalid YAML.
	time.Sleep(200 * time.Millisecond)

	invalidYAML := `this is not valid yaml: [[[`
	if err := os.WriteFile(configFile, []byte(invalidYAML), 0644); err != nil {
		t.Fatalf("failed to write bad config: %v", err)
	}

	// Wait for a couple poll cycles.
	time.Sleep(300 * time.Millisecond)

	// Store should still hold the original valid config.
	loaded := store.Load()
	if loaded == nil {
		t.Fatal("store config should not be nil")
	}
	if len(loaded.Tenants) != 1 || loaded.Tenants[0].ID != "tenant-a" {
		t.Fatalf("store should still have original config, got: %+v", loaded)
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  *GatewayConfig
		wantErr bool
	}{
		{name: "nil config", config: nil, wantErr: true},
		{name: "empty tenants", config: &GatewayConfig{}, wantErr: true},
		{name: "missing ID", config: &GatewayConfig{Tenants: []TenantConfig{{Upstreams: []string{"http://x"}, RatePerSec: 1, Burst: 1}}}, wantErr: true},
		{name: "no upstreams", config: &GatewayConfig{Tenants: []TenantConfig{{ID: "a", RatePerSec: 1, Burst: 1}}}, wantErr: true},
		{name: "zero rate", config: &GatewayConfig{Tenants: []TenantConfig{{ID: "a", Upstreams: []string{"http://x"}, RatePerSec: 0, Burst: 1}}}, wantErr: true},
		{name: "zero burst", config: &GatewayConfig{Tenants: []TenantConfig{{ID: "a", Upstreams: []string{"http://x"}, RatePerSec: 1, Burst: 0}}}, wantErr: true},
		{name: "valid", config: &GatewayConfig{Tenants: []TenantConfig{{ID: "a", Upstreams: []string{"http://x"}, RatePerSec: 5, Burst: 10}}}, wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
