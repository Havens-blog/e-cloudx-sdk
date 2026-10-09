// Package aws AWS RDS 指标适配器(RDSMetricQuerier 必达厂商之一)。
//
// 平移蓝本:internal/shared/cloudx/aws/nas_metrics.go / disk_metrics.go
// (CloudWatch GetMetricData + 同包 newCloudWatchClient/aggregateCWMetricDaily/
// nasMetricDateRange/nasRangeBounds 全量复用);RDS 是地域性资源,querier 签名带
// region + engine(engine 仅写库留痕,指标名不做引擎分派)。
//
// 指标口径(T1 探测定案,probe-report §1.3 / logs 实盘验证):
//   - Namespace = AWS/RDS,维度 {DBInstanceIdentifier: rdsID},四指标同批
//     GetMetricData(Average,日粒度 period=86400):
//     CPU=CPUUtilization(%)、FreeableMemory(字节)、FreeStorageSpace(字节)、
//     DatabaseConnections(count 绝对值);
//   - 内存使用率无直给指标,按 DBInstanceClass → 总内存规格表派生:
//     memory% = (Total-Freeable)/Total × 100(公式与探测 nasprobe 同源,T1
//     定案 AC-5;为避免 aws ⇄ nasprobe 测试期导入环,本包就地实现等价纯函数
//     见文末;实盘 db.m7g.large=8GiB 已验证)。规格未命中的实例
//     (新规格/降级类)换算输入不可靠 → 内存留 0 且 warn(不伪造,Hard Rule);
//   - 磁盘使用率同法派生:disk% = Used/Total,Total=AllocatedStorage(GB,
//     GetInstance 资产路径同源取规格),Freeable 从 FreeStorageSpace;
//     AllocatedStorage<=0 时磁盘留 0+warn;
//   - 连接数 DatabaseConnections 为 count 绝对值,直给取整;
//   - 某日四指标均无数据点则不出行(缺失日不填假值);缺失指标该字段留 0
//     (适配器不填假值),四指标全 0 由写路径 DAO 打 zero_exception。
//
// 失败路径三分:调用失败 ERROR+error;真实无指标数据返回空切片(INFO 语义,
// 不计失败);实例规格元数据(DBInstanceClass/AllocatedStorage)是换算输入,
// 取不到时报错(不静默降级猜测)。
package aws

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/gotomicro/ego/core/elog"
)

var _ cloudx.RDSMetricQuerier = (*RDSAdapter)(nil)

// 指标命名空间/指标名/维度名(T1 探测定案,不得臆造未验证名)
const (
	rdsCWMetricNamespace                 = "AWS/RDS"
	rdsCWMetricCPU                       = "CPUUtilization"
	rdsCWMetricFreeableMemory            = "FreeableMemory"
	rdsCWMetricFreeStorageSpace          = "FreeStorageSpace"
	rdsCWMetricDatabaseConnections       = "DatabaseConnections"
	rdsCWDimInstanceID                   = "DBInstanceIdentifier"
	rdsCWDailyPeriod               int32 = 86400
)

