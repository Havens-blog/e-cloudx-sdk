package aliyun

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/cdn"
)

// GetDomainMetrics 查询 [startDate, endDate](含两端)内域名的逐日指标。
// 每指标单次范围调用(StartTime=首日 00:00、EndTime=末日 23:59:59)拉取
// 全区间 5min 粒度数据(Interval=300),再按运营时区(Asia/Shanghai)逐日聚合:
// 流量求和、带宽取峰值、命中率取均值。每域名 3 次 API(原逐日 30×3=90 次)。
func (a *CDNAdapter) GetDomainMetrics(ctx context.Context, domainName, domainID string, startDate, endDate string) ([]types.CDNMetric, error) {
	if domainName == "" {
		return nil, fmt.Errorf("阿里云CDN指标查询需要域名")
	}
	dates, err := metricDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	client, err := a.createClient()
	if err != nil {
		return nil, err
	}

	// 与 tencent/huawei/volcano 对齐的单次范围调用(区间 Start/End 见各 fetch);
	// 若范围响应混入非目标日期模块,聚合按时间戳分桶天然忽略
	traffic, err := a.fetchTraffic(client, domainName, dates)
	if err != nil {
		return nil, err
	}
	bps, err := a.fetchBps(client, domainName, dates)
	if err != nil {
		return nil, err
	}
	hitRate, err := a.fetchHitRate(client, domainName, dates)
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
		if v, ok := traffic[d]; ok {
			m.Bytes = v
		}
		if v, ok := bps[d]; ok {
			m.Bandwidth = v
		}
		if v, ok := hitRate[d]; ok {
			m.HitRate = v
		}
		metrics = append(metrics, m)
	}
	return metrics, nil
}

// unknownHitRate 命中率未知哨兵值
const unknownHitRate = -1.0

// fetchTraffic 单次范围拉 DescribeDomainTrafficData(Start=dates[0] 00:00、
// End=dates[last] 23:59:59,Interval=300 固定 5min 粒度),按日累加流量(字节)。
// 风险提示: 30 天单次响应约 8640 个模块;若生产遇阿里截断,回退方案为
// 分段范围查询(按周/月拆分 Start/End,聚合按日分桶无需改动)。
func (a *CDNAdapter) fetchTraffic(client *cdn.Client, domainName string, dates []string) (map[string]int64, error) {
	if len(dates) == 0 {
		return map[string]int64{}, nil
	}
	start, end := rangeBoundsUTC(dates)
	request := cdn.CreateDescribeDomainTrafficDataRequest()
	request.DomainName = domainName
	request.StartTime = start
	request.EndTime = end
	request.Interval = "300" // 5min 粒度

	response, err := client.DescribeDomainTrafficData(request)
	if err != nil {
		return nil, fmt.Errorf("查询CDN流量数据失败(%s~%s): %w", dates[0], dates[len(dates)-1], err)
	}
	// 无数据日(如刚添加的域名)在聚合 map 中缺键,由调用方跳过,不写库
	return aggregateAliyunTraffic(response.TrafficDataPerInterval.DataModule), nil
}

// fetchBps 单次范围拉 DescribeDomainBpsData,按日取带宽峰值(bps)。
func (a *CDNAdapter) fetchBps(client *cdn.Client, domainName string, dates []string) (map[string]int64, error) {
	if len(dates) == 0 {
		return map[string]int64{}, nil
	}
	start, end := rangeBoundsUTC(dates)
	request := cdn.CreateDescribeDomainBpsDataRequest()
	request.DomainName = domainName
	request.StartTime = start
	request.EndTime = end
	request.Interval = "300"

	response, err := client.DescribeDomainBpsData(request)
	if err != nil {
		return nil, fmt.Errorf("查询CDN带宽数据失败(%s~%s): %w", dates[0], dates[len(dates)-1], err)
	}
	return aggregateAliyunBps(response.BpsDataPerInterval.DataModule), nil
}

// fetchHitRate 单次范围拉 DescribeDomainHitRateData,按日取命中率均值(0-1)。
func (a *CDNAdapter) fetchHitRate(client *cdn.Client, domainName string, dates []string) (map[string]float64, error) {
	if len(dates) == 0 {
		return map[string]float64{}, nil
	}
	start, end := rangeBoundsUTC(dates)
	request := cdn.CreateDescribeDomainHitRateDataRequest()
	request.DomainName = domainName
	request.StartTime = start
	request.EndTime = end
	request.Interval = "300"

	response, err := client.DescribeDomainHitRateData(request)
	if err != nil {
		return nil, fmt.Errorf("查询CDN命中率数据失败(%s~%s): %w", dates[0], dates[len(dates)-1], err)
	}
	return aggregateAliyunHitRate(response.HitRateInterval.DataModule), nil
}

// ==================== 纯聚合函数(单测覆盖) ====================

