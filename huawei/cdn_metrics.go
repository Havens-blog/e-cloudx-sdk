package huawei

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/global"
	cdnv1 "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cdn/v1"
	cdnv1model "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cdn/v1/model"
	cdnv1region "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cdn/v1/region"
)

// unknownHitRate 命中率未知哨兵值
const unknownHitRate = -1.0

// createV1Client 创建 CDN v1 客户端(统计接口 ShowDomainStats 仅 v1 提供,
// 域名管理走 v2)。CDN 是全局服务,使用全局凭证 + cn-north-1 region。
func (a *CDNAdapter) createV1Client() (*cdnv1.CdnClient, error) {
	auth, err := global.NewCredentialsBuilder().
		WithAk(a.accessKeyID).
		WithSk(a.accessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}

	client, err := cdnv1.CdnClientBuilder().
		WithRegion(cdnv1region.CN_NORTH_1).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云CDN客户端失败: %w", err)
	}

	return cdnv1.NewCdnClient(client), nil
}

// GetDomainMetrics 查询 [startDate, endDate](含两端)内域名的逐日指标。
// 走 v1 ShowDomainStats(action=detail, interval=86400, group_by=domain),
// 单次仅支持一个 stat_type,分 3 次调用:flux(流量,字节)、bw(带宽,bps)、
// hit_flux(命中流量,字节)。命中率无直接字段,由 hit_flux/flux 计算。
// 返回 result 为 {域名: {stat_type: [逐日数组]}} 的裸 JSON(SDK 未强建模),手工解析。
//
// 注意:华为 stat_type 合法值为 flux/bw/hit_flux 等,**无 hit_flux_rate**
// (那是腾讯云的字段名;误用会报 CDN.0001 item name is incorrect)。
func (a *CDNAdapter) GetDomainMetrics(ctx context.Context, domainName, domainID string, startDate, endDate string) ([]types.CDNMetric, error) {
	if domainName == "" {
		return nil, fmt.Errorf("华为云CDN指标查询需要域名")
	}
	dates, err := metricDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	client, err := a.createV1Client()
	if err != nil {
		return nil, err
	}

	flux, err := a.fetchDomainStats(client, domainName, "flux", dates, aggregateModeSum)
	if err != nil {
		return nil, err
	}
	bw, err := a.fetchDomainStats(client, domainName, "bw", dates, aggregateModeMax)
	if err != nil {
		return nil, err
	}
	hitFlux, err := a.fetchDomainStats(client, domainName, "hit_flux", dates, aggregateModeSum)
	if err != nil {
		return nil, err
	}

	return buildDailyMetrics(domainName, dates, flux, bw, hitFlux), nil
}

// buildDailyMetrics 按日期序列合并三路指标;缺失日保持零值/未知哨兵。
// 命中率 = 命中流量/总流量(flux>0 时),否则保持 -1(未知,不伪造 0%)。
func buildDailyMetrics(domainName string, dates []string, flux, bw, hitFlux map[string]float64) []types.CDNMetric {
	metrics := make([]types.CDNMetric, 0, len(dates))
	for _, d := range dates {
		m := types.CDNMetric{
			Domain:  domainName,
			Date:    d,
			HitRate: unknownHitRate,
		}
		if v, ok := flux[d]; ok {
			m.Bytes = int64(v)
		}
		if v, ok := bw[d]; ok {
			m.Bandwidth = int64(v)
		}
		if v, ok := hitFlux[d]; ok {
			if total := flux[d]; total > 0 {
				m.HitRate = v / total
				if m.HitRate > 1 {
					m.HitRate = 1
				}
			}
			// total==0 时命中率无意义,保持 -1
		}
		metrics = append(metrics, m)
	}
	return metrics
}

// fetchDomainStats 查询单个 stat_type 的逐日数组,按日期映射聚合。
func (a *CDNAdapter) fetchDomainStats(
	client *cdnv1.CdnClient,
	domainName, statType string,
	dates []string,
	mode aggregateMode,
) (map[string]float64, error) {
	startMs := dayStartUnixMilli(dates[0])
	request := &cdnv1model.ShowDomainStatsRequest{
		Action:     "detail",
		StartTime:  startMs,
		EndTime:    startMs + int64(len(dates))*86400*1000, // 左闭右开,86400 需对齐零点
		DomainName: domainName,
		StatType:   statType,
		Interval:   int64Ptr(86400),
		GroupBy:    strPtr("domain"),
	}

	response, err := client.ShowDomainStats(request)
	if err != nil {
		return nil, fmt.Errorf("查询CDN统计 %s 失败: %w", statType, err)
	}
	if response == nil || response.Result == nil {
		return nil, nil
	}
	// result[域名][stat_type] → []float64(逐日,与 interval=86400 对齐)
	domainData, ok := lookupDomainResult(response.Result, domainName)
	if !ok {
		return nil, nil
	}
	series, ok := lookupSeries(domainData, statType)
	if !ok {
		return nil, nil
	}
	return aggregateDailySeries(series, dates[0], mode), nil
}

