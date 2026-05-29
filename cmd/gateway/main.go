package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
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
	jwtSecret := flag.String("jwt-secret", "", "HMAC secret for JWT validation (prefer GATEWAY_JWT_SECRET env var)")
	configPath := flag.String("config", "configs/tenants.yaml", "path to tenant config")
	configPoll := flag.Duration("config-poll", 2*time.Second, "config poll interval")
	redisAddr := flag.String("redis-addr", "localhost:6379", "redis address")
	redisDB := flag.Int("redis-db", 0, "redis database")
	breakerMaxFailures := flag.Int("breaker-max-failures", 3, "default consecutive failures before opening breaker")
	breakerOpenSeconds := flag.Int("breaker-open-seconds", 5, "default seconds to keep breaker open")
	readTimeout := flag.Duration("read-timeout", 15*time.Second, "HTTP server read timeout")
	writeTimeout := flag.Duration("write-timeout", 30*time.Second, "HTTP server write timeout")
	idleTimeout := flag.Duration("idle-timeout", 60*time.Second, "HTTP server idle timeout")
	maxBodyBytes := flag.Int64("max-body-bytes", 1<<20, "maximum request body size in bytes (default 1MB)")
	tlsCert := flag.String("tls-cert", "", "path to TLS certificate file (optional, enables HTTPS)")
	tlsKey := flag.String("tls-key", "", "path to TLS key file (optional, enables HTTPS)")
	drainTimeout := flag.Duration("drain-timeout", 10*time.Second, "graceful shutdown drain timeout")
	upstreamTimeout := flag.Duration("upstream-timeout", 30*time.Second, "timeout for upstream requests")
	flag.Parse()

	// Prefer env var for JWT secret to avoid exposing it in ps/shell history.
	secret := os.Getenv("GATEWAY_JWT_SECRET")
	if secret == "" {
		secret = *jwtSecret
	}
	if secret == "" {
		log.Fatal("jwt secret is required: set GATEWAY_JWT_SECRET env var or --jwt-secret flag")
	}

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
	runtimeSnapshot, err := buildRuntimeFromConfig(initialConfig, secret, *breakerMaxFailures, defaultOpenDuration, *upstreamTimeout, nil)
	if err != nil {
		log.Fatalf("failed to build runtime from config: %v", err)
	}
	rtStore := newRuntimeStore(runtimeSnapshot)

	watcher := config.NewWatcher(*configPath, *configPoll)
	watcherCtx, watcherCancel := context.WithCancel(context.Background())
	defer watcherCancel()
	go func() {
		err := watcher.Start(watcherCtx, configStore, func(newCfg *config.GatewayConfig) {
			// Carry forward existing breakers so state isn't lost on reload.
			existingBreakers := rtStore.Load().breakers
			snapshot, err := buildRuntimeFromConfig(newCfg, secret, *breakerMaxFailures, defaultOpenDuration, *upstreamTimeout, existingBreakers)
			if err != nil {
				log.Printf("[config-reload] failed to build runtime: %v", err)
				return
			}
			rtStore.Store(snapshot)
			log.Printf("[config-reload] reloaded from %s", *configPath)
		})
		if err != nil {
			log.Printf("[config-watcher] stopped: %v", err)
		}
	}()

	routerMux := mux.NewRouter()
	routerMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}).Methods(http.MethodGet)
	routerMux.Handle("/metrics", metrics.Handler()).Methods(http.MethodGet)
	routerMux.PathPrefix("/").Handler(
		maxBodyMiddleware(*maxBodyBytes,
			tenantHandler(rtStore, limiter, metrics),
		),
	)

	server := &http.Server{
		Addr:         *addr,
		Handler:      routerMux,
		ReadTimeout:  *readTimeout,
		WriteTimeout: *writeTimeout,
		IdleTimeout:  *idleTimeout,
	}

	// Start server in a goroutine so we can handle shutdown signals.
	errCh := make(chan error, 1)
	go func() {
		if *tlsCert != "" && *tlsKey != "" {
			log.Printf("gateway listening on %s (TLS)", *addr)
			errCh <- server.ListenAndServeTLS(*tlsCert, *tlsKey)
		} else {
			if *tlsCert != "" || *tlsKey != "" {
				log.Fatal("both --tls-cert and --tls-key must be provided for HTTPS")
			}
			log.Printf("gateway listening on %s", *addr)
			errCh <- server.ListenAndServe()
		}
	}()

	// Graceful shutdown on SIGINT / SIGTERM.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("received signal %v, draining connections...", sig)
		ctx, cancel := context.WithTimeout(context.Background(), *drainTimeout)
		defer cancel()
		watcherCancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Fatalf("graceful shutdown failed: %v", err)
		}
		log.Println("server stopped gracefully")
	case err := <-errCh:
		log.Fatalf("server error: %v", err)
	}
}

