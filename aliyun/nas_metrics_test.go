package aliyun

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/cms"
	"github.com/gotomicro/ego/core/elog"
)

// stubCMSClient CMS 客户端测试桩:按 MetricName 返回预置响应/错误,记录请求。
type stubCMSClient struct {
	respByMetric map[string]*cms.DescribeMetricListResponse
	err          error
	gotReqs      []*cms.DescribeMetricListRequest
}

func (s *stubCMSClient) DescribeMetricList(request *cms.DescribeMetricListRequest) (*cms.DescribeMetricListResponse, error) {
	s.gotReqs = append(s.gotReqs, request)
	if s.err != nil {
		return nil, s.err
	}
	if resp, ok := s.respByMetric[request.MetricName]; ok {
		return resp, nil
	}
	// 未预置的指标按空数据返回(非调用失败)
	return &cms.DescribeMetricListResponse{Datapoints: "[]"}, nil
}

func stubResp(datapointsJSON string) *cms.DescribeMetricListResponse {
	return &cms.DescribeMetricListResponse{Datapoints: datapointsJSON}
}

// newMetricTestAdapter 构造带测试钩子的 NASAdapter。
func newMetricTestAdapter(cmsClient cmsMetricClient, userID string) (*NASAdapter, *stubCMSClient, *[]string) {
	stub, _ := cmsClient.(*stubCMSClient)
	regions := &[]string{}
	adapter := NewNASAdapter("ak", "sk", "cn-hangzhou", elog.DefaultLogger)
	adapter.metricHooks = &nasMetricHooks{
		cmsFactory: func(region string) (cmsMetricClient, error) {
			*regions = append(*regions, region)
			return cmsClient, nil
		},
		resolveUserID: func() (string, error) {
			return userID, nil
		},
	}
	return adapter, stub, regions
}

// TestParseCMSDatapoints 数据点解析:字段大小写不统一、空串、缺 value 跳过。
func TestParseCMSDatapoints(t *testing.T) {
	raw := `[{"timestamp":1789660800000,"Value":1479761633280},
	         {"Timestamp":1789747200000,"value":20305059840},
	         {"timestamp":1789833600000,"Maximum":999},
	         {"timestamp":1789920000000}]`
	points, err := parseCMSDatapoints(raw)
	if err != nil {
		t.Fatalf("parseCMSDatapoints err = %v, want nil", err)
	}
	if len(points) != 3 {
		t.Fatalf("points = %d, want 3(缺 value 的数据点跳过)", len(points))
	}
	if points[0].Timestamp != 1789660800000 || points[0].Value != 1479761633280 {
		t.Fatalf("points[0] = %+v, want ts=1789660800000 value=1479761633280", points[0])
	}
	if points[1].Value != 20305059840 {
		t.Fatalf("points[1].Value = %v, want 20305059840(小写 value 兼容)", points[1].Value)
	}
	if points[2].Value != 999 {
		t.Fatalf("points[2].Value = %v, want 999(缺 value 时兜底 Maximum)", points[2].Value)
	}

	empty, err := parseCMSDatapoints("")
	if err != nil || len(empty) != 0 {
		t.Fatalf("空串解析 = (%v, %v), want (nil, nil)", empty, err)
	}
}

// TestCMSNumberJSONNumber 数值提取容忍 json.Number 形态
// (与华为包 47da689 同一防呆:凡落进 interface{} 的数字可能是 json.Number)。
func TestCMSNumberJSONNumber(t *testing.T) {
	if f, ok := cmsNumber(float64(3)); !ok || f != 3 {
		t.Fatalf("float64 形态 = (%v, %v), want (3, true)", f, ok)
	}
	if f, ok := cmsNumber(json.Number("1479761633280")); !ok || f != 1479761633280 {
		t.Fatalf("json.Number 形态 = (%v, %v), want (1479761633280, true)", f, ok)
	}
	if _, ok := cmsNumber(json.Number("abc")); ok {
		t.Fatal("非法 json.Number 应返回 false")
	}
	if _, ok := cmsNumber("not-a-number"); ok {
		t.Fatal("非法字符串应返回 false")
	}
}

