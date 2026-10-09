package huawei

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	cesv1model "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1/model"
)

// stubCESDiskClient CES Disk 指标客户端测试桩(ListMetrics + BatchListMetricData):
// 记录请求,按预置队列/单响应返回,支持错误注入。
type stubCESDiskClient struct {
	listResps []*cesv1model.ListMetricsResponse // 队列:逐次弹出(读/写序列回退场景)
	listResp  *cesv1model.ListMetricsResponse   // 队列空时的兜底响应
	listErr   error
	batchResp *cesv1model.BatchListMetricDataResponse
	batchErr  error
	listReqs  []*cesv1model.ListMetricsRequest
	batchReqs []*cesv1model.BatchListMetricDataRequest
}

func (s *stubCESDiskClient) ListMetrics(request *cesv1model.ListMetricsRequest) (*cesv1model.ListMetricsResponse, error) {
	s.listReqs = append(s.listReqs, request)
	if s.listErr != nil {
		return nil, s.listErr
	}
	if len(s.listResps) > 0 {
		resp := s.listResps[0]
		s.listResps = s.listResps[1:]
		return resp, nil
	}
	return s.listResp, nil
}

func (s *stubCESDiskClient) BatchListMetricData(request *cesv1model.BatchListMetricDataRequest) (*cesv1model.BatchListMetricDataResponse, error) {
	s.batchReqs = append(s.batchReqs, request)
	if s.batchErr != nil {
		return nil, s.batchErr
	}
	return s.batchResp, nil
}

// Disk 指标适配器单测(T3,复用 nas_metrics_test.go 的 datapoint/batchMetric 与
// cdn_metrics.go 的 strPtr)。
//
// 规格锚点:probe-report §1.2/§2(T1 定案:namespace=SYS.EVS disk_device_* 系列,
// 设备维度键 disk_name=<实例UUID>-<设备名> 须按盘 ID 前缀匹配;使用率唯一可按盘
// 关联口径=SYS.EVS disk_device_io_util,打 instance_level 标注)。

// listMetricsResp 构造 ListMetrics 单页返回(单指标多序列)。
func listMetricsResp(dimName, dimValue string) *cesv1model.ListMetricsResponse {
	return &cesv1model.ListMetricsResponse{
		Metrics: &[]cesv1model.MetricInfoList{{
			Namespace:  "SYS.EVS",
			MetricName: "disk_device_read_requests_rate",
			Dimensions: []cesv1model.MetricsDimensionResp{
				{Name: strPtr(dimName), Value: strPtr(dimValue)},
			},
		}},
		MetaData: &cesv1model.MetricListMetaDataResp{},
	}
}

// ebsBatchFor huawei 批量响应:5 指标 × 单数据点。
func diskBatchResp(dimValue string, tsMs int64, readReq, writeReq, readBytes, writeBytes, ioUtil float64) *cesv1model.BatchListMetricDataResponse {
	dims := &[]cesv1model.MetricsDimensionResp{{Name: strPtr("disk_name"), Value: strPtr(dimValue)}}
	metric := func(name string, v float64) cesv1model.BatchMetricData {
		return cesv1model.BatchMetricData{
			Namespace:  strPtr("SYS.EVS"),
			MetricName: name,
			Dimensions: dims,
			Datapoints: []cesv1model.DatapointForBatchMetric{datapoint(tsMs, v)},
		}
	}
	return &cesv1model.BatchListMetricDataResponse{Metrics: &[]cesv1model.BatchMetricData{
		metric("disk_device_read_requests_rate", readReq),
		metric("disk_device_write_requests_rate", writeReq),
		metric("disk_device_read_bytes_rate", readBytes),
		metric("disk_device_write_bytes_rate", writeBytes),
		metric("disk_device_io_util", ioUtil),
	}}
}

// newDiskMetricTestAdapter 构造带测试钩子的 DiskAdapter(指标查询路径)。
func newDiskMetricTestAdapter(client cesDiskMetricClient) (*DiskAdapter, *[]string) {
	regions := &[]string{}
	adapter := NewDiskAdapter("ak", "sk", "cn-north-4", elog.DefaultLogger)
	adapter.diskMetricHooks = &diskMetricHooks{
		cesFactory: func(region string) (cesDiskMetricClient, error) {
			*regions = append(*regions, region)
			return client, nil
		},
	}
	return adapter, regions
}

