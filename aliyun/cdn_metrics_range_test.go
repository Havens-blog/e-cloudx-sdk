package aliyun

import (
	"context"
	"testing"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/cdn"
)

// rangeQueryDays 三日范围: 首日 09-13、中间 09-14(无数据日)、末日 09-15
var rangeQueryDays = []string{"2026-09-13", "2026-09-14", "2026-09-15"}

// multiDayModules 模拟单次范围调用返回的全区间 5min 明细:
//   - 首日边界: 09-13 00:00 CST = 09-12T16:00:00Z
//   - 末日边界: 09-15 23:55 CST = 09-15T15:55:00Z
//   - 09-14 无任何模块(无数据日,不应产出该日指标)
//   - 混入不可解析时间戳模块(聚合应忽略)
var multiDayModules = []cdn.DataModule{
	{TimeStamp: "2026-09-12T16:00:00Z", Traf: 100, Bps: 1024.9, Value: "97.35"}, // 09-13 00:00 CST 首日边界
	{TimeStamp: "2026-09-12T16:05:00Z", Traf: 250, Bps: 3000, Value: "100.00"},  // 09-13
	{TimeStamp: "2026-09-15T15:50:00Z", Traf: 7, Bps: 250.1, Value: "0.9"},      // 09-15 23:50 CST
	{TimeStamp: "2026-09-15T15:55:00Z", Traf: 43, Bps: 5000, Value: "80"},       // 09-15 23:55 CST 末日边界
	{TimeStamp: "2026-09-15T15:56:00Z", Traf: 0, Bps: 10, Value: "-"},           // 09-15 无效命中率
	{TimeStamp: "not-a-time", Traf: 999, Bps: 9999, Value: "50"},                // 不可解析,忽略
	{TimeStamp: "", Traf: 1, Bps: 1, Value: "1"},                                // 空时间戳,忽略
}

// perDayModules 按日切分模块流(等价于旧版逐日调用的单日响应)
func perDayModules(modules []cdn.DataModule) map[string][]cdn.DataModule {
	byDay := make(map[string][]cdn.DataModule)
	for _, m := range modules {
		if d, ok := parseAliyunTimeStamp(m.TimeStamp); ok {
			byDay[d] = append(byDay[d], m)
		}
	}
	return byDay
}

// perDayTraffic 复现旧版 fetchTrafficByDay 的逐日聚合语义
func perDayTraffic(byDay map[string][]cdn.DataModule, dates []string) map[string]int64 {
	merged := make(map[string]int64)
	for _, d := range dates {
		sums := aggregateAliyunTraffic(byDay[d])
		if len(sums) == 0 {
			continue // 当日无数据不写库
		}
		total := int64(0)
		for _, v := range sums {
			total += v
		}
		merged[d] = total
	}
	return merged
}

// perDayBps 复现旧版 fetchBpsByDay 的逐日聚合语义
func perDayBps(byDay map[string][]cdn.DataModule, dates []string) map[string]int64 {
	merged := make(map[string]int64)
	for _, d := range dates {
		peaks := aggregateAliyunBps(byDay[d])
		if len(peaks) == 0 {
			continue
		}
		merged[d] = peaks[d]
	}
	return merged
}

// perDayHitRate 复现旧版 fetchHitRateByDay 的逐日聚合语义
func perDayHitRate(byDay map[string][]cdn.DataModule, dates []string) map[string]float64 {
	merged := make(map[string]float64)
	for _, d := range dates {
		rates := aggregateAliyunHitRate(byDay[d])
		if len(rates) == 0 {
			continue
		}
		merged[d] = rates[d]
	}
	return merged
}

// assertDateMapsEqual 逐日期断言两个聚合 map 相等(含缺失日期)
func assertDateMapsEqual(t *testing.T, name string, got, want map[string]int64) {
	t.Helper()
	for d, wv := range want {
		gv, ok := got[d]
		if !ok {
			t.Errorf("%s: 日期 %s 范围聚合缺失, 逐日聚合 = %d", name, d, wv)
			continue
		}
		if gv != wv {
			t.Errorf("%s: 日期 %s 范围聚合 = %d, 逐日聚合 = %d", name, d, gv, wv)
		}
	}
	for d := range got {
		if _, ok := want[d]; !ok {
			t.Errorf("%s: 日期 %s 仅范围聚合出现(逐日无), 值 = %d", name, d, got[d])
		}
	}
}

