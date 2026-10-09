package types

import (
	"encoding/json"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

// RDSMetric 序列化契约(T2 AC-1):bson/json tag 与落库字段一一对应,字段漂移
// 会让 DAO 显式 bson 文档与模型解码互相错位。与 DiskMetric 测试同型。
func TestRDSMetricSerialization(t *testing.T) {
	m := RDSMetric{
		RdsID:         "pgx-um5ha3g9000", // huawei 实盘形态
		InstanceName:  "订单库主实例",
		Date:          "2026-09-21",
		CPUPercent:    15.7,
		MemoryPercent: 82.06,
		DiskPercent:   33.5,
		Connections:   128,
		Engine:        "postgresql",
		QcStatus:      RDSMetricQcOK,
		AccountID:     42,
		Provider:      "huawei",
	}

	// bson 往返:字段名与 bson tag 一致(DAO 显式 bson 文档与模型解码互证)
	bsonData, err := bson.Marshal(m)
	if err != nil {
		t.Fatalf("bson marshal: %v", err)
	}
	var rawBson bson.M
	if err := bson.Unmarshal(bsonData, &rawBson); err != nil {
		t.Fatalf("bson unmarshal: %v", err)
	}
	wantBson := []string{
		"rds_id", "instance_name", "date",
		"cpu_percent", "memory_percent", "disk_percent", "connections",
		"engine", "qc_status", "account_id", "provider",
	}
	if len(rawBson) != len(wantBson) {
		t.Fatalf("bson 字段数 = %d, want %d: %v", len(rawBson), len(wantBson), rawBson)
	}
	for _, k := range wantBson {
		if _, ok := rawBson[k]; !ok {
			t.Fatalf("缺少 bson 字段 %q, got %v", k, rawBson)
		}
	}
	var back RDSMetric
	if err := bson.Unmarshal(bsonData, &back); err != nil {
		t.Fatalf("bson decode: %v", err)
	}
	if back != m {
		t.Fatalf("bson roundtrip mismatch:\n got %+v\nwant %+v", back, m)
	}

	// json tag 精确值(读取接口 T8 按这些名字出参)
	jsonData, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	var rawJSON map[string]any
	if err := json.Unmarshal(jsonData, &rawJSON); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if len(rawJSON) != len(wantBson) {
		t.Fatalf("json 字段数 = %d, want %d: %v", len(rawJSON), len(wantBson), rawJSON)
	}
	for _, k := range wantBson {
		if _, ok := rawJSON[k]; !ok {
			t.Fatalf("缺少 json 字段 %q", k)
		}
	}
}

// qc_status 字面量单一来源(T2 AC-1):复用 NASMetricQc* 常量,与 NAS/OSS/Disk
// 同一套字面量——漂移会让 T8 读取侧把异常行当正常零负载。
func TestRDSMetricQcConstants(t *testing.T) {
	if RDSMetricQcOK != "" {
		t.Fatalf("RDSMetricQcOK = %q, want 空串(正常数据零值)", RDSMetricQcOK)
	}
	if RDSMetricQcZeroException != "zero_exception" {
		t.Fatalf("RDSMetricQcZeroException = %q, want zero_exception", RDSMetricQcZeroException)
	}
}
