package types

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

// NASMetric 落库字段形状冻结(T2 AC-1):fs_id/date/capacity(GB)/used_capacity(GB)/qc_status
// 为落库语义字段;utilization 不落库(Hard Rule:避免重采时 capacity/used/utilization
// 三字段不一致,读取时由 capacity/used_capacity 派生),模型层不得携带该字段。
func TestNASMetricBsonShape(t *testing.T) {
	raw, err := bson.Marshal(NASMetric{
		FsID: "fs-1", FsName: "jlc-fat-sfs-turbo", Date: "2026-09-19",
		Capacity: 2457.32, UsedCapacity: 1378.14,
		QcStatus: NASMetricQcZeroException, AccountID: 1, Provider: "huawei",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var doc map[string]any
	if err := bson.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"fs_id", "fs_name", "date", "capacity", "used_capacity", "qc_status", "account_id", "provider"} {
		if _, ok := doc[key]; !ok {
			t.Fatalf("NASMetric 缺少落库字段 %q, doc=%v", key, doc)
		}
	}
	if _, ok := doc["utilization"]; ok {
		t.Fatal("NASMetric 不得携带 utilization 字段(utilization 读取时派生,不落库)")
	}
	// capacity 须为数值型(GB,二进制 GiB):数量级自检下界 1MB(=1/1024 GiB)与
	// 探测实测值(如 1378.14 GB)需要小数精度,不得收敛成整数
	if doc["capacity"] != 2457.32 {
		t.Fatalf("capacity = %v(%T), want float64 2457.32", doc["capacity"], doc["capacity"])
	}
}

// qc_status 取值冻结:zero_exception 是读取侧闭环(T10 data_status 映射)与
// 前端异常渲染的契约字面量,漂移会让异常行被当正常零容量。
func TestNASMetricQcStatusValues(t *testing.T) {
	if NASMetricQcOK != "" {
		t.Fatalf("NASMetricQcOK = %q, want 空串(正常数据零值)", NASMetricQcOK)
	}
	if NASMetricQcZeroException != "zero_exception" {
		t.Fatalf("NASMetricQcZeroException = %q, want zero_exception", NASMetricQcZeroException)
	}
}
