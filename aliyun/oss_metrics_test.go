package aliyun

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/cms"
	"github.com/gotomicro/ego/core/elog"
)

// OSS 指标适配器单测(oss-ops-insight T3)。
// 复用 NAS 指标单测的 stubCMSClient/stubResp(nas_metrics_test.go,同包)。
// 规格锚:probe-report §1.1(M1 实测定案:namespace=acs_oss_dashboard、
// MeteringStorageUtilization(byte)/ObjectCount(个)、维度 BucketName、
// Period=3600、窗口 ≤31 天)。

// newOSSMetricTestAdapter 构造带测试钩子的 OSSAdapter(记录工厂收到的 region)。
func newOSSMetricTestAdapter(cmsClient cmsMetricClient) (*OSSAdapter, *stubCMSClient, *[]string) {
	stub, _ := cmsClient.(*stubCMSClient)
	regions := &[]string{}
	adapter := NewOSSAdapter("ak", "sk", "cn-hangzhou", elog.DefaultLogger)
	adapter.ossMetricHooks = &ossMetricHooks{
		cmsFactory: func(region string) (cmsMetricClient, error) {
			*regions = append(*regions, region)
			return cmsClient, nil
		},
	}
	return adapter, stub, regions
}

// TestBuildAliyunOSSMetricsConversion 采集边界字节 → GB 换算正确性(AC-1/AC-5):
// 以 probe-report §1.1 非零锚点为参照(1.155×10^12 byte ≈ 1076.2 GB 数量级);
// 对象数无数据日为 0(与容量独立上报);used 缺失日跳过不落库。
func TestBuildAliyunOSSMetricsConversion(t *testing.T) {
	storageDaily := map[string]float64{
		"2026-09-18": 1073741824, // 恰好 1 GiB → 1 GB
		"2026-09-19": 1155000000000,
	}
	objectDaily := map[string]float64{
		"2026-09-18": 2124371,
	}
	got := buildAliyunOSSMetrics("jlc-prod", []string{"2026-09-18", "2026-09-19", "2026-09-20"}, storageDaily, objectDaily)
	if len(got) != 2 {
		t.Fatalf("rows = %d(%+v), want 2(storage 缺失日 09-20 跳过)", len(got), got)
	}
	first := got[0]
	if first.StorageSize != 1 {
		t.Fatalf("StorageSize = %v, want 1(1073741824 byte = 1 GiB)", first.StorageSize)
	}
	if first.ObjectCount != 2124371 {
		t.Fatalf("ObjectCount = %d, want 2124371", first.ObjectCount)
	}
	// 对象数无数据日:ObjectCount=0(非调用失败,不阻塞容量行)
	second := got[1]
	if second.ObjectCount != 0 {
		t.Fatalf("ObjectCount = %d, want 0(对象数无数据)", second.ObjectCount)
	}
	if want := 1155000000000.0 / (1024 * 1024 * 1024); second.StorageSize != want {
		t.Fatalf("StorageSize = %v, want %v(字节→GB)", second.StorageSize, want)
	}
	if first.BucketName != "jlc-prod" || first.Date != "2026-09-18" || first.Provider != "aliyun" {
		t.Fatalf("行元数据不符: %+v", first)
	}
	if second.Date != "2026-09-19" {
		t.Fatalf("second.Date = %s, want 2026-09-19", second.Date)
	}
	var _ types.OSSMetric = first
}

// TestGetOSSMetricsQueriesDashboardNamespace 定案路径:namespace=acs_oss_dashboard、
// 计量类 Period=3600、维度 {"BucketName":...}(旧版 bucket 键不适用,probe-report §1.1),
// 先容量后对象数两指标,透传 bucket 真实名称。
func TestGetOSSMetricsQueriesDashboardNamespace(t *testing.T) {
	stub := &stubCMSClient{respByMetric: map[string]*cms.DescribeMetricListResponse{
		ossMetricStorageUtilization: stubResp(`[{"timestamp":1789660800000,"Value":1073741824}]`),
		ossMetricObjectCount:        stubResp(`[{"timestamp":1789660800000,"Value":2124371}]`),
	}}
	adapter, _, _ := newOSSMetricTestAdapter(stub)

	metrics, err := adapter.GetOSSMetrics(context.Background(), "jlc-prod-forface-public", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("GetOSSMetrics err = %v, want nil", err)
	}
	if len(metrics) != 1 || metrics[0].StorageSize != 1 || metrics[0].ObjectCount != 2124371 {
		t.Fatalf("metrics = %+v, want 1 行 StorageSize=1GB ObjectCount=2124371", metrics)
	}
	if len(stub.gotReqs) != 2 {
		t.Fatalf("请求数 = %d, want 2(容量+对象数)", len(stub.gotReqs))
	}
	first := stub.gotReqs[0]
	if first.Namespace != "acs_oss_dashboard" {
		t.Fatalf("Namespace = %s, want acs_oss_dashboard(M1 实测定案,probe-report §1.1)", first.Namespace)
	}
	if first.MetricName != ossMetricStorageUtilization || stub.gotReqs[1].MetricName != ossMetricObjectCount {
		t.Fatalf("查询序列 = %s,%s, want %s,%s", first.MetricName, stub.gotReqs[1].MetricName, ossMetricStorageUtilization, ossMetricObjectCount)
	}
	if first.Period != "3600" {
		t.Fatalf("Period = %s, want 3600(计量类指标)", first.Period)
	}
	wantDim := `{"BucketName":"jlc-prod-forface-public"}`
	if first.Dimensions != wantDim {
		t.Fatalf("Dimensions = %s, want %s(维度键 BucketName,透传真实 bucket)", first.Dimensions, wantDim)
	}
}

