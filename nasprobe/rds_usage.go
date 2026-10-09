// RDS 探测纯逻辑(rds-ops-insight M1 探测任务)。
//
// 规格:docs/proposals/rds-ops-insight/proposal.md
// 「必达厂商选择依据」「单位归一化(百分比 0~100,内存 FreeableMemory 换算)」
// 「Key Risks(内存口径差异/多引擎口径)」。
// 复用本包通用分布工具(ProviderUsage/AggregateDistribution/Verdict/
// CheckUsagePercentRange),仅新增 RDS 语义的分布聚合与内存换算;网络探测在
// <provider>/rds_probe_manual_test.go 与本包 rds_distribution_manual_test.go
// (env 门控,只读)。
package nasprobe

import (
	"github.com/Havens-blog/e-cloudx-sdk/types"
)

// RDSToUsage 单厂商实盘 RDS 实例列表聚合为分布统计行。
// Instances 承载实例数(分布双口径之一);RDSInstance.Storage 单位为 GB
// (types.RDSInstance 注释口径),聚合直加进 CapacityGB,不做字节换算
// (Hard Rule:字节不进 GB 字段——此处无字节,天然合规);
// Storage=0 记入 ZeroCap(实盘现状证据,升格判定输入)。
func RDSToUsage(provider string, instances []types.RDSInstance) ProviderUsage {
	usage := ProviderUsage{Provider: provider}
	for _, r := range instances {
		usage.Instances++
		if r.Storage == 0 {
			usage.ZeroCap++
		}
		usage.CapacityGB += float64(r.Storage)
	}
	return usage
}

// MemoryPercentFromFreeable AWS 内存使用率换算公式(M1 关键决策点,AC-5):
// memory% = (1 - FreeableMemory/TotalMemory) × 100。
// FreeableMemory 是 CloudWatch AWS/RDS 的「可释放内存」字节数(Average 统计),
// TotalMemory 为实例规格总内存字节(由 DBInstanceClass 规格表映射,探测验证
// 公式数值合理性,T3 适配器固化映射)。
// 约束:total>0 且 0≤freeable≤total 才可计算(否则返回 false,不产生 NaN/Inf,
// 与 DiskUsagePercentFromIdle「不产生 NaN」同一精神)。
func MemoryPercentFromFreeable(freeable, total float64) (float64, bool) {
	if total <= 0 || freeable < 0 || freeable > total {
		return 0, false
	}
	return (1 - freeable/total) * 100, true
}

// MemoryPercentFromUsed 直接口径内存使用率换算(used/total,AC-5):
// 适配「厂商给已用内存字节」的形态;厂商直接给百分比使用率的
// (aliyun MemoryUsage/huawei mem_usedPercent/tencent MemoryUsage)
// 走 CheckUsagePercentRange 门禁直接归一,不经本函数。
// 约束:total>0 且 0≤used≤total 才可计算(不产生 NaN/Inf)。
func MemoryPercentFromUsed(used, total float64) (float64, bool) {
	if total <= 0 || used < 0 || used > total {
		return 0, false
	}
	return used / total * 100, true
}