// TestMatchDiskDeviceDim 设备序列维度匹配(probe-report §1.2:实盘键形态为
// <盘ID>-<设备名> 前缀形态,不能用卷 ID 精确匹配/用户命名匹配)。
func TestMatchDiskDeviceDim(t *testing.T) {
	cases := []struct {
		dimValue string
		want     bool
	}{
		{"f4bf6adf-1234-vda", true},         // 盘 ID 前缀 + 设备后缀(实盘主形态)
		{"f4bf6adf-1234-volume-volc", true}, // 盘 ID 前缀 + volume 形态
		{"f4bf6adf-1234", true},             // 精确等于
		{"other-uuid-vda", false},           // 无关盘
		{"f4bf6adf-5678-vda", false},        // 不同盘 ID 前缀
	}
	for _, c := range cases {
		if got := matchDiskDeviceDim(c.dimValue, "f4bf6adf-1234"); got != c.want {
			t.Fatalf("matchDiskDeviceDim(%q) = %v, want %v", c.dimValue, got, c.want)
		}
	}
}

// TestBuildHuaweiDiskMetrics 单位归一与口径标注:iops=读+写 request/s;吞吐
// byte/s → MB/s;使用率取 disk_device_io_util 并打 instance_level;缺失日跳过。
func TestBuildHuaweiDiskMetrics(t *testing.T) {
	readReq := map[string]float64{"2026-09-18": 12.5}
	writeReq := map[string]float64{"2026-09-18": 7.5}         // 合计 20 request/s
	readBytes := map[string]float64{"2026-09-18": 524288.0}   // 0.5 MB/s
	writeBytes := map[string]float64{"2026-09-18": 1048576.0} // 1.0 MB/s → 合计 1.5
	ioUtil := map[string]float64{"2026-09-18": 41.79}

	got := buildHuaweiDiskMetrics("vol-uuid", "data-vol", []string{"2026-09-18", "2026-09-19"}, readReq, writeReq, readBytes, writeBytes, ioUtil)
	if len(got) != 1 {
		t.Fatalf("rows = %d, want 1(09-19 无数据点跳过)", len(got))
	}
	row := got[0]
	if row.IOPS != 20 {
		t.Fatalf("IOPS = %v, want 20(读+写 request/s)", row.IOPS)
	}
	if diff := row.Throughput - 1.5; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("Throughput = %v, want 1.5 MB/s(byte/s 归一)", row.Throughput)
	}
	if row.UsagePercent != 41.79 || row.UsageScope != types.DiskUsageScopeInstanceLevel {
		t.Fatalf("usage = (%v, %q), want (41.79, instance_level)", row.UsagePercent, row.UsageScope)
	}
	if row.DiskID != "vol-uuid" || row.Provider != "huawei" {
		t.Fatalf("行元数据不符: %+v", row)
	}
}

// TestBuildHuaweiDiskMetricsUsageMissing 使用率序列无数据的日:usage=0 且
// 不打 instance_level(口径缺失不伪造,写路径打 zero_exception)。
func TestBuildHuaweiDiskMetricsUsageMissing(t *testing.T) {
	got := buildHuaweiDiskMetrics("vol-uuid", "n", []string{"2026-09-18"},
		map[string]float64{"2026-09-18": 1},
		map[string]float64{"2026-09-18": 1},
		map[string]float64{"2026-09-18": 2048},
		map[string]float64{"2026-09-18": 2048},
		nil)
	if len(got) != 1 {
		t.Fatalf("rows = %d, want 1", len(got))
	}
	if got[0].UsagePercent != 0 || got[0].UsageScope != "" {
		t.Fatalf("usage = (%v, %q), want (0, \"\")(无使用率序列)", got[0].UsagePercent, got[0].UsageScope)
	}
}