// TestGetOSSMetricsRegionPassthrough CMS 客户端按适配器账号 region 创建
// (OSS 全局服务,CMS 指标查询与 bucket 所在 region 无关,probe-report §1.1)。
func TestGetOSSMetricsRegionPassthrough(t *testing.T) {
	stub := &stubCMSClient{respByMetric: map[string]*cms.DescribeMetricListResponse{
		ossMetricStorageUtilization: stubResp(`[{"timestamp":1789660800000,"Value":1024}]`),
	}}
	adapter, _, regions := newOSSMetricTestAdapter(stub)
	if _, err := adapter.GetOSSMetrics(context.Background(), "bkt", "2026-09-18", "2026-09-18"); err != nil {
		t.Fatalf("GetOSSMetrics err = %v", err)
	}
	if len(*regions) != 1 || (*regions)[0] != "cn-hangzhou" {
		t.Fatalf("客户端 region = %v, want [cn-hangzhou](账号 defaultRegion)", *regions)
	}
}

// TestGetOSSMetricsAPIFailure 调用失败路径(AC-6 三分之一):返回 error + 空结果
// (适配器打 ERROR + error 字段,执行器记失败计数,不阻塞全流程)。
func TestGetOSSMetricsAPIFailure(t *testing.T) {
	stub := &stubCMSClient{err: errors.New("[400-10002] the metric of project is not exist")}
	adapter, _, _ := newOSSMetricTestAdapter(stub)

	metrics, err := adapter.GetOSSMetrics(context.Background(), "bkt", "2026-09-18", "2026-09-18")
	if err == nil {
		t.Fatal("API 失败应返回 error(失败可观测性), got nil")
	}
	if metrics != nil {
		t.Fatalf("API 失败应返回空结果, got %+v", metrics)
	}
}

// TestGetOSSMetricsNoDatapoints 真实无数据路径(AC-6 三分之一):bucket 无上报
// 返回空切片 + nil error(非调用失败,不触发失败计数)。
func TestGetOSSMetricsNoDatapoints(t *testing.T) {
	stub := &stubCMSClient{respByMetric: map[string]*cms.DescribeMetricListResponse{}}
	adapter, _, _ := newOSSMetricTestAdapter(stub)
	metrics, err := adapter.GetOSSMetrics(context.Background(), "bkt", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("无数据点 err = %v, want nil", err)
	}
	if metrics == nil || len(metrics) != 0 {
		t.Fatalf("无数据点 = %+v, want 空切片", metrics)
	}
}

// TestGetOSSMetricsParamValidation 入参校验:bucketName 必填。
func TestGetOSSMetricsParamValidation(t *testing.T) {
	adapter, _, _ := newOSSMetricTestAdapter(&stubCMSClient{})
	if _, err := adapter.GetOSSMetrics(context.Background(), "", "2026-09-18", "2026-09-18"); err == nil {
		t.Fatal("bucketName 为空应报错")
	}
}

// TestGetOSSMetricsWindowLimit 计量类指标只保留最近 31 天(probe-report §1.1
// 窗口限制):超窗显式报错,不做静默空查询。
func TestGetOSSMetricsWindowLimit(t *testing.T) {
	adapter, _, _ := newOSSMetricTestAdapter(&stubCMSClient{})
	_, err := adapter.GetOSSMetrics(context.Background(), "bkt", "2026-08-01", "2026-09-18")
	if err == nil {
		t.Fatal("超过 31 天窗口应报错")
	}
	if !strings.Contains(err.Error(), "31") {
		t.Fatalf("错误应提示 31 天窗口, got %v", err)
	}
}

// TestGetOSSMetricsCtxCancelled 上下文取消走失败路径(不发起查询)。
func TestGetOSSMetricsCtxCancelled(t *testing.T) {
	adapter, _, _ := newOSSMetricTestAdapter(&stubCMSClient{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	metrics, err := adapter.GetOSSMetrics(ctx, "bkt", "2026-09-18", "2026-09-18")
	if err == nil {
		t.Fatal("ctx 已取消应返回 error")
	}
	if metrics != nil {
		t.Fatalf("ctx 已取消应返回空结果, got %+v", metrics)
	}
}

// TestCreateOSSCMSClientRealWithoutHooks 无钩子时走真实客户端构造(离线:仅
// 构建 CMS 客户端对象,不发起网络请求),按账号 defaultRegion 缓存。
func TestCreateOSSCMSClientRealWithoutHooks(t *testing.T) {
	adapter := NewOSSAdapter("ak", "sk", "cn-hangzhou", elog.DefaultLogger)
	c1, err := adapter.createOSSCMSClient()
	if err != nil {
		t.Fatalf("createOSSCMSClient err = %v, want nil(离线构造)", err)
	}
	c2, err := adapter.createOSSCMSClient()
	if err != nil || c1 != c2 {
		t.Fatalf("二次创建应命中缓存并一致, err=%v same=%v", err, c1 == c2)
	}
}
