package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/gotomicro/ego/core/elog"
)

// aws NAS 指标适配器(NASMetricQuerier 必达厂商之一)。
//
// 指标口径(M1 探测定案,probe-report §1.3/§4):Namespace=AWS/EFS、
// MetricName=StorageBytes、维度 FileSystemId(官方指标,指标路径全链路
// 已验证)。与 CDN 的「CloudFront 主动放弃」不同(aws/cdn_metrics.go 返回
// 空切片跳过),EFS 有标准 CloudWatch 指标,本实现为真实查询路径,不套用
// 主动放弃先例。
//
// 容量字段:EFS 为弹性容量,无总容量指标 → capacity=0(写路径打
// qc_status=zero_exception,例外放行落库可见;实盘首期可能整表为零值/无数据,
// probe-report「AWS 零值注记」)。已用容量 StorageBytes(字节)在采集边界
// /1024^3 归一 GB(Hard Rule:禁止字节写进 GB 字段)。

var _ cloudx.NASMetricQuerier = (*EFSAdapter)(nil)

const (
	// efsMetricNamespace EFS CloudWatch namespace(官方指标)
	efsMetricNamespace = "AWS/EFS"
	// efsMetricStorageBytes 已用存储(字节)
	efsMetricStorageBytes = "StorageBytes"
	// efsDimFileSystemID CloudWatch 维度名
	efsDimFileSystemID = "FileSystemId"
	// efsDailyPeriod 天粒度聚合(CloudWatch Period 单位秒)
	efsDailyPeriod = int32(86400)
)

// cwMetricClient CloudWatch GetMetricData 最小接口(测试桩注入)。
type cwMetricClient interface {
	GetMetricData(ctx context.Context, params *cloudwatch.GetMetricDataInput, optFns ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error)
}

// cwMetricHooks CloudWatch 指标查询测试注入钩子(仅单测使用)。
type cwMetricHooks struct {
	cwFactory func(ctx context.Context, region string) (cwMetricClient, error)
}

// GetNASMetrics 查询 [startDate, endDate](含两端,YYYY-MM-DD)内该文件系统
// 的逐日已用容量指标。region 为实例所在地域,本适配器按该 region 创建
// CloudWatch 客户端,不做全局 region 推断。
//
// 失败路径(API 错误/超时/鉴权):打 ERROR 并携带 error 字段后返回 error,
// 由执行器记入失败计数(proposal「失败可观测性」);真实无数据点返回空切片
// + nil error(非调用失败;实盘 EFS 长期闲置时 StorageBytes 无上报,
// probe-report §1.3,按「零值例外放行」呈现而非采集失效)。
func (a *EFSAdapter) GetNASMetrics(ctx context.Context, fsID, fsName, region, startDate, endDate string) ([]types.NASMetric, error) {
	if fsID == "" {
		return nil, fmt.Errorf("AWS NAS 指标查询需要文件系统 ID")
	}
	if region == "" {
		return nil, fmt.Errorf("AWS NAS 指标查询需要实例 region(按实例所在 region 查询,不做全局推断)")
	}
	dates, err := nasMetricDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("AWS NAS 指标查询已取消: %w", err)
	}

	client, err := a.createCWClient(ctx, region)
	if err != nil {
		a.logMetricFailure(fsID, region, err)
		return nil, err
	}

	startT, endT := nasRangeBounds(dates)
	var timestamps []time.Time
	var values []float64
	nextToken := ""
	for page := 0; page < 10; page++ {
		input := &cloudwatch.GetMetricDataInput{
			StartTime: &startT,
			EndTime:   &endT,
			MetricDataQueries: []cwtypes.MetricDataQuery{{
				Id: awssdk.String("storage_bytes"),
				MetricStat: &cwtypes.MetricStat{
					Metric: &cwtypes.Metric{
						Namespace:  awssdk.String(efsMetricNamespace),
						MetricName: awssdk.String(efsMetricStorageBytes),
						Dimensions: []cwtypes.Dimension{{
							Name:  awssdk.String(efsDimFileSystemID),
							Value: awssdk.String(fsID),
						}},
					},
					Period: awssdk.Int32(efsDailyPeriod),
					Stat:   awssdk.String("Average"),
				},
			}},
		}
		if nextToken != "" {
			input.NextToken = &nextToken
		}
		output, err := client.GetMetricData(ctx, input)
		if err != nil {
			err = fmt.Errorf("查询 EFS StorageBytes 失败: %w", err)
			a.logMetricFailure(fsID, region, err)
			return nil, err
		}
		for _, r := range output.MetricDataResults {
			timestamps = append(timestamps, r.Timestamps...)
			values = append(values, r.Values...)
		}
		if output.NextToken == nil || *output.NextToken == "" {
			break
		}
		nextToken = *output.NextToken
	}

	usedDaily := aggregateCWMetricDaily(timestamps, values)
	if len(usedDaily) == 0 {
		// 真实无数据点(序列注册但长期无上报/实例闲置),非调用失败
		return []types.NASMetric{}, nil
	}
	return buildEFSMetrics(fsID, fsName, dates, usedDaily), nil
}

