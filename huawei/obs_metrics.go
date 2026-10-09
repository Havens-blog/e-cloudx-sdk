package huawei

import (
	"context"
	"fmt"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	cesv1 "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1"
	cesv1model "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1/model"
	cesv1region "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1/region"
)

// huawei OBS 指标适配器(OSSMetricQuerier 必达厂商之一)。
//
// 规格:M1 探测定案(probe-report §1.2/§4,2026-09-20 实盘):文档口径
// **SYS.OBS** 实盘成立(103 个指标逐桶上报,容量与数据面 GetBucketStat
// 逐桶互证一致,130 bucket 非零):
//   - Namespace:SYS.OBS(文档口径 = 实盘口径,无需替代);
//   - 维度:**bucket_name**(ListMetrics 实测);
//   - 总容量:**capacity_total**(byte → 采集边界 /1024^3 归一 GB);
//   - 对象数:**object_num_all**(个)。
//
// Hard Rule:指标路径必须按探测定案 namespace 走 CES,不得回退 obs.go
// 资产路径的静态/静默逻辑——CES 客户端创建走 cesv1region.SafeValueOf,
// region 不在支持列表时显式报错(不做静默回退,AC-2)。
//
// OSS 是全局服务(Querier 签名无 region):CES 按账号 defaultRegion 创建
// (探测先例同口径),按传入 bucketName 逐桶真实查询,不做全局推断。

var _ cloudx.OSSMetricQuerier = (*OBSAdapter)(nil)

const (
	// cesNamespaceOBS M1 实测定案 namespace(文档口径实盘成立,probe-report §1.2)
	cesNamespaceOBS = "SYS.OBS"
	// cesMetricCapacityTotal 总容量(byte;分层 capacity_* 系列本期不采,Hard Rule)
	cesMetricCapacityTotal = "capacity_total"
	// cesMetricObjectNumAll 对象数(个)
	cesMetricObjectNumAll = "object_num_all"
	// cesDimOBSBucketName CES 指标维度名(M1 定案)
	cesDimOBSBucketName = "bucket_name"
)

// GetOSSMetrics 查询 [startDate, endDate](含两端,YYYY-MM-DD)内该存储桶
// 的逐日容量/对象数指标。OSS 全局服务,签名无 region(interfaces.go 定案),
// CES 客户端按账号 defaultRegion 创建(SafeValueOf 显式报错)。
//
// 失败路径三分:调用失败 → ERROR + error;真实无数据(bucket 无上报)→
// 空切片 + nil error;数据点缺值跳过不伪造 0。
func (a *OBSAdapter) GetOSSMetrics(ctx context.Context, bucketName, startDate, endDate string) ([]types.OSSMetric, error) {
	if bucketName == "" {
		return nil, fmt.Errorf("华为云OBS指标查询需要存储桶名称")
	}
	dates, err := metricDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("华为云OBS指标查询已取消: %w", err)
	}

	client, err := a.createOSSCESClient()
	if err != nil {
		a.logOSSMetricFailure(bucketName, err)
		return nil, err
	}

	// 单次批量查询 SYS.OBS 两指标(capacity_total/object_num_all)的日粒度均值
	request := &cesv1model.BatchListMetricDataRequest{
		Body: &cesv1model.BatchListMetricDataRequestBody{
			Metrics: ossCESMetricCandidates(bucketName),
			Period:  cesPeriodPtr(cesv1model.GetBatchPeriodEnum().E_86400),
			Filter:  cesFilterPtr(cesv1model.GetFilterEnum().AVERAGE),
			From:    dayStartUnixMilli(dates[0]),
			To:      dayStartUnixMilli(dates[len(dates)-1]) + 86400_000 - 1,
		},
	}
	response, err := client.BatchListMetricData(request)
	if err != nil {
		err = fmt.Errorf("批量查询CES指标数据失败: %w", err)
		a.logOSSMetricFailure(bucketName, err)
		return nil, err
	}

	capacity, objects := pickOBSMetricData(cesMetricsOf(response))
	if capacity == nil || len(capacity.Datapoints) == 0 {
		// 真实无指标数据(bucket 无上报),非调用失败
		return []types.OSSMetric{}, nil
	}
	capacityDaily := aggregateOBSDaily(capacity.Datapoints)
	var objectDaily map[string]float64
	if objects != nil {
		objectDaily = aggregateOBSDaily(objects.Datapoints)
	}
	return buildOBSMetrics(bucketName, dates, capacityDaily, objectDaily), nil
}

// logOSSMetricFailure 适配器维度失败可观测性:ERROR + error 字段。
func (a *OBSAdapter) logOSSMetricFailure(bucketName string, err error) {
	a.logger.Error("华为云OBS指标查询失败",
		elog.String("bucket", bucketName),
		elog.String("provider", "huawei"),
		elog.FieldErr(err))
}

