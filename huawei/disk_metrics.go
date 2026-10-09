package huawei

import (
	"context"
	"fmt"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	cesv1 "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1"
	cesv1model "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1/model"
	cesv1region "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1/region"
)

// huawei Disk 指标适配器(DiskMetricQuerier 必达厂商之一)。
//
// 规格锚点:disk-ops-insight probe-report §1.2/§2/§4(T1 实盘定案,2026-09-21):
//   - Namespace = SYS.EVS(disk_device_* 系列,13 指标实盘注册、非零验证 240/240);
//     IOPS = disk_device_read/write_requests_rate(request/s);吞吐 =
//     disk_device_read/write_bytes_rate(Byte/s,采集边界经
//     types.BytesPerSecToMBPerSec 归一 MB/s);
//   - 使用率:无云盘级容量口径;唯一可按盘关联的口径是设备级
//     disk_device_io_util(%),打 DiskUsageScopeInstanceLevel 标注
//     (probe-report §2 归一方案;SYS.ECS disk_util_inband 需实例 ID 维度,
//     接口签名无实例上下文,不采);
//   - 维度键形态(实盘,与文档口径不同):disk_name = <盘ID>-<设备名>
//     (如 f4bf6adf-…-vda),须按盘 ID 前缀匹配——不能用卷 ID/用户命名精确匹配。
//     CES ListMetrics 不支持前缀过滤,适配器按指标名 ListMetrics 发现序列后
//     客户端前缀匹配;读序列未命中回退写序列(只写盘形态)。
//
// Hard Rule:指标路径按实例真实 region 经 cesv1region.SafeValueOf 创建 CES
// 客户端(region 不在支持列表显式报错,不做静默回退默认 region);失败路径
// 三分——调用失败 ERROR + error;真实无该盘序列空切片 + nil(INFO 日志)。

var _ cloudx.DiskMetricQuerier = (*DiskAdapter)(nil)

const (
	// cesNamespaceEVS 云硬盘指标 namespace(T1 实盘定案,probe-report §1.2)
	cesNamespaceEVS = "SYS.EVS"
	// cesMetricDevReadReqRate / cesMetricDevWriteReqRate 设备级 IOPS(request/s)
	cesMetricDevReadReqRate  = "disk_device_read_requests_rate"
	cesMetricDevWriteReqRate = "disk_device_write_requests_rate"
	// cesMetricDevReadBytesRate / cesMetricDevWriteBytesRate 设备级吞吐(Byte/s)
	cesMetricDevReadBytesRate  = "disk_device_read_bytes_rate"
	cesMetricDevWriteBytesRate = "disk_device_write_bytes_rate"
	// cesMetricDevIOUtil 设备级 IO 利用率(%,使用率唯一可按盘关联口径)
	cesMetricDevIOUtil = "disk_device_io_util"
	// cesDimDiskName CES 指标维度名(实盘形态 <盘ID>-<设备名>,前缀匹配)
	cesDimDiskName = "disk_name"
	// cesDiskDiscoveryMaxPages ListMetrics 发现翻页上限(防御异常大响应)
	cesDiskDiscoveryMaxPages = 25
)

// cesDiskMetricClient CES Disk 指标最小接口(ListMetrics 序列发现 +
// BatchListMetricData 批量取数;测试桩注入)。
type cesDiskMetricClient interface {
	ListMetrics(request *cesv1model.ListMetricsRequest) (*cesv1model.ListMetricsResponse, error)
	BatchListMetricData(request *cesv1model.BatchListMetricDataRequest) (*cesv1model.BatchListMetricDataResponse, error)
}

// diskMetricHooks CES Disk 指标查询测试注入钩子(非 nil 时替代真实依赖;仅单测使用)。
type diskMetricHooks struct {
	cesFactory func(region string) (cesDiskMetricClient, error)
}

