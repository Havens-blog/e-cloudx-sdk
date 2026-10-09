// Package nasprobe NAS 指标探测(M1 探测任务)共享纯逻辑。
//
// 用途:厂商监控 API 探测(manual probe tests)与实盘容量分布统计共用的
// 单位换算、数量级自检、分布聚合、升格判定与候选指标清单。
// 只读逻辑,不发任何网络请求;网络探测在 <provider>/probe_manual_test.go。
//
// 规格来源:docs/proposals/nas-ops-insight/proposal.md
// 「单位归一化与字段语义」「必达厂商选择依据」「Key Risks」。
package nasprobe

import (
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
)

// bytesPerGB 字节 → GB(二进制 GiB)换算分母。
// 规格口径:ecam_nas_metric 容量字段以 GB(二进制 GiB)为唯一口径,
// 换算在采集边界完成,禁止把字节直接写进 GB 字段。
const bytesPerGB = 1024 * 1024 * 1024

// BytesToGB 字节 → GB(二进制 GiB)。委托至 types 共享实现(采集链路唯一换算入口)。
func BytesToGB(raw float64) float64 {
	return types.BytesToGB(raw)
}

// 数量级自检区间(规格:capacity 落在 [1MB, 1PB] 区间),以 GB 计。
var (
	magnitudeMinGB = BytesToGB(1024 * 1024)                      // 1MB
	magnitudeMaxGB = BytesToGB(1024 * 1024 * 1024 * 1024 * 1024) // 1PB = 1024^5 字节
)

// CheckMagnitude 数量级自检(以 GB 计):非零行 capacity 须落在 [1MB, 1PB] 区间。
// capacity=0 不在本函数职责内(零值走「例外放行+打标 zero_exception」路径,
// 探测语义为「零值=探测未通过」),返回 false 并由调用方单列。
// 负值同样视为不通过。
func CheckMagnitude(capacityGB float64) bool {
	if capacityGB < magnitudeMinGB || capacityGB > magnitudeMaxGB {
		return false
	}
	return true
}

// DescribeMagnitude 返回数量级判定的可读说明(探测报告用)。
func DescribeMagnitude(capacityGB float64) string {
	if capacityGB == 0 {
		return "zero_exception"
	}
	if CheckMagnitude(capacityGB) {
		return "ok"
	}
	return fmt.Sprintf("out_of_range(%.6g GB)", capacityGB)
}

// ProviderUsage 单厂商实盘 NAS 用量统计(容量分布统计行)。
type ProviderUsage struct {
	Provider    string  // 厂商标识(aliyun/tencent/huawei/volcengine/aws)
	Instances   int     // NAS 实例数
	CapacityGB  float64 // 容量合计(按 GB 口径;单位异常时另行标注)
	ZeroCap     int     // capacity=0 实例数(实盘现状证据)
	UnitAnomaly string  // 容量单位语义异常说明(如 aliyun 明显未正确映射),空=无
	ProbeErr    string  // 枚举失败时的错误(该厂商不计入分布分母),空=枚举成功
}

// Distribution 实盘容量按厂商分布聚合结果。
type Distribution struct {
	Usages      []ProviderUsage
	TotalInst   int     // 实例总数
	TotalCapGB  float64 // 容量总数(GB)
	Percentages map[string]float64
}

// AggregateDistribution 按厂商聚合实盘容量分布。
// 百分比 = 该厂商容量 / 总容量 × 100;总容量为 0 时全部记 0(不产生 NaN,
// 与 proposal「utilization 边界不写 NaN」同一精神)。
// 枚举失败(ProbeErr 非空)的厂商不计入分母,调用方须单独记录。
func AggregateDistribution(usages []ProviderUsage) Distribution {
	dist := Distribution{
		Usages:      make([]ProviderUsage, 0, len(usages)),
		Percentages: make(map[string]float64, len(usages)),
	}
	for _, u := range usages {
		if u.ProbeErr != "" {
			continue
		}
		dist.Usages = append(dist.Usages, u)
		dist.TotalInst += u.Instances
		dist.TotalCapGB += u.CapacityGB
	}
	for _, u := range dist.Usages {
		if dist.TotalCapGB > 0 {
			dist.Percentages[u.Provider] = u.CapacityGB / dist.TotalCapGB * 100
		} else {
			dist.Percentages[u.Provider] = 0
		}
	}
	return dist
}

// upgradeThresholdPercent 升格为必达项的占比阈值(严格大于)。
// 规格口径:若 tencent/volcengine 任一占比 >15% 且探测可用,升格为必达项候选。
const upgradeThresholdPercent = 15.0

// upgradeCandidates 尽力而为厂商集合(升格判定只针对这两家)。
var upgradeCandidates = []string{"tencent", "volcengine"}

// Verdict 对分布结果做「必达 vs 尽力而为」升格判定。
// probeOK:厂商 → 监控 API 探测是否可用;探测不可用时不升格,
// 归入「二期补」判定(规格:探测不可用则显式降级为二期补并在发布说明承诺窗口)。
// 返回:upgraded=升格候选(占比 >15% 且探测可用);secondPhase=占比 >15% 但探测不可用(二期补)。
func Verdict(dist Distribution, probeOK map[string]bool) (upgraded, secondPhase []string) {
	for _, p := range upgradeCandidates {
		share, ok := dist.Percentages[p]
		if !ok {
			continue // 该厂商无实盘数据(枚举失败或零实例),不参与判定
		}
		if share <= upgradeThresholdPercent {
			continue
		}
		if probeOK[p] {
			upgraded = append(upgraded, p)
		} else {
			secondPhase = append(secondPhase, p)
		}
	}
	return upgraded, secondPhase
}
