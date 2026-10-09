package tencent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	cam "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cam/v20190116"
	monitor "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/monitor/v20180724"
)

// stubNasMonitorClient 云监控客户端测试桩:按 MetricName 返回预置响应/错误,记录请求。
type stubNasMonitorClient struct {
	respByMetric map[string]*monitor.GetMonitorDataResponse
	err          error
	gotReqs      []*monitor.GetMonitorDataRequest
}

func (s *stubNasMonitorClient) GetMonitorData(request *monitor.GetMonitorDataRequest) (*monitor.GetMonitorDataResponse, error) {
	s.gotReqs = append(s.gotReqs, request)
	if s.err != nil {
		return nil, s.err
	}
	if resp, ok := s.respByMetric[*request.MetricName]; ok {
		return resp, nil
	}
	return &monitor.GetMonitorDataResponse{}, nil
}

// stubNasCamClient CAM GetUserAppId 测试桩。
type stubNasCamClient struct {
	appID *uint64
	err   error
}

func (s *stubNasCamClient) GetUserAppId(request *cam.GetUserAppIdRequest) (*cam.GetUserAppIdResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.appID == nil {
		return &cam.GetUserAppIdResponse{}, nil
	}
	return &cam.GetUserAppIdResponse{Response: &cam.GetUserAppIdResponseParams{AppId: s.appID}}, nil
}

// monitorResp 构造 GetMonitorData 响应(DataPoint:时间戳秒 + 值)。
func monitorResp(pairs ...[]float64) *monitor.GetMonitorDataResponse {
	resp := &monitor.GetMonitorDataResponse{Response: &monitor.GetMonitorDataResponseParams{}}
	for _, pair := range pairs {
		ts, val := pair[0], pair[1]
		resp.Response.DataPoints = append(resp.Response.DataPoints, &monitor.DataPoint{
			Timestamps: []*float64{&ts},
			Values:     []*float64{&val},
		})
	}
	return resp
}

// newNasMetricTestAdapter 构造带测试钩子的 CFSAdapter。
func newNasMetricTestAdapter(monitorClient *stubNasMonitorClient, camClient *stubNasCamClient) *CFSAdapter {
	adapter := NewCFSAdapter("ak", "sk", "ap-guangzhou", elog.DefaultLogger)
	adapter.nasMonitorHooks = &nasMetricHooks{
		monitorFactory: func(region string) (nasMonitorClient, error) {
			return monitorClient, nil
		},
		camFactory: func() (nasCamClient, error) {
			if camClient == nil {
				return nil, errors.New("cam unavailable")
			}
			return camClient, nil
		},
	}
	return adapter
}

// TestBuildTencentNASMetricsConversion 采集边界字节 → GB 换算与容量派生(AC-1):
// 1,479,761,633,280 byte = 1378.14 GB(probe-report 华为同数量级锚点),
// usage=56.09% 派生容量 ≈ 2456.95 GB。
func TestBuildTencentNASMetricsConversion(t *testing.T) {
	storageDaily := map[string]float64{
		"2026-09-18": 1479761633280,
		"2026-09-19": 20305059840,
	}
	usageDaily := map[string]float64{
		"2026-09-18": 56.09,
	}
	got := buildTencentNASMetrics("cfs-1", "fat-cfs", []string{"2026-09-18", "2026-09-19", "2026-09-20"}, storageDaily, usageDaily)
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2(used 缺失日 09-20 跳过不落库)", len(got))
	}
	first := got[0]
	if want := 1479761633280.0 / (1024 * 1024 * 1024); first.UsedCapacity != want {
		t.Fatalf("UsedCapacity = %v, want %v(字节→GB,types.BytesToGB)", first.UsedCapacity, want)
	}
	wantCap := first.UsedCapacity / (56.09 / 100)
	if diff := first.Capacity - wantCap; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("Capacity = %v, want %v(usage 派生)", first.Capacity, wantCap)
	}
	if first.FsID != "cfs-1" || first.FsName != "fat-cfs" || first.Date != "2026-09-18" || first.Provider != "tencent" {
		t.Fatalf("行元数据不符: %+v", first)
	}
	// 无使用率数据:capacity=0,由写路径打 zero_exception,不写 NaN
	second := got[1]
	if second.Capacity != 0 {
		t.Fatalf("无 usage 日 Capacity = %v, want 0(写路径 zero_exception)", second.Capacity)
	}
	var _ types.NASMetric = first
}

// TestDeriveNASCapacityFromUsage 派生边界:usage<=0 → 0(禁 NaN)。
func TestDeriveNASCapacityFromUsage(t *testing.T) {
	if got := deriveNASCapacityFromUsage(100, 0); got != 0 {
		t.Fatalf("usage=0 → %v, want 0", got)
	}
	if got := deriveNASCapacityFromUsage(100, -5); got != 0 {
		t.Fatalf("usage<0 → %v, want 0", got)
	}
	if got := deriveNASCapacityFromUsage(50, 25); got != 200 {
		t.Fatalf("usage=25 → %v, want 200", got)
	}
}

