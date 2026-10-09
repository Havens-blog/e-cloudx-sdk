package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/gotomicro/ego/core/elog"
)

// aws Disk(EBS)指标适配器(DiskMetricQuerier 必达厂商之一)。
//
// 规格锚点:disk-ops-insight probe-report §1.3/§2/§4(T1 实盘定案,2026-09-21):
//   - Namespace = AWS/EBS,维度 VolumeId(官方指标,21/21 非零验证通过);
//   - IOPS = VolumeReadOps / VolumeWriteOps(次/日 Sum → 采集边界按窗口秒数
//     归一 次/秒);吞吐 = VolumeReadBytes / VolumeWriteBytes(byte/日 Sum →
//     按窗口秒数归一 byte/s → types.BytesPerSecToMBPerSec 归一 MB/s);
//   - 使用率:无直接指标,派生公式 usage% = (1 − VolumeIdleTime/86400) × 100
//     实盘成立(vol-00ddfd59 idle=69579s → 19.47%,probe-report §1.3)——语义为
//     繁忙时间占比,打 DiskUsageScopeBusyShare 标注与容量水位区分;某日无
//     VolumeIdleTime 数据点或派生越界(idle>窗口)则使用率留 0 不打标
//     (派生不可靠打标缺失而非伪造,Hard Rule)。
//
// 与 aws/cdn_metrics.go 的「CloudFront 主动放弃」不同(该先例返回空切片跳过),
// EBS 有标准 CloudWatch 指标,本实现为真实查询路径,失败返回 error(AC 显式要求)。
//
// Hard Rule:指标路径按实例真实 region 创建 CloudWatch 客户端(账号 regions
// 配置与实盘可能不一致,probe-report §5),不做全局 region 推断;失败路径三分
// ——调用失败 ERROR + error;真实无数据空切片 + nil。

var _ cloudx.DiskMetricQuerier = (*DiskAdapter)(nil)

const (
	// ebsMetricNamespace EBS CloudWatch namespace(官方指标)
	ebsMetricNamespace = "AWS/EBS"
	// ebsDimVolumeID CloudWatch 维度名(云盘级)
	ebsDimVolumeID = "VolumeId"
	// ebsMetricReadOps / ebsMetricWriteOps 读写操作次数(次/日 Sum)
	ebsMetricReadOps  = "VolumeReadOps"
	ebsMetricWriteOps = "VolumeWriteOps"
	// ebsMetricReadBytes / ebsMetricWriteBytes 读写吞吐(byte/日 Sum)
	ebsMetricReadBytes  = "VolumeReadBytes"
	ebsMetricWriteBytes = "VolumeWriteBytes"
	// ebsMetricIdleTime 空闲秒数(秒/日 Sum,使用率派生输入)
	ebsMetricIdleTime = "VolumeIdleTime"
	// ebsDailyPeriod 天粒度聚合(CloudWatch Period 单位秒)
	ebsDailyPeriod = int32(86400)
	// ebsWindowSeconds 派生归一窗口秒数(与 Period 一致)
	ebsWindowSeconds = 86400.0
)

// ebsMetricHooks CloudWatch 指标查询测试注入钩子(复用 cwMetricHooks;仅单测使用)。
type ebsMetricHooks = cwMetricHooks