// rdsClassTotalMemoryGiB DBInstanceClass → 总内存 GiB 规格表(内存使用率换算
// 输入):T1 探测定案按 DBInstanceClass 映射 DBInstanceClassMemory,类表固化
// (AWS 官方规格;实盘 db.m7g.large=8GiB 已 5/5 验证)。未命中规格返回 false →
// 内存使用率留 0 且 warn(换算不可靠打标缺失而非伪造,Hard Rule)。
var rdsClassTotalMemoryGiB = map[string]int64{
	// T 系(突发型)
	"db.t2.micro": 1, "db.t2.small": 2, "db.t2.medium": 4, "db.t2.large": 8, "db.t2.xlarge": 16, "db.t2.2xlarge": 32,
	"db.t3.micro": 1, "db.t3.small": 2, "db.t3.medium": 4, "db.t3.large": 8, "db.t3.xlarge": 16, "db.t3.2xlarge": 32,
	"db.t4g.micro": 1, "db.t4g.small": 2, "db.t4g.medium": 4, "db.t4g.large": 8, "db.t4g.xlarge": 16, "db.t4g.2xlarge": 32,
	// M 系(通用型)
	"db.m5.large": 8, "db.m5.xlarge": 16, "db.m5.2xlarge": 32, "db.m5.4xlarge": 64, "db.m5.8xlarge": 128, "db.m5.12xlarge": 192, "db.m5.16xlarge": 256, "db.m5.24xlarge": 384,
	"db.m6i.large": 8, "db.m6i.xlarge": 16, "db.m6i.2xlarge": 32, "db.m6i.4xlarge": 64, "db.m6i.8xlarge": 128, "db.m6i.12xlarge": 192, "db.m6i.16xlarge": 256, "db.m6i.24xlarge": 384,
	"db.m6g.large": 8, "db.m6g.xlarge": 16, "db.m6g.2xlarge": 32, "db.m6g.4xlarge": 64, "db.m6g.8xlarge": 128, "db.m6g.12xlarge": 192, "db.m6g.16xlarge": 256,
	"db.m7g.large": 8, "db.m7g.xlarge": 16, "db.m7g.2xlarge": 32, "db.m7g.4xlarge": 64, "db.m7g.8xlarge": 128, "db.m7g.12xlarge": 192, "db.m7g.16xlarge": 256,
	// R 系(内存优化型)
	"db.r5.large": 16, "db.r5.xlarge": 32, "db.r5.2xlarge": 64, "db.r5.4xlarge": 128, "db.r5.8xlarge": 256, "db.r5.12xlarge": 384, "db.r5.16xlarge": 512, "db.r5.24xlarge": 768,
	"db.r6i.large": 16, "db.r6i.xlarge": 32, "db.r6i.2xlarge": 64, "db.r6i.4xlarge": 128, "db.r6i.8xlarge": 256, "db.r6i.12xlarge": 384, "db.r6i.16xlarge": 512, "db.r6i.24xlarge": 768,
	"db.r6g.large": 16, "db.r6g.xlarge": 32, "db.r6g.2xlarge": 64, "db.r6g.4xlarge": 128, "db.r6g.8xlarge": 256, "db.r6g.12xlarge": 384, "db.r6g.16xlarge": 512,
	"db.r7g.large": 16, "db.r7g.xlarge": 32, "db.r7g.2xlarge": 64, "db.r7g.4xlarge": 128, "db.r7g.8xlarge": 256, "db.r7g.12xlarge": 384, "db.r7g.16xlarge": 512, "db.r7g.24xlarge": 768,
}

// GetRDSMetrics 采集指定 RDS 实例 [startDate, endDate] 的每日指标。
// 实现 cloudx.RDSMetricQuerier;仅新增方法,不改动 RDSAdapter 既有结构
// (struct 零改动约定——CloudWatch 客户端每次按真实 region 现建,
// newCloudWatchClient 为 NAS/Disk 共用包级函数)。
func (a *RDSAdapter) GetRDSMetrics(ctx context.Context, rdsID, instanceName, region, engine, startDate, endDate string) ([]types.RDSMetric, error) {
	if rdsID == "" {
		return nil, fmt.Errorf("AWS RDS指标查询需要实例ID")
	}
	if region == "" {
		return nil, fmt.Errorf("AWS RDS指标查询需要实例 region(指标路径不做全局推断)")
	}
	dates, err := nasMetricDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("AWS RDS指标查询已取消: %w", err)
	}

	// 实例规格元数据:内存换算输入(DBInstanceClass 规格表)与磁盘换算分母
	// (AllocatedStorage)——复用资产路径 GetInstance,与主链路同源。
	inst, err := a.GetInstance(ctx, region, rdsID)
	if err != nil {
		a.logRDSMetricFailure(rdsID, engine, region, fmt.Errorf("获取RDS实例规格失败(内存/磁盘换算输入): %w", err))
		return nil, err
	}
	totalMemGiB, okMem := rdsClassTotalMemoryGiB[strings.ToLower(inst.DBInstanceClass)]
	if !okMem {
		a.logger.Warn("RDS实例规格不在DBInstanceClass总内存表内,内存使用率留0",
			elog.String("rds_id", rdsID),
			elog.String("db_instance_class", inst.DBInstanceClass))
	}
	storageGiB := int64(inst.Storage)
	if storageGiB <= 0 {
		a.logger.Warn("RDS实例AllocatedStorage为0,磁盘使用率留0",
			elog.String("rds_id", rdsID),
			elog.Int("allocated_storage", inst.Storage))
	}

	client, err := newCloudWatchClient(ctx, a.account.AccessKeyID, a.account.AccessKeySecret, region)
	if err != nil {
		a.logRDSMetricFailure(rdsID, engine, region, err)
		return nil, err
	}

	series, err := a.fetchRDSMetricSeries(ctx, client, rdsID, dates)
	if err != nil {
		a.logRDSMetricFailure(rdsID, engine, region, err)
		return nil, err
	}

	cpuDaily := rdsCWDailyOf(series["cpu"])
	freeableMemDaily := rdsCWDailyOf(series["mem_freeable"])
	freeStorageDaily := rdsCWDailyOf(series["disk_free"])
	connDaily := rdsCWDailyOf(series["connections"])

	return buildAWSRDSMetrics(rdsID, instanceName, engine, dates, cpuDaily, freeableMemDaily, freeStorageDaily, connDaily, okMem, totalMemGiB, storageGiB), nil
}

