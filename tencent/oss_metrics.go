package tencent

import (
	"context"
	"fmt"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	monitor "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/monitor/v20180724"
)

// tencent OSS 指标适配器(OSSMetricQuerier 尽力而为厂商之一,monitor 子包)。
//
// 规格:M1 探测定案(probe-report §1.4/§4,2026-09-20 实盘验证,tencent 探测可用
// ——归因修正:非订阅问题;bucket 数占比 2.21% ≤15% 不触发升格,维持尽力而为,
// 但适配路径已完全收敛,本文件按定案直接实现):
//   - Namespace:**QCE/COS**(DescribeBaseMetrics 实盘返回 206 个指标);
//   - 存储用量:**StdStorage**(标准存储,**单位 MB 不是 byte** → 采集边界
//     MB→GB 换算走共享 types.MBToGB(委托 types.BytesToGB,Hard Rule 禁止把
//     MB 当 byte 进 BytesToGB,probe-report 遗留行动 #4);分层 ArcStorage/
//     ColdStorage 等不采,分层大小留二期);
//   - 对象数:**StdObjectNumber**(单位 个,与容量独立上报);
//   - 维度:**{"bucket":<bucket>}` 单维即可(probe 实测单维生效,无需 appid
//     双维,不发起 CAM GetUserAppId 调用);
//   - Period:86400(天粒度,probe 同口径)。
//
// region:OSSMetricQuerier 签名无 region(OSS 全局服务,interfaces.go 定案),
// monitor 客户端按账号 defaultRegion 创建(与 aliyun CMS 客户端同型;空
// defaultRegion 兜底 ap-guangzhou)。QCE/COS 数据点若因 bucket 实际 region 与
// defaultRegion 不一致而缺失,按「真实无数据点」返回空切片 + nil error,不伪装、
// 不报错(写路径 [1MB,1PB] 数量级自检与空集可观测兜底)。
//
// 失败路径三分(proposal「失败可观测性」):API 错误/超时/鉴权 → ERROR 日志 +
// 返回 error(执行器记失败计数);真实无数据点(bucket 无上报)→ 空切片 +
// nil error(非调用失败,不阻塞全流程);数据点解析失败值跳过不伪造 0
// (aggregateNASMonitorDaily 同口径)。
//
// 窗口/聚合/时间格式辅助复用 NAS 指标适配器(nas_metrics.go:nasMetricDateRange/
// nasMonitorRangeBounds/formatNasMonitorTime/aggregateNASMonitorDaily,
// 同包单一实现);客户端接口复用 nasMonitorClient(同为 GetMonitorData 最小面)。

var _ cloudx.OSSMetricQuerier = (*COSAdapter)(nil)

const (
	// cosMonitorNamespace COS 云监控 namespace(M1 实测定案,probe-report §1.4)
	cosMonitorNamespace = "QCE/COS"
	// cosMetricStdStorage 标准存储容量(官方文档单位 **MB**,probe-report §1.4)
	cosMetricStdStorage = "StdStorage"
	// cosMetricStdObjectNumber 标准存储对象数(单位 个)
	cosMetricStdObjectNumber = "StdObjectNumber"
	// cosDimBucket 云监控维度键(M1 定案:单维 bucket 即可,无需 appid 双维)
	cosDimBucket = "bucket"
	// cosDefaultRegionFallback 账号 defaultRegion 为空时的兜底 region
	// (monitor 客户端构造需要合法 region;兜底值仅影响查询入口,数据缺失按
	// 「真实无数据点」空切片呈现,不伪装)
	cosDefaultRegionFallback = "ap-guangzhou"
)

// ossMetricHooks OSS 指标查询测试注入钩子(非 nil 时替代真实依赖;仅单测使用)。
type ossMetricHooks struct {
	monitorFactory func(region string) (nasMonitorClient, error)
}

// GetOSSMetrics 查询 [startDate, endDate](含两端,YYYY-MM-DD)内该存储桶
// 的逐日容量/对象数指标。OSS 全局服务,签名无 region(interfaces.go 定案),
// monitor 客户端按账号 defaultRegion 创建(见文件头 region 说明)。
//
// 失败路径三分:调用失败 → ERROR + error;真实无数据 → 空切片 + nil;
// 数据点缺 value 跳过不伪造 0(aggregateNASMonitorDaily)。
func (a *COSAdapter) GetOSSMetrics(ctx context.Context, bucketName, startDate, endDate string) ([]types.OSSMetric, error) {
	if bucketName == "" {
		return nil, fmt.Errorf("腾讯云OSS指标查询需要存储桶名称")
	}
	// 窗口上限复用 NAS 的 92 天(nasMetricMaxRangeDays,QCE 天粒度 GetMonitorData
	// 数据点上限 7200,92 天远在限内)
	dates, err := nasMetricDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("腾讯云OSS指标查询已取消: %w", err)
	}

	client, err := a.createOSSMonitorClient()
	if err != nil {
		a.logOSSMetricFailure(bucketName, err)
		return nil, err
	}

	startT, endT := nasMonitorRangeBounds(dates)

	// 存储用量为行驱动指标(无数据点即无行),对象数与容量独立上报
	storageDaily, err := a.fetchCOSMonitorDaily(client, bucketName, cosMetricStdStorage, startT, endT)
	if err != nil {
		a.logOSSMetricFailure(bucketName, err)
		return nil, err
	}
	if len(storageDaily) == 0 {
		// 真实无指标数据(bucket 无上报/region 不一致),非调用失败
		return []types.OSSMetric{}, nil
	}
	objectDaily, err := a.fetchCOSMonitorDaily(client, bucketName, cosMetricStdObjectNumber, startT, endT)
	if err != nil {
		a.logOSSMetricFailure(bucketName, err)
		return nil, err
	}

	return buildTencentOSSMetrics(bucketName, dates, storageDaily, objectDaily), nil
}

