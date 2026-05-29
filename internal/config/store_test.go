package config

import (
	"testing"
)

func TestNewStore_NilConfig(t *testing.T) {
	_, err := NewStore(nil)
	if err == nil {
		t.Fatal("expected error for nil initial config")
	}
}

func TestStore_LoadAfterStore(t *testing.T) {
	initial := &GatewayConfig{
		Tenants: []TenantConfig{
			{ID: "t1", Upstreams: []string{"http://localhost:9001"}, RatePerSec: 5, Burst: 10},
		},
	}
	store, err := NewStore(initial)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	loaded := store.Load()
	if loaded == nil {
		t.Fatal("expected non-nil config after init")
	}
	if len(loaded.Tenants) != 1 || loaded.Tenants[0].ID != "t1" {
		t.Fatalf("unexpected config: %+v", loaded)
	}

	// Store a new config.
	updated := &GatewayConfig{
		Tenants: []TenantConfig{
			{ID: "t1", Upstreams: []string{"http://localhost:9001"}, RatePerSec: 10, Burst: 20},
			{ID: "t2", Upstreams: []string{"http://localhost:9002"}, RatePerSec: 5, Burst: 10},
		},
	}
	if err := store.Store(updated); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	loaded = store.Load()
	if len(loaded.Tenants) != 2 {
		t.Fatalf("expected 2 tenants after update, got %d", len(loaded.Tenants))
	}
	if loaded.Tenants[0].RatePerSec != 10 {
		t.Fatalf("expected updated rate=10, got %v", loaded.Tenants[0].RatePerSec)
	}
}

func TestStore_StoreNilRejected(t *testing.T) {
	initial := &GatewayConfig{
		Tenants: []TenantConfig{
			{ID: "t1", Upstreams: []string{"http://localhost:9001"}, RatePerSec: 5, Burst: 10},
		},
	}
	store, _ := NewStore(initial)

	err := store.Store(nil)
	if err == nil {
		t.Fatal("expected error when storing nil config")
	}

	// Original config should still be intact.
	loaded := store.Load()
	if loaded == nil || len(loaded.Tenants) != 1 {
		t.Fatal("store should not have been corrupted by nil store attempt")
	}
}
