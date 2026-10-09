package aliyun

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/cms"
	"github.com/gotomicro/ego/core/elog"
)

// aliyun Disk 指标适配器(DiskMetricQuerier 必达厂商之一)。
//
// 规格锚点:disk-ops-insight probe-report §1.1/§2/§4(T1 实盘定案,2026-09-21):
//   - Namespace = acs_ecs_dashboard(实盘成立);
//   - IOPS/吞吐为云盘级(diskId 维度):DiskReadIOPS / DiskWriteIOPS(次/秒)、
//     DiskReadBPS / DiskWriteBPS(byte/s,采集边界经 types.BytesPerSecToMBPerSec
//     归一 MB/s);
//   - 使用率无云盘级容量口径:唯一可用是挂载实例级 vm.DiskUtilization(%,
//     云监控插件,实盘维度形态为 instanceId 单维可查,probe-report §1.1 样本
//     60.693%/88.649%)——须先用 ECS 资产路径解析磁盘挂载实例;未挂载盘
//     (available)使用率无意义,留 0 由写路径打 zero_exception,不伪造。
//
// 口径标注:使用率打 DiskUsageScopeInstanceLevel(维度是实例,非单盘容量水位,
// probe-report §2 归一方案)。注意 acs_ecs_dashboard 的 *BurstUtilization 系列
// 实盘含 -1 哨兵值(probe-report §1.1/遗留行动 #4),本适配器不采集该系列,
// 天然规避;若二期扩采,须在采集边界过滤 -1 为 null。
//
// Hard Rule:指标路径按实例真实 region 创建 CMS 客户端,不做全局 region 推断;
// 失败路径三分——调用失败 ERROR + error;真实无数据空切片 + nil。

var _ cloudx.DiskMetricQuerier = (*DiskAdapter)(nil)

const (
	// diskMetricNamespace ECS 云监控 namespace(T1 实盘定案)
	diskMetricNamespace = "acs_ecs_dashboard"
	// diskMetricReadIOPS / diskMetricWriteIOPS 云盘级 IOPS(次/秒,diskId 维度)
	diskMetricReadIOPS  = "DiskReadIOPS"
	diskMetricWriteIOPS = "DiskWriteIOPS"
	// diskMetricReadBPS / diskMetricWriteBPS 云盘级吞吐(byte/s,diskId 维度)
	diskMetricReadBPS  = "DiskReadBPS"
	diskMetricWriteBPS = "DiskWriteBPS"
	// diskMetricInstanceUsage 挂载实例级磁盘使用率(%,云监控插件口径)
	diskMetricInstanceUsage = "vm.DiskUtilization"
	// diskDimDiskID / diskDimInstanceID 指标维度名(实盘探测形态,单维可查)
	diskDimDiskID     = "diskId"
	diskDimInstanceID = "instanceId"
	// diskDailyPeriod 天粒度聚合(CMS Period 单位秒)
	diskDailyPeriod = "86400"
)

// diskMetricHooks Disk 指标查询测试注入钩子(非 nil 时替代真实依赖;仅单测使用)。
type diskMetricHooks struct {
	cmsFactory func(region string) (cmsMetricClient, error)
	// lookupDisk 挂载实例解析(usage 实例级口径需要 instanceId 维度);
	// 生产路径走 ECS 资产 ListInstancesByIDs,返回 nil 表示盘未挂载。
	lookupDisk func(ctx context.Context, diskID, region string) (*types.DiskInstance, error)
}