// logOSSMetricFailure 适配器维度失败可观测性:ERROR + error 字段。
func (a *COSAdapter) logOSSMetricFailure(bucketName string, err error) {
	a.logger.Error("腾讯云OSS指标查询失败",
		elog.String("bucket", bucketName),
		elog.String("provider", "tencent"),
		elog.FieldErr(err))
}

// createOSSMonitorClient 创建(带缓存的)云监控客户端,按账号 defaultRegion
// (空 defaultRegion 兜底 ap-guangzhou,见文件头 region 说明)。
func (a *COSAdapter) createOSSMonitorClient() (nasMonitorClient, error) {
	if a.ossMetricHooks != nil && a.ossMetricHooks.monitorFactory != nil {
		return a.ossMetricHooks.monitorFactory(a.monitorRegion())
	}
	region := a.monitorRegion()
	a.ossMonitorMu.Lock()
	defer a.ossMonitorMu.Unlock()
	if c, ok := a.ossMonitorClients[region]; ok {
		return c, nil
	}
	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "monitor.tencentcloudapi.com"
	client, err := monitor.NewClient(credential, region, cpf)
	if err != nil {
		return nil, fmt.Errorf("创建云监控客户端失败: %w", err)
	}
	if a.ossMonitorClients == nil {
		a.ossMonitorClients = make(map[string]nasMonitorClient)
	}
	a.ossMonitorClients[region] = client
	return client, nil
}

// monitorRegion OSS 指标查询 region:账号 defaultRegion,空则兜底。
func (a *COSAdapter) monitorRegion() string {
	if a.defaultRegion != "" {
		return a.defaultRegion
	}
	return cosDefaultRegionFallback
}

// fetchCOSMonitorDaily 查询单指标 [startT, endT] 的天粒度数据点。
// 维度按 M1 定案 {"bucket":<bucket>} 单维(透传真实 bucket 名称,无需 appid)。
func (a *COSAdapter) fetchCOSMonitorDaily(client nasMonitorClient, bucket, metricName string, startT, endT time.Time) (map[string]float64, error) {
	request := monitor.NewGetMonitorDataRequest()
	request.Namespace = common.StringPtr(cosMonitorNamespace)
	request.MetricName = common.StringPtr(metricName)
	request.Period = common.Uint64Ptr(nasDailyPeriod)
	request.Instances = []*monitor.Instance{{
		Dimensions: []*monitor.Dimension{
			{Name: common.StringPtr(cosDimBucket), Value: common.StringPtr(bucket)},
		},
	}}
	request.StartTime = common.StringPtr(formatNasMonitorTime(startT))
	request.EndTime = common.StringPtr(formatNasMonitorTime(endT))

	response, err := client.GetMonitorData(request)
	if err != nil {
		return nil, fmt.Errorf("查询OSS指标 %s 失败: %w", metricName, err)
	}
	return aggregateNASMonitorDaily(response), nil
}

// buildTencentOSSMetrics 按日期序列组装指标行:
//   - storage 缺失日跳过不落库(缺失日不填充假值);
//   - **MB → GB** 换算在采集边界(types.MBToGB,内部委托 types.BytesToGB,
//     Hard Rule 单一换算链;StdStorage 单位是 MB 不是 byte,probe-report
//     §1.4/遗留行动 #4);
//   - 对象数无数据日为 0(与容量独立上报,非异常);
//   - AccountID/QcStatus 由执行器与写路径回填/标注,适配器不越权。
func buildTencentOSSMetrics(bucketName string, dates []string, storageDaily, objectDaily map[string]float64) []types.OSSMetric {
	metrics := make([]types.OSSMetric, 0, len(dates))
	for _, d := range dates {
		storageMB, ok := storageDaily[d]
		if !ok {
			continue
		}
		metrics = append(metrics, types.OSSMetric{
			BucketName:  bucketName,
			Date:        d,
			StorageSize: types.MBToGB(storageMB),
			ObjectCount: int64(objectDaily[d]),
			Provider:    "tencent",
		})
	}
	return metrics
}