// GetDiskMetrics 查询 [startDate, endDate](含两端,YYYY-MM-DD)内该云盘的
// 逐日使用率/IOPS/吞吐指标。region 为实例所在地域,本适配器按该 region 创建
// CES 客户端(不做全局推断、不做单 region 静默回退默认 region)。
//
// 失败路径(API 错误/超时/鉴权):打 ERROR 并携带 error 字段后返回 error,
// 由执行器记入失败计数(proposal「失败可观测性」);真实无该盘设备序列返回
// 空切片 + nil error(INFO 日志,非调用失败,不触发失败计数)。
func (a *DiskAdapter) GetDiskMetrics(ctx context.Context, diskID, diskName, region, startDate, endDate string) ([]types.DiskMetric, error) {
	if diskID == "" {
		return nil, fmt.Errorf("华为云磁盘指标查询需要云盘 ID")
	}
	if region == "" {
		return nil, fmt.Errorf("华为云磁盘指标查询需要实例 region(按实例所在 region 查询,不做全局推断、不回退默认 region)")
	}
	dates, err := metricDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("华为云磁盘指标查询已取消: %w", err)
	}

	client, err := a.createCESDiskClient(region)
	if err != nil {
		a.logDiskMetricFailure(diskID, region, err)
		return nil, err
	}

	// 序列发现:实盘维度键形态 <盘ID>-<设备名>,按盘 ID 前缀匹配
	// (probe-report §1.2;读序列未命中回退写序列)
	dimValue, found, err := a.discoverDiskDeviceDim(client, diskID)
	if err != nil {
		a.logDiskMetricFailure(diskID, region, err)
		return nil, err
	}
	if !found {
		// 真实无该盘设备序列(设备未上报/盘未挂载):空切片不触发失败计数
		a.logger.Info("华为云磁盘无 CES 设备序列,跳过指标查询",
			elog.String("disk_id", diskID),
			elog.String("region", region),
			elog.String("provider", "huawei"))
		return []types.DiskMetric{}, nil
	}

	// 单次批量查询 5 指标(4 IO + io_util)的日粒度均值,过滤 average
	request := &cesv1model.BatchListMetricDataRequest{
		Body: &cesv1model.BatchListMetricDataRequestBody{
			Metrics: huaweiDiskMetricCandidates(dimValue),
			Period:  cesPeriodPtr(cesv1model.GetBatchPeriodEnum().E_86400),
			Filter:  cesFilterPtr(cesv1model.GetFilterEnum().AVERAGE),
			From:    dayStartUnixMilli(dates[0]),
			To:      dayStartUnixMilli(dates[len(dates)-1]) + 86400_000 - 1,
		},
	}
	response, err := client.BatchListMetricData(request)
	if err != nil {
		err = fmt.Errorf("批量查询 CES 磁盘指标数据失败: %w", err)
		a.logDiskMetricFailure(diskID, region, err)
		return nil, err
	}

	byMetric := pickCESDiskMetricData(cesMetricsOf(response))
	readReqDaily := aggregateCESDaily(byMetric[cesMetricDevReadReqRate])
	writeReqDaily := aggregateCESDaily(byMetric[cesMetricDevWriteReqRate])
	readBytesDaily := aggregateCESDaily(byMetric[cesMetricDevReadBytesRate])
	writeBytesDaily := aggregateCESDaily(byMetric[cesMetricDevWriteBytesRate])
	ioUtilDaily := aggregateCESDaily(byMetric[cesMetricDevIOUtil])

	metrics := buildHuaweiDiskMetrics(diskID, diskName, dates, readReqDaily, writeReqDaily, readBytesDaily, writeBytesDaily, ioUtilDaily)
	if len(metrics) == 0 {
		// 真实无指标数据点(窗口内无上报),非调用失败
		return []types.DiskMetric{}, nil
	}
	return metrics, nil
}

// logDiskMetricFailure 适配器维度失败可观测性:ERROR + error 字段。
func (a *DiskAdapter) logDiskMetricFailure(diskID, region string, err error) {
	a.logger.Error("华为云磁盘指标查询失败",
		elog.String("disk_id", diskID),
		elog.String("region", region),
		elog.String("provider", "huawei"),
		elog.FieldErr(err))
}

