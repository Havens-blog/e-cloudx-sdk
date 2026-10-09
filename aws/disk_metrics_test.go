package aws

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/gotomicro/ego/core/elog"
)

// Disk(EBS)指标适配器单测(T3,复用 nas_metrics_test.go 的 stubCWClient/cwPoint)。
//
// 规格锚点:probe-report §1.3/§2(T1 定案:AWS/EBS 五指标 Sum 日粒度;吞吐按
// 窗口秒数归一 byte/s→MB/s;IOPS=Ops/窗口秒数;使用率=VolumeIdleTime 派生
// 繁忙占比,打 busy_share 标注)。

// ebsOut 构造 GetMetricData 多序列返回(按 query id)。
func ebsOut(results map[string][]struct {
	ts  time.Time
	val float64
}) *cloudwatch.GetMetricDataOutput {
	out := &cloudwatch.GetMetricDataOutput{}
	for id, pts := range results {
		r := cwtypes.MetricDataResult{Id: awssdk.String(id)}
		for _, p := range pts {
			r.Timestamps = append(r.Timestamps, p.ts)
			r.Values = append(r.Values, p.val)
		}
		out.MetricDataResults = append(out.MetricDataResults, r)
	}
	return out
}

// ebsPoint CST 运营时区某日 08:00 的时间戳(1789689600000 = 2026-09-18 00:00 UTC)。
func ebsPoint(year, month, day int) time.Time {
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

// newEBSMetricTestAdapter 构造带测试钩子的 DiskAdapter(EBS 指标查询路径)。
func newEBSMetricTestAdapter(cwClient cwMetricClient) (*DiskAdapter, *[]string) {
	regions := &[]string{}
	adapter := NewDiskAdapter("ak", "sk", "us-east-1", elog.DefaultLogger)
	adapter.ebsMetricHooks = &cwMetricHooks{
		cwFactory: func(ctx context.Context, r string) (cwMetricClient, error) {
			*regions = append(*regions, r)
			return cwClient, nil
		},
	}
	return adapter, regions
}

// TestBuildAWSDiskMetrics 单位归一与派生(AC:派生公式实盘锚点 vol-00ddfd59
// idle=69579s/86400s → 19.47%):iops=(读+写 Ops)/86400;吞吐=(读+写 Bytes)/86400
// → MB/s;使用率派生打 busy_share;全闲盘派生 0.00 合法;无 idle 日不打标。
func TestBuildAWSDiskMetrics(t *testing.T) {
	readOps := map[string]float64{"2026-09-18": 43200}  // + write 43200 → 1 次/s
	writeOps := map[string]float64{"2026-09-18": 43200} // 86400 次/日
	readBytes := map[string]float64{"2026-09-18": 1048576 * 43200}
	writeBytes := map[string]float64{"2026-09-18": 1048576 * 43200} // 合计 86400 MB/日 → 1 MB/s
	idle := map[string]float64{"2026-09-18": 69579, "2026-09-19": 86399}

	got := buildAWSDiskMetrics("vol-00ddfd59", "data-vol",
		[]string{"2026-09-18", "2026-09-19", "2026-09-20"},
		readOps, writeOps, readBytes, writeBytes, idle)
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2(09-20 无任何数据点跳过)", len(got))
	}
	row := got[0]
	if row.IOPS != 1.0 {
		t.Fatalf("IOPS = %v, want 1.0(86400 Ops/86400s)", row.IOPS)
	}
	if diff := row.Throughput - 1.0; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("Throughput = %v, want 1.0 MB/s(byte/日按窗口秒数归一)", row.Throughput)
	}
	wantUsage := (1 - 69579.0/86400.0) * 100 // ≈ 19.47(实盘锚点)
	if diff := row.UsagePercent - wantUsage; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("UsagePercent = %v, want %v(实盘锚点 19.47)", row.UsagePercent, wantUsage)
	}
	if row.UsageScope != types.DiskUsageScopeBusyShare {
		t.Fatalf("UsageScope = %q, want busy_share(繁忙占比口径标注)", row.UsageScope)
	}
	// 09-19:仅 idle 有数据点(全闲盘)→ iops/吞吐 0,派生 0.00 仍打 busy_share
	row2 := got[1]
	if row2.IOPS != 0 || row2.Throughput != 0 {
		t.Fatalf("09-19 = (iops %v, throughput %v), want (0, 0)", row2.IOPS, row2.Throughput)
	}
	if row2.UsageScope != types.DiskUsageScopeBusyShare {
		t.Fatalf("09-19 UsageScope = %q, want busy_share(全闲盘派生 0.00 属合法值)", row2.UsageScope)
	}
	if row2.UsagePercent > 0.01 {
		t.Fatalf("09-19 UsagePercent = %v, want ≈0.00", row2.UsagePercent)
	}
	if row.DiskID != "vol-00ddfd59" || row.Provider != "aws" {
		t.Fatalf("行元数据不符: %+v", row)
	}
	var _ types.DiskMetric = row
}