// createOSSCESClient 按账号 defaultRegion 创建 CES v1 客户端。
// Hard Rule:走 cesv1region.SafeValueOf 显式报错——不经过 obs.go createClient
// 的 region 静默回退逻辑,region 不在支持列表时显式失败,避免查错地域静默出空数据。
func (a *OBSAdapter) createOSSCESClient() (cesMetricClient, error) {
	if a.ossCesHooks != nil && a.ossCesHooks.cesFactory != nil {
		return a.ossCesHooks.cesFactory(a.defaultRegion)
	}
	auth, err := basic.NewCredentialsBuilder().
		WithAk(a.accessKeyID).
		WithSk(a.accessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}
	regionObj, err := cesv1region.SafeValueOf(a.defaultRegion)
	if err != nil {
		return nil, fmt.Errorf("CES region %s 不在支持列表(OSS 指标路径不做静默回退): %w", a.defaultRegion, err)
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

// ossCESMetricCandidates 单次 BatchListMetricData 的候选指标(SYS.OBS ×
// capacity_total/object_num_all,维度 bucket_name,M1 定案 probe-report §1.2)。
func ossCESMetricCandidates(bucket string) []cesv1model.MetricInfo {
	dimensions := []cesv1model.MetricsDimension{{Name: cesDimOBSBucketName, Value: bucket}}
	metrics := make([]cesv1model.MetricInfo, 0, 2)
	for _, name := range []string{cesMetricCapacityTotal, cesMetricObjectNumAll} {
		metrics = append(metrics, cesv1model.MetricInfo{
			Namespace:  cesNamespaceOBS,
			MetricName: name,
			Dimensions: dimensions,
		})
	}
	return metrics
}

// pickOBSMetricData 从批量响应中取容量与对象数序列。严格按 M1 定案 namespace
// SYS.OBS 过滤,其余 namespace 序列忽略(Hard Rule:不回退静态/静默逻辑);
// capacity_total 为行驱动指标。
func pickOBSMetricData(metrics []cesv1model.BatchMetricData) (capacity, objects *cesv1model.BatchMetricData) {
	for i := range metrics {
		m := &metrics[i]
		if cesNamespaceOf(m) != cesNamespaceOBS || len(m.Datapoints) == 0 {
			continue
		}
		switch m.MetricName {
		case cesMetricCapacityTotal:
			if capacity == nil {
				capacity = m
			}
		case cesMetricObjectNumAll:
			if objects == nil {
				objects = m
			}
		}
	}
	return capacity, objects
}

// ==================== 纯解析/聚合函数(单测覆盖) ====================

// aggregateOBSDaily 日粒度数据点按运营时区(Asia/Shanghai)归日;同日多点保留
// 最后出现的点(日末态快照口径)。值提取统一走 coerceCESValue(47da689 保真
// 解码防呆:json.Number/map 形态兜底;本处数据点为 *float64 强类型,经扩展分支
// 统一走同一防呆链,Implementation Notes「复用 coerceCESValue」)。
func aggregateOBSDaily(datapoints []cesv1model.DatapointForBatchMetric) map[string]float64 {
	result := make(map[string]float64, len(datapoints))
	for _, dp := range datapoints {
		var val float64
		have := false
		for _, cand := range []interface{}{dp.Average, dp.Max, dp.Sum} {
			if f := coerceCESValue(cand); f != nil {
				val, have = *f, true
				break
			}
		}
		if !have {
			continue // 无值数据点跳过,不伪造 0
		}
		date := time.UnixMilli(dp.Timestamp).In(metricCSTZone).Format("2006-01-02")
		result[date] = val
	}
	return result
}

// buildOBSMetrics 按日期序列组装指标行:
//   - capacity 缺失日跳过不落库(缺失日不填充假值);
//   - 字节 → GB 换算在采集边界(types.BytesToGB,Hard Rule);
//   - 对象数无数据日为 0(与容量独立上报,非异常);
//   - AccountID/QcStatus 由执行器与写路径回填/标注,适配器不越权。
func buildOBSMetrics(bucketName string, dates []string, capacityDaily, objectDaily map[string]float64) []types.OSSMetric {
	metrics := make([]types.OSSMetric, 0, len(dates))
	for _, d := range dates {
		rawBytes, ok := capacityDaily[d]
		if !ok {
			continue
		}
		metrics = append(metrics, types.OSSMetric{
			BucketName:  bucketName,
			Date:        d,
			StorageSize: types.BytesToGB(rawBytes),
			ObjectCount: int64(objectDaily[d]), // 无数据=0(对象数与容量独立上报)
			Provider:    "huawei",
		})
	}
	return metrics
}
