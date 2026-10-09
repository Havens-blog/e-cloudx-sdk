package tencent

import (
	"context"
	"errors"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	monitor "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/monitor/v20180724"
)

// tencent OSS 指标适配器单测(oss-ops-insight T4)。
// 复用 NAS 指标单测的 stubNasMonitorClient/monitorResp(nas_metrics_test.go,同包)。
// 规格锚:probe-report §1.4(M1 实测定案:namespace=QCE/COS、StdStorage(**MB**)/
// StdObjectNumber(个)、维度 bucket 单维、Period=86400)。

// newOSSMetricTestAdapter 构造带测试钩子的 COSAdapter(记录工厂收到的 region)。
func newOSSMetricTestAdapter(monitorClient *stubNasMonitorClient) (*COSAdapter, *stubNasMonitorClient, *[]string) {
	regions := &[]string{}
	adapter := NewCOSAdapter("ak", "sk", "ap-guangzhou", elog.DefaultLogger)
	adapter.ossMetricHooks = &ossMetricHooks{
		monitorFactory: func(region string) (nasMonitorClient, error) {
			*regions = append(*regions, region)
			return monitorClient, nil
		},
	}
	return adapter, monitorClient, regions
}

// TestBuildTencentOSSMetricsConversion 采集边界 MB → GB 换算正确性(AC-1/AC-4):
// probe-report §1.4 非零锚点 StdStorage=307(**单位 MB**)→ 307/1024 GB;
// 禁止把 MB 当 byte 进 BytesToGB(遗留行动 #4,若误用会缩小 1024^2 倍);
// 对象数无数据日为 0(与容量独立上报);storage 缺失日跳过不落库。
func TestBuildTencentOSSMetricsConversion(t *testing.T) {
	storageDaily := map[string]float64{
		"2026-09-18": 307, // probe 非零锚点:测试 bucket 307 MB
		"2026-09-19": 1024,
	}
	objectDaily := map[string]float64{
		"2026-09-18": 42,
	}
	got := buildTencentOSSMetrics("jlc-cos-1", []string{"2026-09-18", "2026-09-19", "2026-09-20"}, storageDaily, objectDaily)
	if len(got) != 2 {
		t.Fatalf("rows = %d(%+v), want 2(storage 缺失日 09-20 跳过)", len(got), got)
	}
	first := got[0]
	if want := 307.0 / 1024; first.StorageSize != want {
		t.Fatalf("StorageSize = %v, want %v(307 MB → GB,types.MBToGB)", first.StorageSize, want)
	}
	// MB 与 byte 口径区分:307 MB 若误当 byte 进 BytesToGB 会得到 307/1024^3(差 1024^2 倍)
	if first.StorageSize == types.BytesToGB(307) {
		t.Fatal("StorageSize 不得等于 BytesToGB(307)(MB 不是 byte,probe-report 遗留行动 #4)")
	}
	if first.ObjectCount != 42 {
		t.Fatalf("ObjectCount = %d, want 42", first.ObjectCount)
	}
	// 1024 MB = 1 GiB
	if got[1].StorageSize != 1 {
		t.Fatalf("StorageSize = %v, want 1(1024 MB = 1 GiB)", got[1].StorageSize)
	}
	// 对象数无数据日:ObjectCount=0(非调用失败,不阻塞容量行)
	if got[1].ObjectCount != 0 {
		t.Fatalf("ObjectCount = %d, want 0(对象数无数据)", got[1].ObjectCount)
	}
	if first.BucketName != "jlc-cos-1" || first.Date != "2026-09-18" || first.Provider != "tencent" {
		t.Fatalf("行元数据不符: %+v", first)
	}
	var _ types.OSSMetric = first
}

