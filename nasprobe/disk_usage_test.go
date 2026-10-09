package nasprobe

import (
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
)

// TestDisksToUsage 磁盘列表聚合为分布统计行(数量/容量 GB 双口径)。
func TestDisksToUsage(t *testing.T) {
	disks := []types.DiskInstance{
		{DiskID: "d-1", Size: 40, Status: "In_use", Region: "cn-hangzhou"},
		{DiskID: "d-2", Size: 200, Status: "In_use", Region: "cn-hangzhou"},
		{DiskID: "d-3", Size: 0, Status: "Available", Region: "cn-beijing"},
	}
	usage := DisksToUsage("aliyun", disks)
	if usage.Provider != "aliyun" {
		t.Fatalf("Provider = %s, want aliyun", usage.Provider)
	}
	if usage.Instances != 3 {
		t.Fatalf("Instances = %d, want 3", usage.Instances)
	}
	if usage.CapacityGB != 240 {
		t.Fatalf("CapacityGB = %v, want 240(disk Size 单位为 GB,直加)", usage.CapacityGB)
	}
	if usage.ZeroCap != 1 {
		t.Fatalf("ZeroCap = %d, want 1(size=0 记实盘现状证据)", usage.ZeroCap)
	}
	if usage.UnitAnomaly != "" {
		t.Fatalf("UnitAnomaly = %s, want 空(Size 本身就是 GB 口径)", usage.UnitAnomaly)
	}

	empty := DisksToUsage("aws", nil)
	if empty.Instances != 0 || empty.CapacityGB != 0 {
		t.Fatalf("空列表应为零值行, got %+v", empty)
	}
}

// TestDiskUsagePercentFromIdle AWS 使用率派生公式(本任务关键决策点):
// busy% = (1 - idle/window) × 100,窗口内非 idle 时间占比。
func TestDiskUsagePercentFromIdle(t *testing.T) {
	cases := []struct {
		name   string
		idle   float64
		window float64
		want   float64
		wantOK bool
	}{
		{"全忙(全程读写)", 0, 300, 100, true},
		{"全闲(整窗 idle)", 300, 300, 0, true},
		{"一半忙", 150, 300, 50, true},
		{"近零 idle(约 99.7% 忙)", 1, 300, 99.66666666666667, true},
		{"窗口非法(0)", 0, 0, 0, false},
		{"窗口负数", 0, -10, 0, false},
		{"idle 为负", -1, 300, 0, false},
		{"idle 超窗", 301, 300, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := DiskUsagePercentFromIdle(c.idle, c.window)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if diff := got - c.want; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("usage%% = %v, want %v", got, c.want)
			}
		})
	}
}

// TestCheckUsagePercentRange 使用率 0~100 范围自检(归一口径门禁)。
func TestCheckUsagePercentRange(t *testing.T) {
	valid := []float64{0, 1.5, 50, 99.99, 100}
	for _, v := range valid {
		if !CheckUsagePercentRange(v) {
			t.Fatalf("CheckUsagePercentRange(%v) = false, want true", v)
		}
	}
	invalid := []float64{-0.01, -100, 100.01, 1e9}
	for _, v := range invalid {
		if CheckUsagePercentRange(v) {
			t.Fatalf("CheckUsagePercentRange(%v) = true, want false", v)
		}
	}
}
