// Package nasprobe 实盘容量按厂商分布统计(M1 探测任务,manual probe,只读)。
//
// 对 5 厂商活跃账号逐一只读枚举 NAS 实例,统计各厂商实例数/容量占比,
// 作为「必达 vs 尽力而为」分组与覆盖承诺的证据(AC-4);并输出升格判定输入
// (tencent/volcengine 任一占比 >15% 且探测可用 → 升格为必达项候选)。
//
// SKIP gate(无 env 不跑):NAS_PROBE_MONGODB_DSN(+可选 CAM_ENCRYPTION_KEY)。
//
// Hard Rule:只读凭证,只写统计行到测试日志,不动生产 ecam_nas_metric 表。
package nasprobe

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/aliyun"
	"github.com/Havens-blog/e-cloudx-sdk/aws"
	"github.com/Havens-blog/e-cloudx-sdk/huawei"
	"github.com/Havens-blog/e-cloudx-sdk/tencent"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/volcano"
	"github.com/gotomicro/ego/core/elog"
)

// probeProviders 分布统计覆盖的厂商清单(固定顺序,便于报告对照)。
var probeProviders = []string{"aliyun", "tencent", "huawei", "volcano", "aws"}

// TestManualProbeNASCapacityDistribution 实盘容量按厂商分布统计。
func TestManualProbeNASCapacityDistribution(t *testing.T) {
	if !ProbeDSNEnabled() {
		t.Skip("未设置 NAS_PROBE_MONGODB_DSN,跳过(无 env 不跑)")
	}
	accounts, err := LoadProbeAccounts()
	if err != nil {
		t.Fatalf("加载探测账号失败: %v", err)
	}
	if len(accounts) == 0 {
		t.Fatalf("库中无可用活跃账号")
	}
	byProvider := map[string][]ProbeAccount{}
	for _, a := range accounts {
		byProvider[a.Provider] = append(byProvider[a.Provider], a)
	}
	for _, p := range probeProviders {
		t.Logf("厂商 %s 活跃账号数: %d", p, len(byProvider[p]))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()

	usages := make([]ProviderUsage, 0, len(probeProviders))
	for _, provider := range probeProviders {
		usage := enumerateProviderUsage(ctx, t, provider, byProvider[provider])
		usages = append(usages, usage)
	}

	// 聚合 + 输出报告表格(升格判定需结合厂商探测结果,见各厂商探测测试)
	dist := AggregateDistribution(usages)
	t.Log("===== 实盘 NAS 容量按厂商分布(GB 口径)=====")
	t.Log("| 厂商 | 实例数 | 容量合计(GB) | 占比(%) | capacity=0 实例数 | 单位异常 | 枚举错误 |")
	t.Log("|------|--------|----------------|---------|---------------------|----------|----------|")
	for _, u := range dist.Usages {
		t.Logf("| %s | %d | %.2f | %.2f | %d | %s | %s |",
			u.Provider, u.Instances, u.CapacityGB, dist.Percentages[u.Provider],
			u.ZeroCap, unitAnomalyOrDefault(u.UnitAnomaly), u.ProbeErr)
	}
	t.Logf("合计: 实例 %d, 容量 %.2f GB", dist.TotalInst, dist.TotalCapGB)

	t.Log("===== 升格判定输入(>15% 且探测可用 → 必达候选;>15% 且探测不可用 → 二期补)=====")
	for _, p := range []string{"tencent", "volcengine"} {
		share, ok := dist.Percentages[p]
		if !ok {
			t.Logf("厂商 %s: 无实盘数据,不参与升格判定", p)
			continue
		}
		if share > 15 {
			t.Logf("厂商 %s: 占比 %.2f%% > 15%% —— 探测可用则升格为必达项候选,否则「二期补」", p, share)
		} else {
			t.Logf("厂商 %s: 占比 %.2f%% ≤ 15%% —— 维持尽力而为,记录理由", p, share)
		}
	}
}

// enumerateProviderUsage 枚举单厂商全部账号的全部 region NAS 实例并聚合。
// ProbeErr 仅在「一次成功枚举都没有」时置位(此时该厂商不计入分布分母);
// 部分失败只打日志,报告据日志展开。
func enumerateProviderUsage(ctx context.Context, t *testing.T, provider string, accounts []ProbeAccount) ProviderUsage {
	usage := ProviderUsage{Provider: provider}
	if len(accounts) == 0 {
		usage.ProbeErr = "无活跃账号"
		return usage
	}
	anySuccess := false
	firstErr := ""
	// region 覆盖/扩展:账号 regions 配置与实盘 region 可能不一致
	// (实测 aws 账号配 eu-west-1 而实盘 EFS 在 eu-central-1/us-east-1),
	// NAS_PROBE_<VENDOR>_REGIONS 与账号 regions 取并集(保序去重)。
	for _, acc := range accounts {
		regions := mergeProbeRegions(acc.Regions, EnvRegions(probeVendorEnv(provider), ""))
		if len(regions) == 0 {
			if firstErr == "" {
				firstErr = fmt.Sprintf("账号 %d 未配置 region", acc.ID)
			}
			continue
		}
		for _, region := range regions {
			instances, err := listProviderInstances(ctx, provider, acc, region)
			if err != nil {
				if firstErr == "" {
					firstErr = fmt.Sprintf("账号 %d region %s 枚举失败: %v", acc.ID, region, err)
				}
				t.Logf("[枚举失败] 厂商=%s 账号=%s(%s) region=%s: %v", provider, acc.Name, MaskAK(acc.AK), region, err)
				continue
			}
			anySuccess = true
			usage.Instances += len(instances)
			for _, ins := range instances {
				if ins.Capacity == 0 {
					usage.ZeroCap++
				}
				// Capacity 字段语义按 types.NASInstance 注释为 GB;各厂商实际
				// 单位口径差异(如 aliyun 10485760)在 UnitAnomaly 标注,报告展开。
				usage.CapacityGB += float64(ins.Capacity)
				t.Logf("[明细] 厂商=%s 账号=%s region=%s fs_id=%s type=%s capacity=%d used=%d",
					provider, acc.Name, region, ins.FileSystemID, ins.FileSystemType,
					ins.Capacity, ins.UsedCapacity)
			}
		}
	}
	if !anySuccess && firstErr != "" {
		usage.ProbeErr = firstErr
	}
	return usage
}

// listProviderInstances 按厂商分发只读枚举(直建适配器,不经工厂注册表)。
func listProviderInstances(ctx context.Context, provider string, acc ProbeAccount, region string) ([]types.NASInstance, error) {
	logger := elog.DefaultLogger
	switch provider {
	case "aliyun":
		return aliyun.NewNASAdapter(acc.AK, acc.SK, region, logger).ListInstances(ctx, region)
	case "tencent":
		return tencent.NewCFSAdapter(acc.AK, acc.SK, region, logger).ListInstances(ctx, region)
	case "huawei":
		return huawei.NewSFSAdapter(acc.AK, acc.SK, region, logger).ListInstances(ctx, region)
	case "volcano":
		return volcano.NewNASAdapter(acc.AK, acc.SK, region, logger).ListInstances(ctx, region)
	case "aws":
		return aws.NewEFSAdapter(acc.AK, acc.SK, region, logger).ListInstances(ctx, region)
	default:
		return nil, fmt.Errorf("未知厂商: %s", provider)
	}
}

// unitAnomalyOrDefault 单位异常说明空值兜底。
func unitAnomalyOrDefault(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// probeVendorEnv 厂商 → 环境变量后缀映射(volcano 与 volcengine 同用 VOLC)。
func probeVendorEnv(provider string) string {
	if provider == "volcano" {
		return "VOLC"
	}
	return provider
}

// mergeProbeRegions 保序去重合并两份 region 列表。
func mergeProbeRegions(lists ...[]string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 8)
	for _, list := range lists {
		for _, r := range list {
			r = strings.TrimSpace(r)
			if r != "" && !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	return out
}
