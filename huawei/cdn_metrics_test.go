package huawei

import (
	"encoding/json"
	"testing"
)

func f64(v float64) *float64 { return &v }

// ShowDomainStats result {域名:{stat_type:[逐日数组]}} 的解析与聚合
func TestLookupAndAggregateDailySeries(t *testing.T) {
	result := map[string]interface{}{
		"www.example.com": map[string]interface{}{
			"flux": []interface{}{100.0, "250", "-"}, // 数字/字符串/无数据
		},
	}
	domainData, ok := lookupDomainResult(result, "www.example.com")
	if !ok {
		t.Fatal("domain lookup failed")
	}
	series, ok := lookupSeries(domainData, "flux")
	if !ok || len(series) != 3 {
		t.Fatalf("series = %v, %v", series, ok)
	}
	if series[0] == nil || *series[0] != 100 || series[1] == nil || *series[1] != 250 {
		t.Fatalf("series values = %v", series)
	}
	if series[2] != nil {
		t.Fatalf("no-data day ('-') should be nil, got %v", *series[2])
	}
	got := aggregateDailySeries(series, "2026-09-13", aggregateModeSum)
	if got["2026-09-13"] != 100 || got["2026-09-14"] != 250 {
		t.Fatalf("aggregated = %v", got)
	}
	// 无数据日不得占位 0
	if _, ok := got["2026-09-15"]; ok {
		t.Fatalf("no-data day must be absent, got %v", got["2026-09-15"])
	}
}

// 回归:ShowDomainStats 的 map[string]interface{} 元素是 json.Number
// (SDK 用 UseNumber 解码),此前未被 lookupSeries 识别,全部解析为 nil,
// 导致华为域名日指标永远写 0(bytes=0 且命中率 -1)。必须能解析。
func TestLookupSeriesJSONNumber(t *testing.T) {
	result := map[string]interface{}{
		"d": map[string]interface{}{
			"flux": []interface{}{json.Number("10765798179"), json.Number("-1")},
		},
	}
	series, ok := lookupSeries(result["d"].(map[string]interface{}), "flux")
	if !ok || len(series) != 2 {
		t.Fatalf("series = %v, %v", series, ok)
	}
	if series[0] == nil || *series[0] != 10765798179 {
		t.Fatalf("json.Number 值应解析, got %v", series[0])
	}
	// 哨兵 -1 仍视为无数据
	if series[1] != nil {
		t.Fatalf("sentinel -1 should be nil, got %v", *series[1])
	}
	// 全链路:json.Number 响应 → 聚合出真实流量
	got := aggregateDailySeries(series, "2026-09-14", aggregateModeSum)
	if got["2026-09-14"] != 10765798179 {
		t.Fatalf("aggregated = %v", got)
	}
	if _, ok := got["2026-09-15"]; ok {
		t.Fatalf("no-data day must be absent, got %v", got["2026-09-15"])
	}
}

func TestLookupSeriesNoDataSentinels(t *testing.T) {
	result := map[string]interface{}{
		"d": map[string]interface{}{
			"hit_flux_rate": []interface{}{
				"-",    // 字符串无数据
				-1.0,   // 厂商哨兵 -1
				"-1",   // 字符串哨兵
				nil,    // JSON null
				"null", // 字符串 null
				97.35,  // 正常百分制
			},
		},
	}
	series, ok := lookupSeries(result["d"].(map[string]interface{}), "hit_flux_rate")
	if !ok || len(series) != 6 {
		t.Fatalf("series = %v, %v", series, ok)
	}
	for i := 0; i < 5; i++ {
		if series[i] != nil {
			t.Fatalf("index %d should be nil (no data), got %v", i, *series[i])
		}
	}
	if series[5] == nil || *series[5] != 97.35 {
		t.Fatalf("index 5 = %v", series[5])
	}
}

func TestLookupDomainResultCaseInsensitive(t *testing.T) {
	result := map[string]interface{}{
		"WWW.Example.com": map[string]interface{}{"bw": []interface{}{5.0}},
	}
	if _, ok := lookupDomainResult(result, "www.example.com"); !ok {
		t.Fatal("case-insensitive lookup failed")
	}
}