// TestBuildAWSDiskMetricsIdleMissing 仅 IO 指标有数据、无 VolumeIdleTime 的日:
// 使用率 0 且不打 busy_share(派生不可靠/缺失则打标缺失,不伪造——Hard Rule)。
func TestBuildAWSDiskMetricsIdleMissing(t *testing.T) {
	got := buildAWSDiskMetrics("vol-1", "n", []string{"2026-09-18"},
		map[string]float64{"2026-09-18": 100}, map[string]float64{"2026-09-18": 100},
		map[string]float64{"2026-09-18": 2048}, map[string]float64{"2026-09-18": 2048},
		nil)
	if len(got) != 1 {
		t.Fatalf("rows = %d, want 1", len(got))
	}
	if got[0].UsagePercent != 0 || got[0].UsageScope != "" {
		t.Fatalf("usage = (%v, %q), want (0, \"\")(无 idle 数据不打标)", got[0].UsagePercent, got[0].UsageScope)
	}
}

// TestBuildAWSDiskMetricsIdleOutOfRange idle 越出窗口(异常形态):派生不可靠
// → 使用率 0 + 不打标,不产生越界值入 0~100 门禁。
func TestBuildAWSDiskMetricsIdleOutOfRange(t *testing.T) {
	got := buildAWSDiskMetrics("vol-1", "n", []string{"2026-09-18"},
		map[string]float64{"2026-09-18": 100}, map[string]float64{"2026-09-18": 100},
		map[string]float64{"2026-09-18": 2048}, map[string]float64{"2026-09-18": 2048},
		map[string]float64{"2026-09-18": 90000})
	if len(got) != 1 {
		t.Fatalf("rows = %d, want 1", len(got))
	}
	if got[0].UsagePercent != 0 || got[0].UsageScope != "" {
		t.Fatalf("usage = (%v, %q), want (0, \"\")(idle 越界不打标)", got[0].UsagePercent, got[0].UsageScope)
	}
}

// TestGetDiskMetricsAWSQueryShape 查询形状与透传:5 指标 AWS/EBS Sum 86400、
// VolumeId 维度、按实例真实 region 创建客户端。
func TestGetDiskMetricsAWSQueryShape(t *testing.T) {
	stub := &stubCWClient{out: ebsOut(map[string][]struct {
		ts  time.Time
		val float64
	}{
		"read_ops":    {{ebsPoint(2026, 9, 18), 43200}},
		"write_ops":   {{ebsPoint(2026, 9, 18), 43200}},
		"read_bytes":  {{ebsPoint(2026, 9, 18), 1048576 * 43200}},
		"write_bytes": {{ebsPoint(2026, 9, 18), 1048576 * 43200}},
		"idle_time":   {{ebsPoint(2026, 9, 18), 69579}},
	})}
	adapter, regions := newEBSMetricTestAdapter(stub)

	metrics, err := adapter.GetDiskMetrics(context.Background(), "vol-00ddfd59", "data-vol", "eu-central-1", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("GetDiskMetrics err = %v", err)
	}
	if len(*regions) != 1 || (*regions)[0] != "eu-central-1" {
		t.Fatalf("CloudWatch 工厂收到的 region = %v, want eu-central-1(按实例真实 region)", *regions)
	}
	if len(metrics) != 1 || metrics[0].IOPS != 1.0 || metrics[0].UsageScope != types.DiskUsageScopeBusyShare {
		t.Fatalf("metrics = %+v, want 单行 iops=1 busy_share", metrics)
	}
	if len(stub.got) != 1 {
		t.Fatalf("GetMetricData 次数 = %d, want 1", len(stub.got))
	}
	input := stub.got[0]
	if len(input.MetricDataQueries) != 5 {
		t.Fatalf("queries = %d, want 5(4 IO + idle)", len(input.MetricDataQueries))
	}
	ids := map[string]bool{}
	for _, q := range input.MetricDataQueries {
		ids[*q.Id] = true
		if q.MetricStat == nil || *q.MetricStat.Stat != "Sum" || *q.MetricStat.Period != 86400 {
			t.Fatalf("query %s stat/period 形状不符: %+v", *q.Id, q.MetricStat)
		}
		m := q.MetricStat.Metric
		if m == nil || *m.Namespace != "AWS/EBS" {
			t.Fatalf("query %s namespace 不符: %+v", *q.Id, m)
		}
		if len(m.Dimensions) != 1 || *m.Dimensions[0].Name != "VolumeId" || *m.Dimensions[0].Value != "vol-00ddfd59" {
			t.Fatalf("query %s 维度不符: %+v", *q.Id, m.Dimensions)
		}
	}
	for _, want := range []string{"read_ops", "write_ops", "read_bytes", "write_bytes", "idle_time"} {
		if !ids[want] {
			t.Fatalf("缺 query id %s, got %v", want, ids)
		}
	}
}

