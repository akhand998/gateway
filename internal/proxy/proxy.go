package proxy

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
)

type ReverseProxy struct {
	upstreams []*url.URL
	counter   uint64
}

func NewReverseProxy(upstreamURLs []string) (*ReverseProxy, error) {
	if len(upstreamURLs) == 0 {
		return nil, errors.New("at least one upstream is required")
	}

	upstreams := make([]*url.URL, 0, len(upstreamURLs))
	for _, raw := range upstreamURLs {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return nil, err
		}
		upstreams = append(upstreams, parsed)
	}

	if len(upstreams) == 0 {
		return nil, errors.New("no valid upstreams provided")
	}

	return &ReverseProxy{upstreams: upstreams}, nil
}

func (p *ReverseProxy) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream := p.NextUpstream()
		proxy := httputil.NewSingleHostReverseProxy(upstream)
		proxy.ServeHTTP(w, r)
	})
}

func (p *ReverseProxy) NextUpstream() *url.URL {
	n := uint64(len(p.upstreams))
	for {
		current := atomic.LoadUint64(&p.counter)
		next := current + 1
		if atomic.CompareAndSwapUint64(&p.counter, current, next) {
			return p.upstreams[current%n]
		}
	}
}
