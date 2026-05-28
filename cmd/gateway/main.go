package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"strings"

	"gateway/internal/auth"
	"gateway/internal/proxy"
	"gateway/internal/ratelimit"
	"gateway/internal/router"

	"github.com/redis/go-redis/v9"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	jwtSecret := flag.String("jwt-secret", "dev-secret", "HMAC secret for JWT validation")
	apiKeys := flag.String("api-keys", "api-key-a=tenant-a,api-key-b=tenant-b", "comma-separated apiKey=tenantID entries")
	tenantAUpstreams := flag.String("tenant-a-upstreams", "http://localhost:9001", "comma-separated upstreams for tenant-a")
	tenantBUpstreams := flag.String("tenant-b-upstreams", "http://localhost:9002", "comma-separated upstreams for tenant-b")
	redisAddr := flag.String("redis-addr", "localhost:6379", "redis address")
	redisDB := flag.Int("redis-db", 0, "redis database")
	tenantARate := flag.Float64("tenant-a-rate", 10, "tenant-a rate per second")
	tenantABurst := flag.Int("tenant-a-burst", 20, "tenant-a burst size")
	tenantBRate := flag.Float64("tenant-b-rate", 10, "tenant-b rate per second")
	tenantBBurst := flag.Int("tenant-b-burst", 20, "tenant-b burst size")
	flag.Parse()

	resolver := auth.NewResolver(*jwtSecret, parseAPIKeys(*apiKeys))

	routeConfig := []router.TenantConfig{
		{TenantID: "tenant-a", Upstreams: splitCSV(*tenantAUpstreams)},
		{TenantID: "tenant-b", Upstreams: splitCSV(*tenantBUpstreams)},
	}
	routeTable, err := router.NewRouter(routeConfig)
	if err != nil {
		log.Fatalf("failed to configure tenants: %v", err)
	}

	proxyByTenant := make(map[string]*proxy.ReverseProxy, len(routeConfig))
	for _, tenant := range routeConfig {
		reverseProxy, err := proxy.NewReverseProxy(tenant.Upstreams)
		if err != nil {
			log.Fatalf("failed to configure upstreams for %s: %v", tenant.TenantID, err)
		}
		proxyByTenant[tenant.TenantID] = reverseProxy
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr: *redisAddr,
		DB:   *redisDB,
	})
	limiter := ratelimit.NewRedisLimiter(redisClient)
	rateByTenant := map[string]RateConfig{
		"tenant-a": {RatePerSecond: *tenantARate, Burst: *tenantABurst},
		"tenant-b": {RatePerSecond: *tenantBRate, Burst: *tenantBBurst},
	}

	mux := http.NewServeMux()
	mux.Handle("/", tenantHandler(resolver, routeTable, proxyByTenant, limiter, rateByTenant))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	server := &http.Server{
		Addr:    *addr,
		Handler: mux,
	}

	log.Printf("gateway listening on %s", *addr)
	log.Fatal(server.ListenAndServe())
}

func tenantHandler(
	resolver *auth.Resolver,
	routeTable *router.Router,
	proxies map[string]*proxy.ReverseProxy,
	limiter ratelimit.Limiter,
	rateByTenant map[string]RateConfig,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID, err := resolver.ResolveTenantID(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		if _, err := routeTable.UpstreamsForTenant(tenantID); err != nil {
			http.Error(w, "tenant not found", http.StatusUnauthorized)
			return
		}

		reverseProxy, ok := proxies[tenantID]
		if !ok {
			http.Error(w, "tenant upstream missing", http.StatusInternalServerError)
			return
		}

		rateConfig, ok := rateByTenant[tenantID]
		if !ok {
			http.Error(w, "tenant rate config missing", http.StatusInternalServerError)
			return
		}

		allowed, err := limiter.Allow(r.Context(), tenantID, rateConfig.RatePerSecond, rateConfig.Burst)
		if err != nil {
			log.Printf("rate limit error for %s: %v", tenantID, err)
			allowed = true
		}
		if !allowed {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		ctx := context.WithValue(r.Context(), router.ContextTenantIDKey{}, tenantID)
		reverseProxy.Handler().ServeHTTP(w, r.WithContext(ctx))
	})
}

type RateConfig struct {
	RatePerSecond float64
	Burst         int
}

func parseAPIKeys(raw string) map[string]string {
	result := map[string]string{}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			continue
		}
		apiKey := strings.TrimSpace(parts[0])
		tenantID := strings.TrimSpace(parts[1])
		if apiKey == "" || tenantID == "" {
			continue
		}
		result[apiKey] = tenantID
	}
	return result
}

func splitCSV(raw string) []string {
	items := strings.Split(raw, ",")
	result := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
