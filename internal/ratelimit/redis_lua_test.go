package ratelimit

import (
	"context"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"
)

func skipIfNoRedis(t *testing.T, client *redis.Client) {
	t.Helper()
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("skipping: Redis not available (%v). Set REDIS_ADDR to run this test.", err)
	}
}

func redisAddr() string {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		return "localhost:6379"
	}
	return addr
}

func TestRedisLimiterValidation(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: redisAddr()})
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

func TestRedisLimiterTokenBucket(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: redisAddr()})
	skipIfNoRedis(t, client)
	limiter := NewRedisLimiter(client)

	ctx := context.Background()
	tenantID := "test-bucket-" + t.Name()

	client.Del(ctx, "ratelimit:"+tenantID)

	for i := 0; i < 3; i++ {
		allowed, err := limiter.Allow(ctx, tenantID, 1, 3)
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i, err)
		}
		if !allowed {
			t.Fatalf("request %d: expected allowed (burst=3)", i)
		}
	}

	allowed, err := limiter.Allow(ctx, tenantID, 1, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("expected denied after burst exhausted")
	}

	client.Del(ctx, "ratelimit:"+tenantID)
}
