package aliyun

import (
	"testing"
)

// 引擎分派(探测定案前缀表):SQLServer 走引擎专属名且无实例级连接指标;
// 其余引擎(MySQL/PG/MariaDB/空值兜底)走无前缀通用名。
func TestDispatchAliyunRDSMetricNames(t *testing.T) {
	cases := []struct {
		engine string
		cpu    string
		mem    string
		disk   string
		conn   string
	}{
		{"sqlserver", rdsMetricSQLServerCPU, rdsMetricSQLServerMemory, rdsMetricSQLServerDisk, ""},
		{"SQLServer", rdsMetricSQLServerCPU, rdsMetricSQLServerMemory, rdsMetricSQLServerDisk, ""},
		{"mysql", rdsMetricCPU, rdsMetricMemory, rdsMetricDisk, rdsMetricConnectionUsage},
		{"MySQL", rdsMetricCPU, rdsMetricMemory, rdsMetricDisk, rdsMetricConnectionUsage},
		{"postgresql", rdsMetricCPU, rdsMetricMemory, rdsMetricDisk, rdsMetricConnectionUsage},
		{"mariadb", rdsMetricCPU, rdsMetricMemory, rdsMetricDisk, rdsMetricConnectionUsage},
		{"", rdsMetricCPU, rdsMetricMemory, rdsMetricDisk, rdsMetricConnectionUsage},
	}
	for _, c := range cases {
		cpu, mem, disk, conn := dispatchAliyunRDSMetricNames(c.engine)
		if cpu != c.cpu || mem != c.mem || disk != c.disk || conn != c.conn {
			t.Fatalf("engine=%q dispatch=(%s,%s,%s,%s), want (%s,%s,%s,%s)",
				c.engine, cpu, mem, disk, conn, c.cpu, c.mem, c.disk, c.conn)
		}
	}
}

// ConnectionUsage(%) → 绝对值换算:connections = round(usage% × MaxConnections/100)
func TestConvertAliyunConnectionUsage(t *testing.T) {
	usage := map[string]float64{
		"2026-09-01": 46.7,
		"2026-09-02": 0.0,
		"2026-09-03": 99.995,
	}
	got := convertAliyunConnectionUsage(usage, 5000)
	want := map[string]float64{
		"2026-09-01": 2335,
		"2026-09-02": 0,
		"2026-09-03": 5000,
	}
	for d, w := range want {
		if got[d] != w {
			t.Fatalf("conn[%s]=%v, want %v", d, got[d], w)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("conn daily len=%d, want %d", len(got), len(want))
	}
}

// 行组装:四指标均无数据点的日期跳过、缺失指标留 0(不填假值)、连接数取整、
// engine/provider 透传留痕;仅连接有数据点的日期保留(任一指标有数据即出该日行)。
func TestBuildAliyunRDSMetrics(t *testing.T) {
	dates := []string{"2026-09-01", "2026-09-02", "2026-09-03"}
	rows := buildAliyunRDSMetrics(
		"rm-abc", "web-db", "MySQL", dates,
		map[string]float64{"2026-09-01": 12.3, "2026-09-02": 11.1}, // cpu
		map[string]float64{"2026-09-01": 45.6},                     // mem
		map[string]float64{"2026-09-01": 0, "2026-09-02": 0},       // disk(真实 0 数据点)
		map[string]float64{"2026-09-01": 46.7, "2026-09-03": 21},   // conn
	)
	if len(rows) != 3 {
		t.Fatalf("rows len=%d, want 3(09-03 仅连接有数据点,应保留)", len(rows))
	}
	r0 := rows[0]
	if r0.Date != "2026-09-01" || r0.RdsID != "rm-abc" || r0.InstanceName != "web-db" || r0.Engine != "MySQL" || r0.Provider != "aliyun" {
		t.Fatalf("row0 meta = %+v", r0)
	}
	if r0.CPUPercent != 12.3 || r0.MemoryPercent != 45.6 || r0.DiskPercent != 0 || r0.Connections != 47 {
		t.Fatalf("row0 values = %+v(conn 46.7 应取整为 47,disk 真实 0 保留)", r0)
	}
	r1 := rows[1]
	if r1.CPUPercent != 11.1 || r1.MemoryPercent != 0 || r1.DiskPercent != 0 || r1.Connections != 0 {
		t.Fatalf("row1 values = %+v(mem 缺失日留 0,不填假值)", r1)
	}
	r2 := rows[2]
	if r2.Date != "2026-09-03" || r2.CPUPercent != 0 || r2.MemoryPercent != 0 || r2.DiskPercent != 0 || r2.Connections != 21 {
		t.Fatalf("row2 values = %+v(仅连接有数据点日保留,其余 0)", r2)
	}
	if rows[0].QcStatus != "" || rows[0].AccountID != 0 {
		t.Fatalf("适配器不越权打标/回填账号: %+v", rows[0])
	}
}

// 行组装·全缺失日:四指标均无数据点的日期必须跳过,不出行
func TestBuildAliyunRDSMetrics_SkipsAllMissingDay(t *testing.T) {
	dates := []string{"2026-09-01", "2026-09-02"}
	rows := buildAliyunRDSMetrics(
		"rm-abc", "web-db", "MySQL", dates,
		map[string]float64{"2026-09-01": 12.3},
		map[string]float64{"2026-09-01": 45.6},
		map[string]float64{"2026-09-01": 7.8},
		map[string]float64{},
	)
	if len(rows) != 1 || rows[0].Date != "2026-09-01" {
		t.Fatalf("四指标均无数据点的日期应跳过: rows=%+v", rows)
	}
}
