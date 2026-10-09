// Package aws_test OSS 指标探测(oss-ops-insight M1 探测任务,manual probe,只读)。
//
// AWS CloudWatch `AWS/S3` 的 `BucketSizeBytes`/`NumberOfObjects` 探测(AC-2):
//  1. ListMetrics 确认两指标在实盘 bucket 上已注册(维度 BucketName+StorageType);
//  2. GetMetricData 90 天窗口天粒度非零验证(闲置 bucket 无数据点 →
//     「未通过+原因」零值注记,按 proposal 走零值例外放行+打标路径,
//     不套用 CloudFront「主动放弃」先例);
//  3. BucketSizeBytes 30 天首末点增速(高增长近失证据)。
//
// SKIP gate(无 env 不跑):
//   - NAS_PROBE_AWS_AK / NAS_PROBE_AWS_SK / NAS_PROBE_AWS_REGION 直填,或
//   - NAS_PROBE_MONGODB_DSN(+可选 CAM_ENCRYPTION_KEY)从库加载活跃 aws 账号
//
// Hard Rule:只读凭证,只写样例行到测试日志,不动生产表。
// 复用本包 NAS 探测的 newCloudWatchClient / loadAWSCreds 辅助函数。
package aws_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/aws"
	"github.com/Havens-blog/e-cloudx-sdk/nasprobe"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/gotomicro/ego/core/elog"
)

// s3Namespace S3 标准 CloudWatch namespace(公认标准指标,BucketSizeBytes 为
// 计费基础口径)。
const s3Namespace = "AWS/S3"

// TestManualProbeAWSS3BucketMetrics CloudWatch AWS/S3 指标探测 + 非零验证。
func TestManualProbeAWSS3BucketMetrics(t *testing.T) {
	ak, sk, region := loadAWSCreds(t)
	if ak == "" {
		t.Skip("未设置 NAS_PROBE_AWS_AK/SK/REGION 或 NAS_PROBE_MONGODB_DSN,跳过(无 env 不跑)")
	}
	t.Logf("探测凭证: AK=%s region=%s", nasprobe.MaskAK(ak), region)

	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()

	// 1) 只读枚举实盘 S3 bucket(ListBuckets 账号全局;逐桶补齐 region 与统计快照)
	adapter := aws.NewS3Adapter(ak, sk, region, elog.DefaultLogger)
	buckets, err := adapter.ListBuckets(ctx, "")
	if err != nil {
		t.Fatalf("枚举 S3 bucket 失败(账号凭证或网络问题): %v", err)
	}
	t.Logf("实盘 S3 bucket 数: %d", len(buckets))
	if len(buckets) == 0 {
		t.Skip("账号无 S3 bucket,非零验证无从进行(记录:枚举为空)")
	}
	for _, b := range buckets {
		t.Logf("bucket: %s region=%s storage=%d byte(%.4f GB) objects=%d",
			b.BucketName, b.Region, b.StorageSize, nasprobe.BytesToGB(float64(b.StorageSize)), b.ObjectCount)
	}

	// 2) 逐 bucket:ListMetrics 注册确认 + GetMetricData 90 天非零验证
	start := time.Now().Add(-90 * 24 * time.Hour)
	end := time.Now()
	growthStart := time.Now().Add(-30 * 24 * time.Hour)
	registered, unregistered := 0, 0
	nonZero, zeroAnnotated := 0, 0
	highGrowth := 0
	for _, b := range buckets {
		cwClient, err := newCloudWatchClient(ctx, ak, sk, b.Region)
		if err != nil {
			t.Logf("[FAIL] bucket=%s 创建 CloudWatch 客户端 err=%v", b.BucketName, err)
			continue
		}
		series := listS3Series(ctx, t, cwClient, b.BucketName)
		sizeDim := pickS3StorageType(series, "BucketSizeBytes")
		objDim := pickS3StorageType(series, "NumberOfObjects")
		if sizeDim == "" && objDim == "" {
			unregistered++
			t.Logf("[注册确认][FAIL] bucket=%s ListMetrics 无 BucketSizeBytes/NumberOfObjects 序列(指标未注册或 15 个月无上报被清理)", b.BucketName)
			continue
		}
		registered++

		// BucketSizeBytes 非零验证(90 天窗口,天粒度 Average)
		if sizeDim != "" {
			vals := getS3MetricValues(ctx, t, cwClient, "BucketSizeBytes", b.BucketName, sizeDim, start, end)
			if len(vals) == 0 {
				zeroAnnotated++
				t.Logf("[非零验证][零值注记] bucket=%s BucketSizeBytes(StorageType=%s) 90 天 0 数据点 —— 未通过+原因:闲置 bucket(序列已注册但无上报;storage metrics 仅每日一次、有数据才上报)",
					b.BucketName, sizeDim)
			} else {
				latest := vals[len(vals)-1]
				nonZero++
				t.Logf("[非零验证][PASS] bucket=%s BucketSizeBytes(StorageType=%s) 数据点=%d 最新=%.4g byte = %.4f GB 数量级=%s",
					b.BucketName, sizeDim, len(vals), latest,
					nasprobe.BytesToGB(latest), nasprobe.DescribeMagnitude(nasprobe.BytesToGB(latest)))
				// 30 天增速(高增长近失证据)
				gvals := getS3MetricValues(ctx, t, cwClient, "BucketSizeBytes", b.BucketName, sizeDim, growthStart, end)
				if len(gvals) >= 2 {
					growth, ok := nasprobe.OSSGrowthPercent(gvals[0], gvals[len(gvals)-1])
					if nasprobe.IsHighGrowth(growth, ok) {
						highGrowth++
					}
					t.Logf("[增速] bucket=%s 30天首点=%.4g 末点=%.4g 增速=%.2f%%(ok=%v) 高增长=%v",
						b.BucketName, gvals[0], gvals[len(gvals)-1], growth, ok, nasprobe.IsHighGrowth(growth, ok))
				}
			}
		}
		// NumberOfObjects 存在性验证(零值=0 个对象属合法,只记录不计 FAIL)
		if objDim != "" {
			vals := getS3MetricValues(ctx, t, cwClient, "NumberOfObjects", b.BucketName, objDim, start, end)
			if len(vals) == 0 {
				t.Logf("[对象数][零值注记] bucket=%s NumberOfObjects(StorageType=%s) 90 天 0 数据点(闲置)", b.BucketName, objDim)
			} else {
				t.Logf("[对象数][OK] bucket=%s NumberOfObjects 最新=%.0f 个(StorageType=%s)",
					b.BucketName, vals[len(vals)-1], objDim)
			}
		}
	}

	t.Logf("===== AWS 探测汇总:序列注册 bucket=%d 未注册=%d;BucketSizeBytes 非零=%d 零值注记=%d;高增长 bucket=%d =====",
		registered, unregistered, nonZero, zeroAnnotated, highGrowth)
}

