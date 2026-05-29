package config

type GatewayConfig struct {
	Tenants []TenantConfig `yaml:"tenants"`
}

type TenantConfig struct {
	ID           string   `yaml:"id"`
	Upstreams    []string `yaml:"upstreams"`
	RatePerSec   float64  `yaml:"rate_per_second"`
	Burst        int      `yaml:"burst"`
	APIKeys      []string `yaml:"api_keys"`
	BreakerFails int      `yaml:"breaker_max_failures"`
	BreakerOpen  int      `yaml:"breaker_open_seconds"`
}

type RateConfig struct {
	RatePerSecond float64
	Burst         int
}
