package ratelimit

import (
	"context"

	"golang.org/x/time/rate"
)

type Limiter struct {
	limiter *rate.Limiter
}

func New(rps float64, burst int) *Limiter {
	if rps <= 0 {
		return &Limiter{limiter: rate.NewLimiter(rate.Inf, 0)}
	}
	if burst <= 0 {
		burst = 1
	}
	return &Limiter{limiter: rate.NewLimiter(rate.Limit(rps), burst)}
}

func (l *Limiter) Wait(ctx context.Context) error {
	return l.limiter.Wait(ctx)
}
