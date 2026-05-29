package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"sync/atomic"
	"time"

	"gateway/internal/auth"
	"gateway/internal/circuitbreaker"
	"gateway/internal/config"
	"gateway/internal/observability"
	"gateway/internal/proxy"
	"gateway/internal/ratelimit"
	"gateway/internal/router"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	jwtSecret := flag.String("jwt-secret", "dev-secret", "HMAC secret for JWT validation")
	configPath := flag.String("config", "configs/tenants.yaml", "path to tenant config")
	configPoll := flag.Duration("config-poll", 2*time.Second, "config poll interval")
	redisAddr := flag.String("redis-addr", "localhost:6379", "redis address")
	redisDB := flag.Int("redis-db", 0, "redis database")
	breakerMaxFailures := flag.Int("breaker-max-failures", 3, "default consecutive failures before opening breaker")
	breakerOpenSeconds := flag.Int("breaker-open-seconds", 5, "default seconds to keep breaker open")
	flag.Parse()

	redisClient := redis.NewClient(&redis.Options{
		Addr: *redisAddr,
		DB:   *redisDB,
	})
	limiter := ratelimit.NewRedisLimiter(redisClient)
	metrics := observability.NewMetrics(prometheus.DefaultRegisterer)

	initialConfig, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	configStore, err := config.NewStore(initialConfig)
	if err != nil {
		log.Fatalf("failed to initialize config store: %v", err)
	}

	defaultOpenDuration := time.Duration(*breakerOpenSeconds) * time.Second
	runtimeSnapshot, err := buildRuntimeFromConfig(initialConfig, *jwtSecret, *breakerMaxFailures, defaultOpenDuration)
	if err != nil {
		log.Fatalf("failed to build runtime from config: %v", err)
	}
	runtimeStore := newRuntimeStore(runtimeSnapshot)

	watcher := config.NewWatcher(*configPath, *configPoll)
	watcherCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		err := watcher.Start(watcherCtx, configStore, func(newCfg *config.GatewayConfig) {
			snapshot, err := buildRuntimeFromConfig(newCfg, *jwtSecret, *breakerMaxFailures, defaultOpenDuration)
			if err != nil {
				log.Printf("config reload failed: %v", err)
				return
			}
			runtimeStore.Store(snapshot)
			log.Printf("config reloaded from %s", *configPath)
		})
		if err != nil {
			log.Printf("config watcher stopped: %v", err)
		}
	}()

	routerMux := mux.NewRouter()
	routerMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}).Methods(http.MethodGet)
	routerMux.Handle("/metrics", metrics.Handler()).Methods(http.MethodGet)
	routerMux.PathPrefix("/").Handler(tenantHandler(runtimeStore, limiter, metrics))

	server := &http.Server{
		Addr:    *addr,
		Handler: routerMux,
	}

	log.Printf("gateway listening on %s", *addr)
	log.Fatal(server.ListenAndServe())
}

func tenantHandler(
	runtimeStore *runtimeStore,
	limiter ratelimit.Limiter,
	metrics *observability.Metrics,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeLabel := r.URL.Path
		runtime := runtimeStore.Load()
		if runtime == nil {
			http.Error(w, "config unavailable", http.StatusServiceUnavailable)
			metrics.ObserveRequest("unknown", routeLabel, http.StatusServiceUnavailable)
			return
		}

		resolver := runtime.resolver
		routeTable := runtime.routeTable
		proxies := runtime.proxies
		rateByTenant := runtime.rateByTenant
		breakers := runtime.breakers
		tenantID, err := resolver.ResolveTenantID(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			metrics.ObserveRequest("unknown", routeLabel, http.StatusUnauthorized)
			return
		}

		if _, err := routeTable.UpstreamsForTenant(tenantID); err != nil {
			http.Error(w, "tenant not found", http.StatusUnauthorized)
			metrics.ObserveRequest(tenantID, routeLabel, http.StatusUnauthorized)
			return
		}

		reverseProxy, ok := proxies[tenantID]
		if !ok {
			http.Error(w, "tenant upstream missing", http.StatusInternalServerError)
			metrics.ObserveRequest(tenantID, routeLabel, http.StatusInternalServerError)
			return
		}

		rateConfig, ok := rateByTenant[tenantID]
		if !ok {
			http.Error(w, "tenant rate config missing", http.StatusInternalServerError)
			metrics.ObserveRequest(tenantID, routeLabel, http.StatusInternalServerError)
			return
		}

		allowed, err := limiter.Allow(r.Context(), tenantID, rateConfig.RatePerSecond, rateConfig.Burst)
		if err != nil {
			log.Printf("rate limit error for %s: %v", tenantID, err)
			allowed = true
		}
		if !allowed {
			metrics.IncRateLimit(tenantID)
			metrics.ObserveRequest(tenantID, routeLabel, http.StatusTooManyRequests)
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		upstream := reverseProxy.NextUpstream()
		upstreamKey := upstream.String()
		breaker := breakerFor(breakers, tenantID, upstreamKey)
		if breaker != nil && !breaker.Allow() {
			metrics.SetBreakerState(tenantID, upstreamKey, breakerStateValue(breaker.State()))
			metrics.ObserveRequest(tenantID, routeLabel, http.StatusServiceUnavailable)
			http.Error(w, "circuit breaker open", http.StatusServiceUnavailable)
			return
		}

		start := time.Now()
		proxyHandler := httputil.NewSingleHostReverseProxy(upstream)
		proxyHandler.ModifyResponse = func(resp *http.Response) error {
			metrics.ObserveUpstreamLatency(tenantID, upstreamKey, time.Since(start).Seconds())
			metrics.ObserveRequest(tenantID, routeLabel, resp.StatusCode)
			if breaker != nil {
				if resp.StatusCode >= http.StatusInternalServerError {
					breaker.OnFailure()
				} else {
					breaker.OnSuccess()
				}
				metrics.SetBreakerState(tenantID, upstreamKey, breakerStateValue(breaker.State()))
			}
			return nil
		}
		proxyHandler.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
			metrics.ObserveUpstreamLatency(tenantID, upstreamKey, time.Since(start).Seconds())
			metrics.ObserveRequest(tenantID, routeLabel, http.StatusBadGateway)
			if breaker != nil {
				breaker.OnFailure()
				metrics.SetBreakerState(tenantID, upstreamKey, breakerStateValue(breaker.State()))
			}
			http.Error(rw, "bad gateway", http.StatusBadGateway)
		}

		ctx := context.WithValue(r.Context(), router.ContextTenantIDKey{}, tenantID)
		proxyHandler.ServeHTTP(w, r.WithContext(ctx))
	})
}

