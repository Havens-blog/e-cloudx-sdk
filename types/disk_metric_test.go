package types

import (
	"encoding/json"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

// DiskMetric 落库字段形状冻结(T2 AC-1):disk_id/date/usage_percent/usage_scope/
// iops/throughput/qc_status 为落库语义字段;时延(读/写时延、P99)与派生
// utilization 均不落库(Hard Rule:时延留二期 proposal Out of Scope;无容量字段
// 故无派生 utilization 项),模型层不得携带。
func TestDiskMetricBsonShape(t *testing.T) {
	raw, err := bson.Marshal(DiskMetric{
		DiskID: "d-wz95rqmk", DiskName: "prod-data-01", Date: "2026-09-19",
		UsagePercent: 60.693, UsageScope: DiskUsageScopeInstanceLevel,
		IOPS: 8.256, Throughput: 0.117,
		QcStatus: DiskMetricQcZeroException, AccountID: 1, Provider: "aliyun",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var doc map[string]any
	if err := bson.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"disk_id", "disk_name", "date", "usage_percent", "usage_scope", "iops", "throughput", "qc_status", "account_id", "provider"} {
		if _, ok := doc[key]; !ok {
			t.Fatalf("DiskMetric 缺少落库字段 %q, doc=%v", key, doc)
		}
	}
	for _, key := range []string{"latency", "read_latency", "write_latency", "p99_latency", "utilization", "capacity", "used_capacity"} {
		if _, ok := doc[key]; ok {
			t.Fatalf("DiskMetric 不得携带 %q 字段(Hard Rule:时延不落库/无容量字段)", key)
		}
	}
	// usage_percent/iops/throughput 须为 float64(探测实测小数精度,如 60.693%/8.256 次/s)
	if doc["usage_percent"] != 60.693 {
		t.Fatalf("usage_percent = %v(%T), want float64 60.693", doc["usage_percent"], doc["usage_percent"])
	}
	if doc["iops"] != 8.256 {
		t.Fatalf("iops = %v(%T), want float64 8.256", doc["iops"], doc["iops"])
	}
	// account_id 须为 int64(云账号 ID 整数语义,唯一键组成部分)
	if doc["account_id"] != int64(1) {
		t.Fatalf("account_id = %v(%T), want int64 1", doc["account_id"], doc["account_id"])
	}
}

// DiskMetric JSON 往返(roundtrip):前端趋势/Top 接口读取侧契约,
// json tag 漂移会破坏 API 字段命名。
func TestDiskMetricJSONRoundtrip(t *testing.T) {
	in := DiskMetric{
		DiskID: "vol-00ddfd59", DiskName: "ebs-0", Date: "2026-09-19",
		UsagePercent: 19.47, UsageScope: DiskUsageScopeBusyShare,
		IOPS: 12.5, Throughput: 3.2,
		AccountID: 7, Provider: "aws",
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out DiskMetric
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("roundtrip mismatch: got %+v, want %+v", out, in)
	}
}

// qc_status 取值冻结:zero_exception 是读取侧闭环(T8 data_status 映射)与
// 前端异常渲染的契约字面量,漂移会让异常行被当正常零使用率。
// 取值与 NAS/OSS 共用同一字面量(复用 NASMetricQc* 常量作单一来源)。
func TestDiskMetricQcStatusValues(t *testing.T) {
	if DiskMetricQcOK != "" {
		t.Fatalf("DiskMetricQcOK = %q, want 空串(正常数据零值)", DiskMetricQcOK)
	}
	if DiskMetricQcZeroException != "zero_exception" {
		t.Fatalf("DiskMetricQcZeroException = %q, want zero_exception", DiskMetricQcZeroException)
	}
}

// usage_scope 口径标注取值冻结(T1 探测定案,probe-report §2/遗留行动 #3):
// instance_level(挂载实例视角)/ busy_share(忙闲占比)/ cloud_disk_level(预留),
// 字面量漂移会让前端口径区分呈现失效,厂商间数值不可横比的前提即标注准确。
func TestDiskMetricUsageScopeValues(t *testing.T) {
	cases := map[string]string{
		DiskUsageScopeCloudDiskLevel: "cloud_disk_level",
		DiskUsageScopeInstanceLevel:  "instance_level",
		DiskUsageScopeBusyShare:      "busy_share",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("usage_scope 字面量漂移: got %q, want %q", got, want)
		}
	}
}
