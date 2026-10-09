package aws

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// TestPrefixCacheFreshHit 未过期条目直接命中,不触发 fetch。
func TestPrefixCacheFreshHit(t *testing.T) {
	c := newPrefixCache()
	var calls atomic.Int64
	fetch := func(context.Context) ([]string, error) {
		calls.Add(1)
		return []string{"a/", "b/"}, nil
	}
	got, hit, err := c.get(context.Background(), "k", fetch)
	if err != nil || hit != cacheMiss || len(got) != 2 {
		t.Fatalf("first get: hit=%v got=%v err=%v", hit, got, err)
	}
	got, hit, err = c.get(context.Background(), "k", fetch)
	if err != nil || hit != cacheFresh || len(got) != 2 {
		t.Fatalf("second get: hit=%v got=%v err=%v", hit, got, err)
	}
	if calls.Load() != 1 {
		t.Errorf("fresh hit should not refetch, calls=%d", calls.Load())
	}
	// 完全过期(超过 stale 宽限):同步重取
	c.forceExpire("k")
	got, hit, err = c.get(context.Background(), "k", fetch)
	if err != nil || hit != cacheMiss || len(got) != 2 {
		t.Fatalf("expired get: hit=%v got=%v err=%v", hit, got, err)
	}
	if calls.Load() != 2 {
		t.Errorf("fully expired entry should sync refetch, calls=%d", calls.Load())
	}
}

// TestPrefixCacheStaleServeWithBackgroundRefresh 过期宽限期内先返回旧清单,
// 后台刷新完成后下一次请求拿到新清单(SWR;proposal 实现注记 3)。
func TestPrefixCacheStaleServeWithBackgroundRefresh(t *testing.T) {
	c := newPrefixCache()
	var calls atomic.Int64
	seed := func(context.Context) ([]string, error) {
		calls.Add(1)
		return []string{"old/"}, nil
	}
	if _, hit, err := c.get(context.Background(), "k", seed); err != nil || hit != cacheMiss {
		t.Fatalf("seed: hit=%v err=%v", hit, err)
	}
	c.forceStale("k")

	gate := make(chan struct{})
	slow := func(context.Context) ([]string, error) {
		calls.Add(1)
		<-gate
		return []string{"new/"}, nil
	}
	start := time.Now()
	got, hit, err := c.get(context.Background(), "k", slow)
	if err != nil {
		t.Fatal(err)
	}
	if hit != cacheStale || len(got) != 1 || got[0] != "old/" {
		t.Fatalf("stale serve: hit=%v got=%v", hit, got)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("stale serve blocked %v (must return immediately)", elapsed)
	}
	close(gate) // 放行后台刷新
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.mu.RLock()
		e, ok := c.entries["k"]
		c.mu.RUnlock()
		if ok && len(e.prefixes) == 1 && e.prefixes[0] == "new/" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background refresh did not land in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, hit, err = c.get(context.Background(), "k", slow)
	if err != nil || hit != cacheFresh || got[0] != "new/" {
		t.Fatalf("after refresh: hit=%v got=%v err=%v", hit, got, err)
	}
	if calls.Load() != 2 { // seed + 后台刷新各一次
		t.Errorf("calls=%d, want 2", calls.Load())
	}
}

// TestPrefixCacheRefreshFailureBackoff 后台刷新失败:继续供旧清单,
// 退避窗口内不反复重试(防 S3 故障时每请求打一次)。
func TestPrefixCacheRefreshFailureBackoff(t *testing.T) {
	c := newPrefixCache()
	var calls atomic.Int64
	seed := func(context.Context) ([]string, error) {
		calls.Add(1)
		return []string{"old/"}, nil
	}
	if _, hit, _ := c.get(context.Background(), "k", seed); hit != cacheMiss {
		t.Fatalf("seed hit=%v", hit)
	}
	c.forceStale("k")

	fail := func(context.Context) ([]string, error) {
		calls.Add(1)
		return nil, errors.New("s3 down")
	}
	got, hit, err := c.get(context.Background(), "k", fail)
	if err != nil || hit != cacheStale || got[0] != "old/" {
		t.Fatalf("stale serve on refresh: hit=%v got=%v err=%v", hit, got, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 2 { // 等后台刷新失败落定
		if time.Now().After(deadline) {
			t.Fatal("background refresh did not run")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // 留出退避写入窗口
	for i := 0; i < 3; i++ {
		got, hit, err = c.get(context.Background(), "k", fail)
		if err != nil || hit != cacheStale || got[0] != "old/" {
			t.Fatalf("backoff get %d: hit=%v err=%v", i, hit, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() != 2 {
		t.Errorf("refresh should back off within window, calls=%d", calls.Load())
	}
}