// lookupDomainResult 在 result 中按域名取值(大小写不敏感兜底)。
func lookupDomainResult(result map[string]interface{}, domainName string) (map[string]interface{}, bool) {
	if v, ok := result[domainName]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			return m, true
		}
	}
	lower := strings.ToLower(domainName)
	for k, v := range result {
		if strings.ToLower(k) != lower {
			continue
		}
		if m, ok := v.(map[string]interface{}); ok {
			return m, true
		}
	}
	return nil, false
}

// lookupSeries 取 stat_type 对应的逐日数值数组(容忍数字与字符串形态)。
// 无法解析的值("-"/"null"/厂商无数据哨兵 -1)返回 nil 指针,
// 表示"该日无数据",由调用方跳过——不得占位为 0(会伪造 0% 命中率)。
func lookupSeries(domainData map[string]interface{}, statType string) ([]*float64, bool) {
	raw, ok := domainData[statType]
	if !ok {
		return nil, false
	}
	arr, ok := raw.([]interface{})
	if !ok {
		return nil, false
	}
	series := make([]*float64, 0, len(arr))
	for _, item := range arr {
		switch v := item.(type) {
		case float64:
			if v == noDataSentinel {
				series = append(series, nil)
				continue
			}
			val := v
			series = append(series, &val)
		case json.Number: // ShowDomainStats 的 map[string]interface{} 以 json.Number 保真解码
			f, err := v.Float64()
			if err != nil || f == noDataSentinel {
				series = append(series, nil)
				continue
			}
			val := f
			series = append(series, &val)
		case string:
			trimmed := strings.TrimSpace(v)
			if parsed, err := strconv.ParseFloat(trimmed, 64); err == nil && parsed != noDataSentinel {
				val := parsed
				series = append(series, &val)
			} else {
				series = append(series, nil) // 无数据("-"/"null"/"-1")
			}
		default:
			series = append(series, nil)
		}
	}
	return series, true
}

// noDataSentinel 华为云无数据哨兵值(文档约定返回 "-"、"-1" 或 null)
const noDataSentinel = -1.0

// aggregateDailySeries 逐日数组(interval=86400,自 startDate 零点起)→ 按日聚合。
// nil 元素为无数据日,跳过不写入(命中率的未知语义由缺省 -1 表达)。
func aggregateDailySeries(series []*float64, startDate string, mode aggregateMode) map[string]float64 {
	result := make(map[string]float64, len(series))
	loc := metricCSTZone
	start, err := time.ParseInLocation("2006-01-02", startDate, loc)
	if err != nil {
		return result
	}
	for i, v := range series {
		if v == nil {
			continue // 无数据日:不落聚合结果,保持调用方缺省(如 HitRate=-1)
		}
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		switch mode {
		case aggregateModeSum:
			result[date] += *v
		case aggregateModeMax:
			if *v > result[date] {
				result[date] = *v
			}
		}
	}
	return result
}

// ==================== 聚合模式与辅助 ====================

type aggregateMode int

const (
	aggregateModeSum aggregateMode = iota
	aggregateModeMax
)

// metricCSTZone CDN 指标按运营时区(Asia/Shanghai)取日,勿改用服务器本地时区
var metricCSTZone = time.FixedZone("CST", 8*3600)

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
	const maxRangeDays = 31 // interval=86400 单次最长 31 天
	dates := make([]string, 0, int(end.Sub(start)/24/time.Hour)+1)
	for t := start; !t.After(end); t = t.AddDate(0, 0, 1) {
		if len(dates) >= maxRangeDays {
			return nil, fmt.Errorf("指标查询区间超过 %d 天", maxRangeDays)
		}
		dates = append(dates, t.Format("2006-01-02"))
	}
	return dates, nil
}

// dayStartUnixMilli 运营时区当日零点的毫秒时间戳
func dayStartUnixMilli(date string) int64 {
	t, err := time.ParseInLocation("2006-01-02", date, metricCSTZone)
	if err != nil {
		return 0
	}
	return t.Unix() * 1000
}

func int64Ptr(v int64) *int64 { return &v }
func strPtr(v string) *string { return &v }