// rdsCWSeries 单个 GetMetricData 查询序列的原始数据点(平移 ebsSeries)。
type rdsCWSeries struct {
	timestamps []time.Time
	values     []float64
}

// rdsCWDailyOf 序列按日聚合(空序列聚合为空 map,与 NAS/Disk 同口径)。
func rdsCWDailyOf(s *rdsCWSeries) map[string]float64 {
	if s == nil {
		return map[string]float64{}
	}
	return aggregateCWMetricDaily(s.timestamps, s.values)
}

// rdsCWMetricQueries 单实例四指标查询(CPU/内存可用/存储可用/连接数),
// 全部 Average 日粒度(T1 探测定案口径);维度 DBInstanceIdentifier。
func rdsCWMetricQueries(rdsID string) []cwtypes.MetricDataQuery {
	query := func(id, metricName string) cwtypes.MetricDataQuery {
		return cwtypes.MetricDataQuery{
			Id: awssdk.String(id),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace:  awssdk.String(rdsCWMetricNamespace),
					MetricName: awssdk.String(metricName),
					Dimensions: []cwtypes.Dimension{{
						Name:  awssdk.String(rdsCWDimInstanceID),
						Value: awssdk.String(rdsID),
					}},
				},
				Period: awssdk.Int32(rdsCWDailyPeriod),
				Stat:   awssdk.String("Average"),
			},
		}
	}
	return []cwtypes.MetricDataQuery{
		query("cpu", rdsCWMetricCPU),
		query("mem_freeable", rdsCWMetricFreeableMemory),
		query("disk_free", rdsCWMetricFreeStorageSpace),
		query("connections", rdsCWMetricDatabaseConnections),
	}
}