// logMetricFailure 适配器维度失败可观测性:ERROR + error 字段。
func (a *EFSAdapter) logMetricFailure(fsID, region string, err error) {
	a.logger.Error("AWS NAS 指标查询失败",
		elog.String("fs_id", fsID),
		elog.String("region", region),
		elog.String("provider", "aws"),
		elog.FieldErr(err))
}

// createCWClient 按实例 region 创建 CloudWatch 客户端(静态凭证)。
func (a *EFSAdapter) createCWClient(ctx context.Context, region string) (cwMetricClient, error) {
	if a.cwHooks != nil && a.cwHooks.cwFactory != nil {
		return a.cwHooks.cwFactory(ctx, region)
	}
	return newCloudWatchClient(ctx, a.accessKeyID, a.accessKeySecret, region)
}

// ==================== 纯解析/聚合函数(单测覆盖) ====================

// nasMetricCSTZone NAS 指标按运营时区(Asia/Shanghai)取日,勿改用服务器本地时区
var nasMetricCSTZone = time.FixedZone("CST", 8*3600)

// nasMetricDateRange 解析 [startDate, endDate](含两端)为日期切片(YYYY-MM-DD)。
func nasMetricDateRange(startDate, endDate string) ([]string, error) {
	start, err := time.ParseInLocation("2006-01-02", startDate, nasMetricCSTZone)
	if err != nil {
		return nil, fmt.Errorf("解析 startDate 失败: %w", err)
	}
	end, err := time.ParseInLocation("2006-01-02", endDate, nasMetricCSTZone)
	if err != nil {
		return nil, fmt.Errorf("解析 endDate 失败: %w", err)
	}
	if end.Before(start) {
		return nil, fmt.Errorf("startDate %s 晚于 endDate %s", startDate, endDate)
	}
	const maxRangeDays = 92 // GetMetricData 单次最多 100800 数据点,天粒度留缓冲
	dates := make([]string, 0, int(end.Sub(start)/24/time.Hour)+1)
	for t := start; !t.After(end); t = t.AddDate(0, 0, 1) {
		if len(dates) >= maxRangeDays {
			return nil, fmt.Errorf("指标查询区间超过 %d 天", maxRangeDays)
		}
		dates = append(dates, t.Format("2006-01-02"))
	}
	return dates, nil
}

// nasRangeBounds 指标查询窗口 [CST 首日 00:00, 末日 +24h)。
func nasRangeBounds(dates []string) (time.Time, time.Time) {
	start, err := time.ParseInLocation("2006-01-02", dates[0], nasMetricCSTZone)
	if err != nil {
		return time.Time{}, time.Time{}
	}
	end, err := time.ParseInLocation("2006-01-02", dates[len(dates)-1], nasMetricCSTZone)
	if err != nil {
		return time.Time{}, time.Time{}
	}
	return start, end.Add(24 * time.Hour)
}

// aggregateCWMetricDaily 数据点按运营时区归到自然日;同日多点保留最后出现的
// 点(日末态快照口径)。GetMetricData 按时间升序返回,顺序遍历即覆盖。
func aggregateCWMetricDaily(timestamps []time.Time, values []float64) map[string]float64 {
	if len(timestamps) != len(values) {
		return nil
	}
	result := make(map[string]float64, len(values))
	for i, ts := range timestamps {
		date := ts.In(nasMetricCSTZone).Format("2006-01-02")
		result[date] = values[i]
	}
	return result
}

// buildEFSMetrics 按日期序列组装指标行:
//   - used 缺失日跳过不落库(缺失日不填充假值);
//   - StorageBytes 字节 → GB 换算在采集边界(types.BytesToGB,Hard Rule);
//   - capacity 恒为 0(EFS 弹性容量无总容量指标),写路径打 zero_exception。
func buildEFSMetrics(fsID, fsName string, dates []string, usedDaily map[string]float64) []types.NASMetric {
	metrics := make([]types.NASMetric, 0, len(dates))
	for _, d := range dates {
		usedBytes, ok := usedDaily[d]
		if !ok {
			continue
		}
		metrics = append(metrics, types.NASMetric{
			FsID:         fsID,
			FsName:       fsName,
			Date:         d,
			Capacity:     0, // EFS 弹性容量无总容量指标,零值例外放行由写路径打标
			UsedCapacity: types.BytesToGB(usedBytes),
			Provider:     "aws",
			// AccountID 由执行器(T4)按云账号回填,适配器无账号上下文
		})
	}
	return metrics
}