// createCESDiskClient 按实例真实 region 创建 CES v1 客户端。
// Hard Rule:region 不在支持列表时显式报错——不走 disk.go getClient 的
// defaultRegion 兜底,避免按错误地域查询指标。
func (a *DiskAdapter) createCESDiskClient(region string) (cesDiskMetricClient, error) {
	if a.diskMetricHooks != nil && a.diskMetricHooks.cesFactory != nil {
		return a.diskMetricHooks.cesFactory(region)
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
		return nil, fmt.Errorf("CES region %s 不在支持列表(磁盘指标路径不做静默回退): %w", region, err)
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

// discoverDiskDeviceDim 发现该盘在 SYS.EVS 的设备序列维度值(前缀匹配)。
// 先按读请求速率序列发现,未命中回退写请求速率序列(只写盘形态)。
// 返回 found=false 表示该盘在 SYS.EVS 无任何匹配序列(非调用失败)。
func (a *DiskAdapter) discoverDiskDeviceDim(client cesDiskMetricClient, diskID string) (string, bool, error) {
	for _, metricName := range []string{cesMetricDevReadReqRate, cesMetricDevWriteReqRate} {
		dimValue, found, err := listDiskDeviceDim(client, diskID, metricName)
		if err != nil {
			return "", false, err
		}
		if found {
			return dimValue, true, nil
		}
	}
	return "", false, nil
}

// listDiskDeviceDim ListMetrics 分页发现指定指标的 disk_name 维度值中
// 与 diskID 匹配(精确或前缀)的序列;返回第一个命中值。
func listDiskDeviceDim(client cesDiskMetricClient, diskID, metricName string) (string, bool, error) {
	ns := cesNamespaceEVS
	start := ""
	for page := 0; page < cesDiskDiscoveryMaxPages; page++ {
		request := &cesv1model.ListMetricsRequest{
			Namespace:  &ns,
			MetricName: &metricName,
		}
		if start != "" {
			request.Start = &start
		}
		response, err := client.ListMetrics(request)
		if err != nil {
			return "", false, fmt.Errorf("ListMetrics(%s) 失败: %w", metricName, err)
		}
		if response == nil || response.Metrics == nil {
			break
		}
		for _, m := range *response.Metrics {
			for _, d := range m.Dimensions {
				if d.Name == nil || d.Value == nil {
					continue
				}
				if *d.Name == cesDimDiskName && matchDiskDeviceDim(*d.Value, diskID) {
					return *d.Value, true, nil
				}
			}
		}
		if response.MetaData == nil || response.MetaData.Marker == "" {
			break
		}
		start = response.MetaData.Marker
	}
	return "", false, nil
}

// matchDiskDeviceDim 设备序列维度值与盘匹配。实盘键形态(probe-report §1.2):
// disk_name = <盘ID>-<设备名>(卷 UUID 前缀 + 设备后缀),故按
// 「精确等于 / 盘ID+`-` 前缀」匹配,不能用用户命名或实例 UUID 匹配
// (实例 UUID 形态序列属 SYS.ECS,且接口签名无实例上下文)。
func matchDiskDeviceDim(dimValue, diskID string) bool {
	return dimValue == diskID || strings.HasPrefix(dimValue, diskID+"-")
}

// huaweiDiskMetricCandidates 单次 BatchListMetricData 的候选指标(同维度 5 项:
// 4 个 IO 指标 + 设备级 IO 利用率)。
func huaweiDiskMetricCandidates(dimValue string) []cesv1model.MetricInfo {
	dimensions := []cesv1model.MetricsDimension{{Name: cesDimDiskName, Value: dimValue}}
	names := []string{
		cesMetricDevReadReqRate, cesMetricDevWriteReqRate,
		cesMetricDevReadBytesRate, cesMetricDevWriteBytesRate,
		cesMetricDevIOUtil,
	}
	metrics := make([]cesv1model.MetricInfo, 0, len(names))
	for _, name := range names {
		metrics = append(metrics, cesv1model.MetricInfo{
			Namespace:  cesNamespaceEVS,
			MetricName: name,
			Dimensions: dimensions,
		})
	}
	return metrics
}

// pickCESDiskMetricData 从批量响应中按指标名归集数据点序列(仅取 SYS.EVS,
// 防其他 namespace 混入)。
func pickCESDiskMetricData(metrics []cesv1model.BatchMetricData) map[string][]cesv1model.DatapointForBatchMetric {
	byMetric := map[string][]cesv1model.DatapointForBatchMetric{}
	for i := range metrics {
		m := &metrics[i]
		if cesNamespaceOf(m) != cesNamespaceEVS {
			continue
		}
		byMetric[m.MetricName] = append(byMetric[m.MetricName], m.Datapoints...)
	}
	return byMetric
}

// ==================== 纯解析/聚合函数(单测覆盖) ====================

// buildHuaweiDiskMetrics 按日期序列组装指标行:
//   - 该日 4 个 IO 指标均无数据点则跳过(缺失日不填充假值);
//   - iops = 读+写请求速率之和(request/s,原始单位);吞吐 = 读+写字节速率
//     之和经 types.BytesPerSecToMBPerSec 归一 MB/s(采集边界换算,Hard Rule);
//   - 使用率取 disk_device_io_util 值并打 DiskUsageScopeInstanceLevel 标注
//     (probe-report §2 归一方案);该日无使用率数据点则留 0 且不打标;
//   - qc_status 由写路径(DAO)打 zero_exception,适配器不越权标注。
func buildHuaweiDiskMetrics(diskID, diskName string, dates []string, readReq, writeReq, readBytes, writeBytes, ioUtil map[string]float64) []types.DiskMetric {
	metrics := make([]types.DiskMetric, 0, len(dates))
	for _, d := range dates {
		rq, okRQ := readReq[d]
		wq, okWQ := writeReq[d]
		rb, okRB := readBytes[d]
		wb, okWB := writeBytes[d]
		if !okRQ && !okWQ && !okRB && !okWB {
			continue
		}
		var iops, byteRate float64
		if okRQ {
			iops += rq
		}
		if okWQ {
			iops += wq
		}
		if okRB {
			byteRate += rb
		}
		if okWB {
			byteRate += wb
		}
		usage, scope := 0.0, ""
		if u, ok := ioUtil[d]; ok {
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
			Throughput:   types.BytesPerSecToMBPerSec(byteRate),
			Provider:     "huawei",
			// AccountID 由执行器(T4)按云账号回填,适配器无账号上下文
		})
	}
	return metrics
}