// TestGetDiskMetricsHuaweiQueryShape 查询形状与透传:按真实 region 创建 CES
// 客户端;ListMetrics 前缀发现设备维度值;BatchListMetricData 携带 5 指标 +
// 发现的 disk_name 维度。
func TestGetDiskMetricsHuaweiQueryShape(t *testing.T) {
	stub := &stubCESDiskClient{
		listResp:  listMetricsResp("disk_name", "f4bf6adf-1234-vda"),
		batchResp: diskBatchResp("f4bf6adf-1234-vda", 1789689600000, 12.5, 7.5, 524288.0, 1048576.0, 41.79),
	}
	adapter, regions := newDiskMetricTestAdapter(stub)

	metrics, err := adapter.GetDiskMetrics(context.Background(), "f4bf6adf-1234", "data-vol", "cn-south-1", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("GetDiskMetrics err = %v", err)
	}
	if len(*regions) != 1 || (*regions)[0] != "cn-south-1" {
		t.Fatalf("CES 工厂收到的 region = %v, want cn-south-1(按实例真实 region,无默认兜底)", *regions)
	}
	if len(stub.listReqs) != 1 {
		t.Fatalf("ListMetrics 次数 = %d, want 1(读序列已命中,不再探写序列)", len(stub.listReqs))
	}
	lr := stub.listReqs[0]
	if lr.Namespace == nil || *lr.Namespace != "SYS.EVS" {
		t.Fatalf("ListMetrics Namespace = %v, want SYS.EVS(T1 定案)", lr.Namespace)
	}
	if len(stub.batchReqs) != 1 {
		t.Fatalf("BatchListMetricData 次数 = %d, want 1", len(stub.batchReqs))
	}
	body := stub.batchReqs[0].Body
	if body == nil || len(body.Metrics) != 5 {
		t.Fatalf("批量指标数 = %v, want 5(4 IO + io_util)", body)
	}
	for _, m := range body.Metrics {
		if m.Namespace != "SYS.EVS" {
			t.Fatalf("批量指标 Namespace = %q, want SYS.EVS", m.Namespace)
		}
		if len(m.Dimensions) != 1 || m.Dimensions[0].Name != "disk_name" || m.Dimensions[0].Value != "f4bf6adf-1234-vda" {
			t.Fatalf("批量维度 = %+v, want disk_name=f4bf6adf-1234-vda(ListMetrics 前缀发现)", m.Dimensions)
		}
	}
	if len(metrics) != 1 || metrics[0].IOPS != 20 {
		t.Fatalf("metrics = %+v, want 09-18 单行 iops=20", metrics)
	}
	if metrics[0].UsagePercent != 41.79 || metrics[0].UsageScope != types.DiskUsageScopeInstanceLevel {
		t.Fatalf("usage = (%v, %q), want (41.79, instance_level)", metrics[0].UsagePercent, metrics[0].UsageScope)
	}
}

// TestGetDiskMetricsHuaweiNoSeries 真实无该盘序列(设备未上报/盘未挂载):
// 空切片 + nil(探测不支持/无数据路径,INFO 日志,非调用失败)。
func TestGetDiskMetricsHuaweiNoSeries(t *testing.T) {
	stub := &stubCESDiskClient{
		listResp: listMetricsResp("disk_name", "other-uuid-vda"),
	}
	adapter, _ := newDiskMetricTestAdapter(stub)

	metrics, err := adapter.GetDiskMetrics(context.Background(), "f4bf6adf-1234", "n", "cn-south-1", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("无序列不是失败, err = %v", err)
	}
	if metrics == nil || len(metrics) != 0 {
		t.Fatalf("metrics = %v, want 空切片+nil", metrics)
	}
	if len(stub.batchReqs) != 0 {
		t.Fatal("未发现设备序列不应发起批量查询")
	}
}

// TestGetDiskMetricsHuaweiReadSeriesMissFallsToWrite 读序列 ListMetrics 无命中时
// 回退写序列发现(只写盘形态),写序列命中后照常批量查询。
func TestGetDiskMetricsHuaweiReadSeriesMissFallsToWrite(t *testing.T) {
	readMiss := listMetricsResp("disk_name", "other-uuid-vda")
	writeHit := listMetricsResp("disk_name", "f4bf6adf-1234-vdb")
	stub := &stubCESDiskClient{
		listResps: []*cesv1model.ListMetricsResponse{readMiss, writeHit},
		batchResp: diskBatchResp("f4bf6adf-1234-vdb", 1789689600000, 0, 3, 0, 4096, 8),
	}
	adapter, _ := newDiskMetricTestAdapter(stub)

	metrics, err := adapter.GetDiskMetrics(context.Background(), "f4bf6adf-1234", "n", "cn-south-1", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("GetDiskMetrics err = %v", err)
	}
	if len(stub.listReqs) != 2 {
		t.Fatalf("ListMetrics 次数 = %d, want 2(读未命中回退写)", len(stub.listReqs))
	}
	if len(metrics) != 1 || metrics[0].IOPS != 3 {
		t.Fatalf("metrics = %+v, want 单行 iops=3(写序列命中)", metrics)
	}
}

