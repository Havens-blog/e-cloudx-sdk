package volcano

import (
	"context"
	"fmt"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/volcengine/volcengine-go-sdk/service/cdn"
)

// unknownHitRate 命中率未知哨兵值
const unknownHitRate = -1.0

// GetDomainMetrics 查询 [startDate, endDate](含两端)内域名的逐日指标。
// DescribeEdgeData 单次仅支持一个指标,分 2 次调用:flux(流量,字节求和)
// 与 bandwidth(带宽,bps 取峰值)。Interval 不传由厂商按区间自适应粒度,
// 返回点按运营时区(Asia/Shanghai)逐日聚合。厂商未提供命中率,置 -1。
func (a *CDNAdapter) GetDomainMetrics(ctx context.Context, domainName, domainID string, startDate, endDate string) ([]types.CDNMetric, error) {
	if domainName == "" {
		return nil, fmt.Errorf("火山引擎CDN指标查询需要域名")
	}
	dates, err := metricDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	client, err := a.createClient()
	if err != nil {
		return nil, err
	}

	bytes, err := a.fetchEdgeMetric(client, domainName, "flux", dates[0], dates[len(dates)-1], aggregateModeSum)
	if err != nil {
		return nil, err
	}
	bandwidth, err := a.fetchEdgeMetric(client, domainName, "bandwidth", dates[0], dates[len(dates)-1], aggregateModeMax)
	if err != nil {
		return nil, err
	}

	metrics := make([]types.CDNMetric, 0, len(dates))
	for _, d := range dates {
		m := types.CDNMetric{
			Domain:  domainName,
			Date:    d,
			HitRate: unknownHitRate,
		}
		if v, ok := bytes[d]; ok {
			m.Bytes = int64(v)
		}
		if v, ok := bandwidth[d]; ok {
			m.Bandwidth = int64(v)
		}
		metrics = append(metrics, m)
	}
	return metrics, nil
}

// fetchEdgeMetric 调 DescribeEdgeData 拉单指标全区间数据,逐日聚合。
func (a *CDNAdapter) fetchEdgeMetric(
	client *cdn.CDN,
	domainName, metric, startDate, endDate string,
	mode aggregateMode,
) (map[string]float64, error) {
	startMs, endMs, err := rangeUnixMilli(startDate, endDate)
	if err != nil {
		return nil, err
	}
	input := &cdn.DescribeEdgeDataInput{}
	input.SetDomain(domainName)
	input.SetMetric(metric)
	input.SetStartTime(startMs)
	input.SetEndTime(endMs)

	output, err := client.DescribeEdgeData(input)
	if err != nil {
		return nil, fmt.Errorf("查询CDN指标 %s 失败: %w", metric, err)
	}
	result := make(map[string]float64)
	if output == nil {
		return result, nil
	}
	for _, series := range output.MetricDataList {
		if series == nil {
			continue
		}
		for key, val := range aggregateEdgeValues(series.Values, mode) {
			result[key] = val
		}
	}
	return result, nil
}

// ==================== 聚合模式与纯函数(单测覆盖) ====================

type aggregateMode int

const (
	aggregateModeSum aggregateMode = iota
	aggregateModeMax
)

// aggregateEdgeValues 区间数据点 → 按日聚合(mode 决定求和/峰值)。
func aggregateEdgeValues(points []*cdn.ValueForDescribeEdgeDataOutput, mode aggregateMode) map[string]float64 {
	sums := make(map[string]float64)
	peaks := make(map[string]float64)
	for _, p := range points {
		if p == nil || p.Value == nil || p.TimeStamp == nil {
			continue
		}
		date := dateFromUnixSecond(*p.TimeStamp)
		if date == "" {
			continue
		}
		switch mode {
		case aggregateModeSum:
			sums[date] += *p.Value
		case aggregateModeMax:
			if *p.Value > peaks[date] {
				peaks[date] = *p.Value
			}
		}
	}
	result := make(map[string]float64, len(sums)+len(peaks))
	for date, v := range sums {
		result[date] = v
	}
	for date, v := range peaks {
		result[date] = v
	}
	return result
}

// metricCSTZone CDN 指标按运营时区(Asia/Shanghai)取日,勿改用服务器本地时区
var metricCSTZone = time.FixedZone("CST", 8*3600)

// dateFromUnixSecond 秒级时间戳(容错毫秒)→ 运营时区日期
func dateFromUnixSecond(ts int64) string {
	if ts <= 0 {
		return ""
	}
	if ts > 1e12 { // 毫秒级时间戳
		ts /= 1000
	}
	return time.Unix(ts, 0).In(metricCSTZone).Format("2006-01-02")
}

// rangeUnixMilli [startDate 00:00, endDate 23:59:59](运营时区)→ 毫秒时间戳
func rangeUnixMilli(startDate, endDate string) (int64, int64, error) {
	start, err := time.ParseInLocation("2006-01-02", startDate, metricCSTZone)
	if err != nil {
		return 0, 0, fmt.Errorf("解析 startDate 失败: %w", err)
	}
	end, err := time.ParseInLocation("2006-01-02", endDate, metricCSTZone)
	if err != nil {
		return 0, 0, fmt.Errorf("解析 endDate 失败: %w", err)
	}
	if end.Before(start) {
		return 0, 0, fmt.Errorf("startDate %s 晚于 endDate %s", startDate, endDate)
	}
	return start.Unix() * 1000,
		(end.Add(24*time.Hour - time.Second)).Unix() * 1000,
		nil
}

// metricDateRange 解析 [startDate, endDate](含两端)为日期切片(YYYY-MM-DD)。
func metricDateRange(startDate, endDate string) ([]string, error) {
	start, err := time.ParseInLocation("2006-01-02", startDate, metricCSTZone)
	if err != nil {
		return nil, fmt.Errorf("解析 startDate 失败: %w", err)
	}
	end, err := time.ParseInLocation("2006-01-02", endDate, metricCSTZone)
	if err != nil {
		return nil, fmt.Errorf("解析 endDate 失败: %w", err)
	}
	if end.Before(start) {
		return nil, fmt.Errorf("startDate %s 晚于 endDate %s", startDate, endDate)
	}
	const maxRangeDays = 31 // 边缘数据明细最长 31 天
	dates := make([]string, 0, int(end.Sub(start)/24/time.Hour)+1)
	for t := start; !t.After(end); t = t.AddDate(0, 0, 1) {
		if len(dates) >= maxRangeDays {
			return nil, fmt.Errorf("指标查询区间超过 %d 天", maxRangeDays)
		}
		dates = append(dates, t.Format("2006-01-02"))
	}
	return dates, nil
}
