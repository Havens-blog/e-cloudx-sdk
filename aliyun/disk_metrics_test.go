package aliyun

import (
	"context"
	"errors"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/cms"
	"github.com/gotomicro/ego/core/elog"
)

// Disk 指标适配器单测(T3,复用 nas_metrics_test.go 的 stubCMSClient/stubResp)。
//
// 规格锚点:probe-report §1.1/§2(定案 namespace=acs_ecs_dashboard,IOPS/吞吐
// 云盘级 diskId 维度,使用率唯一口径=挂载实例级 vm.DiskUtilization)、proposal
// 「单位归一化」(byte/s→MB/s 归一,iops 原始单位)。

// newDiskMetricTestAdapter 构造带测试钩子的 DiskAdapter(指标查询路径)。
func newDiskMetricTestAdapter(cmsClient cmsMetricClient, attachedInstanceID string) (*DiskAdapter, *stubCMSClient, *[]string) {
	stub, _ := cmsClient.(*stubCMSClient)
	regions := &[]string{}
	adapter := NewDiskAdapter("ak", "sk", "cn-hangzhou", elog.DefaultLogger)
	adapter.diskMetricHooks = &diskMetricHooks{
		cmsFactory: func(region string) (cmsMetricClient, error) {
			*regions = append(*regions, region)
			return cmsClient, nil
		},
		lookupDisk: func(ctx context.Context, diskID, region string) (*types.DiskInstance, error) {
			inst := &types.DiskInstance{DiskID: diskID}
			if attachedInstanceID != "" {
				inst.InstanceID = attachedInstanceID
			}
			return inst, nil
		},
	}
	return adapter, stub, regions
}

// TestBuildAliyunDiskMetrics 单位归一与口径标注(AC:单位归一/usage_scope 打标):
// iops=读+写之和(次/秒);吞吐 byte/s → MB/s(1024 进位);使用率取
// vm.DiskUtilization 值并打 instance_level;缺失日跳过。
func TestBuildAliyunDiskMetrics(t *testing.T) {
	readIOPS := map[string]float64{"2026-09-18": 8.256, "2026-09-19": 9.1}
	writeIOPS := map[string]float64{"2026-09-18": 1.744} // 09-19 写 IOPS 无数据点
	readBPS := map[string]float64{"2026-09-18": 122570.919}
	writeBPS := map[string]float64{"2026-09-18": 901411.881} // 合计 1023982.8 byte/s
	usage := map[string]float64{"2026-09-18": 60.693}

	got := buildAliyunDiskMetrics("d-wz95rqmk", "data-vol", []string{"2026-09-18", "2026-09-19", "2026-09-20"}, readIOPS, writeIOPS, readBPS, writeBPS, usage)
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2(09-20 无任何数据点跳过,缺失日不填充)", len(got))
	}
	row := got[0]
	if row.IOPS != 10.0 {
		t.Fatalf("IOPS = %v, want 10.0(读 8.256+写 1.744)", row.IOPS)
	}
	wantMB := (122570.919 + 901411.881) / (1024 * 1024)
	if diff := row.Throughput - wantMB; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("Throughput = %v, want %v MB/s(byte/s 归一)", row.Throughput, wantMB)
	}
	if row.UsagePercent != 60.693 || row.UsageScope != types.DiskUsageScopeInstanceLevel {
		t.Fatalf("usage = (%v, %q), want (60.693, instance_level)", row.UsagePercent, row.UsageScope)
	}
	// 09-19:仅读 IOPS 有数据点,吞吐/使用率无数据 → 0 + 无口径标注(不填假值)
	row2 := got[1]
	if row2.IOPS != 9.1 || row2.Throughput != 0 {
		t.Fatalf("09-19 = (iops %v, throughput %v), want (9.1, 0)", row2.IOPS, row2.Throughput)
	}
	if row2.UsagePercent != 0 || row2.UsageScope != "" {
		t.Fatalf("09-19 usage = (%v, %q), want (0, \"\")(该日无使用率数据点)", row2.UsagePercent, row2.UsageScope)
	}
	if row.DiskID != "d-wz95rqmk" || row.DiskName != "data-vol" || row.Provider != "aliyun" {
		t.Fatalf("行元数据不符: %+v", row)
	}
	var _ types.DiskMetric = row
}