// TestGetOSSMetricsQueriesQCECOS 定案路径:namespace=QCE/COS、指标序列
// StdStorage→StdObjectNumber、维度 bucket 单维(M1 定案:单维生效,无需 appid
// 双维,不发起 CAM 调用)、Period=86400、时间窗口 CST 格式。
func TestGetOSSMetricsQueriesQCECOS(t *testing.T) {
	stub := &stubNasMonitorClient{respByMetric: map[string]*monitor.GetMonitorDataResponse{
		cosMetricStdStorage:      monitorResp([]float64{1789660800, 307}),
		cosMetricStdObjectNumber: monitorResp([]float64{1789660800, 42}),
	}}
	adapter, _, _ := newOSSMetricTestAdapter(stub)

	metrics, err := adapter.GetOSSMetrics(context.Background(), "jlc-cos-1", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("GetOSSMetrics err = %v, want nil", err)
	}
	if len(metrics) != 1 || metrics[0].StorageSize != 307.0/1024 || metrics[0].ObjectCount != 42 {
		t.Fatalf("metrics = %+v, want 1 行 StorageSize=307MB→GB ObjectCount=42", metrics)
	}
	if len(stub.gotReqs) != 2 {
		t.Fatalf("请求数 = %d, want 2(容量+对象数)", len(stub.gotReqs))
	}
	req := stub.gotReqs[0]
	if *req.Namespace != cosMonitorNamespace {
		t.Fatalf("Namespace = %s, want %s(M1 实测定案,probe-report §1.4)", *req.Namespace, cosMonitorNamespace)
	}
	if *req.MetricName != cosMetricStdStorage || stub.gotReqs[1].MetricName == nil || *stub.gotReqs[1].MetricName != cosMetricStdObjectNumber {
		t.Fatalf("查询序列 = %s,%v, want %s,%s", *req.MetricName, stub.gotReqs[1].MetricName, cosMetricStdStorage, cosMetricStdObjectNumber)
	}
	if req.Period == nil || *req.Period != nasDailyPeriod {
		t.Fatalf("Period = %v, want %d(天粒度)", req.Period, nasDailyPeriod)
	}
	if len(req.Instances) != 1 || len(req.Instances[0].Dimensions) != 1 {
		t.Fatalf("Instances 须单维 bucket: %+v", req.Instances)
	}
	dim := req.Instances[0].Dimensions[0]
	if *dim.Name != cosDimBucket || *dim.Value != "jlc-cos-1" {
		t.Fatalf("维度 = %s/%s, want bucket/jlc-cos-1(单维,透传真实 bucket)", *dim.Name, *dim.Value)
	}
	if *req.StartTime != "2026-09-18T00:00:00+08:00" || *req.EndTime != "2026-09-19T00:00:00+08:00" {
		t.Fatalf("时间窗 = %s ~ %s, want 2026-09-18T00:00:00+08:00 ~ 2026-09-19T00:00:00+08:00", *req.StartTime, *req.EndTime)
	}
}

// TestGetOSSMetricsRegionPassthrough monitor 客户端按账号 defaultRegion 创建
// (OSSMetricQuerier 无 region 签名,OSS 全局服务,与 aliyun CMS 客户端同型)。
func TestGetOSSMetricsRegionPassthrough(t *testing.T) {
	stub := &stubNasMonitorClient{respByMetric: map[string]*monitor.GetMonitorDataResponse{
		cosMetricStdStorage: monitorResp([]float64{1789660800, 1024}),
	}}
	adapter, _, regions := newOSSMetricTestAdapter(stub)
	if _, err := adapter.GetOSSMetrics(context.Background(), "bkt", "2026-09-18", "2026-09-18"); err != nil {
		t.Fatalf("GetOSSMetrics err = %v", err)
	}
	if len(*regions) != 1 || (*regions)[0] != "ap-guangzhou" {
		t.Fatalf("客户端 region = %v, want [ap-guangzhou](账号 defaultRegion)", *regions)
	}
}

// TestGetOSSMetricsRegionFallback 账号 defaultRegion 为空时兜底 ap-guangzhou
// (monitor 客户端构造需要合法 region;数据缺失按「真实无数据点」呈现)。
func TestGetOSSMetricsRegionFallback(t *testing.T) {
	regions := &[]string{}
	adapter := NewCOSAdapter("ak", "sk", "", elog.DefaultLogger)
	adapter.ossMetricHooks = &ossMetricHooks{
		monitorFactory: func(region string) (nasMonitorClient, error) {
			*regions = append(*regions, region)
			return &stubNasMonitorClient{}, nil
		},
	}
	if _, err := adapter.GetOSSMetrics(context.Background(), "bkt", "2026-09-18", "2026-09-18"); err != nil {
		t.Fatalf("GetOSSMetrics err = %v", err)
	}
	if len(*regions) != 1 || (*regions)[0] != "ap-guangzhou" {
		t.Fatalf("客户端 region = %v, want [ap-guangzhou](兜底)", *regions)
	}
}

