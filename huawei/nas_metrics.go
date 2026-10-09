package huawei

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	cesv1 "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1"
	cesv1model "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1/model"
	cesv1region "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1/region"
)

// huawei NAS 指标适配器(NASMetricQuerier 必达厂商之一)。
//
// 指标口径(M1 探测定案,probe-report §1.2/§4,2026-09-19 cn-south-1 实测):
//   - Namespace:实盘 SFS Turbo(HPC 型)全部上报在 **SYS.EFS**;文档口径
//     SYS.SFS_Turbo 在实盘 0 上报。SYS.EFS 为主候选,SYS.SFS(普通 SFS 文档
//     口径,账号暂无实例、实盘未验证)为兼容候选,同批查询按数据择优——
//     适配器不预知文件系统类型(接口签名仅 fsID/fsName/region);
//   - 维度:efs_instance_id = 文件系统 ID;
//   - 已用容量:used_capacity(单位 byte → 采集边界 /1024^3 归一 GB);
//   - 使用率:used_capacity_percent(%);
//   - 总容量:无直接指标,按 used ÷ percent 派生(percent≤0 时派生无效 →
//     capacity=0 写路径打 zero_exception,禁 NaN,probe-report 遗留行动 #3)。
//
// Hard Rule:指标路径用实例真实 region 创建 CES 客户端,不经过 sfs.go
// 资产路径的「单 region 静默回退 cn-north-4」逻辑(region 不在支持列表显式报错)。

var _ cloudx.NASMetricQuerier = (*SFSAdapter)(nil)

const (
	// cesNamespaceEFS M1 实测定案 namespace(实盘 SFS Turbo 全部上报于此)
	cesNamespaceEFS = "SYS.EFS"
	// cesNamespaceSFS 普通 SFS 文档口径 namespace(实盘无实例样本,兼容候选)
	cesNamespaceSFS = "SYS.SFS"
	// cesMetricUsedCapacity 已用容量(byte)
	cesMetricUsedCapacity = "used_capacity"
	// cesMetricUsedCapacityPercent 已用容量百分比(%)
	cesMetricUsedCapacityPercent = "used_capacity_percent"
	// cesDimFSID CES 指标维度名(M1 定案)
	cesDimFSID = "efs_instance_id"
)

// cesMetricClient CES BatchListMetricData 最小接口(测试桩注入)。
type cesMetricClient interface {
	BatchListMetricData(request *cesv1model.BatchListMetricDataRequest) (*cesv1model.BatchListMetricDataResponse, error)
}

// cesMetricHooks CES 指标查询测试注入钩子(仅单测使用)。
type cesMetricHooks struct {
	cesFactory func(region string) (cesMetricClient, error)
}

// GetNASMetrics 查询 [startDate, endDate](含两端,YYYY-MM-DD)内该文件系统
// 的逐日容量/用量指标。region 为实例所在地域,本适配器按该 region 创建 CES
// 客户端(不做全局推断、不做单 region 静默回退)。
//
// 失败路径(API 错误/超时/鉴权):打 ERROR 并携带 error 字段后返回 error,
// 由执行器记入失败计数(proposal「失败可观测性」);真实无数据点返回空切片
// + nil error(非调用失败,不触发失败计数)。
func (a *SFSAdapter) GetNASMetrics(ctx context.Context, fsID, fsName, region, startDate, endDate string) ([]types.NASMetric, error) {
	if fsID == "" {
		return nil, fmt.Errorf("华为云NAS指标查询需要文件系统ID")
	}
	if region == "" {
		return nil, fmt.Errorf("华为云NAS指标查询需要实例 region(按实例所在 region 查询,不做全局推断)")
	}
	dates, err := metricDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("华为云NAS指标查询已取消: %w", err)
	}

	client, err := a.createCESClient(region)
	if err != nil {
		a.logMetricFailure(fsID, region, err)
		return nil, err
	}

	// 单次批量查询两 namespace × 两指标的日粒度均值(过滤 average),
	// 同批按数据择优,避免逐 namespace 串行回退
	request := &cesv1model.BatchListMetricDataRequest{
		Body: &cesv1model.BatchListMetricDataRequestBody{
			Metrics: cesMetricCandidates(fsID),
			Period:  cesPeriodPtr(cesv1model.GetBatchPeriodEnum().E_86400),
			Filter:  cesFilterPtr(cesv1model.GetFilterEnum().AVERAGE),
			From:    dayStartUnixMilli(dates[0]),
			To:      dayStartUnixMilli(dates[len(dates)-1]) + 86400_000 - 1,
		},
	}
	response, err := client.BatchListMetricData(request)
	if err != nil {
		err = fmt.Errorf("批量查询CES指标数据失败: %w", err)
		a.logMetricFailure(fsID, region, err)
		return nil, err
	}

	used, percent := pickCESMetricData(cesMetricsOf(response))
	if used == nil || len(used.Datapoints) == 0 {
		// 真实无指标数据(namespace/维度均无上报),非调用失败
		return []types.NASMetric{}, nil
	}
	usedDaily := aggregateCESDaily(used.Datapoints)
	var percentDaily map[string]float64
	if percent != nil {
		percentDaily = aggregateCESDaily(percent.Datapoints)
	}
	return buildCESMetrics(fsID, fsName, dates, usedDaily, percentDaily), nil
}