// GetDiskMetrics 查询 [startDate, endDate](含两端,YYYY-MM-DD)内该云盘的
// 逐日使用率/IOPS/吞吐指标。region 为实例所在地域(调用方从实例元数据取),
// 本适配器按该 region 创建 CloudWatch 客户端,不做全局 region 推断。
//
// 失败路径(API 错误/超时/鉴权):打 ERROR 并携带 error 字段后返回 error,
// 由执行器记入失败计数(proposal「失败可观测性」);真实无数据点返回空切片
// + nil error(非调用失败,不触发失败计数)。
func (a *DiskAdapter) GetDiskMetrics(ctx context.Context, diskID, diskName, region, startDate, endDate string) ([]types.DiskMetric, error) {
	if diskID == "" {
		return nil, fmt.Errorf("AWS 磁盘指标查询需要云盘 ID")
	}
	if region == "" {
		return nil, fmt.Errorf("AWS 磁盘指标查询需要实例 region(按实例所在 region 查询,不做全局推断)")
	}
	dates, err := nasMetricDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("AWS 磁盘指标查询已取消: %w", err)
	}

	client, err := a.createEBSClient(ctx, region)
	if err != nil {
		a.logEBSMetricFailure(diskID, region, err)
		return nil, err
	}

	startT, endT := nasRangeBounds(dates)
	queries := ebsMetricQueries(diskID)
	series := map[string]*ebsSeries{}
	nextToken := ""
	for page := 0; page < 10; page++ {
		input := &cloudwatch.GetMetricDataInput{
			StartTime:         &startT,
			EndTime:           &endT,
			MetricDataQueries: queries,
		}
		if nextToken != "" {
			input.NextToken = &nextToken
		}
		output, err := client.GetMetricData(ctx, input)
		if err != nil {
			err = fmt.Errorf("查询 EBS 指标失败: %w", err)
			a.logEBSMetricFailure(diskID, region, err)
			return nil, err
		}
		for _, r := range output.MetricDataResults {
			if r.Id == nil {
				continue
			}
			s, ok := series[*r.Id]
			if !ok {
				s = &ebsSeries{}
				series[*r.Id] = s
			}
			s.timestamps = append(s.timestamps, r.Timestamps...)
			s.values = append(s.values, r.Values...)
		}
		if output.NextToken == nil || *output.NextToken == "" {
			break
		}
		nextToken = *output.NextToken
	}

	readOpsDaily := dailyOf(series["read_ops"])
	writeOpsDaily := dailyOf(series["write_ops"])
	readBytesDaily := dailyOf(series["read_bytes"])
	writeBytesDaily := dailyOf(series["write_bytes"])
	idleDaily := dailyOf(series["idle_time"])

	metrics := buildAWSDiskMetrics(diskID, diskName, dates, readOpsDaily, writeOpsDaily, readBytesDaily, writeBytesDaily, idleDaily)
	if len(metrics) == 0 {
		// 真实无指标数据点(卷未挂载/未上报),非调用失败:空切片不触发失败计数
		return []types.DiskMetric{}, nil
	}
	return metrics, nil
}

// newCloudWatchClient 按实例 region 创建 CloudWatch 客户端(静态凭证)。
// NAS(EFS)与 Disk(EBS)两条指标路径共用,region 由调用方按实例真实地域传入。
func newCloudWatchClient(ctx context.Context, accessKeyID, accessKeySecret, region string) (cwMetricClient, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			accessKeyID,
			accessKeySecret,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("加载AWS配置失败: %w", err)
	}
	return cloudwatch.NewFromConfig(cfg), nil
}

// ebsSeries 单个 GetMetricData 查询序列的原始数据点。
type ebsSeries struct {
	timestamps []time.Time
	values     []float64
}

// dailyOf 序列按日聚合(空序列返回空 map,聚合函数对长度不一致防御返回 nil)。
func dailyOf(s *ebsSeries) map[string]float64 {
	if s == nil {
		return map[string]float64{}
	}
	return aggregateCWMetricDaily(s.timestamps, s.values)
}

// logEBSMetricFailure 适配器维度失败可观测性:ERROR + error 字段。
func (a *DiskAdapter) logEBSMetricFailure(diskID, region string, err error) {
	a.logger.Error("AWS 磁盘指标查询失败",
		elog.String("disk_id", diskID),
		elog.String("region", region),
		elog.String("provider", "aws"),
		elog.FieldErr(err))
}

// createEBSClient 按实例 region 创建 CloudWatch 客户端。
func (a *DiskAdapter) createEBSClient(ctx context.Context, region string) (cwMetricClient, error) {
	if a.ebsMetricHooks != nil && a.ebsMetricHooks.cwFactory != nil {
		return a.ebsMetricHooks.cwFactory(ctx, region)
	}
	return newCloudWatchClient(ctx, a.accessKeyID, a.accessKeySecret, region)
}