// listS3Series ListMetrics 发现该 bucket 在 AWS/S3 下的全部指标序列
// (指标名 → 出现过的 StorageType 集合)。
func listS3Series(ctx context.Context, t *testing.T, cwClient *cloudwatch.Client, bucket string) map[string]map[string]bool {
	series := map[string]map[string]bool{}
	var nextToken *string
	for page := 0; page < 20; page++ {
		out, err := cwClient.ListMetrics(ctx, &cloudwatch.ListMetricsInput{
			Namespace:  awssdk.String(s3Namespace),
			Dimensions: []types.DimensionFilter{{Name: awssdk.String("BucketName"), Value: awssdk.String(bucket)}},
			NextToken:  nextToken,
		})
		if err != nil {
			t.Logf("[注册确认] bucket=%s ListMetrics err=%v", bucket, err)
			return series
		}
		for _, m := range out.Metrics {
			if m.MetricName == nil {
				continue
			}
			st := ""
			for _, d := range m.Dimensions {
				if d.Name != nil && *d.Name == "StorageType" && d.Value != nil {
					st = *d.Value
				}
			}
			if series[*m.MetricName] == nil {
				series[*m.MetricName] = map[string]bool{}
			}
			series[*m.MetricName][st] = true
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}
	names := make([]string, 0, len(series))
	for name := range series {
		names = append(names, name)
	}
	sort.Strings(names)
	t.Logf("[注册确认] bucket=%s AWS/S3 序列: %v", bucket, names)
	return series
}

// pickS3StorageType 为指定指标选查询用 StorageType 维度值:
// BucketSizeBytes 优先 StandardStorage(标准存储计费口径),其余取首个;
// NumberOfObjects 恒为 AllStorageTypes。序列不存在返回 ""。
func pickS3StorageType(series map[string]map[string]bool, metricName string) string {
	sts := series[metricName]
	if len(sts) == 0 {
		return ""
	}
	if sts["StandardStorage"] {
		return "StandardStorage"
	}
	if sts["AllStorageTypes"] {
		return "AllStorageTypes"
	}
	for st := range sts {
		if st != "" {
			return st
		}
	}
	return ""
}

// getS3MetricValues GetMetricData 查询单 bucket 单指标 90 天天粒度 Average 序列
// (按时间升序返回原始值)。
func getS3MetricValues(ctx context.Context, t *testing.T, cwClient *cloudwatch.Client, metricName, bucket, storageType string, start, end time.Time) []float64 {
	out, err := cwClient.GetMetricData(ctx, &cloudwatch.GetMetricDataInput{
		StartTime: &start,
		EndTime:   &end,
		MetricDataQueries: []types.MetricDataQuery{{
			Id: awssdk.String("m"),
			MetricStat: &types.MetricStat{
				Metric: &types.Metric{
					Namespace:  awssdk.String(s3Namespace),
					MetricName: awssdk.String(metricName),
					Dimensions: []types.Dimension{
						{Name: awssdk.String("BucketName"), Value: awssdk.String(bucket)},
						{Name: awssdk.String("StorageType"), Value: awssdk.String(storageType)},
					},
				},
				Period: awssdk.Int32(86400),
				Stat:   awssdk.String("Average"),
			},
		}},
	})
	if err != nil {
		t.Logf("[查询失败] bucket=%s metric=%s err=%v", bucket, metricName, err)
		return nil
	}
	if len(out.MetricDataResults) == 0 {
		return nil
	}
	return out.MetricDataResults[0].Values // GetMetricData 按时间升序
}
