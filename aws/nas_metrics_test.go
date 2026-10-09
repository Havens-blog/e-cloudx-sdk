package aws

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/gotomicro/ego/core/elog"
)

// stubCWClient CloudWatch 客户端测试桩:记录输入,返回预置结果/错误。
type stubCWClient struct {
	out     *cloudwatch.GetMetricDataOutput
	err     error
	got     []*cloudwatch.GetMetricDataInput
	region_ string // 供工厂闭包断言 region 透传
}

func (s *stubCWClient) GetMetricData(ctx context.Context, params *cloudwatch.GetMetricDataInput, optFns ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error) {
	s.got = append(s.got, params)
	if s.err != nil {
		return nil, s.err
	}
	return s.out, nil
}

func newCWMetricTestAdapter(cwClient cwMetricClient) (*EFSAdapter, *[]string) {
	regions := &[]string{}
	adapter := NewEFSAdapter("ak", "sk", "us-east-1", elog.DefaultLogger)
	adapter.cwHooks = &cwMetricHooks{
		cwFactory: func(ctx context.Context, r string) (cwMetricClient, error) {
			*regions = append(*regions, r)
			return cwClient, nil
		},
	}
	return adapter, regions
}

// cwResult 构造 GetMetricData 单序列返回。
func cwResult(ts []time.Time, vals []float64) *cloudwatch.GetMetricDataOutput {
	return &cloudwatch.GetMetricDataOutput{
		MetricDataResults: []cwtypes.MetricDataResult{{
			Id:         aws.String("storage_bytes"),
			Timestamps: ts,
			Values:     vals,
		}},
	}
}

// cwPoint 构造 UTC 时间戳(对应运营时区 Asia/Shanghai 的自然日)。
func cwPoint(year, month, day, hourUTC int, val float64) (time.Time, float64) {
	return time.Date(year, time.Month(month), day, hourUTC, 0, 0, 0, time.UTC), val
}

// TestAggregateCWMetricDailyLastPerDay 同日多点取最后(日末态),跨时区日切正确。
func TestAggregateCWMetricDailyLastPerDay(t *testing.T) {
	ts1, v1 := cwPoint(2026, 9, 18, 16, 100) // 2026-09-19 00:00 CST
	ts2, v2 := cwPoint(2026, 9, 18, 17, 200) // 同日较晚点
	ts3, v3 := cwPoint(2026, 9, 19, 16, 300) // 09-20 CST
	got := aggregateCWMetricDaily([]time.Time{ts1, ts2, ts3}, []float64{v1, v2, v3})
	if got["2026-09-19"] != 200 || got["2026-09-20"] != 300 {
		t.Fatalf("aggregate = %v, want 09-19:200 09-20:300", got)
	}
	if got := aggregateCWMetricDaily([]time.Time{ts1}, []float64{}); got != nil {
		t.Fatalf("时间戳/值长度不一致应返回 nil, got %v", got)
	}
}

// TestBuildEFSMetricsConversion 采集边界字节 → GB 换算(AC-3);capacity 恒 0
// (EFS 弹性容量无总容量指标,写路径 zero_exception);缺失日跳过。
func TestBuildEFSMetricsConversion(t *testing.T) {
	usedDaily := map[string]float64{
		"2026-09-18": 107374182400, // 100 GB
		"2026-09-19": 53687091200,  // 50 GB
	}
	got := buildEFSMetrics("fs-0aa52000", "nfs-prod", []string{"2026-09-18", "2026-09-19", "2026-09-20"}, usedDaily)
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2(缺失日 09-20 跳过)", len(got))
	}
	if got[0].UsedCapacity != 100 || got[1].UsedCapacity != 50 {
		t.Fatalf("UsedCapacity = %v/%v, want 100/50 GB(StorageBytes 字节→GB)", got[0].UsedCapacity, got[1].UsedCapacity)
	}
	for _, m := range got {
		if m.Capacity != 0 {
			t.Fatalf("Capacity = %v, want 0(EFS 无总容量指标)", m.Capacity)
		}
		if m.Provider != "aws" || m.FsID != "fs-0aa52000" {
			t.Fatalf("行元数据不符: %+v", m)
		}
		var _ types.NASMetric = m
	}
}

// TestGetNASMetricsQueryShape 查询形状(M1 定案):AWS/EFS StorageBytes、
// 维度 FileSystemId、period 86400、stat Average。
func TestGetNASMetricsQueryShape(t *testing.T) {
	ts1, v1 := cwPoint(2026, 9, 18, 16, 107374182400) // CST 2026-09-19? -> 09-19
	stub := &stubCWClient{out: cwResult([]time.Time{ts1}, []float64{v1})}
	adapter, _ := newCWMetricTestAdapter(stub)

	// 日期范围取 09-18,数据点 09-19 超出 → 空;查询形状仍可断言
	metrics, err := adapter.GetNASMetrics(context.Background(), "fs-1", "n", "eu-central-1", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("GetNASMetrics err = %v", err)
	}
	if len(metrics) != 0 {
		t.Fatalf("区间外数据点不应落行, got %+v", metrics)
	}
	if len(stub.got) != 1 {
		t.Fatalf("请求次数 = %d, want 1", len(stub.got))
	}
	q := stub.got[0].MetricDataQueries[0]
	stat := q.MetricStat
	if *stat.Metric.Namespace != "AWS/EFS" || *stat.Metric.MetricName != "StorageBytes" {
		t.Fatalf("namespace/metric = %s/%s, want AWS/EFS StorageBytes(M1 定案)", *stat.Metric.Namespace, *stat.Metric.MetricName)
	}
	if len(stat.Metric.Dimensions) != 1 || *stat.Metric.Dimensions[0].Name != "FileSystemId" || *stat.Metric.Dimensions[0].Value != "fs-1" {
		t.Fatalf("维度 = %+v, want FileSystemId=fs-1", stat.Metric.Dimensions)
	}
	if *stat.Period != 86400 || *stat.Stat != "Average" {
		t.Fatalf("period/stat = %d/%s, want 86400/Average", *stat.Period, *stat.Stat)
	}
}

