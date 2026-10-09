package ratelimit

import (
	"context"
	"testing"
	"time"
)

// TestNewRateLimiter 构造冒烟：NewRateLimiter 返回可用实例且 qps 透传为 burst
func TestNewRateLimiter(t *testing.T) {
	rl := NewRateLimiter(100)
	if rl == nil {
		t.Fatal("NewRateLimiter(100) returned nil")
	}
	if rl.limiter == nil {
		t.Fatal("internal limiter not initialized")
	}
	if !rl.Allow() {
		t.Fatal("burst=qps>0 should allow the first request")
	}
}

// TestWait_NoBlock 冒烟：qps 充足时 Wait 无阻塞立即返回
func TestWait_NoBlock(t *testing.T) {
	rl := NewRateLimiter(1000)

	start := time.Now()
	if err := rl.Wait(context.Background()); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("Wait blocked for %v, expected immediate return", elapsed)
	}
}

// TestAllow_ThrottleSemantics 限流语义：burst 耗尽后 Allow 拒绝（不阻塞）
func TestAllow_ThrottleSemantics(t *testing.T) {
	rl := NewRateLimiter(1) // burst=1：首个请求消费唯一令牌

	if !rl.Allow() {
		t.Fatal("first Allow at burst=1 should succeed")
	}
	if rl.Allow() {
		t.Fatal("second immediate Allow after consuming burst=1 should be rejected")
	}
}