func assertRateMapsEqual(t *testing.T, name string, got, want map[string]float64) {
	t.Helper()
	for d, wv := range want {
		gv, ok := got[d]
		if !ok {
			t.Errorf("%s: 日期 %s 范围聚合缺失, 逐日聚合 = %v", name, d, wv)
			continue
		}
		if diff := gv - wv; diff < -1e-9 || diff > 1e-9 {
			t.Errorf("%s: 日期 %s 范围聚合 = %v, 逐日聚合 = %v", name, d, gv, wv)
		}
	}
	for d := range got {
		if _, ok := want[d]; !ok {
			t.Errorf("%s: 日期 %s 仅范围聚合出现(逐日无), 值 = %v", name, d, got[d])
		}
	}
}

// TestRangeBoundsUTC 范围边界: Start=首日 00:00 CST、End=末日 23:59:59 CST(UTC ISO)
func TestRangeBoundsUTC(t *testing.T) {
	start, end := rangeBoundsUTC(rangeQueryDays)
	// 09-13 00:00 CST = 09-12T16:00:00Z
	if start != "2026-09-12T16:00:00Z" {
		t.Fatalf("start = %s, want 2026-09-12T16:00:00Z", start)
	}
	// 09-15 23:59:59 CST = 09-15T15:59:59Z
	if end != "2026-09-15T15:59:59Z" {
		t.Fatalf("end = %s, want 2026-09-15T15:59:59Z", end)
	}
	// 单日范围: 边界为同一天起止
	start, end = rangeBoundsUTC([]string{"2026-09-14"})
	if start != "2026-09-13T16:00:00Z" || end != "2026-09-14T15:59:59Z" {
		t.Fatalf("单日范围 start=%s end=%s", start, end)
	}
	// 空日期切片防御
	if s, e := rangeBoundsUTC(nil); s != "" || e != "" {
		t.Fatalf("空区间应返回空串, got %s ~ %s", s, e)
	}
}

// TestRangeAggregateTrafficMatchesPerDay 范围流量聚合与逐日聚合逐日期相等;
// 无数据日(09-14)两侧均不产出;首末日边界时间戳均被聚合。
func TestRangeAggregateTrafficMatchesPerDay(t *testing.T) {
	byDay := perDayModules(multiDayModules)
	got := aggregateAliyunTraffic(multiDayModules) // 单次范围响应聚合
	want := perDayTraffic(byDay, rangeQueryDays)   // 逐日聚合合并

	if _, ok := got["2026-09-14"]; ok {
		t.Fatalf("无数据日 09-14 不应产出流量指标")
	}
	if got["2026-09-13"] != 350 {
		t.Fatalf("首日边界聚合 = %d, want 350", got["2026-09-13"])
	}
	if got["2026-09-15"] != 50 {
		t.Fatalf("末日边界聚合 = %d, want 50", got["2026-09-15"])
	}
	assertDateMapsEqual(t, "traffic", got, want)
}

func TestRangeAggregateBpsMatchesPerDay(t *testing.T) {
	byDay := perDayModules(multiDayModules)
	got := aggregateAliyunBps(multiDayModules)
	want := perDayBps(byDay, rangeQueryDays)

	if _, ok := got["2026-09-14"]; ok {
		t.Fatalf("无数据日 09-14 不应产出带宽指标")
	}
	if got["2026-09-13"] != 3000 || got["2026-09-15"] != 5000 {
		t.Fatalf("bps peaks = %v, want 09-13=3000 09-15=5000", got)
	}
	assertDateMapsEqual(t, "bps", got, want)
}

func TestRangeAggregateHitRateMatchesPerDay(t *testing.T) {
	byDay := perDayModules(multiDayModules)
	got := aggregateAliyunHitRate(multiDayModules)
	want := perDayHitRate(byDay, rangeQueryDays)

	if _, ok := got["2026-09-14"]; ok {
		t.Fatalf("无数据日 09-14 不应产出命中率指标")
	}
	wantFirstDay := (0.9735 + 1) / 2
	if diff := got["2026-09-13"] - wantFirstDay; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("首日命中率 = %v, want %v", got["2026-09-13"], wantFirstDay)
	}
	wantLastDay := (0.9 + 0.8) / 2 // "0.9" 已是小数, "80" 百分制归一
	if diff := got["2026-09-15"] - wantLastDay; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("末日命中率 = %v, want %v", got["2026-09-15"], wantLastDay)
	}
	assertRateMapsEqual(t, "hitRate", got, want)
}

// TestGetDomainMetricsGuards 入参守卫: 空域名/起止倒置在任何 API 调用前报错
func TestGetDomainMetricsGuards(t *testing.T) {
	a := &CDNAdapter{}
	if _, err := a.GetDomainMetrics(context.Background(), "", "id", "2026-09-13", "2026-09-15"); err == nil {
		t.Fatal("空域名应报错")
	}
	if _, err := a.GetDomainMetrics(context.Background(), "d", "id", "2026-09-15", "2026-09-13"); err == nil {
		t.Fatal("起止倒置应报错")
	}
}