// TestGetNASMetricsAWSEndToEnd 字节→GB 全链路(数据点落在查询区间内)。
func TestGetNASMetricsAWSEndToEnd(t *testing.T) {
	// 2026-09-18 16:00 UTC = 2026-09-19 00:00 CST(恰好是 09-19 的首毫秒,
	// 仍属 09-19 日桶);查询区间取 09-18~09-19 覆盖
	ts1, _ := cwPoint(2026, 9, 18, 16, 107374182400)
	stub := &stubCWClient{out: cwResult([]time.Time{ts1}, []float64{107374182400})}
	adapter, _ := newCWMetricTestAdapter(stub)

	metrics, err := adapter.GetNASMetrics(context.Background(), "fs-1", "n", "eu-central-1", "2026-09-18", "2026-09-19")
	if err != nil {
		t.Fatalf("GetNASMetrics err = %v", err)
	}
	if len(metrics) != 1 {
		t.Fatalf("rows = %d, want 1", len(metrics))
	}
	if metrics[0].Date != "2026-09-19" || metrics[0].UsedCapacity != 100 {
		t.Fatalf("row = %+v, want 2026-09-19 used=100GB", metrics[0])
	}
}

// TestGetNASMetricsAWSRegionPassthrough 按实例 region 查询(AC-4):
// 实盘 region eu-central-1/us-east-1 与账号配置(eu-west-1)不一致,
// 必须用实例真实 region(M1 遗留行动 #1 的前置正确性)。
func TestGetNASMetricsAWSRegionPassthrough(t *testing.T) {
	stub := &stubCWClient{out: &cloudwatch.GetMetricDataOutput{}}
	adapter, regions := newCWMetricTestAdapter(stub)
	if _, err := adapter.GetNASMetrics(context.Background(), "fs-1", "n", "eu-central-1", "2026-09-18", "2026-09-18"); err != nil {
		t.Fatalf("GetNASMetrics err = %v", err)
	}
	if len(*regions) != 1 || (*regions)[0] != "eu-central-1" {
		t.Fatalf("客户端 region = %v, want [eu-central-1](实例真实 region)", *regions)
	}
}

// TestGetNASMetricsAWSFailure 调用失败路径(AC-5):返回 error + 空结果。
// 显式区别于 CDN 的「主动放弃」(cdn_metrics.go 返回空切片+nil error):
// NAS 必达路径失败必须可观测。
func TestGetNASMetricsAWSFailure(t *testing.T) {
	stub := &stubCWClient{err: errors.New("AccessDenied: not authorized")}
	adapter, _ := newCWMetricTestAdapter(stub)
	metrics, err := adapter.GetNASMetrics(context.Background(), "fs-1", "n", "eu-central-1", "2026-09-18", "2026-09-18")
	if err == nil {
		t.Fatal("调用失败应返回 error(失败可观测性,非 CloudFront 主动放弃)")
	}
	if metrics != nil {
		t.Fatalf("调用失败应返回空结果, got %+v", metrics)
	}
}

// TestGetNASMetricsAWSNoDatapoints 真实无数据点返回空切片 + nil error
// (实盘 EFS 闲置场景,probe-report §1.3:序列注册但 90 天无上报)。
func TestGetNASMetricsAWSNoDatapoints(t *testing.T) {
	stub := &stubCWClient{out: &cloudwatch.GetMetricDataOutput{MetricDataResults: []cwtypes.MetricDataResult{
		{Id: aws.String("storage_bytes")}, // 无 Timestamps/Values
	}}}
	adapter, _ := newCWMetricTestAdapter(stub)
	metrics, err := adapter.GetNASMetrics(context.Background(), "fs-1", "n", "eu-central-1", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("无数据点 err = %v, want nil", err)
	}
	if metrics == nil || len(metrics) != 0 {
		t.Fatalf("无数据点 = %+v, want 空切片", metrics)
	}
}

// TestGetNASMetricsAWSParamValidation 入参校验。
func TestGetNASMetricsAWSParamValidation(t *testing.T) {
	adapter, _ := newCWMetricTestAdapter(&stubCWClient{})
	if _, err := adapter.GetNASMetrics(context.Background(), "", "n", "eu-central-1", "2026-09-18", "2026-09-18"); err == nil {
		t.Fatal("fsID 为空应报错")
	}
	if _, err := adapter.GetNASMetrics(context.Background(), "fs-1", "n", "", "2026-09-18", "2026-09-18"); err == nil {
		t.Fatal("region 为空应报错(按实例 region 查询)")
	}
	if _, err := adapter.GetNASMetrics(context.Background(), "fs-1", "n", "eu-central-1", "2026-09-19", "2026-09-18"); err == nil {
		t.Fatal("startDate 晚于 endDate 应报错")
	}
}
