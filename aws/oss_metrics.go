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

// aws S3 指标适配器(OSSMetricQuerier 必达厂商之一)。
//
// 规格:M1 探测定案(probe-report §1.3/§4,2026-09-20 实盘 84/84 注册序列
// 全部非零):Namespace=AWS/S3、MetricName=BucketSizeBytes(byte)+
// NumberOfObjects(个),维度 BucketName + StorageType——容量取
// **StandardStorage**(标准存储计费口径),对象数取 **AllStorageTypes**;
// 每日上报 1 次(天粒度)。
//
// 与 CDN 的「CloudFront 主动放弃」先例(aws/cdn_metrics.go 返回空切片跳过)
// 不同:S3 有标准 CloudWatch 指标,本实现为真实查询路径,调用失败返回 error,
// 不套用主动放弃先例(AC-3 Hard Rule 对应项)。
//
// OSS 是全局服务(Querier 签名无 region):S3 CloudWatch 指标按 bucket 所在
// region 上报,适配器先经 GetBucketLocation 解析 bucket 真实 region,再在该
// region 创建 CloudWatch 客户端——按 bucket 真实查询,不做全局推断。
//
// 失败路径三分(proposal「失败可观测性」):API 错误/超时/鉴权 → ERROR 日志
// + 返回 error(执行器记失败计数);真实无数据点(未注册/闲置 bucket,实盘
// 20 个未注册走零值例外打标呈现)→ 空切片 + nil error(非调用失败)。

var _ cloudx.OSSMetricQuerier = (*S3Adapter)(nil)

const (
	// s3MetricNamespace S3 标准 CloudWatch namespace(公认标准指标)
	s3MetricNamespace = "AWS/S3"
	// s3MetricBucketSizeBytes 存储量(byte,计费基础口径)
	s3MetricBucketSizeBytes = "BucketSizeBytes"
	// s3MetricNumberOfObjects 对象数(个)
	s3MetricNumberOfObjects = "NumberOfObjects"
	// s3DimBucketName CloudWatch 维度名
	s3DimBucketName = "BucketName"
	// s3DimStorageType CloudWatch 维度名(容量/对象数口径区分)
	s3DimStorageType = "StorageType"
	// s3StorageTypeStandard 容量口径:标准存储(probe-report §1.3 定案)
	s3StorageTypeStandard = "StandardStorage"
	// s3StorageTypeAll 对象数口径:全部存储类型(probe-report §1.3 定案)
	s3StorageTypeAll = "AllStorageTypes"
	// s3DailyPeriod 天粒度聚合(CloudWatch Period 单位秒)
	s3DailyPeriod = int32(86400)
)

// ossMetricHooks S3 指标查询测试注入钩子(非 nil 时替代真实依赖;仅单测使用)。
type ossMetricHooks struct {
	// bucketRegion 替代 GetBucketLocation 的 bucket 真实 region 解析
	bucketRegion func(ctx context.Context, bucketName string) (string, error)
	// cwFactory 替代 CloudWatch 客户端创建(记录 region 透传)
	cwFactory func(ctx context.Context, region string) (cwMetricClient, error)
}

// GetOSSMetrics 查询 [startDate, endDate](含两端,YYYY-MM-DD)内该存储桶
// 的逐日容量/对象数指标。OSS 全局服务,签名无 region(interfaces.go 定案);
// CloudWatch 按 bucket 真实 region(GetBucketLocation 解析)创建。
//
// 失败路径三分:调用失败(region 解析失败/GetMetricData 失败)→ ERROR +
// error;真实无数据点 → 空切片 + nil;数据点缺值跳过不伪造 0。
func (a *S3Adapter) GetOSSMetrics(ctx context.Context, bucketName, startDate, endDate string) ([]types.OSSMetric, error) {
	if bucketName == "" {
		return nil, fmt.Errorf("AWS S3 指标查询需要存储桶名称")
	}
	dates, err := nasMetricDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("AWS S3 指标查询已取消: %w", err)
	}

	// S3 CloudWatch 指标按 bucket 所在 region 上报:先解析 bucket 真实 region
	region, err := a.resolveS3BucketRegion(ctx, bucketName)
	if err != nil {
		a.logOSSMetricFailure(bucketName, err)
		return nil, err
	}
	client, err := a.createS3CWClient(ctx, region)
	if err != nil {
		a.logOSSMetricFailure(bucketName, err)
		return nil, err
	}

	sizeDaily, objectDaily, err := a.fetchS3Daily(ctx, client, bucketName, dates)
	if err != nil {
		a.logOSSMetricFailure(bucketName, err)
		return nil, err
	}
	if len(sizeDaily) == 0 {
		// 真实无数据点(序列未注册/闲置无上报),非调用失败
		return []types.OSSMetric{}, nil
	}
	return buildS3Metrics(bucketName, dates, sizeDaily, objectDaily), nil
}

// logOSSMetricFailure 适配器维度失败可观测性:ERROR + error 字段。
func (a *S3Adapter) logOSSMetricFailure(bucketName string, err error) {
	a.logger.Error("AWS S3 指标查询失败",
		elog.String("bucket", bucketName),
		elog.String("provider", "aws"),
		elog.FieldErr(err))
}

