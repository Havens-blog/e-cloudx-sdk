package huawei

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	huaweicoreutils "github.com/huaweicloud/huaweicloud-sdk-go-v3/core/utils"
	cesv1model "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1/model"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1/region"
)

// stubCESClient CES 客户端测试桩:记录请求,返回预置响应/错误。
type stubCESClient struct {
	resp *cesv1model.BatchListMetricDataResponse
	err  error
	got  []*cesv1model.BatchListMetricDataRequest
}

func (s *stubCESClient) BatchListMetricData(request *cesv1model.BatchListMetricDataRequest) (*cesv1model.BatchListMetricDataResponse, error) {
	s.got = append(s.got, request)
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func newCESMetricTestAdapter(cesClient cesMetricClient) (*SFSAdapter, *[]string) {
	regions := &[]string{}
	adapter := NewSFSAdapter("ak", "sk", "cn-north-4", elog.DefaultLogger)
	adapter.cesHooks = &cesMetricHooks{
		cesFactory: func(r string) (cesMetricClient, error) {
			*regions = append(*regions, r)
			return cesClient, nil
		},
	}
	return adapter, regions
}

// datapoint 构造日粒度数据点。
func datapoint(tsMs int64, avg float64) cesv1model.DatapointForBatchMetric {
	return cesv1model.DatapointForBatchMetric{Timestamp: tsMs, Average: &avg}
}

// batchMetric 构造一条批量指标返回。
func batchMetric(namespace, name string, points ...cesv1model.DatapointForBatchMetric) cesv1model.BatchMetricData {
	return cesv1model.BatchMetricData{
		Namespace:  strPtr(namespace),
		MetricName: name,
		Datapoints: points,
	}
}

// TestCoerceCESValueJSONNumber 响应 map 元素为 json.Number 的解析回归
// (AC-2;CDN ShowDomainStats 同类缺陷 47da689:lookupSeries 未识别
// json.Number 导致全部解析为 nil、华为侧永远无数据)。
// 以华为 SDK 真实解码路径(utils.Unmarshal = jsoniter UseNumber)解码
// CES 形态的响应片段,逐元素经 coerceCESValue 提取。
func TestCoerceCESValueJSONNumber(t *testing.T) {
	raw := `{"datapoints":[{"average":1479761633280,"timestamp":1789660800000},
	                      {"average":"-",  "timestamp":1789750000000},
	                      {"average":20305059840,"timestamp":1789833600000}]}`
	var decoded map[string]interface{}
	if err := huaweicoreutils.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("SDK 解码失败: %v", err)
	}
	dps, ok := decoded["datapoints"].([]interface{})
	if !ok || len(dps) != 3 {
		t.Fatalf("datapoints 解码异常: %T(%v)", decoded["datapoints"], decoded["datapoints"])
	}
	// jsoniter UseNumber 下数字元素类型必须识别为 json.Number(缺陷前提验证)
	first := dps[0].(map[string]interface{})
	if _, isJSONNumber := first["average"].(json.Number); !isJSONNumber {
		t.Fatalf("average 元素类型 = %T, want json.Number(SDK UseNumber 解码)", first["average"])
	}
	v := coerceCESValue(first["average"])
	if v == nil || *v != 1479761633280 {
		t.Fatalf("coerceCESValue(json.Number) = %v, want 1479761633280", v)
	}
	// 无数据哨兵 "-":返回 nil(该点无数据,不伪造 0)
	if v := coerceCESValue(dps[1].(map[string]interface{})["average"]); v != nil {
		t.Fatalf("coerceCESValue(\"-\") = %v, want nil", v)
	}
	if v := coerceCESValue(dps[2].(map[string]interface{})["average"]); v == nil || *v != 20305059840 {
		t.Fatalf("coerceCESValue(第二个点) = %v, want 20305059840", v)
	}
	// 其他形态:float64 / 非法输入
	if v := coerceCESValue(float64(3.5)); v == nil || *v != 3.5 {
		t.Fatalf("coerceCESValue(float64) = %v, want 3.5", v)
	}
	if v := coerceCESValue("not-a-number"); v != nil {
		t.Fatalf("coerceCESValue(非法字符串) = %v, want nil", v)
	}
	if v := coerceCESValue(nil); v != nil {
		t.Fatalf("coerceCESValue(nil) = %v, want nil", v)
	}
}