// TestGetOSSMetricsAPIFailure 调用失败路径(AC-5 三分之一):返回 error + 空结果
// (适配器打 ERROR + error 字段,执行器记失败计数,不阻塞全流程)。
func TestGetOSSMetricsAPIFailure(t *testing.T) {
	stub := &stubNasMonitorClient{err: errors.New("AuthFailure.SignatureFailure: auth failed")}
	adapter, _, _ := newOSSMetricTestAdapter(stub)

	metrics, err := adapter.GetOSSMetrics(context.Background(), "bkt", "2026-09-18", "2026-09-18")
	if err == nil {
		t.Fatal("API 失败应返回 error(失败可观测性), got nil")
	}
	if metrics != nil {
		t.Fatalf("API 失败应返回空结果, got %+v", metrics)
	}
}

// TestGetOSSMetricsClientFactoryFailure 真实客户端构造失败(AC-1):返回 error,
// 不发起指标查询。
func TestGetOSSMetricsClientFactoryFailure(t *testing.T) {
	adapter := NewCOSAdapter("ak", "sk", "ap-guangzhou", elog.DefaultLogger)
	adapter.ossMetricHooks = &ossMetricHooks{
		monitorFactory: func(region string) (nasMonitorClient, error) {
			return nil, errors.New("client construction failed")
		},
	}
	metrics, err := adapter.GetOSSMetrics(context.Background(), "bkt", "2026-09-18", "2026-09-18")
	if err == nil {
		t.Fatal("客户端构造失败应返回 error")
	}
	if metrics != nil {
		t.Fatalf("客户端构造失败应返回空结果, got %+v", metrics)
	}
}

// TestGetOSSMetricsNoDatapoints 真实无数据路径(AC-5 三分之一):bucket 无上报
// 返回空切片 + nil error(非调用失败,不触发失败计数);对象数指标不发起查询
// (容量行驱动,无容量即无行)。
func TestGetOSSMetricsNoDatapoints(t *testing.T) {
	stub := &stubNasMonitorClient{respByMetric: map[string]*monitor.GetMonitorDataResponse{}}
	adapter, _, _ := newOSSMetricTestAdapter(stub)
	metrics, err := adapter.GetOSSMetrics(context.Background(), "bkt", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("无数据点 err = %v, want nil", err)
	}
	if metrics == nil || len(metrics) != 0 {
		t.Fatalf("无数据点 = %+v, want 空切片", metrics)
	}
	if len(stub.gotReqs) != 1 {
		t.Fatalf("请求数 = %d, want 1(容量无数据即短路,不查对象数)", len(stub.gotReqs))
	}
}

// TestGetOSSMetricsParamValidation 入参校验:bucketName 必填。
func TestGetOSSMetricsParamValidation(t *testing.T) {
	adapter, _, _ := newOSSMetricTestAdapter(&stubNasMonitorClient{})
	if _, err := adapter.GetOSSMetrics(context.Background(), "", "2026-09-18", "2026-09-18"); err == nil {
		t.Fatal("bucketName 为空应报错")
	}
}

// TestGetOSSMetricsCtxCancelled 上下文取消走失败路径(不发起查询)。
func TestGetOSSMetricsCtxCancelled(t *testing.T) {
	adapter, _, _ := newOSSMetricTestAdapter(&stubNasMonitorClient{})
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

// TestGetOSSMetricsWindowLimit 指标查询区间上限(复用 NAS 92 天窗口守卫,
// nasMetricMaxRangeDays):超窗显式报错,不做静默空查询。
func TestGetOSSMetricsWindowLimit(t *testing.T) {
	adapter, _, _ := newOSSMetricTestAdapter(&stubNasMonitorClient{})
	if _, err := adapter.GetOSSMetrics(context.Background(), "bkt", "2026-01-01", "2026-09-18"); err == nil {
		t.Fatal("超过 92 天窗口应报错")
	}
}

// TestCreateOSSMonitorClientRealWithoutHooks 无钩子时走真实客户端构造(离线:
// 仅构建 monitor 客户端对象,不发起网络请求),按账号 defaultRegion 缓存(AC-1
// 真实客户端构造)。
func TestCreateOSSMonitorClientRealWithoutHooks(t *testing.T) {
	adapter := NewCOSAdapter("ak", "sk", "ap-guangzhou", elog.DefaultLogger)
	c1, err := adapter.createOSSMonitorClient()
	if err != nil {
		t.Fatalf("createOSSMonitorClient err = %v, want nil(离线构造)", err)
	}
	c2, err := adapter.createOSSMonitorClient()
	if err != nil || c1 != c2 {
		t.Fatalf("二次创建应命中缓存并一致, err=%v same=%v", err, c1 == c2)
	}
}
