package config

// GatewayConfig holds the complete gateway configuration loaded from YAML.
type GatewayConfig struct {
	Tenants []TenantConfig `yaml:"tenants"`
}

// TenantConfig holds per-tenant configuration.
type TenantConfig struct {
	ID           string   `yaml:"id"`
	Upstreams    []string `yaml:"upstreams"`
	RatePerSec   float64  `yaml:"rate_per_second"`
	Burst        int      `yaml:"burst"`
	APIKeys      []string `yaml:"api_keys"`
	BreakerFails int      `yaml:"breaker_max_failures"`
	BreakerOpen  int      `yaml:"breaker_open_seconds"`
}

// RateConfig holds rate limiting parameters for a single tenant.
// Moved here from main.go so it can be used across packages.
type RateConfig struct {
	RatePerSecond float64
	Burst         int
}