func TestAggregateDailySeriesMax(t *testing.T) {
	series := []*float64{f64(500), f64(3000), f64(250)}
	maxed := aggregateDailySeries(series, "2026-09-14", aggregateModeMax)
	if maxed["2026-09-16"] != 250 {
		t.Fatalf("max = %v", maxed)
	}
	// 无数据日(nil)跳过
	gapped := aggregateDailySeries([]*float64{nil, f64(98)}, "2026-09-14", aggregateModeMax)
	if _, ok := gapped["2026-09-14"]; ok {
		t.Fatalf("no-data day must be absent, got %v", gapped["2026-09-14"])
	}
	if gapped["2026-09-15"] != 98 {
		t.Fatalf("max = %v", gapped["2026-09-15"])
	}
}

// 命中率 = hit_flux / flux;flux 缺失/为 0 时保持 -1(未知),不伪造
func TestBuildDailyMetricsHitFluxRatio(t *testing.T) {
	flux := map[string]float64{
		"2026-09-14": 1000,
		"2026-09-15": 500,
		"2026-09-16": 0, // 无流量日
	}
	hitFlux := map[string]float64{
		"2026-09-14": 973,
		"2026-09-15": 500, // 命中率 100%
		"2026-09-16": 0,
	}

	metrics := buildDailyMetrics("www.example.com", []string{"2026-09-14", "2026-09-15", "2026-09-16", "2026-09-17"}, flux, map[string]float64{}, hitFlux)
	if len(metrics) != 4 {
		t.Fatalf("len = %d", len(metrics))
	}
	if metrics[0].HitRate < 0.9729 || metrics[0].HitRate > 0.9731 {
		t.Fatalf("09-14 hit = %v, want 0.973", metrics[0].HitRate)
	}
	if metrics[1].HitRate != 1 {
		t.Fatalf("09-15 hit = %v, want 1", metrics[1].HitRate)
	}
	// flux==0 日与无数据缺失日(09-17)都必须保持 -1
	if metrics[2].HitRate != unknownHitRate || metrics[3].HitRate != unknownHitRate {
		t.Fatalf("no-data hit = %v, %v; want -1", metrics[2].HitRate, metrics[3].HitRate)
	}
}

// 回归:响应含 "-" / -1 / null 无数据日时,产出的 CDNMetric.HitRate
// 必须保持未知哨兵 -1,不得被伪造为 0%(hit_flux 无数据 + flux 有值)
func TestBuildDailyMetricsNoDataDaysKeepUnknownHitRate(t *testing.T) {
	metrics := buildDailyMetrics(
		"www.example.com",
		[]string{"2026-09-14", "2026-09-15"},
		map[string]float64{"2026-09-14": 1000}, // flux 有值但有 hit_flux 缺失
		map[string]float64{},
		map[string]float64{"2026-09-15": 800},
	)
	if metrics[0].HitRate != unknownHitRate {
		t.Fatalf("09-14 hit_flux 缺失应保持 -1, got %v", metrics[0].HitRate)
	}
	// 09-15:有 hit_flux 但 flux 缺失 → 命中率无意义,保持 -1
	if metrics[1].HitRate != unknownHitRate {
		t.Fatalf("09-15 flux 缺失命中率应保持 -1, got %v", metrics[1].HitRate)
	}
}

func TestMetricDateRangeHuawei(t *testing.T) {
	dates, err := metricDateRange("2026-09-13", "2026-09-15")
	if err != nil {
		t.Fatal(err)
	}
	if len(dates) != 3 {
		t.Fatalf("dates = %v", dates)
	}
	if _, err := metricDateRange("2026-01-01", "2026-09-15"); err == nil {
		t.Fatal("expected error for oversized range")
	}
}

func TestDayStartUnixMilli(t *testing.T) {
	if got := dayStartUnixMilli("2026-09-15"); got != 1789401600000 { // 09-15 00:00 CST
		t.Fatalf("dayStart = %d", got)
	}
}
