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

// aliyun OSS 指标适配器(OSSMetricQuerier 必达厂商之一)。
//
// 规格:M1 探测定案(probe-report §1.1/§4,2026-09-20 实盘非零验证 PASS=6/6):
//   - Namespace:**acs_oss_dashboard**(实盘非零验证通过;proposal 假设的
//     旧版 acs_oss 实盘基本失效——容量/对象类候选全部 400,不采用);
//   - 存储用量:**MeteringStorageUtilization**(单位 byte → 采集边界
//     /1024^3 归一 GB,共享 types.BytesToGB);
//   - 对象数:**ObjectCount**(单位 个,与容量独立上报);
//   - 维度:**{"BucketName":<bucket>}`(旧版 bucket 键不适用);
//   - Period:**3600**(计量类指标;86400 旧版口径无数据);
//   - 窗口限制:**≤31 天**(计量类指标只保留最近 31 天,补采须注意)。
//
// OSS 是全局服务(Querier 签名无 region):CMS 指标查询与 bucket 所在 region
// 无关(probe-report §1.1),按适配器账号 defaultRegion 创建 CMS 客户端,
// 按传入 bucketName 逐桶真实查询,不做全局推断。
//
// 失败路径三分(proposal「失败可观测性」):API 错误/超时/鉴权 → ERROR 日志
// + 返回 error(执行器记失败计数);真实无数据点(bucket 无上报/超窗)→
// 空切片 + nil error(非调用失败,不阻塞全流程);数据点解析失败值跳过
// 不伪造 0(与 NAS 同口径)。

var _ cloudx.OSSMetricQuerier = (*OSSAdapter)(nil)

const (
	// ossMetricNamespace M1 实测定案 namespace(实盘非零验证通过,probe-report §1.1)
	ossMetricNamespace = "acs_oss_dashboard"
	// ossMetricStorageUtilization 存储用量(byte,计量类)
	ossMetricStorageUtilization = "MeteringStorageUtilization"
	// ossMetricObjectCount 对象数(个)
	ossMetricObjectCount = "ObjectCount"
	// ossMetricHourPeriod 计量类指标粒度(CMS Period 单位秒,86400 旧版口径无数据)
	ossMetricHourPeriod = "3600"
	// ossDimBucketName 维度键(M1 定案:旧版 bucket 键不适用)
	ossDimBucketName = "BucketName"
	// ossMetricMaxRangeDays 计量类指标只保留最近 31 天(probe-report §1.1)
	ossMetricMaxRangeDays = 31
)

// ossMetricHooks OSS 指标查询测试注入钩子(非 nil 时替代真实依赖;仅单测使用)。
type ossMetricHooks struct {
	cmsFactory func(region string) (cmsMetricClient, error)
}

// GetOSSMetrics 查询 [startDate, endDate](含两端,YYYY-MM-DD)内该存储桶
// 的逐日容量/对象数指标。OSS 全局服务,签名无 region(interfaces.go 定案),
// CMS 客户端按账号 defaultRegion 创建。
//
// 失败路径三分:调用失败 → ERROR + error;真实无数据 → 空切片 + nil;
// 数据点缺 value 跳过不伪造 0(parseCMSDatapoints)。
func (a *OSSAdapter) GetOSSMetrics(ctx context.Context, bucketName, startDate, endDate string) ([]types.OSSMetric, error) {
	if bucketName == "" {
		return nil, fmt.Errorf("阿里云OSS指标查询需要存储桶名称")
	}
	dates, err := metricDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}
	if len(dates) > ossMetricMaxRangeDays {
		return nil, fmt.Errorf("阿里云OSS计量类指标查询区间 %d 天超过 31 天窗口(计量类指标只保留最近 31 天,probe-report §1.1)", len(dates))
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("阿里云OSS指标查询已取消: %w", err)
	}

	client, err := a.createOSSCMSClient()
	if err != nil {
		a.logOSSMetricFailure(bucketName, err)
		return nil, err
	}

	fromMs, toMs := nasRangeBoundsMs(dates)

	// 存储用量为行驱动指标(无数据点即无行),对象数与容量独立上报
	storagePoints, err := a.fetchOSSDaily(client, bucketName, ossMetricStorageUtilization, fromMs, toMs)
	if err != nil {
		a.logOSSMetricFailure(bucketName, err)
		return nil, err
	}
	if len(storagePoints) == 0 {
		// 真实无指标数据(bucket 无上报/超出 31 天窗口),非调用失败
		return []types.OSSMetric{}, nil
	}
	objectPoints, err := a.fetchOSSDaily(client, bucketName, ossMetricObjectCount, fromMs, toMs)
	if err != nil {
		a.logOSSMetricFailure(bucketName, err)
		return nil, err
	}

	storageDaily := aggregateCMSDaily(storagePoints)
	objectDaily := aggregateCMSDaily(objectPoints)
	return buildAliyunOSSMetrics(bucketName, dates, storageDaily, objectDaily), nil
}