// TestGetDiskMetricsAWSCallFailure 调用失败:返回 error(ERROR 路径)——不套用
// CDN CloudFront「主动放弃」先例(AC 显式要求)。
func TestGetDiskMetricsAWSCallFailure(t *testing.T) {
	stub := &stubCWClient{err: errors.New("cloudwatch throttled")}
	adapter, _ := newEBSMetricTestAdapter(stub)

	metrics, err := adapter.GetDiskMetrics(context.Background(), "vol-1", "n", "eu-central-1", "2026-09-18", "2026-09-18")
	if err == nil {
		t.Fatalf("调用失败应返回 error, got metrics=%v", metrics)
	}
	if len(metrics) != 0 {
		t.Fatalf("失败路径不应返回数据行, got %v", metrics)
	}
	if !strings.Contains(err.Error(), "查询 EBS 指标失败") {
		t.Fatalf("错误应携带上下文, got %v", err)
	}
}

// TestGetDiskMetricsAWSNoData 真实无数据(卷未挂载/未上报):空切片 + nil。
func TestGetDiskMetricsAWSNoData(t *testing.T) {
	stub := &stubCWClient{out: &cloudwatch.GetMetricDataOutput{}}
	adapter, _ := newEBSMetricTestAdapter(stub)

	metrics, err := adapter.GetDiskMetrics(context.Background(), "vol-1", "n", "eu-central-1", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("无数据不是失败, err = %v", err)
	}
	if metrics == nil || len(metrics) != 0 {
		t.Fatalf("metrics = %v, want 空切片+nil", metrics)
	}
}

// TestGetDiskMetricsAWSValidation 入参校验:diskID/region 必填与日期范围。
func TestGetDiskMetricsAWSValidation(t *testing.T) {
	adapter, _ := newEBSMetricTestAdapter(&stubCWClient{})
	if _, err := adapter.GetDiskMetrics(context.Background(), "", "n", "eu-central-1", "2026-09-18", "2026-09-18"); err == nil {
		t.Fatal("diskID 为空应报错")
	}
	if _, err := adapter.GetDiskMetrics(context.Background(), "vol-1", "n", "", "2026-09-18", "2026-09-18"); err == nil {
		t.Fatal("region 为空应报错")
	}
	if _, err := adapter.GetDiskMetrics(context.Background(), "vol-1", "n", "eu-central-1", "2026-09-19", "2026-09-18"); err == nil {
		t.Fatal("startDate 晚于 endDate 应报错")
	}
}

// TestNewCloudWatchClientRealConstruction 真实客户端构造壳(离线):config 加载
// 与静态凭证装配不发起网络调用,构造成功(AC:真实客户端构造非 mock)。
func TestNewCloudWatchClientRealConstruction(t *testing.T) {
	client, err := newCloudWatchClient(context.Background(), "ak", "sk", "eu-central-1")
	if err != nil || client == nil {
		t.Fatalf("newCloudWatchClient = (%v, %v), want 非 nil 客户端", client, err)
	}
}
