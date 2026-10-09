package nasprobe

import (
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
)

// TestRDSToUsage RDS 实例列表聚合为分布统计行(实例数/存储 GB 双口径)。
// 规格口径:RDSInstance.Storage 单位 GB(types 注释),直加不换算;
// 实例数与存储容量是分布判定的双口径(proposal「实盘 RDS 数/规格按厂商占比(双口径)」)。
func TestRDSToUsage(t *testing.T) {
	instances := []types.RDSInstance{
		{InstanceID: "rm-1", Engine: "mysql", Storage: 100, Memory: 4096},
		{InstanceID: "rm-2", Engine: "postgresql", Storage: 500, Memory: 8192},
		{InstanceID: "rm-3", Engine: "mariadb", Storage: 0},
	}
	usage := RDSToUsage("aliyun", instances)
	if usage.Provider != "aliyun" {
		t.Fatalf("Provider = %s, want aliyun", usage.Provider)
	}
	if usage.Instances != 3 {
		t.Fatalf("Instances = %d, want 3", usage.Instances)
	}
	if usage.CapacityGB != 600 {
		t.Fatalf("CapacityGB = %v, want 600(Storage 单位 GB,直加)", usage.CapacityGB)
	}
	if usage.ZeroCap != 1 {
		t.Fatalf("ZeroCap = %d, want 1(Storage=0 记实盘现状证据)", usage.ZeroCap)
	}
	if usage.UnitAnomaly != "" {
		t.Fatalf("UnitAnomaly = %s, want 空(Storage 本身就是 GB 口径)", usage.UnitAnomaly)
	}

	empty := RDSToUsage("aws", nil)
	if empty.Instances != 0 || empty.CapacityGB != 0 {
		t.Fatalf("空列表应为零值行, got %+v", empty)
	}
}

// TestMemoryPercentFromFreeable AWS 内存使用率换算公式(本任务关键决策点,AC-5):
// memory% = (1 - FreeableMemory/TotalMemory) × 100。
// FreeableMemory 是 CloudWatch AWS/RDS 的「可释放内存」字节数(Minimum/Maximum/
// Average 均可,探测用 Average),TotalMemory 为实例规格总内存字节
// (由 DBInstanceClass 规格表映射,T3 适配器输入)。
func TestMemoryPercentFromFreeable(t *testing.T) {
	const GiB = 1024.0 * 1024.0 * 1024.0
	cases := []struct {
		name     string
		freeable float64
		total    float64
		want     float64
		wantOK   bool
	}{
		{"全空闲(内存全可用)", 8 * GiB, 8 * GiB, 0, true},
		{"全占用(freeable=0)", 0, 8 * GiB, 100, true},
		{"半占用", 4 * GiB, 8 * GiB, 50, true},
		{"典型负载(75% 占用)", 2 * GiB, 8 * GiB, 75, true},
		{"总内存非法(0)", 2 * GiB, 0, 0, false},
		{"总内存负数", 2 * GiB, -8 * GiB, 0, false},
		{"freeable 为负", -1, 8 * GiB, 0, false},
		{"freeable 超总内存", 9 * GiB, 8 * GiB, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := MemoryPercentFromFreeable(c.freeable, c.total)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if diff := got - c.want; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("memory%% = %v, want %v", got, c.want)
			}
			if !CheckUsagePercentRange(got) {
				t.Fatalf("memory%% = %v 越出 0~100 归一口径", got)
			}
		})
	}
}

// TestMemoryPercentFromUsed 直接口径内存使用率换算(used/total,AC-5):
// 适配「厂商直接给已用内存字节」的形态(huawei mem_usedPercent 为百分比直给,
// tencent MemoryUsage 为百分比直给;本函数覆盖字节型 used/total 兜底)。
func TestMemoryPercentFromUsed(t *testing.T) {
	const GiB = 1024.0 * 1024.0 * 1024.0
	cases := []struct {
		name   string
		used   float64
		total  float64
		want   float64
		wantOK bool
	}{
		{"零占用", 0, 8 * GiB, 0, true},
		{"全占用", 8 * GiB, 8 * GiB, 100, true},
		{"半占用", 4 * GiB, 8 * GiB, 50, true},
		{"总内存非法(0)", 2 * GiB, 0, 0, false},
		{"used 为负", -1, 8 * GiB, 0, false},
		{"used 超总内存", 9 * GiB, 8 * GiB, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := MemoryPercentFromUsed(c.used, c.total)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if diff := got - c.want; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("memory%% = %v, want %v", got, c.want)
			}
			if !CheckUsagePercentRange(got) {
				t.Fatalf("memory%% = %v 越出 0~100 归一口径", got)
			}
		})
	}
}
