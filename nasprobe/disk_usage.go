// Disk 探测纯逻辑(disk-ops-insight M1 探测任务)。
//
// 规格:docs/proposals/disk-ops-insight/proposal.md
// 「必达厂商选择依据」「使用率口径归一(百分比 0~100)」「Key Risks(AWS 无直接
// 使用率需从 VolumeIdleTime 派生)」。
// 复用本包通用分布工具(ProviderUsage/AggregateDistribution/Verdict),
// 仅新增 Disk 语义的分布聚合与使用率派生;网络探测在
// <provider>/disk_probe_manual_test.go 与本包 disk_distribution_manual_test.go
// (env 门控,只读)。
package nasprobe

import (
	"github.com/Havens-blog/e-cloudx-sdk/types"
)

// DisksToUsage 单厂商实盘云硬盘列表聚合为分布统计行。
// Instances 承载磁盘数(分布双口径之一);DiskInstance.Size 单位为 GB
// (types.DiskInstance 注释口径),聚合直加进 CapacityGB,不做字节换算
// (Hard Rule:字节不进 GB 字段——此处无字节,天然合规);
// Size=0 记入 ZeroCap(实盘现状证据,升格判定输入)。
func DisksToUsage(provider string, disks []types.DiskInstance) ProviderUsage {
	usage := ProviderUsage{Provider: provider}
	for _, d := range disks {
		usage.Instances++
		if d.Size == 0 {
			usage.ZeroCap++
		}
		usage.CapacityGB += float64(d.Size)
	}
	return usage
}

// DiskUsagePercentFromIdle AWS 磁盘使用率派生公式(M1 关键决策点):
// 使用率 ≈ (1 - VolumeIdleTime/窗口秒数) × 100,即窗口内非空闲时间占比。
// VolumeIdleTime 是 CloudWatch AWS/EBS 对指定统计周期内无读写请求的秒数
// (Sum 统计),窗口 = 统计周期秒数;磁盘"繁忙占比"是使用率的可观测代理,
// 与容量型使用率(空间水位)语义不同,探测报告须分开记录两种口径。
// 约束:window>0 且 0≤idle≤window 才可计算(否则返回 false,不产生 NaN/Inf,
// 与 AggregateDistribution「不产生 NaN」同一精神)。
func DiskUsagePercentFromIdle(idleSeconds, windowSeconds float64) (float64, bool) {
	if windowSeconds <= 0 || idleSeconds < 0 || idleSeconds > windowSeconds {
		return 0, false
	}
	return (1 - idleSeconds/windowSeconds) * 100, true
}

// usagePercentMax 使用率归一口径上界(0~100,proposal「usage_percent 为磁盘
// 使用率 0~100 或厂商百分比口径归一」)。
const usagePercentMax = 100.0

// CheckUsagePercentRange 使用率 0~100 范围自检(归一口径门禁):
// 探测/采集边界的厂商百分比须落在 [0,100],越界视为口径异常。
func CheckUsagePercentRange(v float64) bool {
	return v >= 0 && v <= usagePercentMax
}