// GetDiskMetrics 查询 [startDate, endDate](含两端,YYYY-MM-DD)内该云盘的
// 逐日使用率/IOPS/吞吐指标。region 为实例所在地域(调用方从实例元数据取),
// 本适配器按该 region 创建 CMS 客户端,不做全局 region 推断。
//
// 失败路径(API 错误/超时/鉴权):打 ERROR 并携带 error 字段后返回 error,
// 由执行器记入失败计数(proposal「失败可观测性」);真实无数据点返回空切片
// + nil error(非调用失败,不触发失败计数)。
func (a *DiskAdapter) GetDiskMetrics(ctx context.Context, diskID, diskName, region, startDate, endDate string) ([]types.DiskMetric, error) {
	if diskID == "" {
		return nil, fmt.Errorf("阿里云磁盘指标查询需要云盘 ID")
	}
	if region == "" {
		return nil, fmt.Errorf("阿里云磁盘指标查询需要实例 region(按实例所在 region 查询,不做全局推断)")
	}
	dates, err := metricDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("阿里云磁盘指标查询已取消: %w", err)
	}

	client, err := a.createCMSClient(region)
	if err != nil {
		a.logDiskMetricFailure(diskID, region, err)
		return nil, err
	}

	fromMs, toMs := nasRangeBoundsMs(dates)
	diskDims := fmt.Sprintf(`{%q:%q}`, diskDimDiskID, diskID)
	fetchDaily := func(metricName string) (map[string]float64, error) {
		points, err := a.fetchDiskDaily(client, metricName, diskDims, fromMs, toMs)
		if err != nil {
			return nil, err
		}
		return aggregateCMSDaily(points), nil
	}
	readIOPSDaily, err := fetchDaily(diskMetricReadIOPS)
	if err != nil {
		a.logDiskMetricFailure(diskID, region, err)
		return nil, err
	}
	writeIOPSDaily, err := fetchDaily(diskMetricWriteIOPS)
	if err != nil {
		a.logDiskMetricFailure(diskID, region, err)
		return nil, err
	}
	readBPSDaily, err := fetchDaily(diskMetricReadBPS)
	if err != nil {
		a.logDiskMetricFailure(diskID, region, err)
		return nil, err
	}
	writeBPSDaily, err := fetchDaily(diskMetricWriteBPS)
	if err != nil {
		a.logDiskMetricFailure(diskID, region, err)
		return nil, err
	}

	// 使用率:先解析挂载实例(T1 定案唯一口径 vm.DiskUtilization 为实例级);
	// 解析调用失败按调用失败路径返回 error,盘未挂载(available)使用率无意义
	// → 留空(proposal「qc_status 异常」:可能为 null 或 0 打标)。
	instance, err := a.lookupAttachedInstance(ctx, diskID, region)
	if err != nil {
		a.logDiskMetricFailure(diskID, region, err)
		return nil, err
	}
	usageDaily := map[string]float64{}
	if instance != nil && instance.InstanceID != "" {
		points, err := a.fetchDiskDaily(client, diskMetricInstanceUsage,
			fmt.Sprintf(`{%q:%q}`, diskDimInstanceID, instance.InstanceID), fromMs, toMs)
		if err != nil {
			a.logDiskMetricFailure(diskID, region, err)
			return nil, err
		}
		usageDaily = aggregateCMSDaily(points)
	}

	metrics := buildAliyunDiskMetrics(diskID, diskName, dates, readIOPSDaily, writeIOPSDaily, readBPSDaily, writeBPSDaily, usageDaily)
	if len(metrics) == 0 {
		// 真实无指标数据(盘无 IO/未上报),非调用失败:空切片不触发失败计数
		return []types.DiskMetric{}, nil
	}
	return metrics, nil
}

// logDiskMetricFailure 适配器维度失败可观测性:ERROR + error 字段。
func (a *DiskAdapter) logDiskMetricFailure(diskID, region string, err error) {
	a.logger.Error("阿里云磁盘指标查询失败",
		elog.String("disk_id", diskID),
		elog.String("region", region),
		elog.String("provider", "aliyun"),
		elog.FieldErr(err))
}

// createCMSClient 按实例 region 创建(带缓存的)CMS 客户端。
func (a *DiskAdapter) createCMSClient(region string) (cmsMetricClient, error) {
	if a.diskMetricHooks != nil && a.diskMetricHooks.cmsFactory != nil {
		return a.diskMetricHooks.cmsFactory(region)
	}
	a.metricMu.Lock()
	defer a.metricMu.Unlock()
	if c, ok := a.metricClients[region]; ok {
		return c, nil
	}
	credential := credentials.NewAccessKeyCredential(a.accessKeyID, a.accessKeySecret)
	config := sdk.NewConfig()
	config.Scheme = "https"
	client, err := cms.NewClientWithOptions(region, config, credential)
	if err != nil {
		return nil, fmt.Errorf("创建CMS客户端失败: %w", err)
	}
	client.Domain = fmt.Sprintf("metrics.%s.aliyuncs.com", region)
	if a.metricClients == nil {
		a.metricClients = make(map[string]cmsMetricClient)
	}
	a.metricClients[region] = client
	return client, nil
}

