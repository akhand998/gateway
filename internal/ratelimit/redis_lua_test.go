package ratelimit

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestRedisLimiterValidation(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	limiter := NewRedisLimiter(client)

	if _, err := limiter.Allow(context.Background(), "", 1, 1); err == nil {
		t.Fatalf("expected error for empty tenant id")
	}

	if _, err := limiter.Allow(context.Background(), "tenant-a", 0, 1); err == nil {
		t.Fatalf("expected error for invalid rate")
	}

	if _, err := limiter.Allow(context.Background(), "tenant-a", 1, 0); err == nil {
		t.Fatalf("expected error for invalid burst")
	}
}