type RateConfig struct {
	RatePerSecond float64
	Burst         int
}

type runtimeSnapshot struct {
	resolver     *auth.Resolver
	routeTable   *router.Router
	proxies      map[string]*proxy.ReverseProxy
	rateByTenant map[string]RateConfig
	breakers     map[string]map[string]*circuitbreaker.Breaker
}

type runtimeStore struct {
	value atomic.Value
}

func newRuntimeStore(initial *runtimeSnapshot) *runtimeStore {
	store := &runtimeStore{}
	store.value.Store(initial)
	return store
}

func (s *runtimeStore) Load() *runtimeSnapshot {
	value := s.value.Load()
	if value == nil {
		return nil
	}
	snapshot, _ := value.(*runtimeSnapshot)
	return snapshot
}

func (s *runtimeStore) Store(snapshot *runtimeSnapshot) {
	if snapshot == nil {
		return
	}
	s.value.Store(snapshot)
}

func buildRuntimeFromConfig(cfg *config.GatewayConfig, jwtSecret string, defaultBreakerFailures int, defaultBreakerOpen time.Duration) (*runtimeSnapshot, error) {
	apiKeys := make(map[string]string)
	routeConfig := make([]router.TenantConfig, 0, len(cfg.Tenants))
	rateByTenant := make(map[string]RateConfig, len(cfg.Tenants))
	proxies := make(map[string]*proxy.ReverseProxy, len(cfg.Tenants))
	breakers := make(map[string]map[string]*circuitbreaker.Breaker, len(cfg.Tenants))

	for _, tenant := range cfg.Tenants {
		routeConfig = append(routeConfig, router.TenantConfig{
			TenantID:  tenant.ID,
			Upstreams: tenant.Upstreams,
		})

		for _, apiKey := range tenant.APIKeys {
			if apiKey != "" {
				apiKeys[apiKey] = tenant.ID
			}
		}

		rateByTenant[tenant.ID] = RateConfig{
			RatePerSecond: tenant.RatePerSec,
			Burst:         tenant.Burst,
		}

		reverseProxy, err := proxy.NewReverseProxy(tenant.Upstreams)
		if err != nil {
			return nil, err
		}
		proxies[tenant.ID] = reverseProxy

		failureLimit := tenant.BreakerFails
		if failureLimit <= 0 {
			failureLimit = defaultBreakerFailures
		}
		openSeconds := tenant.BreakerOpen
		openDuration := defaultBreakerOpen
		if openSeconds > 0 {
			openDuration = time.Duration(openSeconds) * time.Second
		}

		upstreamBreakers := make(map[string]*circuitbreaker.Breaker, len(tenant.Upstreams))
		for _, upstream := range tenant.Upstreams {
			if upstream == "" {
				continue
			}
			upstreamBreakers[upstream] = circuitbreaker.New(failureLimit, openDuration)
		}
		breakers[tenant.ID] = upstreamBreakers
	}

	routeTable, err := router.NewRouter(routeConfig)
	if err != nil {
		return nil, err
	}

	resolver := auth.NewResolver(jwtSecret, apiKeys)
	return &runtimeSnapshot{
		resolver:     resolver,
		routeTable:   routeTable,
		proxies:      proxies,
		rateByTenant: rateByTenant,
		breakers:     breakers,
	}, nil
}

func breakerFor(breakers map[string]map[string]*circuitbreaker.Breaker, tenantID, upstream string) *circuitbreaker.Breaker {
	perTenant, ok := breakers[tenantID]
	if !ok {
		return nil
	}
	breaker, ok := perTenant[upstream]
	if ok {
		return breaker
	}
	return nil
}

func breakerStateValue(state circuitbreaker.State) int {
	switch state {
	case circuitbreaker.Open:
		return 1
	case circuitbreaker.HalfOpen:
		return 2
	default:
		return 0
	}
}