// TestDeriveCESCapacity 总容量派生(AC-2,probe-report 遗留行动 #3):
// jlc-fat 实锚 1378.14GB ÷ 56.09% ≈ 2457GB;percent=0 → 0(禁 NaN)。
func TestDeriveCESCapacity(t *testing.T) {
	used := 1479761633280.0 / (1024 * 1024 * 1024) // 1378.14 GB
	got := deriveCESCapacity(used, 56.09)
	if got < 2455 || got > 2460 {
		t.Fatalf("deriveCESCapacity = %v, want ≈2457 GB", got)
	}
	if c := deriveCESCapacity(used, 0); c != 0 {
		t.Fatalf("percent=0 → %v, want 0(派生无效,写路径 zero_exception)", c)
	}
	if c := deriveCESCapacity(used, -1); c != 0 {
		t.Fatalf("percent<0 → %v, want 0", c)
	}
	if isNaN(deriveCESCapacity(used, 0)) {
		t.Fatal("禁止 NaN")
	}
}

func isNaN(f float64) bool { return f != f }

// TestBuildCESMetrics 逐日指标行组装:字节→GB、percent 缺失日 capacity=0、
// used 缺失日跳过(AC-2)。
func TestBuildCESMetrics(t *testing.T) {
	usedDaily := map[string]float64{
		"2026-09-18": 1479761633280,
		"2026-09-19": 20305059840,
	}
	percentDaily := map[string]float64{
		"2026-09-18": 56.09,
		// 09-19 percent 缺失 → capacity 派生无效 → 0
	}
	got := buildCESMetrics("98d68b8d-xxxx", "jlc-fat-sfs-turbo", []string{"2026-09-18", "2026-09-19", "2026-09-20"}, usedDaily, percentDaily)
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2(used 缺失日 09-20 跳过)", len(got))
	}
	first := got[0]
	if first.UsedCapacity < 1378 || first.UsedCapacity > 1379 {
		t.Fatalf("UsedCapacity = %v, want ≈1378.14 GB(字节→GB)", first.UsedCapacity)
	}
	if first.Capacity < 2455 || first.Capacity > 2460 {
		t.Fatalf("Capacity = %v, want ≈2457 GB(派生)", first.Capacity)
	}
	second := got[1]
	if second.Capacity != 0 {
		t.Fatalf("percent 缺失日 Capacity = %v, want 0", second.Capacity)
	}
	if second.UsedCapacity < 18.9 || second.UsedCapacity > 19.0 {
		t.Fatalf("UsedCapacity = %v, want ≈18.91 GB", second.UsedCapacity)
	}
	if first.FsID != "98d68b8d-xxxx" || first.Provider != "huawei" || first.Date != "2026-09-18" {
		t.Fatalf("行元数据不符: %+v", first)
	}
	var _ types.NASMetric = first
}

// TestAggregateCESDaily 同日多点取最后(日末态),跨时区日切正确。
func TestAggregateCESDaily(t *testing.T) {
	points := []cesv1model.DatapointForBatchMetric{
		datapoint(1789747200000, 100), // 2026-09-18 16:00 UTC → 09-19 CST
		datapoint(1789747300000, 200), // 同日较晚点
		datapoint(1789833600000, 300), // 09-20 CST 00:00
	}
	got := aggregateCESDaily(points)
	if got["2026-09-19"] != 200 || got["2026-09-20"] != 300 {
		t.Fatalf("aggregateCESDaily = %v, want 09-19:200 09-20:300", got)
	}
	// 无值数据点(Average/Max/Sum 全空)跳过
	points = append(points, cesv1model.DatapointForBatchMetric{Timestamp: 1789920000000})
	got = aggregateCESDaily(points)
	if _, ok := got["2026-09-21"]; ok {
		t.Fatal("无值数据点不应落日桶(不伪造 0)")
	}
}

