package aws

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/gotomicro/ego/core/elog"
)

// S3 指标适配器单测(oss-ops-insight T3)。
// 规格锚:probe-report §1.3(M1 实测定案:AWS/S3 BucketSizeBytes(byte,
// StandardStorage 标准存储口径)+ NumberOfObjects(个,AllStorageTypes),
// 维度 BucketName+StorageType,天粒度每日上报)。
// 与 CloudFront「主动放弃」先例不同(aws/cdn_metrics.go):S3 有标准指标,
// 本路径为真实查询,调用失败返回 error(AC-3)。

// newS3MetricTestAdapter 构造带测试钩子的 S3Adapter(bucket region 解析与
// CloudWatch 工厂均可注入,并记录工厂收到的 region)。
func newS3MetricTestAdapter(cwClient cwMetricClient, bucketRegion string) (*S3Adapter, *stubCWClient, *[]string) {
	stub, _ := cwClient.(*stubCWClient)
	regions := &[]string{}
	adapter := NewS3Adapter("ak", "sk", "us-east-1", elog.DefaultLogger)
	adapter.ossMetricHooks = &ossMetricHooks{
		bucketRegion: func(ctx context.Context, bucketName string) (string, error) {
			return bucketRegion, nil
		},
		cwFactory: func(ctx context.Context, r string) (cwMetricClient, error) {
			*regions = append(*regions, r)
			return cwClient, nil
		},
	}
	return adapter, stub, regions
}

// ossCWResult 构造 GetMetricData 双序列返回(容量 + 对象数,按 Id 区分)。
func ossCWResult(sizeTs []time.Time, sizeVals []float64, objTs []time.Time, objVals []float64) *cloudwatch.GetMetricDataOutput {
	return &cloudwatch.GetMetricDataOutput{
		MetricDataResults: []cwtypes.MetricDataResult{
			{Id: awssdk.String("bucket_size_bytes"), Timestamps: sizeTs, Values: sizeVals},
			{Id: awssdk.String("number_of_objects"), Timestamps: objTs, Values: objVals},
		},
	}
}

// TestGetOSSMetricsSuccess 双指标查询 + 采集边界字节 → GB 换算(AC-3/AC-5);
// 对象数无数据日为 0(与容量独立上报);容量缺失日跳过不落库。
func TestGetOSSMetricsSuccess(t *testing.T) {
	ts1, _ := cwPoint(2026, 9, 18, 16, 0) // 2026-09-19 00:00 CST
	stub := &stubCWClient{out: ossCWResult([]time.Time{ts1}, []float64{1073741824}, []time.Time{ts1}, []float64{2124371})}
	adapter, _, _ := newS3MetricTestAdapter(stub, "eu-west-1")

	metrics, err := adapter.GetOSSMetrics(context.Background(), "aws-jlc-prod-db-backup", "2026-09-19", "2026-09-19")
	if err != nil {
		t.Fatalf("GetOSSMetrics err = %v, want nil", err)
	}
	if len(metrics) != 1 {
		t.Fatalf("rows = %d(%+v), want 1", len(metrics), metrics)
	}
	m := metrics[0]
	if m.StorageSize != 1 {
		t.Fatalf("StorageSize = %v, want 1(1073741824 byte = 1 GiB)", m.StorageSize)
	}
	if m.ObjectCount != 2124371 {
		t.Fatalf("ObjectCount = %d, want 2124371", m.ObjectCount)
	}
	if m.BucketName != "aws-jlc-prod-db-backup" || m.Date != "2026-09-19" || m.Provider != "aws" {
		t.Fatalf("行元数据不符: %+v", m)
	}
	var _ types.OSSMetric = m

	// 请求断言:双指标 + 维度 BucketName/StorageType 口径(probe-report §1.3)
	if len(stub.got) != 1 {
		t.Fatalf("请求数 = %d, want 1", len(stub.got))
	}
	queries := stub.got[0].MetricDataQueries
	if len(queries) != 2 {
		t.Fatalf("查询数 = %d, want 2(BucketSizeBytes+NumberOfObjects)", len(queries))
	}
	byID := map[string]cwtypes.MetricDataQuery{}
	for _, q := range queries {
		byID[awssdk.ToString(q.Id)] = q
	}
	sizeQ, ok := byID["bucket_size_bytes"]
	if !ok {
		t.Fatal("缺少 bucket_size_bytes 查询")
	}
	objQ, ok := byID["number_of_objects"]
	if !ok {
		t.Fatal("缺少 number_of_objects 查询")
	}
	sizeMetric := sizeQ.MetricStat.Metric
	if awssdk.ToString(sizeMetric.Namespace) != "AWS/S3" || awssdk.ToString(sizeMetric.MetricName) != "BucketSizeBytes" {
		t.Fatalf("容量指标 = %s/%s, want AWS/S3/BucketSizeBytes", awssdk.ToString(sizeMetric.Namespace), awssdk.ToString(sizeMetric.MetricName))
	}
	objMetric := objQ.MetricStat.Metric
	if awssdk.ToString(objMetric.MetricName) != "NumberOfObjects" {
		t.Fatalf("对象数指标 = %s, want NumberOfObjects", awssdk.ToString(objMetric.MetricName))
	}
	dimsOf := func(q cwtypes.MetricDataQuery) map[string]string {
		dims := map[string]string{}
		for _, d := range q.MetricStat.Metric.Dimensions {
			dims[awssdk.ToString(d.Name)] = awssdk.ToString(d.Value)
		}
		return dims
	}
	sizeDims := dimsOf(sizeQ)
	if sizeDims["BucketName"] != "aws-jlc-prod-db-backup" || sizeDims["StorageType"] != "StandardStorage" {
		t.Fatalf("容量维度 = %v, want BucketName=<bucket>/StorageType=StandardStorage", sizeDims)
	}
	objDims := dimsOf(objQ)
	if objDims["StorageType"] != "AllStorageTypes" {
		t.Fatalf("对象数 StorageType = %s, want AllStorageTypes", objDims["StorageType"])
	}
	if awssdk.ToInt32(sizeQ.MetricStat.Period) != 86400 {
		t.Fatalf("Period = %d, want 86400(天粒度)", awssdk.ToInt32(sizeQ.MetricStat.Period))
	}
}