// ebsMetricQueries 单卷 5 指标查询(4 IO + idle),全部 Sum 日粒度
// (probe-report §1.3:T1 探测以 Sum 口径实盘验证)。
func ebsMetricQueries(volumeID string) []cwtypes.MetricDataQuery {
	query := func(id, metricName string) cwtypes.MetricDataQuery {
		return cwtypes.MetricDataQuery{
			Id: awssdk.String(id),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace:  awssdk.String(ebsMetricNamespace),
					MetricName: awssdk.String(metricName),
					Dimensions: []cwtypes.Dimension{{
						Name:  awssdk.String(ebsDimVolumeID),
						Value: awssdk.String(volumeID),
					}},
				},
				Period: awssdk.Int32(ebsDailyPeriod),
				Stat:   awssdk.String("Sum"),
			},
		}
	}
	return []cwtypes.MetricDataQuery{
		query("read_ops", ebsMetricReadOps),
		query("write_ops", ebsMetricWriteOps),
		query("read_bytes", ebsMetricReadBytes),
		query("write_bytes", ebsMetricWriteBytes),
		query("idle_time", ebsMetricIdleTime),
	}
}

// ==================== 纯解析/聚合函数(单测覆盖) ====================

// buildAWSDiskMetrics 按日期序列组装指标行:
//   - 该日 5 个指标均无数据点则跳过(缺失日不填充假值;仅 idle 有数据点的
//     全闲盘日保留——派生 0.00 属合法值,probe-report §1.3);
//   - iops = (读+写 Ops)/86400(次/秒);吞吐 = (读+写 Bytes)/86400 byte/s
//     经 types.BytesPerSecToMBPerSec 归一 MB/s(采集边界换算,Hard Rule);
//   - 使用率按 VolumeIdleTime 派生(types.DiskBusySharePercentFromIdle,T1
//     定案公式)并打 DiskUsageScopeBusyShare 标注;无 idle 数据点或派生越界
//     则留 0 且不打标(不伪造,Hard Rule);
//   - qc_status 由写路径(DAO)打 zero_exception,适配器不越权标注。
func buildAWSDiskMetrics(diskID, diskName string, dates []string, readOps, writeOps, readBytes, writeBytes, idle map[string]float64) []types.DiskMetric {
	metrics := make([]types.DiskMetric, 0, len(dates))
	for _, d := range dates {
		ro, okRO := readOps[d]
		wo, okWO := writeOps[d]
		rb, okRB := readBytes[d]
		wb, okWB := writeBytes[d]
		idleSec, okIdle := idle[d]
		if !okRO && !okWO && !okRB && !okWB && !okIdle {
			continue
		}
		var opsSum, bytesSum, iops, throughput float64
		if okRO {
			opsSum += ro
		}
		if okWO {
			opsSum += wo
		}
		if okRO || okWO {
			iops = opsSum / ebsWindowSeconds
		}
		if okRB {
			bytesSum += rb
		}
		if okWB {
			bytesSum += wb
		}
		if okRB || okWB {
			throughput = types.BytesPerSecToMBPerSec(bytesSum / ebsWindowSeconds)
		}
		usage, scope := 0.0, ""
		if okIdle {
			if u, ok := types.DiskBusySharePercentFromIdle(idleSec, ebsWindowSeconds); ok {
				usage, scope = u, types.DiskUsageScopeBusyShare
			}
		}
		metrics = append(metrics, types.DiskMetric{
			DiskID:       diskID,
			DiskName:     diskName,
			Date:         d,
			UsagePercent: usage,
			UsageScope:   scope,
			IOPS:         iops,
			Throughput:   throughput,
			Provider:     "aws",
			// AccountID 由执行器(T4)按云账号回填,适配器无账号上下文
		})
	}
	return metrics
}
