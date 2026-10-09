package huawei

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	cesv1model "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1/model"
)

// OBS 指标适配器单测(oss-ops-insight T3)。
// 复用 NAS 指标单测的 stubCESClient/datapoint/batchMetric/strPtr
// (nas_metrics_test.go,同包)。
// 规格锚:probe-report §1.2(M1 实测定案:namespace=SYS.OBS 文档口径实盘成立、
// 维度 bucket_name、容量 capacity_total(byte)、对象数 object_num_all(个),
// 与数据面 GetBucketStat 逐桶互证一致)。

// newOBSMetricTestAdapter 构造带测试钩子的 OBSAdapter。
func newOBSMetricTestAdapter(cesClient cesMetricClient) (*OBSAdapter, *stubCESClient) {
	stub, _ := cesClient.(*stubCESClient)
	adapter := NewOBSAdapter("ak", "sk", "cn-south-1", elog.DefaultLogger)
	adapter.ossCesHooks = &cesMetricHooks{
		cesFactory: func(region string) (cesMetricClient, error) {
			return cesClient, nil
		},
	}
	return adapter, stub
}

// obsBatchResp 构造 SYS.OBS 批量响应(capacity_total + object_num_all)。
func obsBatchResp(capacityBytes, objectCount float64) *cesv1model.BatchListMetricDataResponse {
	return &cesv1model.BatchListMetricDataResponse{Metrics: &[]cesv1model.BatchMetricData{
		batchMetric("SYS.OBS", "capacity_total", datapoint(1789660800000, capacityBytes)),
		batchMetric("SYS.OBS", "object_num_all", datapoint(1789660800000, objectCount)),
	}}
}

// TestGetOSSMetricsSuccess SYS.OBS 定案路径:维度 bucket_name、容量 byte→GB
// (AC-2/AC-5)、对象数透传;非零锚点取 probe-report §1.2 的
// fat-jlc-pub-file 6.488×10^12 byte ≈ 6042.24 GB 同数量级样本。
func TestGetOSSMetricsSuccess(t *testing.T) {
	stub := &stubCESClient{resp: obsBatchResp(1073741824, 2124371)}
	adapter, _ := newOBSMetricTestAdapter(stub)

	metrics, err := adapter.GetOSSMetrics(context.Background(), "fat-jlc-pub-file", "2026-09-18", "2026-09-18")
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
	if m.BucketName != "fat-jlc-pub-file" || m.Date != "2026-09-18" || m.Provider != "huawei" {
		t.Fatalf("行元数据不符: %+v", m)
	}
	var _ types.OSSMetric = m

	// 请求断言:SYS.OBS namespace + bucket_name 维度 + 86400 天粒度 Average
	if len(stub.got) != 1 {
		t.Fatalf("请求数 = %d, want 1", len(stub.got))
	}
	body := stub.got[0].Body
	if len(body.Metrics) != 2 {
		t.Fatalf("批量指标数 = %d, want 2(capacity_total+object_num_all)", len(body.Metrics))
	}
	for _, mi := range body.Metrics {
		if mi.Namespace != "SYS.OBS" {
			t.Fatalf("Namespace = %s, want SYS.OBS(M1 定案)", mi.Namespace)
		}
		if len(mi.Dimensions) != 1 || mi.Dimensions[0].Name != "bucket_name" || mi.Dimensions[0].Value != "fat-jlc-pub-file" {
			t.Fatalf("维度 = %+v, want bucket_name=fat-jlc-pub-file(probe-report §1.2)", mi.Dimensions)
		}
	}
	if body.Metrics[0].MetricName != "capacity_total" || body.Metrics[1].MetricName != "object_num_all" {
		t.Fatalf("指标名 = %s,%s, want capacity_total,object_num_all", body.Metrics[0].MetricName, body.Metrics[1].MetricName)
	}
	if body.Period == nil || *body.Period != cesv1model.GetBatchPeriodEnum().E_86400 {
		t.Fatal("Period 应为 86400(天粒度)")
	}
	if body.Filter == nil || *body.Filter != cesv1model.GetFilterEnum().AVERAGE {
		t.Fatal("Filter 应为 AVERAGE")
	}
}

// TestGetOSSMetricsNoData 真实无数据路径(AC-6 三分之一):bucket 无上报返回
// 空切片 + nil error(非调用失败,不触发失败计数)。
func TestGetOSSMetricsNoData(t *testing.T) {
	empty := &cesv1model.BatchListMetricDataResponse{Metrics: &[]cesv1model.BatchMetricData{}}
	adapter, _ := newOBSMetricTestAdapter(&stubCESClient{resp: empty})
	metrics, err := adapter.GetOSSMetrics(context.Background(), "bkt", "2026-09-18", "2026-09-18")
	if err != nil {
		t.Fatalf("无数据点 err = %v, want nil", err)
	}
	if metrics == nil || len(metrics) != 0 {
		t.Fatalf("无数据点 = %+v, want 空切片", metrics)
	}
}