// maxBodyMiddleware limits the request body size to prevent memory exhaustion.
func maxBodyMiddleware(maxBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next.ServeHTTP(w, r)
	})
}

// tenantHandler is the core request handler. It chains:
// auth → routing → rate limiting → circuit breaking → proxying.
func tenantHandler(
	rtStore *runtimeStore,
	limiter ratelimit.Limiter,
	metrics *observability.Metrics,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeLabel := r.URL.Path
		runtime := rtStore.Load()
		if runtime == nil {
			http.Error(w, "config unavailable", http.StatusServiceUnavailable)
			metrics.ObserveRequest("unknown", routeLabel, http.StatusServiceUnavailable)
			return
		}

		// --- Auth ---
		tenantID, err := runtime.resolver.ResolveTenantID(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			metrics.ObserveRequest("unknown", routeLabel, http.StatusUnauthorized)
			return
		}

		// --- Routing ---
		if _, err := runtime.routeTable.UpstreamsForTenant(tenantID); err != nil {
			http.Error(w, "tenant not found", http.StatusUnauthorized)
			metrics.ObserveRequest(tenantID, routeLabel, http.StatusUnauthorized)
			return
		}

		reverseProxy, ok := runtime.proxies[tenantID]
		if !ok {
			http.Error(w, "tenant upstream missing", http.StatusInternalServerError)
			metrics.ObserveRequest(tenantID, routeLabel, http.StatusInternalServerError)
			return
		}

		rateConfig, ok := runtime.rateByTenant[tenantID]
		if !ok {
			http.Error(w, "tenant rate config missing", http.StatusInternalServerError)
			metrics.ObserveRequest(tenantID, routeLabel, http.StatusInternalServerError)
			return
		}

		// --- Rate Limiting ---
		allowed, err := limiter.Allow(r.Context(), tenantID, rateConfig.RatePerSecond, rateConfig.Burst)
		if err != nil {
			log.Printf("[rate-limit] error for %s: %v (fail-open)", tenantID, err)
			allowed = true
		}
		if !allowed {
			metrics.IncRateLimit(tenantID)
			metrics.ObserveRequest(tenantID, routeLabel, http.StatusTooManyRequests)
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		// --- Circuit Breaker ---
		upstream := reverseProxy.NextUpstream()
		upstreamKey := upstream.String()
		breaker := breakerFor(runtime.breakers, tenantID, upstreamKey)
		if breaker != nil && !breaker.Allow() {
			metrics.SetBreakerState(tenantID, upstreamKey, breakerStateValue(breaker.State()))
			metrics.ObserveRequest(tenantID, routeLabel, http.StatusServiceUnavailable)
			http.Error(w, "circuit breaker open", http.StatusServiceUnavailable)
			return
		}

		// --- Proxy ---
		start := time.Now()
		transport := runtime.transports[tenantID]
		proxyHandler := &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				req.URL.Scheme = upstream.Scheme
				req.URL.Host = upstream.Host
				req.Host = upstream.Host
				if _, ok := req.Header["User-Agent"]; !ok {
					req.Header.Set("User-Agent", "")
				}
			},
			Transport: transport,
			ModifyResponse: func(resp *http.Response) error {
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
			},
			ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
				metrics.ObserveUpstreamLatency(tenantID, upstreamKey, time.Since(start).Seconds())
				metrics.ObserveRequest(tenantID, routeLabel, http.StatusBadGateway)
				if breaker != nil {
					breaker.OnFailure()
					metrics.SetBreakerState(tenantID, upstreamKey, breakerStateValue(breaker.State()))
				}
				http.Error(rw, "bad gateway", http.StatusBadGateway)
			},
		}

		ctx := context.WithValue(r.Context(), router.ContextTenantIDKey{}, tenantID)
		proxyHandler.ServeHTTP(w, r.WithContext(ctx))
	})
}