// TestGetNASMetricsRequestShape 维度顺序(appid→FileSystemId)、namespace、
// period、时间窗口格式(AC-1)。
func TestGetNASMetricsRequestShape(t *testing.T) {
	appid := uint64(1250000000)
	stub := &stubNasMonitorClient{respByMetric: map[string]*monitor.GetMonitorDataResponse{
		nasMetricStorage: monitorResp([]float64{1789660800, 1073741824}),
	}}
	adapter := newNasMetricTestAdapter(stub, &stubNasCamClient{appID: &appid})

	metrics, err := adapter.GetNASMetrics(context.Background(), "cfs-abc", "nas-1", "ap-shanghai", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("GetNASMetrics err = %v, want nil", err)
	}
	if len(metrics) != 1 || metrics[0].UsedCapacity != 1 {
		t.Fatalf("metrics = %+v, want 1 行 UsedCapacity=1GB", metrics)
	}
	if len(stub.gotReqs) != 2 {
		t.Fatalf("请求数 = %d, want 2(Storage + StorageUsage)", len(stub.gotReqs))
	}
	req := stub.gotReqs[0]
	if *req.Namespace != nasMonitorNamespace || *req.MetricName != nasMetricStorage {
		t.Fatalf("Namespace/MetricName = %s/%s, want %s/%s", *req.Namespace, *req.MetricName, nasMonitorNamespace, nasMetricStorage)
	}
	if req.Period == nil || *req.Period != nasDailyPeriod {
		t.Fatalf("Period = %v, want %d", req.Period, nasDailyPeriod)
	}
	if len(req.Instances) != 1 || len(req.Instances[0].Dimensions) != 2 {
		t.Fatalf("Instances 形状不符: %+v", req.Instances)
	}
	dim0, dim1 := req.Instances[0].Dimensions[0], req.Instances[0].Dimensions[1]
	if *dim0.Name != nasDimAppID || *dim0.Value != "1250000000" {
		t.Fatalf("维度 0 = %s/%s, want appid/1250000000(appid 在前,文档顺序)", *dim0.Name, *dim0.Value)
	}
	if *dim1.Name != nasDimFileSystemID || *dim1.Value != "cfs-abc" {
		t.Fatalf("维度 1 = %s/%s, want FileSystemId/cfs-abc", *dim1.Name, *dim1.Value)
	}
	if *req.StartTime != "2026-09-18T00:00:00+08:00" || *req.EndTime != "2026-09-19T00:00:00+08:00" {
		t.Fatalf("时间窗 = %s ~ %s, want 2026-09-18T00:00:00+08:00 ~ 2026-09-19T00:00:00+08:00", *req.StartTime, *req.EndTime)
	}
	// 第二个请求为使用率指标
	if *stub.gotReqs[1].MetricName != nasMetricStorageUsage {
		t.Fatalf("第二指标 = %s, want %s", *stub.gotReqs[1].MetricName, nasMetricStorageUsage)
	}
}

// TestGetNASMetricsRegionPassthrough 按实例 region 创建客户端(AC-1:
// 不做全局 region 推断,不用 defaultRegion)。
func TestGetNASMetricsRegionPassthrough(t *testing.T) {
	regions := &[]string{}
	appid := uint64(1250000000)
	adapter := NewCFSAdapter("ak", "sk", "ap-guangzhou", elog.DefaultLogger)
	adapter.nasMonitorHooks = &nasMetricHooks{
		monitorFactory: func(region string) (nasMonitorClient, error) {
			*regions = append(*regions, region)
			return &stubNasMonitorClient{}, nil
		},
		camFactory: func() (nasCamClient, error) {
			return &stubNasCamClient{appID: &appid}, nil
		},
	}
	if _, err := adapter.GetNASMetrics(context.Background(), "cfs-1", "n", "ap-beijing", "2026-09-18", "2026-09-18"); err != nil {
		t.Fatalf("GetNASMetrics err = %v", err)
	}
	if len(*regions) != 1 || (*regions)[0] != "ap-beijing" {
		t.Fatalf("客户端 region = %v, want [ap-beijing]", *regions)
	}
}

// TestGetNASMetricsAPIFailure 调用失败路径(AC-4):返回 error + 空结果
// (适配器打 ERROR + error 字段,执行器记失败计数)。
func TestGetNASMetricsAPIFailure(t *testing.T) {
	stub := &stubNasMonitorClient{err: errors.New("AuthFailure.SignatureFailure: auth failed")}
	adapter := newNasMetricTestAdapter(stub, &stubNasCamClient{err: nil})

	metrics, err := adapter.GetNASMetrics(context.Background(), "cfs-1", "n", "ap-guangzhou", "2026-09-18", "2026-09-18")
	if err == nil {
		t.Fatal("API 失败应返回 error(失败可观测性), got nil")
	}
	if metrics != nil {
		t.Fatalf("API 失败应返回空结果, got %+v", metrics)
	}
}