// TestGetOSSMetricsAPIFailure 调用失败路径(AC-6 三分之一):返回 error + 空结果
// (适配器打 ERROR + error 字段,执行器记失败计数)。
func TestGetOSSMetricsAPIFailure(t *testing.T) {
	adapter, _ := newOBSMetricTestAdapter(&stubCESClient{err: errors.New("CES 500 internal")})
	metrics, err := adapter.GetOSSMetrics(context.Background(), "bkt", "2026-09-18", "2026-09-18")
	if err == nil {
		t.Fatal("API 失败应返回 error(失败可观测性), got nil")
	}
	if metrics != nil {
		t.Fatalf("API 失败应返回空结果, got %+v", metrics)
	}
}

// TestGetOSSMetricsParamValidation 入参校验:bucketName 必填。
func TestGetOSSMetricsParamValidation(t *testing.T) {
	adapter, _ := newOBSMetricTestAdapter(&stubCESClient{})
	if _, err := adapter.GetOSSMetrics(context.Background(), "", "2026-09-18", "2026-09-18"); err == nil {
		t.Fatal("bucketName 为空应报错")
	}
}

// TestCreateOSSCESClientRegionError Hard Rule:指标路径走 SafeValueOf 显式报错,
// 不经 obs.go createClient 的静默回退(AC-2)。
func TestCreateOSSCESClientRegionError(t *testing.T) {
	adapter := NewOBSAdapter("ak", "sk", "not-a-real-region", elog.DefaultLogger)
	_, err := adapter.createOSSCESClient()
	if err == nil {
		t.Fatal("region 不在支持列表应显式报错(不做静默回退)")
	}
	if !strings.Contains(err.Error(), "不做静默回退") {
		t.Fatalf("错误应声明不做静默回退, got %v", err)
	}
}

// TestAggregateOBSDailyLastPerDay 同日多点保留最后出现的点(日末态快照),
// 跨日按运营时区(Asia/Shanghai)切分;无值数据点跳过不伪造 0。
func TestAggregateOBSDailyLastPerDay(t *testing.T) {
	// 2026-09-18 16:00:00 UTC = 2026-09-19 00:00 CST(日切边界验证)
	v100, v200 := 100.0, 200.0
	points := []cesv1model.DatapointForBatchMetric{
		{Timestamp: 1789747200000, Average: &v100},
		{Timestamp: 1789747300000, Average: &v200},
		{Timestamp: 1789833600000}, // 无值数据点
	}
	got := aggregateOBSDaily(points)
	if len(got) != 1 {
		t.Fatalf("days = %d(%v), want 1(无值点跳过)", len(got), got)
	}
	if got["2026-09-19"] != 200 {
		t.Fatalf("2026-09-19 = %v, want 200(同日多点取最后)", got["2026-09-19"])
	}
}

// TestCoerceCESValuePtrFloat coerceCESValue 对 *float64 强类型数据点的分支
// (BatchListMetricData 响应形态);json.Number 回归由 NAS 单测覆盖,此处补
// map/nil 形态防呆锚点。
func TestCoerceCESValuePtrFloat(t *testing.T) {
	v := 1.5
	if f := coerceCESValue(&v); f == nil || *f != 1.5 {
		t.Fatalf("*float64 形态 = %v, want 1.5", f)
	}
	var nilPtr *float64
	if f := coerceCESValue(nilPtr); f != nil {
		t.Fatalf("nil *float64 应返回 nil, got %v", f)
	}
	if f := coerceCESValue(json.Number("42")); f == nil || *f != 42 {
		t.Fatalf("json.Number 形态 = %v, want 42(47da689 回归)", f)
	}
}

// TestBuildOBSMetricsObjectDailyNil 对象数序列缺失日:ObjectCount=0(与容量独立上报)。
func TestBuildOBSMetricsObjectDailyNil(t *testing.T) {
	got := buildOBSMetrics("bkt", []string{"2026-09-18"}, map[string]float64{"2026-09-18": 1073741824}, nil)
	if len(got) != 1 || got[0].StorageSize != 1 || got[0].ObjectCount != 0 {
		t.Fatalf("rows = %+v, want 1 行 StorageSize=1 ObjectCount=0", got)
	}
}

// 注:无钩子的真实 CES 客户端构造不做单测——CES SafeBuild 会自动请求 IAM
// 解析 project id(需真实凭证),真实构造壳由 T1 保留的 oss_probe_manual_test
// 覆盖(env 门控),与 Implementation Notes 口径一致。