// logOSSMetricFailure 适配器维度失败可观测性:ERROR + error 字段。
func (a *OSSAdapter) logOSSMetricFailure(bucketName string, err error) {
	a.logger.Error("阿里云OSS指标查询失败",
		elog.String("bucket", bucketName),
		elog.String("provider", "aliyun"),
		elog.FieldErr(err))
}

// createOSSCMSClient 创建(带缓存的)CMS 客户端。OSS 全局服务:CMS 指标查询
// 与 bucket 所在 region 无关(probe-report §1.1 探测先例),按账号 defaultRegion
// 创建(空 region 时兜底 cn-hangzhou endpoint,与探测脚本同口径)。
func (a *OSSAdapter) createOSSCMSClient() (cmsMetricClient, error) {
	if a.ossMetricHooks != nil && a.ossMetricHooks.cmsFactory != nil {
		return a.ossMetricHooks.cmsFactory(a.defaultRegion)
	}
	region := a.defaultRegion
	if region == "" {
		region = "cn-hangzhou"
	}
	a.ossMetricMu.Lock()
	defer a.ossMetricMu.Unlock()
	if c, ok := a.ossMetricClients[region]; ok {
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
	if a.ossMetricClients == nil {
		a.ossMetricClients = make(map[string]cmsMetricClient)
	}
	a.ossMetricClients[region] = client
	return client, nil
}

// fetchOSSDaily 查询单指标 [fromMs, toMs] 的 3600s 粒度数据点(NextToken 翻页聚合)。
// 维度按 M1 定案 {"BucketName":<bucket>}(透传真实 bucket 名称)。
func (a *OSSAdapter) fetchOSSDaily(client cmsMetricClient, bucket, metricName string, fromMs, toMs int64) ([]cmsDataPoint, error) {
	var all []cmsDataPoint
	nextToken := ""
	for page := 0; page < 10; page++ {
		request := cms.CreateDescribeMetricListRequest()
		request.Namespace = ossMetricNamespace
		request.MetricName = metricName
		request.Period = ossMetricHourPeriod
		request.Length = "1000"
		request.Dimensions = fmt.Sprintf(`{%q:%q}`, ossDimBucketName, bucket)
		request.StartTime = strconv.FormatInt(fromMs, 10)
		request.EndTime = strconv.FormatInt(toMs, 10)
		if nextToken != "" {
			request.NextToken = nextToken
		}
		response, err := client.DescribeMetricList(request)
		if err != nil {
			return nil, fmt.Errorf("查询OSS指标 %s 失败: %w", metricName, err)
		}
		if response == nil {
			break
		}
		points, err := parseCMSDatapoints(response.Datapoints)
		if err != nil {
			return nil, fmt.Errorf("解析OSS指标 %s 响应失败: %w", metricName, err)
		}
		all = append(all, points...)
		if response.NextToken == "" {
			break
		}
		nextToken = response.NextToken
	}
	return all, nil
}

// buildAliyunOSSMetrics 按日期序列组装指标行:
//   - storage 缺失日跳过不落库(缺失日不填充假值);
//   - 字节 → GB 换算在采集边界(types.BytesToGB,Hard Rule);
//   - 对象数无数据日为 0(与容量独立上报,非异常);
//   - AccountID/QcStatus 由执行器与写路径回填/标注,适配器不越权。
func buildAliyunOSSMetrics(bucketName string, dates []string, storageDaily, objectDaily map[string]float64) []types.OSSMetric {
	metrics := make([]types.OSSMetric, 0, len(dates))
	for _, d := range dates {
		storageBytes, ok := storageDaily[d]
		if !ok {
			continue
		}
		metrics = append(metrics, types.OSSMetric{
			BucketName:  bucketName,
			Date:        d,
			StorageSize: types.BytesToGB(storageBytes),
			ObjectCount: int64(objectDaily[d]), // 无数据=0(对象数与容量独立上报)
			Provider:    "aliyun",
		})
	}
	return metrics
}

// nasRangeBoundsMs/nasDailyPeriod 等窗口与聚合辅助复用 NAS 指标适配器
// (nas_metrics.go:parseCMSDatapoints/aggregateCMSDaily/cmsNumber/metricCSTZone),
// 单位换算统一走 types.BytesToGB(禁止复制粘贴,AC-5)。
