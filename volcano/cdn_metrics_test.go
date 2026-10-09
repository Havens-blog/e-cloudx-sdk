package volcano

import (
	"testing"

	"github.com/volcengine/volcengine-go-sdk/service/cdn"
)

// 区间数据点 → 日聚合:流量求和、带宽取峰
func TestAggregateEdgeValuesSum(t *testing.T) {
	// 2026-09-15 00:00 CST = 2026-09-14 16:00 UTC = 1789401600 (秒)
	points := []*cdn.ValueForDescribeEdgeDataOutput{
		{TimeStamp: int64Ptr(1789401600), Value: float64Ptr(100)},
		{TimeStamp: int64Ptr(1789401900), Value: float64Ptr(250)},
		{TimeStamp: int64Ptr(1789401900000), Value: float64Ptr(5)}, // 毫秒级容错
		{TimeStamp: nil, Value: float64Ptr(999)},
		{TimeStamp: int64Ptr(1789401600), Value: nil},
	}
	got := aggregateEdgeValues(points, aggregateModeSum)
	if got["2026-09-15"] != 355 {
		t.Fatalf("sum = %v, want 355", got["2026-09-15"])
	}
}

func TestAggregateEdgeValuesMax(t *testing.T) {
	points := []*cdn.ValueForDescribeEdgeDataOutput{
		{TimeStamp: int64Ptr(1789401600), Value: float64Ptr(1024.9)},
		{TimeStamp: int64Ptr(1789401900), Value: float64Ptr(3000)},
		{TimeStamp: int64Ptr(1789402200), Value: float64Ptr(250.1)},
	}
	got := aggregateEdgeValues(points, aggregateModeMax)
	if got["2026-09-15"] != 3000 {
		t.Fatalf("max = %v, want 3000", got["2026-09-15"])
	}
}

func TestDateFromUnixSecond(t *testing.T) {
	if d := dateFromUnixSecond(1789401600); d != "2026-09-15" { // 09-15 00:00 CST
		t.Fatalf("date = %s", d)
	}
	if d := dateFromUnixSecond(1789401600000); d != "2026-09-15" { // 毫秒级
		t.Fatalf("ms date = %s", d)
	}
	if d := dateFromUnixSecond(0); d != "" {
		t.Fatalf("zero date = %s", d)
	}
}

func TestMetricDateRangeVolcano(t *testing.T) {
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

func TestRangeUnixMilli(t *testing.T) {
	startMs, endMs, err := rangeUnixMilli("2026-09-15", "2026-09-16")
	if err != nil {
		t.Fatal(err)
	}
	// 09-15 00:00 CST = 09-14 16:00 UTC
	if startMs != 1789401600000 {
		t.Fatalf("startMs = %d", startMs)
	}
	// 09-16 23:59:59 CST
	if endMs != 1789574399000 {
		t.Fatalf("endMs = %d", endMs)
	}
}

func int64Ptr(v int64) *int64       { return &v }
func float64Ptr(v float64) *float64 { return &v }