// TestAggregateCMSDailyLastPerDay 同日多点保留最后出现的点(日末态快照),
// 跨日按运营时区(Asia/Shanghai)切分。
func TestAggregateCMSDailyLastPerDay(t *testing.T) {
	// 2026-09-18 16:00:00 UTC = 2026-09-19 00:00 CST(日切边界验证)
	points := []cmsDataPoint{
		{Timestamp: 1789747200000, Value: 100}, // 2026-09-18 16:00 UTC → 09-19 CST
		{Timestamp: 1789747300000, Value: 200}, // 同日较晚点
		{Timestamp: 1789833600000, Value: 300}, // 09-20 CST 00:00
	}
	got := aggregateCMSDaily(points)
	if len(got) != 2 {
		t.Fatalf("days = %d(%v), want 2", len(got), got)
	}
	if got["2026-09-19"] != 200 {
		t.Fatalf("2026-09-19 = %v, want 200(同日多点取最后)", got["2026-09-19"])
	}
}

// TestBuildAliyunNASMetricsConversion 采集边界字节 → GB 换算正确性(AC-1):
// 1,479,761,633,280 byte = 1378.14 GB(probe-report 华为同数量级锚点)。
func TestBuildAliyunNASMetricsConversion(t *testing.T) {
	usedDaily := map[string]float64{
		"2026-09-18": 1479761633280,
		"2026-09-19": 20305059840,
	}
	capDaily := map[string]float64{
		"2026-09-18": 4398046511104, // 4 TiB
	}
	got := buildAliyunNASMetrics("fs-1", "fat-nas", []string{"2026-09-18", "2026-09-19", "2026-09-20"}, usedDaily, capDaily)
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2(used 缺失日 09-20 跳过不落库)", len(got))
	}
	first := got[0]
	if want := 1479761633280.0 / (1024 * 1024 * 1024); first.UsedCapacity != want {
		t.Fatalf("UsedCapacity = %v, want %v(字节→GB)", first.UsedCapacity, want)
	}
	if want := 4398046511104.0 / (1024 * 1024 * 1024); first.Capacity != want {
		t.Fatalf("Capacity = %v, want %v", first.Capacity, want)
	}
	if first.FsID != "fs-1" || first.FsName != "fat-nas" || first.Date != "2026-09-18" || first.Provider != "aliyun" {
		t.Fatalf("行元数据不符: %+v", first)
	}
	// 通用型(无总容量指标):capacity=0,由写路径打 zero_exception
	second := got[1]
	if second.Capacity != 0 {
		t.Fatalf("无容量指标日 Capacity = %v, want 0(写路径 zero_exception)", second.Capacity)
	}
	var _ types.NASMetric = first
}

// TestGetNASMetricsExtremeFallbackToAligned 极速型无数据点时回退通用型
// AlignedSize;维度按文档顺序携带 userId/fileSystemId。
func TestGetNASMetricsExtremeFallbackToAligned(t *testing.T) {
	stub := &stubCMSClient{respByMetric: map[string]*cms.DescribeMetricListResponse{
		nasMetricAlignedSize: stubResp(`[{"timestamp":1789660800000,"Value":1073741824}]`),
	}}
	adapter, _, _ := newMetricTestAdapter(stub, "1557808511111111")

	metrics, err := adapter.GetNASMetrics(context.Background(), "fs-abc", "nas-1", "cn-hangzhou", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("GetNASMetrics err = %v, want nil", err)
	}
	if len(metrics) != 1 || metrics[0].UsedCapacity != 1 {
		t.Fatalf("metrics = %+v, want 1 行 UsedCapacity=1GB", metrics)
	}
	var asked []string
	for _, req := range stub.gotReqs {
		asked = append(asked, req.MetricName)
	}
	if len(asked) < 2 || asked[0] != nasMetricExtremeCapacityUsed || asked[1] != nasMetricAlignedSize {
		t.Fatalf("查询序列 = %v, want 先 %s 后 %s(回退)", asked, nasMetricExtremeCapacityUsed, nasMetricAlignedSize)
	}
	dim := stub.gotReqs[0].Dimensions
	want := `{"userId":"1557808511111111","fileSystemId":"fs-abc"}`
	if dim != want {
		t.Fatalf("Dimensions = %s, want %s(userId 在前,文档顺序)", dim, want)
	}
	if stub.gotReqs[0].Period != "86400" || stub.gotReqs[0].Namespace != "acs_nas" {
		t.Fatalf("Period/Namespace = %s/%s, want 86400/acs_nas", stub.gotReqs[0].Period, stub.gotReqs[0].Namespace)
	}
}

