package aliyun

import (
	"testing"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/cdn"
)

// 5min 粒度 → 日聚合:流量求和、带宽取峰、命中率归一均值
func TestAggregateAliyunTraffic(t *testing.T) {
	modules := []cdn.DataModule{
		{TimeStamp: "2026-09-14T16:00:00Z", Traf: 100},           // 09-15 00:00 CST
		{TimeStamp: "2026-09-14T16:05:00Z", Value: "250"},        // Traf 缺失回退 Value
		{TimeStamp: "2026-09-14T16:10:00Z", Traf: 0, Value: "-"}, // 无数据
		{TimeStamp: "", Traf: 999},                               // 无时间戳忽略
	}
	got := aggregateAliyunTraffic(modules)
	if got["2026-09-15"] != 350 {
		t.Fatalf("traffic = %d, want 350", got["2026-09-15"])
	}
	if _, ok := got["2026-09-14"]; ok {
		t.Fatalf("UTC 时间应归到 CST 日期 09-15, 不应出现 09-14")
	}
}

func TestAggregateAliyunBps(t *testing.T) {
	modules := []cdn.DataModule{
		{TimeStamp: "2026-09-14T16:00:00Z", Bps: 1024.9},
		{TimeStamp: "2026-09-14T16:05:00Z", Bps: 3000},
		{TimeStamp: "2026-09-14T16:10:00Z", Bps: 250.1},
	}
	got := aggregateAliyunBps(modules)
	if got["2026-09-15"] != 3000 {
		t.Fatalf("bps peak = %d, want 3000", got["2026-09-15"])
	}
}

func TestAggregateAliyunHitRate(t *testing.T) {
	modules := []cdn.DataModule{
		{TimeStamp: "2026-09-14T16:00:00Z", Value: "97.35"}, // 百分制
		{TimeStamp: "2026-09-14T16:05:00Z", Value: "100.00"},
		{TimeStamp: "2026-09-14T16:10:00Z", Value: "-"},   // 非数字跳过
		{TimeStamp: "2026-09-14T16:15:00Z", Value: "0.9"}, // 已是小数
	}
	got := aggregateAliyunHitRate(modules)
	// (0.9735 + 1 + 0.9) / 3
	want := (0.9735 + 1 + 0.9) / 3
	if diff := got["2026-09-15"] - want; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("hit rate = %v, want %v", got["2026-09-15"], want)
	}
}

func TestNormalizeHitRateValue(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{97.35, 0.9735},
		{100, 1},
		{0.5, 0.5}, // 已是 0-1
		{-5, 0},    // 负值夹紧
		{250, 1},   // 上溢夹紧
	}
	for _, c := range cases {
		got := normalizeHitRateValue(c.in)
		diff := got - c.want
		if diff < -1e-9 || diff > 1e-9 {
			t.Errorf("normalizeHitRateValue(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMetricDateRange(t *testing.T) {
	dates, err := metricDateRange("2026-09-13", "2026-09-15")
	if err != nil {
		t.Fatal(err)
	}
	if len(dates) != 3 || dates[0] != "2026-09-13" || dates[2] != "2026-09-15" {
		t.Fatalf("dates = %v", dates)
	}
	// 起止倒置
	if _, err := metricDateRange("2026-09-15", "2026-09-13"); err == nil {
		t.Fatal("expected error for inverted range")
	}
	// 超长区间
	if _, err := metricDateRange("2026-01-01", "2026-09-15"); err == nil {
		t.Fatal("expected error for oversized range")
	}
}

func TestDayBoundsUTC(t *testing.T) {
	start, end := dayBoundsUTC("2026-09-14")
	// 09-14 00:00 CST = 09-13 16:00 UTC
	if start != "2026-09-13T16:00:00Z" {
		t.Fatalf("start = %s", start)
	}
	if end != "2026-09-14T15:59:59Z" { // 09-14 23:59:59 CST
		t.Fatalf("end = %s", end)
	}
}

func TestParseAliyunTimeStamp(t *testing.T) {
	if d, ok := parseAliyunTimeStamp("2026-09-14T16:00:00Z"); !ok || d != "2026-09-15" {
		t.Fatalf("RFC3339 = %s, %v", d, ok)
	}
	if d, ok := parseAliyunTimeStamp("2026-09-15 00:30:00"); !ok || d != "2026-09-15" {
		t.Fatalf("space form = %s, %v", d, ok)
	}
	if _, ok := parseAliyunTimeStamp("garbage"); ok {
		t.Fatal("expected parse failure")
	}
}
