package nasprobe

import (
	"math"
	"testing"
)

func TestBytesToGB(t *testing.T) {
	tests := []struct {
		name string
		raw  float64
		want float64
	}{
		{name: "1GiB字节换算为1GB", raw: 1073741824, want: 1},
		{name: "零字节为零", raw: 0, want: 0},
		{name: "1PB字节换算", raw: 1024 * 1024 * 1024 * 1024 * 1024, want: 1048576},
		{name: "10TiB字节", raw: 10 * 1024 * 1024 * 1024 * 1024, want: 10240},
		{name: "30GiB字节", raw: 30 * 1024 * 1024 * 1024, want: 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BytesToGB(tt.raw)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("BytesToGB(%v) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestCheckMagnitude(t *testing.T) {
	tests := []struct {
		name string
		gb   float64
		want bool
	}{
		{name: "1MB下限边界通过", gb: BytesToGB(1024 * 1024), want: true},
		{name: "1PB上限边界通过", gb: BytesToGB(1024 * 1024 * 1024 * 1024 * 1024), want: true},
		{name: "1GB通过", gb: 1, want: true},
		{name: "10TiB通过", gb: 10240, want: true},
		{name: "0.5MB低于下限不通过", gb: 0.5 * BytesToGB(1024*1024), want: false},
		{name: "2PB超出上限不通过", gb: 2 * BytesToGB(1024*1024*1024*1024*1024), want: false},
		{name: "零值不通过(零值单列)", gb: 0, want: false},
		{name: "负值不通过", gb: -1, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CheckMagnitude(tt.gb); got != tt.want {
				t.Fatalf("CheckMagnitude(%v GB) = %v, want %v", tt.gb, got, tt.want)
			}
		})
	}
}

func TestDescribeMagnitude(t *testing.T) {
	if got := DescribeMagnitude(0); got != "zero_exception" {
		t.Fatalf("DescribeMagnitude(0) = %q, want zero_exception", got)
	}
	if got := DescribeMagnitude(10); got != "ok" {
		t.Fatalf("DescribeMagnitude(10) = %q, want ok", got)
	}
	if got := DescribeMagnitude(-5); got == "ok" || got == "zero_exception" {
		t.Fatalf("DescribeMagnitude(-5) = %q, 应为 out_of_range", got)
	}
}

func TestAggregateDistribution(t *testing.T) {
	t.Run("常规占比计算", func(t *testing.T) {
		dist := AggregateDistribution([]ProviderUsage{
			{Provider: "aliyun", Instances: 2, CapacityGB: 300},
			{Provider: "volcengine", Instances: 1, CapacityGB: 100},
			{Provider: "huawei", Instances: 1, CapacityGB: 0, ZeroCap: 1},
		})
		if dist.TotalInst != 4 || dist.TotalCapGB != 400 {
			t.Fatalf("总数错误: inst=%d cap=%v, want 4/400", dist.TotalInst, dist.TotalCapGB)
		}
		if got := dist.Percentages["aliyun"]; math.Abs(got-75) > 1e-9 {
			t.Fatalf("aliyun 占比 = %v, want 75", got)
		}
		if got := dist.Percentages["volcengine"]; math.Abs(got-25) > 1e-9 {
			t.Fatalf("volcengine 占比 = %v, want 25", got)
		}
		if got := dist.Percentages["huawei"]; got != 0 {
			t.Fatalf("huawei 占比 = %v, want 0", got)
		}
	})
	t.Run("枚举失败厂商不计入分母", func(t *testing.T) {
		dist := AggregateDistribution([]ProviderUsage{
			{Provider: "aliyun", Instances: 1, CapacityGB: 100},
			{Provider: "aws", Instances: 0, ProbeErr: "ListInstances failed"},
		})
		if dist.TotalInst != 1 || dist.TotalCapGB != 100 {
			t.Fatalf("枚举失败厂商被计入: inst=%d cap=%v", dist.TotalInst, dist.TotalCapGB)
		}
		if _, ok := dist.Percentages["aws"]; ok {
			t.Fatalf("枚举失败厂商不应有占比")
		}
	})
	t.Run("总容量为零不产生NaN", func(t *testing.T) {
		dist := AggregateDistribution([]ProviderUsage{
			{Provider: "huawei", Instances: 3, CapacityGB: 0, ZeroCap: 3},
			{Provider: "aws", Instances: 2, CapacityGB: 0, ZeroCap: 2},
		})
		if dist.TotalCapGB != 0 {
			t.Fatalf("总容量应为 0")
		}
		for p, v := range dist.Percentages {
			if math.IsNaN(v) || v != 0 {
				t.Fatalf("厂商 %s 占比 = %v, 应为 0(不产生 NaN)", p, v)
			}
		}
	})
	t.Run("空输入", func(t *testing.T) {
		dist := AggregateDistribution(nil)
		if dist.TotalInst != 0 || dist.TotalCapGB != 0 || len(dist.Percentages) != 0 {
			t.Fatalf("空输入应得空分布")
		}
	})
}

func TestVerdict(t *testing.T) {
	t.Run("占比超15%且探测可用则升格", func(t *testing.T) {
		dist := AggregateDistribution([]ProviderUsage{
			{Provider: "tencent", Instances: 5, CapacityGB: 60},
			{Provider: "aliyun", Instances: 1, CapacityGB: 40},
		})
		upgraded, secondPhase := Verdict(dist, map[string]bool{"tencent": true})
		if len(upgraded) != 1 || upgraded[0] != "tencent" {
			t.Fatalf("upgraded = %v, want [tencent]", upgraded)
		}
		if len(secondPhase) != 0 {
			t.Fatalf("secondPhase = %v, want 空", secondPhase)
		}
	})
	t.Run("占比超15%但探测不可用归入二期补", func(t *testing.T) {
		dist := AggregateDistribution([]ProviderUsage{
			{Provider: "tencent", Instances: 5, CapacityGB: 60},
			{Provider: "aliyun", Instances: 1, CapacityGB: 40},
		})
		upgraded, secondPhase := Verdict(dist, map[string]bool{"tencent": false})
		if len(upgraded) != 0 {
			t.Fatalf("upgraded = %v, want 空", upgraded)
		}
		if len(secondPhase) != 1 || secondPhase[0] != "tencent" {
			t.Fatalf("secondPhase = %v, want [tencent]", secondPhase)
		}
	})
	t.Run("恰好15%不升格(严格大于)", func(t *testing.T) {
		dist := AggregateDistribution([]ProviderUsage{
			{Provider: "volcengine", Instances: 3, CapacityGB: 15},
			{Provider: "aliyun", Instances: 1, CapacityGB: 85},
		})
		upgraded, secondPhase := Verdict(dist, map[string]bool{"volcengine": true})
		if len(upgraded) != 0 || len(secondPhase) != 0 {
			t.Fatalf("15%% 不应触发: upgraded=%v secondPhase=%v", upgraded, secondPhase)
		}
	})
	t.Run("占比不超15%维持尽力而为", func(t *testing.T) {
		dist := AggregateDistribution([]ProviderUsage{
			{Provider: "volcengine", Instances: 1, CapacityGB: 10},
			{Provider: "aliyun", Instances: 1, CapacityGB: 90},
		})
		upgraded, secondPhase := Verdict(dist, map[string]bool{"volcengine": true})
		if len(upgraded) != 0 || len(secondPhase) != 0 {
			t.Fatalf("10%% 不应触发: upgraded=%v secondPhase=%v", upgraded, secondPhase)
		}
	})
	t.Run("无实盘数据的厂商不参与判定", func(t *testing.T) {
		dist := AggregateDistribution([]ProviderUsage{
			{Provider: "aliyun", Instances: 1, CapacityGB: 100},
		})
		upgraded, secondPhase := Verdict(dist, map[string]bool{"tencent": true, "volcengine": true})
		if len(upgraded) != 0 || len(secondPhase) != 0 {
			t.Fatalf("无数据厂商不应触发: upgraded=%v secondPhase=%v", upgraded, secondPhase)
		}
	})
	t.Run("只针对tencent与volcengine判定", func(t *testing.T) {
		dist := AggregateDistribution([]ProviderUsage{
			{Provider: "huawei", Instances: 1, CapacityGB: 90},
			{Provider: "aws", Instances: 1, CapacityGB: 10},
		})
		upgraded, _ := Verdict(dist, map[string]bool{"huawei": true, "aws": true})
		if len(upgraded) != 0 {
			t.Fatalf("必达厂商不应重复出现在升格清单: %v", upgraded)
		}
	})
}