// TestGetDiskMetricsAliyunQueryShape 查询形状与透传(AC:真实 region/disk_id
// 透传、探测定案 namespace/维度):4 个云盘级指标走 diskId 维度,使用率走
// vm.DiskUtilization + 挂载实例 instanceId 维度(实盘探测形态,probe-report §1.1)。
func TestGetDiskMetricsAliyunQueryShape(t *testing.T) {
	// 覆盖 2026-09-18 单日数据
	stub := &stubCMSClient{respByMetric: map[string]*cms.DescribeMetricListResponse{
		"DiskReadIOPS":       stubResp(`[{"timestamp":1789689600000,"value":8.256}]`),
		"DiskWriteIOPS":      stubResp(`[{"timestamp":1789689600000,"value":1.744}]`),
		"DiskReadBPS":        stubResp(`[{"timestamp":1789689600000,"value":122570.919}]`),
		"DiskWriteBPS":       stubResp(`[{"timestamp":1789689600000,"value":901411.881}]`),
		"vm.DiskUtilization": stubResp(`[{"timestamp":1789689600000,"value":60.693}]`),
	}}
	adapter, stub, regions := newDiskMetricTestAdapter(stub, "i-wz95rqmk")

	metrics, err := adapter.GetDiskMetrics(context.Background(), "d-wz95rqmk", "data-vol", "cn-hangzhou", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("GetDiskMetrics err = %v", err)
	}
	if len(*regions) != 1 || (*regions)[0] != "cn-hangzhou" {
		t.Fatalf("CMS 工厂收到的 region = %v, want cn-hangzhou(按实例真实 region,客户端单次创建复用)", *regions)
	}
	if len(metrics) != 1 || metrics[0].Date != "2026-09-18" || metrics[0].IOPS != 10.0 {
		t.Fatalf("metrics = %+v, want 09-18 单行 iops=10", metrics)
	}
	if metrics[0].UsagePercent != 60.693 || metrics[0].UsageScope != types.DiskUsageScopeInstanceLevel {
		t.Fatalf("usage = (%v, %q), want 实例级使用率打标", metrics[0].UsagePercent, metrics[0].UsageScope)
	}
	// 查询形状:云盘级指标 diskId 维度 + 使用率 instanceId 维度
	byMetricUse := map[string][]string{}
	for _, req := range stub.gotReqs {
		if req.Namespace != "acs_ecs_dashboard" {
			t.Fatalf("Namespace = %q, want acs_ecs_dashboard(T1 定案)", req.Namespace)
		}
		if req.Period != "86400" {
			t.Fatalf("Period = %q, want 86400(天粒度)", req.Period)
		}
		byMetricUse[req.MetricName] = append(byMetricUse[req.MetricName], req.Dimensions)
	}
	for _, name := range []string{"DiskReadIOPS", "DiskWriteIOPS", "DiskReadBPS", "DiskWriteBPS"} {
		dims, ok := byMetricUse[name]
		if !ok || len(dims) != 1 || dims[0] != `{"diskId":"d-wz95rqmk"}` {
			t.Fatalf("metric %s 维度 = %v, want diskId=d-wz95rqmk 单维", name, dims)
		}
	}
	usageDims, ok := byMetricUse["vm.DiskUtilization"]
	if !ok || len(usageDims) != 1 || usageDims[0] != `{"instanceId":"i-wz95rqmk"}` {
		t.Fatalf("vm.DiskUtilization 维度 = %v, want instanceId=i-wz95rqmk(挂载实例级口径)", usageDims)
	}
}