// fetchRDSMetricSeries 执行四指标 GetMetricData(NextToken 分页防截断,聚合到
// 序列 map;AWS 单次响应可能截断,与 NAS/disk 同上限 10 页)。
func (a *RDSAdapter) fetchRDSMetricSeries(ctx context.Context, client cwMetricClient, rdsID string, dates []string) (map[string]*rdsCWSeries, error) {
	startTime, endTime := nasRangeBounds(dates)
	series := make(map[string]*rdsCWSeries, 4)
	nextToken := ""
	for page := 0; page < 10; page++ {
		input := &cloudwatch.GetMetricDataInput{
			StartTime:         &startTime,
			EndTime:           &endTime,
			MetricDataQueries: rdsCWMetricQueries(rdsID),
		}
		if nextToken != "" {
			input.NextToken = &nextToken
		}
		output, err := client.GetMetricData(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("查询RDS指标失败: %w", err)
		}
		for _, r := range output.MetricDataResults {
			if r.Id == nil {
				continue
			}
			s, ok := series[*r.Id]
			if !ok {
				s = &rdsCWSeries{}
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
	return series, nil
}

// logRDSMetricFailure 适配器维度失败可观测性:ERROR + error 字段。
func (a *RDSAdapter) logRDSMetricFailure(rdsID, engine, region string, err error) {
	a.logger.Error("AWS RDS指标查询失败",
		elog.String("rds_id", rdsID),
		elog.String("engine", engine),
		elog.String("region", region),
		elog.String("provider", "aws"),
		elog.FieldErr(err))
}

// ==================== 纯解析/聚合函数(单测覆盖) ====================

// memoryPercentFromFreeable AWS 内存使用率换算(公式与探测 nasprobe 同源,
// probe 定案 AC-5):memory% = (1 - freeable/total) × 100。
// 适配器不得反向引用 nasprobe 工具包(nasprobe 测试引用 aws 探测文件,反向
// import 会形成测试期导入环),故本包就地实现等价纯函数;公式变更须与
// nasprobe.MemoryPercentFromFreeable 同步,注释留痕。
func memoryPercentFromFreeable(freeable, total float64) (float64, bool) {
	if total <= 0 || freeable < 0 || freeable > total {
		return 0, false
	}
	return (1 - freeable/total) * 100, true
}

// memoryPercentFromUsed 直接口径使用率换算:used/total × 100(RDS 磁盘使用率
// 分母 = AllocatedStorage 字节;约束同 memoryPercentFromFreeable,不产生
// NaN/Inf)。
func memoryPercentFromUsed(used, total float64) (float64, bool) {
	if total <= 0 || used < 0 || used > total {
		return 0, false
	}
	return used / total * 100, true
}

// buildAWSRDSMetrics 按日期序列组装指标行:
//   - 某日四指标均无数据点(经换算后)则跳过(缺失日不填充假值);
//   - memory% = memoryPercentFromFreeable(Freeable, Total GiB)(okMem=false 时
//     留 0——换算输入不可靠不伪造);disk% = memoryPercentFromUsed(Used, Total),
//     分母 AllocatedStorage<=0 时留 0;换算越界(负值等)同样留 0;
//   - CPU % / 连接数绝对值直给取整;缺失指标该字段留 0,四指标全 0 由
//     写路径 DAO 打 zero_exception;engine 原样透传(写库留痕)。
func buildAWSRDSMetrics(rdsID, instanceName, engine string, dates []string, cpuDaily, freeableMemDaily, freeStorageDaily, connDaily map[string]float64, okMem bool, totalMemGiB, storageGiB int64) []types.RDSMetric {
	const gib = int64(1024 * 1024 * 1024)
	memDaily := make(map[string]float64, len(freeableMemDaily))
	if okMem {
		totalBytes := float64(totalMemGiB * gib)
		for d, freeable := range freeableMemDaily {
			if p, ok := memoryPercentFromFreeable(freeable, totalBytes); ok {
				memDaily[d] = p
			}
		}
	}
	diskDaily := make(map[string]float64, len(freeStorageDaily))
	if storageGiB > 0 {
		totalBytes := float64(storageGiB * gib)
		for d, free := range freeStorageDaily {
			if p, ok := memoryPercentFromUsed(totalBytes-free, totalBytes); ok {
				diskDaily[d] = p
			}
		}
	}

	metrics := make([]types.RDSMetric, 0, len(dates))
	for _, d := range dates {
		cpu, okC := cpuDaily[d]
		mem, okM := memDaily[d]
		disk, okD := diskDaily[d]
		conn, okN := connDaily[d]
		if !okC && !okM && !okD && !okN {
			continue
		}
		metrics = append(metrics, types.RDSMetric{
			RdsID:         rdsID,
			InstanceName:  instanceName,
			Date:          d,
			CPUPercent:    cpu,
			MemoryPercent: mem,
			DiskPercent:   disk,
			Connections:   int64(math.Round(conn)),
			Engine:        engine,
			Provider:      "aws",
		})
	}
	return metrics
}
