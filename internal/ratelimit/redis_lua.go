package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const luaTokenBucket = `
local key    = KEYS[1]
local rate   = tonumber(ARGV[1])
local burst  = tonumber(ARGV[2])
local now    = tonumber(ARGV[3])

local state  = redis.call("HMGET", key, "tokens", "last")
local tokens = tonumber(state[1]) or burst
local last   = tonumber(state[2]) or now

local elapsed = math.max(0, now - last) / 1000
local newTokens = math.min(burst, tokens + elapsed * rate)

if newTokens >= 1 then
    newTokens = newTokens - 1
    redis.call("HMSET", key, "tokens", newTokens, "last", now)
    redis.call("EXPIRE", key, math.ceil(burst / rate) + 1)
    return 1
else
    return 0
end
`

type RedisLimiter struct {
	client *redis.Client
}

func NewRedisLimiter(client *redis.Client) *RedisLimiter {
	return &RedisLimiter{client: client}
}

func (l *RedisLimiter) Allow(ctx context.Context, tenantID string, ratePerSecond float64, burst int) (bool, error) {
	if tenantID == "" {
		return false, fmt.Errorf("tenant id is required")
	}
	if ratePerSecond <= 0 {
		return false, fmt.Errorf("rate must be greater than zero")
	}
	if burst <= 0 {
		return false, fmt.Errorf("burst must be greater than zero")
	}

	key := fmt.Sprintf("ratelimit:%s", tenantID)
	now := time.Now().UnixMilli()

	result, err := l.client.Eval(ctx, luaTokenBucket, []string{key}, ratePerSecond, burst, now).Int()
	if err != nil {
		return false, err
	}

	return result == 1, nil
}
