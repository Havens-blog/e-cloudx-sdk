package aws

import (
	"testing"
)

// DBInstanceClass → 总内存规格表关键锚点(实盘验证值 + 跨系代表值)。
func TestRDSClassTotalMemoryGiB(t *testing.T) {
	cases := map[string]int64{
		"db.m7g.large":    8, // 实盘 5/5 验证值(probe-report §1.3)
		"db.t3.micro":     1,
		"db.m5.xlarge":    16,
		"db.r5.2xlarge":   64,
		"db.r7g.24xlarge": 768,
	}
	for class, want := range cases {
		got, ok := rdsClassTotalMemoryGiB[class]
		if !ok || got != want {
			t.Fatalf("class=%s = (%d,%v), want (%d,true)", class, got, ok, want)
		}
	}
	if _, ok := rdsClassTotalMemoryGiB["db.serverless"]; ok {
		t.Fatal("未定案规格不应在表内(换算输入不可靠必须留 0+warn)")
	}
}

// 四指标查询体:namespace AWS/RDS、维度 DBInstanceIdentifier、Average 日粒度。
func TestRDSCWMetricQueries(t *testing.T) {
	queries := rdsCWMetricQueries("db-abc")
	if len(queries) != 4 {
		t.Fatalf("queries len=%d, want 4", len(queries))
	}
	wantIDs := []string{"cpu", "mem_freeable", "disk_free", "connections"}
	wantNames := []string{rdsCWMetricCPU, rdsCWMetricFreeableMemory, rdsCWMetricFreeStorageSpace, rdsCWMetricDatabaseConnections}
	for i, q := range queries {
		if *q.Id != wantIDs[i] {
			t.Fatalf("id[%d]=%s, want %s", i, *q.Id, wantIDs[i])
		}
		ms := q.MetricStat
		if ms == nil || ms.Metric == nil || *ms.Metric.Namespace != rdsCWMetricNamespace || *ms.Metric.MetricName != wantNames[i] {
			t.Fatalf("query[%d] namespace/name = %+v", i, ms)
		}
		if *ms.Period != rdsCWDailyPeriod || *ms.Stat != "Average" {
			t.Fatalf("query[%d] period/stat = %d/%s, want 86400/Average", i, *ms.Period, *ms.Stat)
		}
		dims := ms.Metric.Dimensions
		if len(dims) != 1 || *dims[0].Name != rdsCWDimInstanceID || *dims[0].Value != "db-abc" {
			t.Fatalf("query[%d] dims=%+v", i, dims)
		}
	}
}

// 行组装与换算:
//   - memory% = (Total-Freeable)/Total(m7g.large=8GiB,Freeable=2GiB → 75%);
//   - disk% = Used/Total(AllocatedStorage=100GiB,Free=90GiB → 10%);
//   - 规格表未命中(okMem=false)内存留 0;分母<=0 磁盘留 0;换算越界留 0;
//   - 缺失日跳过、CPU/连接数直给取整、engine/provider 透传。
func TestBuildAWSRDSMetrics(t *testing.T) {
	const gib = float64(1024 * 1024 * 1024)
	dates := []string{"2026-09-01", "2026-09-02", "2026-09-03"}
	rows := buildAWSRDSMetrics(
		"db-abc", "app-db", "mysql", dates,
		map[string]float64{"2026-09-01": 21.4, "2026-09-02": 22.8},          // cpu %
		map[string]float64{"2026-09-01": 2 * gib, "2026-09-02": 2 * gib},    // freeable bytes
		map[string]float64{"2026-09-01": 90 * gib, "2026-09-02": 100 * gib}, // free storage bytes
		map[string]float64{"2026-09-01": 33.4, "2026-09-02": 0},             // conn count
		true, 8, 100,
	)
	if len(rows) != 2 {
		t.Fatalf("rows len=%d, want 2(09-03 四指标均无数据点跳过)", len(rows))
	}
	r0 := rows[0]
	if r0.CPUPercent != 21.4 || r0.MemoryPercent != 75 || r0.DiskPercent != 10 || r0.Connections != 33 {
		t.Fatalf("row0=%+v(mem 75/disk 10/conn 33)", r0)
	}
	r1 := rows[1]
	if r1.DiskPercent != 0 || r1.Connections != 0 || r1.MemoryPercent != 75 {
		t.Fatalf("row1=%+v(disk 全空闲=0,conn 真实 0 保留)", r1)
	}
	if r0.Engine != "mysql" || r0.Provider != "aws" || r0.RdsID != "db-abc" || r0.InstanceName != "app-db" {
		t.Fatalf("row0 meta=%+v", r0)
	}

	// 内存规格未命中:内存留 0(换算输入不可靠不伪造),其余指标不受影响
	rowsNoMem := buildAWSRDSMetrics("db-x", "x", "mysql", dates,
		map[string]float64{"2026-09-01": 10},
		map[string]float64{"2026-09-01": 1},
		map[string]float64{"2026-09-01": 10},
		map[string]float64{"2026-09-01": 5},
		false, 0, 100)
	if len(rowsNoMem) != 1 || rowsNoMem[0].MemoryPercent != 0 {
		t.Fatalf("okMem=false 内存应留 0: %+v", rowsNoMem)
	}

	// 分母 <=0:磁盘留 0
	rowsNoDisk := buildAWSRDSMetrics("db-x", "x", "mysql", dates,
		map[string]float64{"2026-09-01": 10},
		map[string]float64{"2026-09-01": 1},
		map[string]float64{"2026-09-01": 10},
		map[string]float64{"2026-09-01": 5},
		true, 8, 0)
	if len(rowsNoDisk) != 1 || rowsNoDisk[0].DiskPercent != 0 {
		t.Fatalf("storageGiB<=0 磁盘应留 0: %+v", rowsNoDisk)
	}

	// 换算越界(Freeable > Total):nasprobe 返回 !ok → 内存留 0,不接受负值
	rowsOver := buildAWSRDSMetrics("db-x", "x", "mysql", dates,
		map[string]float64{"2026-09-01": 10},
		map[string]float64{"2026-09-01": 9 * gib}, // 超过 8GiB 总量
		map[string]float64{"2026-09-01": 10},
		map[string]float64{"2026-09-01": 5},
		true, 8, 100)
	if len(rowsOver) != 1 || rowsOver[0].MemoryPercent != 0 {
		t.Fatalf("换算越界内存应留 0: %+v", rowsOver)
	}
}
