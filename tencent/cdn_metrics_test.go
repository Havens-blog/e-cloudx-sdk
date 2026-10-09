package tencent

import (
	"testing"

	tencentcdn "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cdn/v20180606"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
)

// 5min 粒度 → 日聚合:流量求和、带宽取峰、命中率归一均值
func TestAggregateTencentDetailSum(t *testing.T) {
	points := []*tencentcdn.TimestampData{
		{Time: common.StringPtr("2026-09-15 00:00:00"), Value: common.Float64Ptr(100)},
		{Time: common.StringPtr("2026-09-15 00:05:00"), Value: common.Float64Ptr(250)},
		{Time: common.StringPtr("2026-09-15 00:10:00"), Value: common.Float64Ptr(0)},
		{Time: common.StringPtr("garbage"), Value: common.Float64Ptr(999)}, // 无法解析忽略
		{Time: nil, Value: common.Float64Ptr(999)},                         // 无时间忽略
	}
	got := aggregateTencentDetail(points, aggregateModeSum)
	if got["2026-09-15"] != 350 {
		t.Fatalf("sum = %v, want 350", got["2026-09-15"])
	}
}

func TestAggregateTencentDetailMax(t *testing.T) {
	points := []*tencentcdn.TimestampData{
		{Time: common.StringPtr("2026-09-15 00:00:00"), Value: common.Float64Ptr(1024.9)},
		{Time: common.StringPtr("2026-09-15 00:05:00"), Value: common.Float64Ptr(3000)},
		{Time: common.StringPtr("2026-09-15 00:10:00"), Value: common.Float64Ptr(250.1)},
	}
	got := aggregateTencentDetail(points, aggregateModeMax)
	if got["2026-09-15"] != 3000 {
		t.Fatalf("max = %v, want 3000", got["2026-09-15"])
	}
}

func TestAggregateTencentDetailAvg(t *testing.T) {
	points := []*tencentcdn.TimestampData{
		{Time: common.StringPtr("2026-09-15 00:00:00"), Value: common.Float64Ptr(97.35)}, // 百分制
		{Time: common.StringPtr("2026-09-15 00:05:00"), Value: common.Float64Ptr(100)},
		{Time: common.StringPtr("2026-09-15 00:10:00"), Value: common.Float64Ptr(0.9)}, // 已是小数
		{Time: common.StringPtr("2026-09-15 00:15:00"), Value: nil},                    // 无值跳过
	}
	got := aggregateTencentDetail(points, aggregateModeAvg)
	want := (97.35 + 100 + 0.9) / 3
	if diff := got["2026-09-15"] - want; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("avg = %v, want %v", got["2026-09-15"], want)
	}
}

func TestMetricDateRangeTencent(t *testing.T) {
	dates, err := metricDateRange("2026-09-13", "2026-09-15")
	if err != nil {
		t.Fatal(err)
	}
	if len(dates) != 3 || dates[0] != "2026-09-13" || dates[2] != "2026-09-15" {
		t.Fatalf("dates = %v", dates)
	}
	if _, err := metricDateRange("2026-09-15", "2026-09-13"); err == nil {
		t.Fatal("expected error for inverted range")
	}
	if _, err := metricDateRange("2026-01-01", "2026-09-15"); err == nil {
		t.Fatal("expected error for oversized range")
	}
}

func TestParseTencentTime(t *testing.T) {
	if d, ok := parseTencentTime("2026-09-15 00:00:00"); !ok || d != "2026-09-15" {
		t.Fatalf("datetime = %s, %v", d, ok)
	}
	if d, ok := parseTencentTime("2026-09-15"); !ok || d != "2026-09-15" {
		t.Fatalf("date = %s, %v", d, ok)
	}
	if _, ok := parseTencentTime("  "); ok {
		t.Fatal("expected parse failure for empty")
	}
}
