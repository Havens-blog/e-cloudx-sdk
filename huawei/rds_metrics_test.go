package huawei

import (
	"testing"

	cesv1model "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1/model"
)

// 维度键与连接指标按引擎分派(探测定案):postgresql → postgresql_cluster_id +
// rds042_database_connections;其余(MySQL/空值兜底)→ rds_cluster_id +
// rds006_conn_count。
func TestDispatchHuaweiRDSDimAndConn(t *testing.T) {
	cases := []struct {
		engine string
		dim    string
		conn   string
	}{
		{"postgresql", rdsCESDimPostgreSQL, rdsCESMetricConnPG},
		{"PostgreSQL", rdsCESDimPostgreSQL, rdsCESMetricConnPG},
		{"Postgres", rdsCESDimPostgreSQL, rdsCESMetricConnPG},
		{"mysql", rdsCESDimMySQL, rdsCESMetricConnMy},
		{"MySQL", rdsCESDimMySQL, rdsCESMetricConnMy},
		{"", rdsCESDimMySQL, rdsCESMetricConnMy},
	}
	for _, c := range cases {
		dim, conn := dispatchHuaweiRDSDimAndConn(c.engine)
		if dim != c.dim || conn != c.conn {
			t.Fatalf("engine=%q dispatch=(%s,%s), want (%s,%s)", c.engine, dim, conn, c.dim, c.conn)
		}
	}
}

// 批量查询体:namespace SYS.RDS、四指标同维度、维度值=实例ID。
func TestRDSCESMetricCandidates(t *testing.T) {
	candidates := rdsCESMetricCandidates("rds-1", rdsCESDimMySQL, rdsCESMetricConnMy)
	if len(candidates) != 4 {
		t.Fatalf("candidates len=%d, want 4", len(candidates))
	}
	wantNames := []string{rdsCESMetricCPU, rdsCESMetricMemory, rdsCESMetricDisk, rdsCESMetricConnMy}
	for i, m := range candidates {
		if m.Namespace != rdsCESNamespace {
			t.Fatalf("ns=%q, want %q", m.Namespace, rdsCESNamespace)
		}
		if m.MetricName != wantNames[i] {
			t.Fatalf("metric[%d]=%q, want %q", i, m.MetricName, wantNames[i])
		}
		if len(m.Dimensions) != 1 || m.Dimensions[0].Name != rdsCESDimMySQL || m.Dimensions[0].Value != "rds-1" {
			t.Fatalf("dims[%d]=%+v, 维度键按引擎分派且值=实例ID", i, m.Dimensions)
		}
	}
}

// 按 MetricName 取日值:无匹配/无数据点返回空 map(全部无数据 → 空行不触发失败)。
func TestRDSCESDailyByMetric(t *testing.T) {
	dp := cesv1model.DatapointForBatchMetric{
		Timestamp: dayStartUnixMilli("2026-09-01"),
		Average:   float64Ptr(42.5),
	}
	metrics := []cesv1model.BatchMetricData{
		{MetricName: rdsCESMetricCPU, Datapoints: []cesv1model.DatapointForBatchMetric{dp}},
		{MetricName: rdsCESMetricMemory, Datapoints: []cesv1model.DatapointForBatchMetric{}},
	}
	if got := rdsCESDailyByMetric(metrics, rdsCESMetricCPU); got == nil || got["2026-09-01"] != 42.5 {
		t.Fatalf("cpu daily=%v, want 09-01=42.5", got)
	}
	if got := rdsCESDailyByMetric(metrics, rdsCESMetricMemory); got != nil {
		t.Fatalf("无数据点指标应返回空 map(nil), got %v", got)
	}
	if got := rdsCESDailyByMetric(metrics, rdsCESMetricDisk); got != nil {
		t.Fatalf("未请求指标应返回空, got %v", got)
	}
}

func float64Ptr(v float64) *float64 { return &v }

// 行组装:缺失日跳过、% 直给、连接数取整、engine/provider 透传。
func TestBuildHuaweiRDSMetrics(t *testing.T) {
	dates := []string{"2026-09-01", "2026-09-02"}
	rows := buildHuaweiRDSMetrics(
		"rds-1", "pg-db", "PostgreSQL", dates,
		map[string]float64{"2026-09-01": 3.06, "2026-09-02": 2.98}, // cpu %
		map[string]float64{"2026-09-01": 40.5},                     // mem %
		map[string]float64{},                                       // disk(缺失)
		map[string]float64{"2026-09-01": 7.985, "2026-09-02": 0},   // conn count
	)
	if len(rows) != 2 {
		t.Fatalf("rows len=%d, want 2", len(rows))
	}
	if rows[0].CPUPercent != 3.06 || rows[0].MemoryPercent != 40.5 || rows[0].DiskPercent != 0 || rows[0].Connections != 8 {
		t.Fatalf("row0=%+v(disk 缺失留 0,conn 7.985 取整 8)", rows[0])
	}
	if rows[1].CPUPercent != 2.98 || rows[1].MemoryPercent != 0 || rows[1].Connections != 0 {
		t.Fatalf("row1=%+v(mem/disk 缺失留 0)", rows[1])
	}
	if rows[0].Engine != "PostgreSQL" || rows[0].Provider != "huawei" || rows[0].RdsID != "rds-1" {
		t.Fatalf("row0 meta=%+v", rows[0])
	}
}