// logMetricFailure 适配器维度失败可观测性:ERROR + error 字段。
func (a *SFSAdapter) logMetricFailure(fsID, region string, err error) {
	a.logger.Error("华为云NAS指标查询失败",
		elog.String("fs_id", fsID),
		elog.String("region", region),
		elog.String("provider", "huawei"),
		elog.FieldErr(err))
}

// createCESClient 按实例真实 region 创建 CES v1 客户端。
// Hard Rule:region 不在支持列表时显式报错——不经过 sfs.go createClient 的
// 「单 region 静默回退 cn-north-4」逻辑,避免非 cn-north-4 实例查错地域。
func (a *SFSAdapter) createCESClient(region string) (cesMetricClient, error) {
	if a.cesHooks != nil && a.cesHooks.cesFactory != nil {
		return a.cesHooks.cesFactory(region)
	}
	auth, err := basic.NewCredentialsBuilder().
		WithAk(a.accessKeyID).
		WithSk(a.accessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}
	regionObj, err := cesv1region.SafeValueOf(region)
	if err != nil {
		return nil, fmt.Errorf("CES region %s 不在支持列表(指标路径不做静默回退): %w", region, err)
	}
	client, err := cesv1.CesClientBuilder().
		WithRegion(regionObj).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建CES客户端失败: %w", err)
	}
	return cesv1.NewCesClient(client), nil
}

// cesMetricCandidates 单次 BatchListMetricData 的候选指标(两 namespace ×
// used_capacity / used_capacity_percent):
// SYS.EFS 为 M1 实测定案(实盘 SFS Turbo 全部上报于此,probe-report §1.2);
// SYS.SFS 为普通 SFS 文档口径兼容候选(账号暂无普通 SFS 实例,实盘未验证)。
func cesMetricCandidates(fsID string) []cesv1model.MetricInfo {
	dimensions := []cesv1model.MetricsDimension{{Name: cesDimFSID, Value: fsID}}
	metrics := make([]cesv1model.MetricInfo, 0, 4)
	for _, ns := range []string{cesNamespaceEFS, cesNamespaceSFS} {
		for _, name := range []string{cesMetricUsedCapacity, cesMetricUsedCapacityPercent} {
			metrics = append(metrics, cesv1model.MetricInfo{
				Namespace:  ns,
				MetricName: name,
				Dimensions: dimensions,
			})
		}
	}
	return metrics
}

func cesPeriodPtr(p cesv1model.BatchPeriod) *cesv1model.BatchPeriod { return &p }
func cesFilterPtr(f cesv1model.Filter) *cesv1model.Filter           { return &f }

// cesMetricsOf 空安全管理取回指标列表。
func cesMetricsOf(response *cesv1model.BatchListMetricDataResponse) []cesv1model.BatchMetricData {
	if response == nil || response.Metrics == nil {
		return nil
	}
	return *response.Metrics
}

// pickCESMetricData 从批量响应中择优取已用容量与百分比序列:
// 优先有数据点的 SYS.EFS(M1 实测定案),SYS.SFS 仅在其无数据时兜底。
// used 与 percent 取同一 namespace(容量派生口径一致)。
// cesNamespaceOf 取响应指标的 namespace(空安全)。
func cesNamespaceOf(m *cesv1model.BatchMetricData) string {
	if m == nil || m.Namespace == nil {
		return ""
	}
	return *m.Namespace
}

