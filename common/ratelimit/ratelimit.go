package ratelimit

import (
	"context"

	"golang.org/x/time/rate"
)

// RateLimiter 云厂商 API 限流器
type RateLimiter struct {
	limiter *rate.Limiter
}

// NewRateLimiter 创建限流器
// qps: 每秒请求数限制
func NewRateLimiter(qps int) *RateLimiter {
	return &RateLimiter{
		limiter: rate.NewLimiter(rate.Limit(qps), qps),
	}
}

// Wait 等待限流器允许请求
func (r *RateLimiter) Wait(ctx context.Context) error {
	return r.limiter.Wait(ctx)
}

// Allow 检查是否允许请求（不阻塞）
func (r *RateLimiter) Allow() bool {
	return r.limiter.Allow()
}