// resolveS3BucketRegion 解析 bucket 真实 region(GetBucketLocation)。
// 解析失败显式报错——不静默退回 defaultRegion 查错地域(空数据比报错更难排查)。
func (a *S3Adapter) resolveS3BucketRegion(ctx context.Context, bucketName string) (string, error) {
	if a.ossMetricHooks != nil && a.ossMetricHooks.bucketRegion != nil {
		return a.ossMetricHooks.bucketRegion(ctx, bucketName)
	}
	// 账号 defaultRegion 客户端仅用于 GetBucketLocation(该 API 全局可达)
	client, err := a.createClient(ctx, "")
	if err != nil {
		return "", fmt.Errorf("创建S3客户端失败: %w", err)
	}
	region, err := a.getBucketRegion(ctx, client, bucketName)
	if err != nil {
		return "", fmt.Errorf("获取存储桶 %s region 失败: %w", bucketName, err)
	}
	return region, nil
}

// createS3CWClient 按 bucket 真实 region 创建 CloudWatch 客户端(静态凭证)。
func (a *S3Adapter) createS3CWClient(ctx context.Context, region string) (cwMetricClient, error) {
	if a.ossMetricHooks != nil && a.ossMetricHooks.cwFactory != nil {
		return a.ossMetricHooks.cwFactory(ctx, region)
	}
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			a.accessKeyID,
			a.accessKeySecret,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("加载AWS配置失败: %w", err)
	}
	return cloudwatch.NewFromConfig(cfg), nil
}

// fetchS3Daily 单次 GetMetricData 双查询(BucketSizeBytes/NumberOfObjects)+
// NextToken 翻页聚合,返回逐日容量/对象数 map。
func (a *S3Adapter) fetchS3Daily(ctx context.Context, client cwMetricClient, bucketName string, dates []string) (map[string]float64, map[string]float64, error) {
	startT, endT := nasRangeBounds(dates)
	var sizeTs, objTs []time.Time
	var sizeVals, objVals []float64
	nextToken := ""
	for page := 0; page < 10; page++ {
		input := &cloudwatch.GetMetricDataInput{
			StartTime: &startT,
			EndTime:   &endT,
			MetricDataQueries: []cwtypes.MetricDataQuery{
				{
					Id: awssdk.String("bucket_size_bytes"),
					MetricStat: &cwtypes.MetricStat{
						Metric: &cwtypes.Metric{
							Namespace:  awssdk.String(s3MetricNamespace),
							MetricName: awssdk.String(s3MetricBucketSizeBytes),
							Dimensions: []cwtypes.Dimension{
								{Name: awssdk.String(s3DimBucketName), Value: awssdk.String(bucketName)},
								{Name: awssdk.String(s3DimStorageType), Value: awssdk.String(s3StorageTypeStandard)},
							},
						},
						Period: awssdk.Int32(s3DailyPeriod),
						Stat:   awssdk.String("Average"),
					},
				},
				{
					Id: awssdk.String("number_of_objects"),
					MetricStat: &cwtypes.MetricStat{
						Metric: &cwtypes.Metric{
							Namespace:  awssdk.String(s3MetricNamespace),
							MetricName: awssdk.String(s3MetricNumberOfObjects),
							Dimensions: []cwtypes.Dimension{
								{Name: awssdk.String(s3DimBucketName), Value: awssdk.String(bucketName)},
								{Name: awssdk.String(s3DimStorageType), Value: awssdk.String(s3StorageTypeAll)},
							},
						},
						Period: awssdk.Int32(s3DailyPeriod),
						Stat:   awssdk.String("Average"),
					},
				},
			},
		}
		if nextToken != "" {
			input.NextToken = &nextToken
		}
		output, err := client.GetMetricData(ctx, input)
		if err != nil {
			return nil, nil, fmt.Errorf("查询 S3 指标失败: %w", err)
		}
		// 两序列时间戳独立累积(容量/对象数上报节奏独立,不可共用下标)
		for _, r := range output.MetricDataResults {
			switch awssdk.ToString(r.Id) {
			case "bucket_size_bytes":
				sizeTs = append(sizeTs, r.Timestamps...)
				sizeVals = append(sizeVals, r.Values...)
			case "number_of_objects":
				objTs = append(objTs, r.Timestamps...)
				objVals = append(objVals, r.Values...)
			}
		}
		if output.NextToken == nil || *output.NextToken == "" {
			break
		}
		nextToken = *output.NextToken
	}
	return aggregateCWMetricDaily(sizeTs, sizeVals), aggregateCWMetricDaily(objTs, objVals), nil
}

// buildS3Metrics 按日期序列组装指标行:
//   - size 缺失日跳过不落库(缺失日不填充假值);
//   - BucketSizeBytes 字节 → GB 换算在采集边界(types.BytesToGB,Hard Rule);
//   - 对象数无数据日为 0(与容量独立上报,非异常);
//   - AccountID/QcStatus 由执行器与写路径回填/标注,适配器不越权。
func buildS3Metrics(bucketName string, dates []string, sizeDaily, objectDaily map[string]float64) []types.OSSMetric {
	metrics := make([]types.OSSMetric, 0, len(dates))
	for _, d := range dates {
		sizeBytes, ok := sizeDaily[d]
		if !ok {
			continue
		}
		metrics = append(metrics, types.OSSMetric{
			BucketName:  bucketName,
			Date:        d,
			StorageSize: types.BytesToGB(sizeBytes),
			ObjectCount: int64(objectDaily[d]), // 无数据=0(对象数与容量独立上报)
			Provider:    "aws",
		})
	}
	return metrics
}