// TestPickCESMetricData namespace 择优:优先有数据的 SYS.EFS(M1 实测定案),
// SYS.SFS 兜底;used 与 percent 同 namespace(容量派生口径一致)。
func TestPickCESMetricData(t *testing.T) {
	// EFS 有数据 → 全取 EFS
	metrics := []cesv1model.BatchMetricData{
		batchMetric(cesNamespaceEFS, cesMetricUsedCapacity, datapoint(1, 100)),
		batchMetric(cesNamespaceEFS, cesMetricUsedCapacityPercent, datapoint(1, 50)),
		batchMetric(cesNamespaceSFS, cesMetricUsedCapacity, datapoint(1, 999)),
	}
	used, percent := pickCESMetricData(metrics)
	if used == nil || cesNamespaceOf(used) != cesNamespaceEFS {
		t.Fatalf("used namespace = %v, want SYS.EFS", used)
	}
	if percent == nil || cesNamespaceOf(percent) != cesNamespaceEFS {
		t.Fatalf("percent namespace = %v, want SYS.EFS", percent)
	}

	// EFS 无数据 → 落 SYS.SFS(普通 SFS 实例场景)
	metrics = []cesv1model.BatchMetricData{
		batchMetric(cesNamespaceEFS, cesMetricUsedCapacity),
		batchMetric(cesNamespaceSFS, cesMetricUsedCapacity, datapoint(1, 100)),
		batchMetric(cesNamespaceSFS, cesMetricUsedCapacityPercent, datapoint(1, 50)),
	}
	used, percent = pickCESMetricData(metrics)
	if used == nil || cesNamespaceOf(used) != cesNamespaceSFS {
		t.Fatalf("used namespace = %v, want SYS.SFS(EFS 无数据时兜底)", used)
	}
	if percent == nil || cesNamespaceOf(percent) != cesNamespaceSFS {
		t.Fatalf("percent namespace = %v, want SYS.SFS", percent)
	}

	// used/percent 跨 namespace:以 used 的 namespace 为准,异 namespace 的 percent 丢弃
	metrics = []cesv1model.BatchMetricData{
		batchMetric(cesNamespaceEFS, cesMetricUsedCapacity, datapoint(1, 100)),
		batchMetric(cesNamespaceSFS, cesMetricUsedCapacityPercent, datapoint(1, 50)),
	}
	used, percent = pickCESMetricData(metrics)
	if used == nil || percent != nil {
		t.Fatalf("跨 namespace 择优错误: used=%v percent=%v", used, percent)
	}
}

// TestGetNASMetricsBatchRequestShape 批量请求形状(AC-2):
// 两 namespace × 两指标、维度 efs_instance_id、period 86400、filter average、
// 查询窗口覆盖 [startDate, endDate] 全天。
func TestGetNASMetricsBatchRequestShape(t *testing.T) {
	stub := &stubCESClient{resp: &cesv1model.BatchListMetricDataResponse{
		Metrics: &[]cesv1model.BatchMetricData{
			batchMetric(cesNamespaceEFS, cesMetricUsedCapacity, datapoint(1789660800000, 1479761633280)),
			batchMetric(cesNamespaceEFS, cesMetricUsedCapacityPercent, datapoint(1789660800000, 56.09)),
		},
	}}
	adapter, _ := newCESMetricTestAdapter(stub)

	metrics, err := adapter.GetNASMetrics(context.Background(), "98d68b8d", "jlc-fat-sfs-turbo", "cn-south-1", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("GetNASMetrics err = %v", err)
	}
	if len(metrics) != 1 || metrics[0].UsedCapacity < 1378 || metrics[0].Capacity < 2455 {
		t.Fatalf("metrics = %+v, want 1 行 used≈1378GB capacity≈2457GB", metrics)
	}
	if len(stub.got) != 1 {
		t.Fatalf("请求次数 = %d, want 1(单批查询)", len(stub.got))
	}
	req := stub.got[0]
	if len(req.Body.Metrics) != 4 {
		t.Fatalf("批量指标数 = %d, want 4(2 namespace × 2 指标)", len(req.Body.Metrics))
	}
	for _, m := range req.Body.Metrics {
		if len(m.Dimensions) != 1 || m.Dimensions[0].Name != "efs_instance_id" || m.Dimensions[0].Value != "98d68b8d" {
			t.Fatalf("维度 = %+v, want efs_instance_id=98d68b8d", m.Dimensions)
		}
	}
	if got := req.Body.Period.Value(); got != "86400" {
		t.Fatalf("Period = %s, want 86400", got)
	}
	if got := req.Body.Filter.Value(); got != "average" {
		t.Fatalf("Filter = %s, want average", got)
	}
	// 窗口:CST 2026-09-18 00:00 ~ 23:59:59.999(毫秒)
	if wantFrom := int64(1789660800000); req.Body.From != wantFrom {
		t.Fatalf("From = %d, want %d(CST 09-18 00:00)", req.Body.From, wantFrom)
	}
	if wantTo := int64(1789747199999); req.Body.To != wantTo {
		t.Fatalf("To = %d, want %d(CST 09-18 末毫秒)", req.Body.To, wantTo)
	}
}