// TestGetOSSMetricsRegionPassthrough CloudWatch 按 bucket 真实 region 创建
// (透传断言:eu-west-1 ≠ defaultRegion us-east-1,不做全局推断,AC-1 同型)。
func TestGetOSSMetricsRegionPassthrough(t *testing.T) {
	ts1, _ := cwPoint(2026, 9, 18, 16, 0)
	stub := &stubCWClient{out: ossCWResult([]time.Time{ts1}, []float64{1024}, nil, nil)}
	adapter, _, regions := newS3MetricTestAdapter(stub, "eu-west-1")
	if _, err := adapter.GetOSSMetrics(context.Background(), "bkt", "2026-09-19", "2026-09-19"); err != nil {
		t.Fatalf("GetOSSMetrics err = %v", err)
	}
	if len(*regions) != 1 || (*regions)[0] != "eu-west-1" {
		t.Fatalf("客户端 region = %v, want [eu-west-1](bucket 真实 region)", *regions)
	}
}

// TestGetOSSMetricsRegionResolveFailure bucket region 解析失败走调用失败路径
// (返回 error,不静默退回 defaultRegion 查错地域)。
func TestGetOSSMetricsRegionResolveFailure(t *testing.T) {
	adapter := NewS3Adapter("ak", "sk", "us-east-1", elog.DefaultLogger)
	adapter.ossMetricHooks = &ossMetricHooks{
		bucketRegion: func(ctx context.Context, bucketName string) (string, error) {
			return "", errors.New("GetBucketLocation denied")
		},
	}
	metrics, err := adapter.GetOSSMetrics(context.Background(), "bkt", "2026-09-19", "2026-09-19")
	if err == nil {
		t.Fatal("region 解析失败应返回 error")
	}
	if metrics != nil {
		t.Fatalf("region 解析失败应返回空结果, got %+v", metrics)
	}
}

// TestGetOSSMetricsAPIFailure 调用失败路径(AC-6 三分之一):GetMetricData 失败
// 返回 error(不套用 CloudFront 主动放弃先例,AC-3)。
func TestGetOSSMetricsAPIFailure(t *testing.T) {
	stub := &stubCWClient{err: errors.New("AccessDenied")}
	adapter, _, _ := newS3MetricTestAdapter(stub, "eu-west-1")
	metrics, err := adapter.GetOSSMetrics(context.Background(), "bkt", "2026-09-19", "2026-09-19")
	if err == nil {
		t.Fatal("API 失败应返回 error(失败可观测性), got nil")
	}
	if metrics != nil {
		t.Fatalf("API 失败应返回空结果, got %+v", metrics)
	}
}

// TestGetOSSMetricsNoDatapoints 真实无数据路径(AC-6 三分之一):未注册/闲置
// bucket 无数据点返回空切片 + nil error(probe-report §1.3:20 个未注册 bucket
// 上线初期走零值例外打标呈现,非采集失效)。
func TestGetOSSMetricsNoDatapoints(t *testing.T) {
	stub := &stubCWClient{out: &cloudwatch.GetMetricDataOutput{}}
	adapter, _, _ := newS3MetricTestAdapter(stub, "eu-west-1")
	metrics, err := adapter.GetOSSMetrics(context.Background(), "bkt", "2026-09-19", "2026-09-19")
	if err != nil {
		t.Fatalf("无数据点 err = %v, want nil", err)
	}
	if metrics == nil || len(metrics) != 0 {
		t.Fatalf("无数据点 = %+v, want 空切片", metrics)
	}
}

// TestGetOSSMetricsParamValidation 入参校验:bucketName 必填。
func TestGetOSSMetricsParamValidation(t *testing.T) {
	adapter, _, _ := newS3MetricTestAdapter(&stubCWClient{}, "eu-west-1")
	if _, err := adapter.GetOSSMetrics(context.Background(), "", "2026-09-19", "2026-09-19"); err == nil {
		t.Fatal("bucketName 为空应报错")
	}
}

// TestBuildS3MetricsMissingDaySkip 容量缺失日跳过不落库;对象数无数据日为 0。
func TestBuildS3MetricsMissingDaySkip(t *testing.T) {
	got := buildS3Metrics("bkt", []string{"2026-09-18", "2026-09-19"}, map[string]float64{"2026-09-18": 1073741824}, map[string]float64{})
	if len(got) != 1 {
		t.Fatalf("rows = %d(%+v), want 1(缺失日 09-19 跳过)", len(got), got)
	}
	if got[0].StorageSize != 1 || got[0].ObjectCount != 0 {
		t.Fatalf("row = %+v, want StorageSize=1 ObjectCount=0", got[0])
	}
}