// TestGetDiskMetricsHuaweiCallFailure 批量查询调用失败:返回 error(ERROR 路径)。
func TestGetDiskMetricsHuaweiCallFailure(t *testing.T) {
	stub := &stubCESDiskClient{
		listResp: listMetricsResp("disk_name", "f4bf6adf-1234-vda"),
		batchErr: errors.New("ces 500"),
	}
	adapter, _ := newDiskMetricTestAdapter(stub)

	metrics, err := adapter.GetDiskMetrics(context.Background(), "f4bf6adf-1234", "n", "cn-south-1", "2026-09-18", "2026-09-18")
	if err == nil {
		t.Fatalf("调用失败应返回 error, got metrics=%v", metrics)
	}
	if len(metrics) != 0 {
		t.Fatalf("失败路径不应返回数据行, got %v", metrics)
	}
}

// TestGetDiskMetricsHuaweiListMetricsFailure ListMetrics 调用失败:返回 error。
func TestGetDiskMetricsHuaweiListMetricsFailure(t *testing.T) {
	stub := &stubCESDiskClient{listErr: errors.New("ces 403")}
	adapter, _ := newDiskMetricTestAdapter(stub)

	if _, err := adapter.GetDiskMetrics(context.Background(), "f4bf6adf-1234", "n", "cn-south-1", "2026-09-18", "2026-09-18"); err == nil {
		t.Fatal("ListMetrics 调用失败应返回 error")
	}
}

// TestGetDiskMetricsHuaweiValidation 入参校验:diskID/region 必填(region 空
// 不得静默回退默认 region——Hard Rule:指标路径不做单 region 静默回退)。
func TestGetDiskMetricsHuaweiValidation(t *testing.T) {
	adapter, _ := newDiskMetricTestAdapter(&stubCESDiskClient{})
	if _, err := adapter.GetDiskMetrics(context.Background(), "", "n", "cn-south-1", "2026-09-18", "2026-09-18"); err == nil {
		t.Fatal("diskID 为空应报错")
	}
	if _, err := adapter.GetDiskMetrics(context.Background(), "vol-1", "n", "", "2026-09-18", "2026-09-18"); err == nil {
		t.Fatal("region 为空应报错(不回退默认 region)")
	}
	if _, err := adapter.GetDiskMetrics(context.Background(), "vol-1", "n", "cn-south-1", "2026-09-19", "2026-09-18"); err == nil {
		t.Fatal("startDate 晚于 endDate 应报错")
	}
}

// TestGetDiskMetricsHuaweiRangeBoundary 数据点落窗口边界外不产出行(查询窗口
// = [首日 00:00, 末日+24h) 运营时区)。
func TestGetDiskMetricsHuaweiRangeBoundary(t *testing.T) {
	// 数据点在 2026-09-18 CST,查询窗口仅 09-19 → 空切片
	stub := &stubCESDiskClient{
		listResp:  listMetricsResp("disk_name", "f4bf6adf-1234-vda"),
		batchResp: diskBatchResp("f4bf6adf-1234-vda", 1789689600000, 1, 1, 1, 1, 1),
	}
	adapter, _ := newDiskMetricTestAdapter(stub)

	metrics, err := adapter.GetDiskMetrics(context.Background(), "f4bf6adf-1234", "n", "cn-south-1", "2026-09-19", "2026-09-19")
	if err != nil {
		t.Fatalf("GetDiskMetrics err = %v", err)
	}
	if len(metrics) != 0 {
		t.Fatalf("窗口外数据点不应产出行, got %v", metrics)
	}
}

// TestCreateCESDiskClientInvalidRegion 非法 region 显式报错且不做静默回退默认
// region(Hard Rule;合法 region 的客户端构造会发起 IAM project-id 网络调用,
// 离线不可测,由 disk_probe_manual_test env 门控覆盖)。
func TestCreateCESDiskClientInvalidRegion(t *testing.T) {
	adapter := NewDiskAdapter("ak", "sk", "cn-north-4", elog.DefaultLogger)
	if _, err := adapter.createCESDiskClient("bad-region"); err == nil {
		t.Fatal("非法 region 应显式报错")
	} else if !strings.Contains(err.Error(), "不做静默回退") {
		t.Fatalf("错误应声明不做静默回退, got %v", err)
	}
}
