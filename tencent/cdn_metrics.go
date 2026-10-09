package tencent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	tencentcdn "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cdn/v20180606"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
)

// unknownHitRate 命中率未知哨兵值
const unknownHitRate = -1.0

// GetDomainMetrics 查询 [startDate, endDate](含两端)内域名的逐日指标。
// 单次调用按 5min 粒度拉全区间(≤31 天),逐日聚合:
// 流量求和、带宽取峰值、命中率取均值。指标分 3 次调用
// (flux/bandwidth/fluxHitRate),腾讯云 DescribeCdnData 单次仅支持一个指标。
func (a *CDNAdapter) GetDomainMetrics(ctx context.Context, domainName, domainID string, startDate, endDate string) ([]types.CDNMetric, error) {
	if domainName == "" {
		return nil, fmt.Errorf("腾讯云CDN指标查询需要域名")
	}
	dates, err := metricDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	client, err := a.createClient()
	if err != nil {
		return nil, err
	}

	bytes, err := a.fetchCdnMetricRange(client, domainName, "flux", dates[0], dates[len(dates)-1], aggregateModeSum)
	if err != nil {
		return nil, err
	}
	bandwidth, err := a.fetchCdnMetricRange(client, domainName, "bandwidth", dates[0], dates[len(dates)-1], aggregateModeMax)
	if err != nil {
		return nil, err
	}
	hitRate, err := a.fetchCdnMetricRange(client, domainName, "fluxHitRate", dates[0], dates[len(dates)-1], aggregateModeAvg)
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
		if v, ok := hitRate[d]; ok {
			m.HitRate = v
		}
		metrics = append(metrics, m)
	}
	return metrics, nil
}

// fetchCdnMetricRange 调 DescribeCdnData 拉单指标全区间 5min 明细,
// 按运营时区(Asia/Shanghai)逐日聚合。
func (a *CDNAdapter) fetchCdnMetricRange(
	client *tencentcdn.Client,
	domainName, metric, startDate, endDate string,
	mode aggregateMode,
) (map[string]float64, error) {
	request := tencentcdn.NewDescribeCdnDataRequest()
	request.Metric = common.StringPtr(metric)
	request.Domains = common.StringPtrs([]string{domainName})
	// 腾讯云时间为北京时间格式字符串;区间为 [当日 00:00:00, 当日 23:59:59]
	request.StartTime = common.StringPtr(startDate + " 00:00:00")
	request.EndTime = common.StringPtr(endDate + " 23:59:59")
	request.Interval = common.StringPtr("5min")

	response, err := client.DescribeCdnData(request)
	if err != nil {
		return nil, fmt.Errorf("查询CDN指标 %s 失败: %w", metric, err)
	}
	result := make(map[string]float64)
	if response == nil || response.Response == nil {
		return result, nil
	}
	for _, resource := range response.Response.Data {
		if resource == nil {
			continue
		}
		for _, item := range resource.CdnData {
			if item == nil {
				continue
			}
			for key, val := range aggregateTencentDetail(item.DetailData, mode) {
				result[key] = val
			}
		}
	}
	return result, nil
}

// ==================== 聚合模式 ====================

type aggregateMode int

const (
	aggregateModeSum aggregateMode = iota
	aggregateModeMax
	aggregateModeAvg
)

// aggregateTencentDetail 5min 粒度明细点 → 按日聚合(mode 决定求和/峰值/均值)。
func aggregateTencentDetail(points []*tencentcdn.TimestampData, mode aggregateMode) map[string]float64 {
	sums := make(map[string]float64)
	counts := make(map[string]int)
	peaks := make(map[string]float64)
	for _, p := range points {
		if p == nil || p.Value == nil {
			continue
		}
		date, ok := parseTencentTime(stringPtrVal(p.Time))
		if !ok {
			continue
		}
		v := float64PtrVal(p.Value)
		switch mode {
		case aggregateModeSum:
			sums[date] += v
		case aggregateModeMax:
			if v > peaks[date] {
				peaks[date] = v
			}
		case aggregateModeAvg:
			sums[date] += v
			counts[date]++
		}
	}
	result := make(map[string]float64, len(sums)+len(peaks))
	for date, v := range sums {
		if mode == aggregateModeAvg {
			if counts[date] > 0 {
				result[date] = v / float64(counts[date])
			}
			continue
		}
		result[date] = v
	}
	for date, v := range peaks {
		result[date] = v
	}
	return result
}

// metricCSTZone CDN 指标按运营时区(Asia/Shanghai)取日,勿改用服务器本地时区
var metricCSTZone = time.FixedZone("CST", 8*3600)

// stringPtrVal / float64PtrVal nil 安全解引用(本 SDK 版本无 StringValue 等取值助手)
func stringPtrVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func float64PtrVal(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// parseTencentTime 解析腾讯云时间点("2018-09-04 10:40:00" 北京时间,或
// day 粒度的 "2018-09-04")到日期。
func parseTencentTime(ts string) (string, bool) {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return "", false
	}
	layouts := []string{"2006-01-02 15:04:05", "2006-01-02"}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, ts, metricCSTZone); err == nil {
			return t.Format("2006-01-02"), true
		}
	}
	return "", false
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
	const maxRangeDays = 31 // 5min 粒度最长 31 天
	dates := make([]string, 0, int(end.Sub(start)/24/time.Hour)+1)
	for t := start; !t.After(end); t = t.AddDate(0, 0, 1) {
		if len(dates) >= maxRangeDays {
			return nil, fmt.Errorf("指标查询区间超过 %d 天", maxRangeDays)
		}
		dates = append(dates, t.Format("2006-01-02"))
	}
	return dates, nil
}