// TestGetNASMetricsRegionPassthrough 按实例 region 创建客户端(AC-4):
// 传给 CES 工厂的是实例真实 region,而非 defaultRegion(cn-north-4)。
func TestGetNASMetricsRegionPassthrough(t *testing.T) {
	stub := &stubCESClient{resp: &cesv1model.BatchListMetricDataResponse{
		Metrics: &[]cesv1model.BatchMetricData{
			batchMetric(cesNamespaceEFS, cesMetricUsedCapacity, datapoint(1789660800000, 1024)),
		},
	}}
	adapter, regions := newCESMetricTestAdapter(stub)
	if _, err := adapter.GetNASMetrics(context.Background(), "fs-1", "n", "cn-south-1", "2026-09-18", "2026-09-18"); err != nil {
		t.Fatalf("GetNASMetrics err = %v", err)
	}
	if len(*regions) != 1 || (*regions)[0] != "cn-south-1" {
		t.Fatalf("客户端 region = %v, want [cn-south-1]", *regions)
	}
}

// TestCreateCESClientNoSilentRegionFallback Hard Rule:指标路径不经过
// 「单 region 静默回退 cn-north-4」——非法 region 在 SafeValueOf 处显式报错
// (先于网络调用返回,对比 sfs.go 资产路径的静默回退行为)。
func TestCreateCESClientNoSilentRegionFallback(t *testing.T) {
	adapter := NewSFSAdapter("ak", "sk", "cn-north-4", elog.DefaultLogger)
	if _, err := adapter.createCESClient("mars-1"); err == nil {
		t.Fatal("非法 region 应显式报错(不得静默回退 cn-north-4)")
	} else if !strings.Contains(err.Error(), "不做静默回退") {
		t.Fatalf("错误信息应说明不静默回退: %v", err)
	}
	// 注:合法 region 的完整客户端构建会触发 SDK 项目 ID 解析(IAM 网络调用),
	// 单测不覆盖;region 支持性锚点见 TestCESRegionSafeValueOfExists。
}

// TestGetNASMetricsCESFailure 调用失败路径(AC-5):返回 error + 空结果。
func TestGetNASMetricsCESFailure(t *testing.T) {
	stub := &stubCESClient{err: errors.New("401 auth failed")}
	adapter, _ := newCESMetricTestAdapter(stub)
	metrics, err := adapter.GetNASMetrics(context.Background(), "fs-1", "n", "cn-south-1", "2026-09-18", "2026-09-18")
	if err == nil {
		t.Fatal("CES 调用失败应返回 error(失败可观测性)")
	}
	if metrics != nil {
		t.Fatalf("调用失败应返回空结果, got %+v", metrics)
	}
}

// TestGetNASMetricsNoCESDatapoints 真实无指标数据返回空切片 + nil error。
func TestGetNASMetricsNoCESDatapoints(t *testing.T) {
	stub := &stubCESClient{resp: &cesv1model.BatchListMetricDataResponse{
		Metrics: &[]cesv1model.BatchMetricData{},
	}}
	adapter, _ := newCESMetricTestAdapter(stub)
	metrics, err := adapter.GetNASMetrics(context.Background(), "fs-1", "n", "cn-south-1", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("无数据点 err = %v, want nil", err)
	}
	if metrics == nil || len(metrics) != 0 {
		t.Fatalf("无数据点 = %+v, want 空切片", metrics)
	}
}

// TestGetNASMetricsParamValidation 入参校验。
func TestGetNASMetricsParamValidation(t *testing.T) {
	adapter, _ := newCESMetricTestAdapter(&stubCESClient{})
	if _, err := adapter.GetNASMetrics(context.Background(), "", "n", "cn-south-1", "2026-09-18", "2026-09-18"); err == nil {
		t.Fatal("fsID 为空应报错")
	}
	if _, err := adapter.GetNASMetrics(context.Background(), "fs-1", "n", "", "2026-09-18", "2026-09-18"); err == nil {
		t.Fatal("region 为空应报错")
	}
}

// TestCESRegionSafeValueOfExists region 注册表锚:cn-south-1(实盘 region)
// 必须在 CES region 支持列表中(M1 探测依赖此路径),防止 SDK 升级后
// SafeValueOf 对真实 region 误报。
func TestCESRegionSafeValueOfExists(t *testing.T) {
	if _, err := region.SafeValueOf("cn-south-1"); err != nil {
		t.Fatalf("cn-south-1 应在 CES region 支持列表: %v", err)
	}
}
