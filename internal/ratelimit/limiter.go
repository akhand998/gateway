package ratelimit

import "context"

type Limiter interface {
	Allow(ctx context.Context, tenantID string, ratePerSecond float64, burst int) (bool, error)
}
