package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReverseProxyRoundRobin(t *testing.T) {
	serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("upstream-a"))
	}))
	defer serverA.Close()

	serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("upstream-b"))
	}))
	defer serverB.Close()

	reverseProxy, err := NewReverseProxy([]string{serverA.URL, serverB.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gateway := httptest.NewServer(reverseProxy.Handler())
	defer gateway.Close()

	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		resp, err := http.Get(gateway.URL)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		seen[string(body)] = true
	}

	if !seen["upstream-a"] || !seen["upstream-b"] {
		t.Fatalf("expected requests to hit both upstreams, saw: %v", seen)
	}
}