// TestGetDiskMetricsAliyunUnattachedDisk 未挂载盘(available):无挂载实例 →
// 不查使用率、UsageScope 留空(0 由写路径打 zero_exception,不伪造)。
func TestGetDiskMetricsAliyunUnattachedDisk(t *testing.T) {
	stub := &stubCMSClient{respByMetric: map[string]*cms.DescribeMetricListResponse{
		"DiskReadIOPS": stubResp(`[{"timestamp":1789689600000,"value":3}]`),
	}}
	adapter, stub, _ := newDiskMetricTestAdapter(stub, "")

	metrics, err := adapter.GetDiskMetrics(context.Background(), "d-1", "spare", "cn-hangzhou", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("GetDiskMetrics err = %v", err)
	}
	if len(metrics) != 1 || metrics[0].IOPS != 3 {
		t.Fatalf("metrics = %+v, want 单行 iops=3", metrics)
	}
	if metrics[0].UsagePercent != 0 || metrics[0].UsageScope != "" {
		t.Fatalf("usage = (%v, %q), want (0, \"\")(未挂载盘无使用率口径)", metrics[0].UsagePercent, metrics[0].UsageScope)
	}
	for _, req := range stub.gotReqs {
		if req.MetricName == "vm.DiskUtilization" {
			t.Fatal("未挂载盘不应查询 vm.DiskUtilization")
		}
	}
}

// TestGetDiskMetricsAliyunCallFailure 调用失败:返回 error(ERROR 日志路径),
// 由执行器记入失败计数,不静默吞掉。
func TestGetDiskMetricsAliyunCallFailure(t *testing.T) {
	stub := &stubCMSClient{err: errors.New("cms timeout")}
	adapter, _, _ := newDiskMetricTestAdapter(stub, "i-1")

	metrics, err := adapter.GetDiskMetrics(context.Background(), "d-1", "n", "cn-hangzhou", "2026-09-18", "2026-09-18")
	if err == nil {
		t.Fatalf("调用失败应返回 error, got metrics=%v", metrics)
	}
	if len(metrics) != 0 {
		t.Fatalf("失败路径不应返回数据行, got %v", metrics)
	}
}

// TestGetDiskMetricsAliyunNoData 真实无数据(无 IO 盘/未上报):空切片 + nil,
// 非调用失败,不触发失败计数。
func TestGetDiskMetricsAliyunNoData(t *testing.T) {
	stub := &stubCMSClient{}
	adapter, _, _ := newDiskMetricTestAdapter(stub, "i-1")

	metrics, err := adapter.GetDiskMetrics(context.Background(), "d-1", "n", "cn-hangzhou", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("无数据不是失败, err = %v", err)
	}
	if metrics == nil || len(metrics) != 0 {
		t.Fatalf("metrics = %v, want 空切片+nil", metrics)
	}
}

// TestGetDiskMetricsAliyunValidation 入参校验:diskID/region 必填(region 按
// 实例真实地域查询,不做全局推断)。
func TestGetDiskMetricsAliyunValidation(t *testing.T) {
	adapter, _, _ := newDiskMetricTestAdapter(&stubCMSClient{}, "i-1")
	if _, err := adapter.GetDiskMetrics(context.Background(), "", "n", "cn-hangzhou", "2026-09-18", "2026-09-18"); err == nil {
		t.Fatal("diskID 为空应报错")
	}
	if _, err := adapter.GetDiskMetrics(context.Background(), "d-1", "n", "", "2026-09-18", "2026-09-18"); err == nil {
		t.Fatal("region 为空应报错")
	}
	if _, err := adapter.GetDiskMetrics(context.Background(), "d-1", "n", "cn-hangzhou", "2026-09-19", "2026-09-18"); err == nil {
		t.Fatal("startDate 晚于 endDate 应报错")
	}
}

// TestCreateCMSClientRealConstruction 真实客户端构造壳(离线):按 region 创建
// 成功且带 metrics 域名;二次调用命中缓存返回同一实例(AC:真实客户端构造非 mock)。
func TestCreateCMSClientRealConstruction(t *testing.T) {
	adapter := NewDiskAdapter("ak", "sk", "cn-hangzhou", elog.DefaultLogger)
	client, err := adapter.createCMSClient("cn-hangzhou")
	if err != nil || client == nil {
		t.Fatalf("createCMSClient = (%v, %v), want 非 nil 客户端", client, err)
	}
	cached, err := adapter.createCMSClient("cn-hangzhou")
	if err != nil || cached != client {
		t.Fatalf("二次调用应命中缓存返回同一实例, got (%v, %v)", cached, err)
	}
}