// lookupAttachedInstance 解析云盘当前挂载实例(usage 实例级口径的维度输入)。
// 生产路径复用 ECS 资产 ListInstancesByIDs(只读);查无此盘返回 nil(视为
// 未挂载,不视为失败)。
func (a *DiskAdapter) lookupAttachedInstance(ctx context.Context, diskID, region string) (*types.DiskInstance, error) {
	if a.diskMetricHooks != nil && a.diskMetricHooks.lookupDisk != nil {
		return a.diskMetricHooks.lookupDisk(ctx, diskID, region)
	}
	list, err := a.ListInstancesByIDs(ctx, region, []string{diskID})
	if err != nil {
		return nil, fmt.Errorf("解析云盘挂载实例失败: %w", err)
	}
	if len(list) == 0 {
		return nil, nil
	}
	return &list[0], nil
}

// fetchDiskDaily 查询单指标 [fromMs, toMs] 的天粒度数据点(NextToken 翻页聚合)。
func (a *DiskAdapter) fetchDiskDaily(client cmsMetricClient, metricName, dimensions string, fromMs, toMs int64) ([]cmsDataPoint, error) {
	var all []cmsDataPoint
	nextToken := ""
	for page := 0; page < 10; page++ {
		request := cms.CreateDescribeMetricListRequest()
		request.Namespace = diskMetricNamespace
		request.MetricName = metricName
		request.Period = diskDailyPeriod
		request.Length = "1000"
		request.Dimensions = dimensions
		request.StartTime = strconv.FormatInt(fromMs, 10)
		request.EndTime = strconv.FormatInt(toMs, 10)
		if nextToken != "" {
			request.NextToken = nextToken
		}
		response, err := client.DescribeMetricList(request)
		if err != nil {
			return nil, fmt.Errorf("查询磁盘指标 %s 失败: %w", metricName, err)
		}
		if response == nil {
			break
		}
		points, err := parseCMSDatapoints(response.Datapoints)
		if err != nil {
			return nil, fmt.Errorf("解析磁盘指标 %s 响应失败: %w", metricName, err)
		}
		all = append(all, points...)
		if response.NextToken == "" {
			break
		}
		nextToken = response.NextToken
	}
	return all, nil
}

// ==================== 纯解析/聚合函数(单测覆盖) ====================

// buildAliyunDiskMetrics 按日期序列组装指标行:
//   - 该日 4 个云盘级指标均无数据点则跳过(缺失日不填充假值);
//   - iops = 读+写之和(次/秒,原始单位);吞吐 = 读+写 BPS 之和经
//     types.BytesPerSecToMBPerSec 归一 MB/s(采集边界换算,Hard Rule);
//   - 使用率取 vm.DiskUtilization 值并打 DiskUsageScopeInstanceLevel 标注
//     (probe-report §2 归一方案);该日无使用率数据点则留 0 且不打标;
//   - qc_status 由写路径(DAO)打 zero_exception,适配器不越权标注。
func buildAliyunDiskMetrics(diskID, diskName string, dates []string, readIOPS, writeIOPS, readBPS, writeBPS, usageDaily map[string]float64) []types.DiskMetric {
	metrics := make([]types.DiskMetric, 0, len(dates))
	for _, d := range dates {
		rI, okRI := readIOPS[d]
		wI, okWI := writeIOPS[d]
		rB, okRB := readBPS[d]
		wB, okWB := writeBPS[d]
		if !okRI && !okWI && !okRB && !okWB {
			continue
		}
		var iops, bps float64
		if okRI {
			iops += rI
		}
		if okWI {
			iops += wI
		}
		if okRB {
			bps += rB
		}
		if okWB {
			bps += wB
		}
		usage, scope := 0.0, ""
		if u, ok := usageDaily[d]; ok {
			usage = u
			scope = types.DiskUsageScopeInstanceLevel
		}
		metrics = append(metrics, types.DiskMetric{
			DiskID:       diskID,
			DiskName:     diskName,
			Date:         d,
			UsagePercent: usage,
			UsageScope:   scope,
			IOPS:         iops,
			Throughput:   types.BytesPerSecToMBPerSec(bps),
			Provider:     "aliyun",
			// AccountID 由执行器(T4)按云账号回填,适配器无账号上下文
		})
	}
	return metrics
}