// TestGetNASMetricsRegionPassthrough 按实例 region 创建客户端(AC-4:
// 不做全局 region 推断,不用 defaultRegion)。
func TestGetNASMetricsRegionPassthrough(t *testing.T) {
	stub := &stubCMSClient{respByMetric: map[string]*cms.DescribeMetricListResponse{
		nasMetricExtremeCapacityUsed: stubResp(`[{"timestamp":1789660800000,"Value":1024}]`),
	}}
	adapter, _, regions := newMetricTestAdapter(stub, "uid")
	// 实例 region(cn-shenzhen)≠ defaultRegion(cn-hangzhou)
	if _, err := adapter.GetNASMetrics(context.Background(), "fs-1", "n", "cn-shenzhen", "2026-09-18", "2026-09-18"); err != nil {
		t.Fatalf("GetNASMetrics err = %v", err)
	}
	if len(*regions) != 1 || (*regions)[0] != "cn-shenzhen" {
		t.Fatalf("客户端 region = %v, want [cn-shenzhen]", *regions)
	}
}

// TestGetNASMetricsAPIFailure 调用失败路径(AC-5):返回 error + 空结果
// (适配器打 ERROR + error 字段,执行器记失败计数)。
func TestGetNASMetricsAPIFailure(t *testing.T) {
	stub := &stubCMSClient{err: errors.New("InvalidAccessKeyId.NotFound: auth failed")}
	adapter, _, _ := newMetricTestAdapter(stub, "uid")

	metrics, err := adapter.GetNASMetrics(context.Background(), "fs-1", "n", "cn-hangzhou", "2026-09-18", "2026-09-18")
	if err == nil {
		t.Fatal("API 失败应返回 error(失败可观测性), got nil")
	}
	if metrics != nil {
		t.Fatalf("API 失败应返回空结果, got %+v", metrics)
	}
}

// TestGetNASMetricsUserIDFailure 账号 userId 解析失败同样走失败路径。
func TestGetNASMetricsUserIDFailure(t *testing.T) {
	stub := &stubCMSClient{}
	adapter := NewNASAdapter("ak", "sk", "cn-hangzhou", elog.DefaultLogger)
	adapter.metricHooks = &nasMetricHooks{
		cmsFactory: func(region string) (cmsMetricClient, error) { return stub, nil },
		resolveUserID: func() (string, error) {
			return "", errors.New("sts unavailable")
		},
	}
	metrics, err := adapter.GetNASMetrics(context.Background(), "fs-1", "n", "cn-hangzhou", "2026-09-18", "2026-09-18")
	if err == nil {
		t.Fatal("userId 解析失败应返回 error")
	}
	if metrics != nil {
		t.Fatalf("userId 解析失败应返回空结果, got %+v", metrics)
	}
}

// TestGetNASMetricsNoDatapoints 真实无指标数据(实例无上报)返回空切片 +
// nil error(非调用失败,不触发失败计数)。
func TestGetNASMetricsNoDatapoints(t *testing.T) {
	stub := &stubCMSClient{respByMetric: map[string]*cms.DescribeMetricListResponse{}}
	adapter, _, _ := newMetricTestAdapter(stub, "uid")
	metrics, err := adapter.GetNASMetrics(context.Background(), "fs-1", "n", "cn-hangzhou", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("无数据点 err = %v, want nil", err)
	}
	if metrics == nil || len(metrics) != 0 {
		t.Fatalf("无数据点 = %+v, want 空切片", metrics)
	}
}

// TestGetNASMetricsParamValidation 入参校验(fsID/region 必填)。
func TestGetNASMetricsParamValidation(t *testing.T) {
	adapter, _, _ := newMetricTestAdapter(&stubCMSClient{}, "uid")
	if _, err := adapter.GetNASMetrics(context.Background(), "", "n", "cn-hangzhou", "2026-09-18", "2026-09-18"); err == nil {
		t.Fatal("fsID 为空应报错")
	}
	if _, err := adapter.GetNASMetrics(context.Background(), "fs-1", "n", "", "2026-09-18", "2026-09-18"); err == nil {
		t.Fatal("region 为空应报错(按实例 region 查询)")
	}
}
