// OSS 探测纯逻辑(oss-ops-insight M1 探测任务)。
//
// 规格:docs/proposals/oss-ops-insight/proposal.md
// 「必达厂商选择依据」「Urgency(近 30 天高增长 bucket 近失证据)」。
// 复用本包通用分布工具(ProviderUsage/AggregateDistribution/Verdict/BytesToGB),
// 仅新增 OSS bucket 语义的聚合与增速计算;网络探测在 <provider>/oss_probe_manual_test.go
// 与本包 oss_distribution_manual_test.go(env 门控,只读)。
package nasprobe

import (
	"github.com/Havens-blog/e-cloudx-sdk/types"
)

// OSSBucketsToUsage 单厂商实盘 OSS bucket 列表聚合为分布统计行。
// Instances 字段承载 bucket 数(双口径之一);StorageSize 为字节,
// 聚合边界经 BytesToGB 换算 GB(Hard Rule:字节不进 GB 字段);
// StorageSize=0 记入 ZeroCap(实盘现状证据,升格判定输入)。
func OSSBucketsToUsage(provider string, buckets []types.OSSBucket) ProviderUsage {
	usage := ProviderUsage{Provider: provider}
	for _, b := range buckets {
		usage.Instances++
		if b.StorageSize == 0 {
			usage.ZeroCap++
		}
		usage.CapacityGB += BytesToGB(float64(b.StorageSize))
	}
	return usage
}

// ossHighGrowthThresholdPercent 高增长 bucket 判定阈值(严格大于)。
// proposal Urgency 口径为「近 30 天存储量增速 > X%」而未定 X;
// 探测取 30% 作为近失证据门槛(存储量月增 30% 属显著异常增长),
// 探测报告中记录该假设,后续可调。
const ossHighGrowthThresholdPercent = 30.0

// OSSGrowthPercent 近 30 天存储量增速(首末日口径,百分比)。
// first<=0 或 last<0 时无法计算(闲置/新建/异常数据),返回 false,
// 不产生 NaN/Inf(与 AggregateDistribution「不产生 NaN」同一精神)。
func OSSGrowthPercent(first, last float64) (float64, bool) {
	if first <= 0 || last < 0 {
		return 0, false
	}
	return (last - first) / first * 100, true
}

// IsHighGrowth 高增长判定:增速可计算且严格大于阈值。
func IsHighGrowth(growthPercent float64, ok bool) bool {
	return ok && growthPercent > ossHighGrowthThresholdPercent
}