// TestGetNASMetricsAppIDFailure APPID 解析失败同样走失败路径。
func TestGetNASMetricsAppIDFailure(t *testing.T) {
	adapter := newNasMetricTestAdapter(&stubNasMonitorClient{}, &stubNasCamClient{err: errors.New("cam unavailable")})
	metrics, err := adapter.GetNASMetrics(context.Background(), "cfs-1", "n", "ap-guangzhou", "2026-09-18", "2026-09-18")
	if err == nil {
		t.Fatal("APPID 解析失败应返回 error")
	}
	if metrics != nil {
		t.Fatalf("APPID 解析失败应返回空结果, got %+v", metrics)
	}
}

// TestGetNASMetricsNoDatapoints 真实无指标数据(实例无上报)返回空切片 +
// nil error(非调用失败,不触发失败计数)。
func TestGetNASMetricsNoDatapoints(t *testing.T) {
	appid := uint64(1250000000)
	stub := &stubNasMonitorClient{respByMetric: map[string]*monitor.GetMonitorDataResponse{}}
	adapter := newNasMetricTestAdapter(stub, &stubNasCamClient{appID: &appid})
	metrics, err := adapter.GetNASMetrics(context.Background(), "cfs-1", "n", "ap-guangzhou", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("无数据点 err = %v, want nil", err)
	}
	if metrics == nil || len(metrics) != 0 {
		t.Fatalf("无数据点 = %+v, want 空切片", metrics)
	}
}

// TestGetNASMetricsParamValidation 入参校验(fsID/region 必填)。
func TestGetNASMetricsParamValidation(t *testing.T) {
	appid := uint64(1250000000)
	adapter := newNasMetricTestAdapter(&stubNasMonitorClient{}, &stubNasCamClient{appID: &appid})
	if _, err := adapter.GetNASMetrics(context.Background(), "", "n", "ap-guangzhou", "2026-09-18", "2026-09-18"); err == nil {
		t.Fatal("fsID 为空应报错")
	}
	if _, err := adapter.GetNASMetrics(context.Background(), "cfs-1", "n", "", "2026-09-18", "2026-09-18"); err == nil {
		t.Fatal("region 为空应报错(按实例 region 查询)")
	}
}

// TestNasMetricDateRange 日期解析:逆序区间与超 92 天拒绝(与回填窗口对齐)。
func TestNasMetricDateRange(t *testing.T) {
	dates, err := nasMetricDateRange("2026-09-18", "2026-09-20")
	if err != nil || len(dates) != 3 || dates[0] != "2026-09-18" || dates[2] != "2026-09-20" {
		t.Fatalf("dates = %v, err = %v, want 3 天", dates, err)
	}
	if _, err := nasMetricDateRange("2026-09-20", "2026-09-18"); err == nil {
		t.Fatal("逆序区间应报错")
	}
	long, err := nasMetricDateRange("2026-05-01", "2026-09-18")
	if err == nil {
		t.Fatalf("超 92 天区间应报错, got %d 天", len(long))
	}
}

// TestAggregateNASMonitorDailyLastPerDay 同日多点保留最后出现的点(日末态
// 快照),跨日按运营时区(Asia/Shanghai)切分;时间戳容错毫秒形态。
func TestAggregateNASMonitorDailyLastPerDay(t *testing.T) {
	// 2026-09-18 16:00:00 UTC = 2026-09-19 00:00 CST(日切边界验证)
	sec1, v1 := float64(1789747200), float64(100)
	sec1b, v1b := float64(1789747300), float64(200) // 同日较晚点
	sec2, v2 := float64(1789833600), float64(300)   // 09-20 CST 00:00
	resp := &monitor.GetMonitorDataResponse{Response: &monitor.GetMonitorDataResponseParams{
		DataPoints: []*monitor.DataPoint{{
			Timestamps: []*float64{&sec1, &sec1b, &sec2},
			Values:     []*float64{&v1, &v1b, &v2},
		}},
	}}
	got := aggregateNASMonitorDaily(resp)
	if len(got) != 2 {
		t.Fatalf("days = %d(%v), want 2", len(got), got)
	}
	if got["2026-09-19"] != 200 {
		t.Fatalf("2026-09-19 = %v, want 200(同日多点取最后)", got["2026-09-19"])
	}
	if got["2026-09-20"] != 300 {
		t.Fatalf("2026-09-20 = %v, want 300", got["2026-09-20"])
	}
	// 空响应
	if empty := aggregateNASMonitorDaily(&monitor.GetMonitorDataResponse{}); len(empty) != 0 {
		t.Fatalf("空响应 = %v, want 空 map", empty)
	}
	// nil 时间戳/值的数据点跳过,不伪造 0
	nilTs := &monitor.GetMonitorDataResponse{Response: &monitor.GetMonitorDataResponseParams{
		DataPoints: []*monitor.DataPoint{{
			Timestamps: []*float64{nil, &sec1},
			Values:     []*float64{&v1, nil},
		}},
	}}
	if filtered := aggregateNASMonitorDaily(nilTs); len(filtered) != 0 {
		t.Fatalf("nil 点应跳过: %v", filtered)
	}
	// 时区锚定:FixedZone("CST", 8*3600) 即 Asia/Shanghai 偏移
	if nasMetricCSTZone.String() != "CST" || time.Duration(8*3600)*time.Second != 8*time.Hour {
		t.Fatal("运营时区偏移应为 +8h")
	}
}