func pickCESMetricData(metrics []cesv1model.BatchMetricData) (used, percent *cesv1model.BatchMetricData) {
	for i := range metrics {
		m := &metrics[i]
		if ns := cesNamespaceOf(m); ns != cesNamespaceEFS && ns != cesNamespaceSFS {
			continue
		}
		if len(m.Datapoints) == 0 {
			continue
		}
		switch m.MetricName {
		case cesMetricUsedCapacity:
			if used == nil || cesNamespaceOf(used) != cesNamespaceEFS {
				used = m
			}
		case cesMetricUsedCapacityPercent:
			if percent == nil || cesNamespaceOf(percent) != cesNamespaceEFS {
				percent = m
			}
		}
	}
	// used 与 percent 必须同 namespace,否则派生口径错位
	if used != nil && percent != nil && cesNamespaceOf(used) != cesNamespaceOf(percent) {
		if cesNamespaceOf(used) == cesNamespaceEFS {
			percent = nil
		} else {
			used = nil
		}
	}
	return used, percent
}

// ==================== 纯解析/聚合函数(单测覆盖) ====================

// coerceCESValue 从解码后的 map 元素提取指标值(容忍 float64/json.Number/
// 字符串数值三形态)。
//
// 华为云 SDK 以 jsoniter UseNumber 解码响应(core/utils/json_utils.go),凡落进
// interface{} 的数字元素类型是 json.Number 而非 float64——CDN ShowDomainStats
// 曾因此全部静默解析为 nil(commit 47da689,「响应 map 元素为 json.Number」)。
// CES BatchListMetricData 响应模型虽为强类型,若 CES 结构变化回退到 map 形态,
// 值提取必须经本函数兜底,不得直接 `.(float64)` 断言。
func coerceCESValue(v interface{}) *float64 {
	switch n := v.(type) {
	case float64:
		return &n
	case *float64:
		if n == nil {
			return nil
		}
		f := *n
		return &f
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return nil
		}
		return &f
	case int64:
		f := float64(n)
		return &f
	case string:
		trimmed := strings.TrimSpace(n)
		if f, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return &f
		}
		return nil
	default:
		return nil
	}
}

// aggregateCESDaily 日粒度数据点按运营时区(Asia/Shanghai)归日;
// 同日多点保留最后出现的点(日末态快照口径)。
func aggregateCESDaily(datapoints []cesv1model.DatapointForBatchMetric) map[string]float64 {
	result := make(map[string]float64, len(datapoints))
	for _, dp := range datapoints {
		// filter=average 请求下 Average 必有值;Max/Sum 为防御兜底
		var val float64
		switch {
		case dp.Average != nil:
			val = *dp.Average
		case dp.Max != nil:
			val = *dp.Max
		case dp.Sum != nil:
			val = *dp.Sum
		default:
			continue // 无值数据点跳过,不伪造 0
		}
		date := time.UnixMilli(dp.Timestamp).In(metricCSTZone).Format("2006-01-02")
		result[date] = val
	}
	return result
}

// deriveCESCapacity 华为无总容量直接指标,按 M1 定案由
// used_capacity ÷ used_capacity_percent 派生(probe-report §1.2)。
// percent<=0 时派生无效:返回 0(写路径打 zero_exception),禁止 NaN。
func deriveCESCapacity(usedGB, percent float64) float64 {
	if percent <= 0 {
		return 0
	}
	return usedGB / (percent / 100)
}

// buildCESMetrics 按日期序列组装指标行:
//   - used 缺失日跳过不落库(缺失日不填充假值);
//   - 字节 → GB 换算在采集边界(types.BytesToGB,Hard Rule);
//   - capacity 由 percent 派生,percent 缺失/为 0 时 capacity=0(零值例外放行
//     打标由写路径处理),不写 NaN。
func buildCESMetrics(fsID, fsName string, dates []string, usedDaily, percentDaily map[string]float64) []types.NASMetric {
	metrics := make([]types.NASMetric, 0, len(dates))
	for _, d := range dates {
		rawBytes, ok := usedDaily[d]
		if !ok {
			continue
		}
		usedGB := types.BytesToGB(rawBytes)
		percent := 0.0
		if percentDaily != nil {
			percent = percentDaily[d]
		}
		metrics = append(metrics, types.NASMetric{
			FsID:         fsID,
			FsName:       fsName,
			Date:         d,
			Capacity:     deriveCESCapacity(usedGB, percent),
			UsedCapacity: usedGB,
			Provider:     "huawei",
			// AccountID 由执行器(T4)按云账号回填,适配器无账号上下文
		})
	}
	return metrics
}