// dayBoundsUTC 单日(运营时区 Asia/Shanghai)的 UTC ISO 起止时间,
// 阿里云 Describe*Data 接口接受 ISO8601 格式。
func dayBoundsUTC(date string) (string, string) {
	loc := metricCSTZone
	t, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return date + "T00:00:00Z", date + "T23:59:59Z"
	}
	return t.UTC().Format("2006-01-02T15:04:05Z"),
		t.Add(24*time.Hour - time.Second).UTC().Format("2006-01-02T15:04:05Z")
}

// metricCSTZone CDN 指标按运营时区(Asia/Shanghai)取日,勿改用服务器本地时区
var metricCSTZone = time.FixedZone("CST", 8*3600)

// rangeBoundsUTC 多日范围(运营时区 Asia/Shanghai)的 UTC ISO 起止时间:
// Start=首日 00:00、End=末日 23:59:59,沿用 dayBoundsUTC 的单日边界语义。
func rangeBoundsUTC(dates []string) (string, string) {
	if len(dates) == 0 {
		return "", ""
	}
	start, _ := dayBoundsUTC(dates[0])
	_, end := dayBoundsUTC(dates[len(dates)-1])
	return start, end
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
	const maxRangeDays = 92 // 阿里云指标接口最长 90 天,留缓冲防跨界
	dates := make([]string, 0, int(end.Sub(start)/24/time.Hour)+1)
	for t := start; !t.After(end); t = t.AddDate(0, 0, 1) {
		if len(dates) >= maxRangeDays {
			return nil, fmt.Errorf("指标查询区间超过 %d 天", maxRangeDays)
		}
		dates = append(dates, t.Format("2006-01-02"))
	}
	return dates, nil
}

// parseAliyunTimeStamp 解析阿里云 DataModule.TimeStamp(支持 RFC3339 与
// "2006-01-02 15:04:05" 两种形态),按运营时区归到日期。
func parseAliyunTimeStamp(ts string) (string, bool) {
	if ts == "" {
		return "", false
	}
	layouts := []string{"2006-01-02T15:04:05Z", "2006-01-02T15:04:05+08:00", "2006-01-02 15:04:05"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, ts); err == nil {
			return t.In(metricCSTZone).Format("2006-01-02"), true
		}
	}
	return "", false
}

// aliyunTrafficFromModule 单个区间的流量(字节):
// 优先取 Traf(int64),缺失(=0)时回退解析 Value 字符串。
func aliyunTrafficFromModule(m cdn.DataModule) int64 {
	if m.Traf > 0 {
		return m.Traf
	}
	v, err := strconv.ParseInt(strings.TrimSpace(m.Value), 10, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// aliyunRateFromModule 解析命中率字符串(如 "97.35" 为百分制,归一到 0-1)。
// 厂商未产出(空串/非数字/"-")时返回 (0, false)。
func aliyunRateFromModule(m cdn.DataModule) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(m.Value), 64)
	if err != nil {
		return 0, false
	}
	return normalizeHitRateValue(v), true
}

// normalizeHitRateValue 命中率归一:百分制(>1)除以 100,夹紧到 [0,1]。
// 阈值 >1 的歧义边界(厂商报 "1.00" 无法区分 1% 与 100%)取分数解释。
func normalizeHitRateValue(v float64) float64 {
	if v > 1 {
		v = v / 100
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// aggregateAliyunTraffic 5min/1h 粒度区间 → 按日流量求和(字节)。
func aggregateAliyunTraffic(modules []cdn.DataModule) map[string]int64 {
	result := make(map[string]int64, 1)
	for _, m := range modules {
		date, ok := parseAliyunTimeStamp(m.TimeStamp)
		if !ok {
			continue
		}
		result[date] += aliyunTrafficFromModule(m)
	}
	return result
}

// aggregateAliyunBps 5min/1h 粒度区间 → 按日带宽峰值(bps)。
func aggregateAliyunBps(modules []cdn.DataModule) map[string]int64 {
	result := make(map[string]int64, 1)
	for _, m := range modules {
		date, ok := parseAliyunTimeStamp(m.TimeStamp)
		if !ok {
			continue
		}
		if m.Bps > float64(result[date]) {
			result[date] = int64(m.Bps)
		}
	}
	return result
}

// aggregateAliyunHitRate 5min/1h 粒度区间 → 按日命中率均值(0-1)。
func aggregateAliyunHitRate(modules []cdn.DataModule) map[string]float64 {
	sums := make(map[string]float64, 1)
	counts := make(map[string]int, 1)
	for _, m := range modules {
		date, ok := parseAliyunTimeStamp(m.TimeStamp)
		if !ok {
			continue
		}
		v, ok := aliyunRateFromModule(m)
		if !ok {
			continue
		}
		sums[date] += v
		counts[date]++
	}
	result := make(map[string]float64, len(sums))
	for date, sum := range sums {
		if counts[date] > 0 {
			result[date] = sum / float64(counts[date])
		}
	}
	return result
}