// runtimeSnapshot holds all pre-built objects needed to handle requests.
// The entire struct is swapped atomically on config reload.
type runtimeSnapshot struct {
	resolver     *auth.Resolver
	routeTable   *router.Router
	proxies      map[string]*proxy.ReverseProxy
	transports   map[string]*http.Transport
	rateByTenant map[string]config.RateConfig
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

// buildRuntimeFromConfig constructs a new runtimeSnapshot from a GatewayConfig.
// existingBreakers allows preserving circuit breaker state across config reloads
// for tenant/upstream pairs that haven't changed.
func buildRuntimeFromConfig(
	cfg *config.GatewayConfig,
	jwtSecret string,
	defaultBreakerFailures int,
	defaultBreakerOpen time.Duration,
	upstreamTimeout time.Duration,
	existingBreakers map[string]map[string]*circuitbreaker.Breaker,
) (*runtimeSnapshot, error) {
	apiKeys := make(map[string]string)
	routeConfig := make([]router.TenantConfig, 0, len(cfg.Tenants))
	rateByTenant := make(map[string]config.RateConfig, len(cfg.Tenants))
	proxies := make(map[string]*proxy.ReverseProxy, len(cfg.Tenants))
	transports := make(map[string]*http.Transport, len(cfg.Tenants))
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

		rateByTenant[tenant.ID] = config.RateConfig{
			RatePerSecond: tenant.RatePerSec,
			Burst:         tenant.Burst,
		}

		reverseProxy, err := proxy.NewReverseProxy(tenant.Upstreams)
		if err != nil {
			return nil, fmt.Errorf("tenant %s: %w", tenant.ID, err)
		}
		proxies[tenant.ID] = reverseProxy

		// Per-tenant transport with connection pooling and timeouts.
		transports[tenant.ID] = &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
			ResponseHeaderTimeout: upstreamTimeout,
		}

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
			// Reuse existing breaker if the tenant+upstream pair exists from previous config.
			if existingBreakers != nil {
				if perTenant, ok := existingBreakers[tenant.ID]; ok {
					if existing, ok := perTenant[upstream]; ok {
						upstreamBreakers[upstream] = existing
						continue
					}
				}
			}
			upstreamBreakers[upstream] = circuitbreaker.New(failureLimit, openDuration)
		}
		breakers[tenant.ID] = upstreamBreakers
	}

	routeTable, err := router.NewRouter(routeConfig)
	if err != nil {
		return nil, err
	}

	// Build upstream URL map for Director func.
	upstreamURLs := make(map[string]*url.URL)
	for _, tenant := range cfg.Tenants {
		for _, raw := range tenant.Upstreams {
			if _, exists := upstreamURLs[raw]; !exists {
				parsed, err := url.Parse(strings.TrimSpace(raw))
				if err != nil {
					return nil, fmt.Errorf("parse upstream %s: %w", raw, err)
				}
				upstreamURLs[raw] = parsed
			}
		}
	}

	resolver := auth.NewResolver(jwtSecret, apiKeys)
	return &runtimeSnapshot{
		resolver:     resolver,
		routeTable:   routeTable,
		proxies:      proxies,
		transports:   transports,
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
