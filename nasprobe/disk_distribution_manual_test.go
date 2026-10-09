// Package nasprobe 实盘云硬盘按厂商分布统计(disk-ops-insight M1 探测任务,manual probe,只读)。
//
// 对 5 厂商活跃账号逐一只读枚举云硬盘(地域性资源,逐 region 枚举),统计
// 各厂商 Disk 数/容量占比(双口径),作为「必达 vs 尽力而为」分组与升格/
// 降级判定的证据(AC-6):占比 >15% 且探测可用 → 升格必达;否则维持尽力
// 而为并记录理由(探测不可用 → 二期补)。
//
// 复用本包通用分布工具(AggregateDistribution/Verdict/DisksToUsage)与只读
// 账号加载器(LoadProbeAccounts);磁盘枚举走各厂商现有 DiskAdapter
// ListInstances(地域性资源,逐账号 region + NAS_PROBE_<VENDOR>_REGIONS 覆盖,
// 与 NAS 探测同型)。
//
// 口径说明(探测报告引用):DiskInstance.Size 单位 GB(资产表枚举快照),
// 容量合计 = Size 直加(无字节换算);磁盘数占比与容量占比双口径分列。
//
// SKIP gate(无 env 不跑):NAS_PROBE_MONGODB_DSN(+可选 CAM_ENCRYPTION_KEY)。
//
// Hard Rule:只读凭证,只写统计行到测试日志,不动生产表。
package nasprobe

import (
	"context"
	"fmt"
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

// TestManualProbeDiskDistribution 实盘云硬盘数/容量按厂商分布统计(AC-6)。
func TestManualProbeDiskDistribution(t *testing.T) {
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
		usage := enumerateProviderDisks(ctx, t, provider, byProvider[provider])
		usages = append(usages, usage)
	}

	dist := AggregateDistribution(usages)
	t.Log("===== 实盘云硬盘按厂商分布(双口径)=====")
	t.Log("| 厂商 | 磁盘数 | 磁盘数占比(%) | 容量合计(GB) | 容量占比(%) | size=0 数 | 枚举错误 |")
	t.Log("|------|--------|----------------|---------------|--------------|-----------|----------|")
	for _, u := range dist.Usages {
		countShare := 0.0
		capShare := 0.0
		if dist.TotalInst > 0 {
			countShare = float64(u.Instances) / float64(dist.TotalInst) * 100
		}
		if dist.TotalCapGB > 0 {
			capShare = u.CapacityGB / dist.TotalCapGB * 100
		}
		t.Logf("| %s | %d | %.2f | %.2f | %.2f | %d | %s |",
			u.Provider, u.Instances, countShare, u.CapacityGB, capShare, u.ZeroCap, unitAnomalyOrDefault(u.ProbeErr))
	}
	t.Logf("合计: 磁盘 %d, 容量 %.2f GB", dist.TotalInst, dist.TotalCapGB)

	// 升格判定输入(tencent/volcengine 任一占比 >15% 且探测可用 → 升格必达;
	// >15% 但探测不可用 → 二期补;≤15% → 维持尽力而为并记录理由)。
	// 探测可用性由各厂商 disk_probe_manual_test 结果回填(见探测报告)。
	t.Log("===== 升格判定输入(tencent / volcengine)=====")
	for _, p := range []string{"tencent", "volcano"} {
		u := providerUsage(usages, p)
		countShare, capShare := 0.0, 0.0
		if dist.TotalInst > 0 {
			countShare = float64(u.Instances) / float64(dist.TotalInst) * 100
		}
		if dist.TotalCapGB > 0 {
			capShare = u.CapacityGB / dist.TotalCapGB * 100
		}
		if countShare > 15 || capShare > 15 {
			t.Logf("厂商 %s: 磁盘数占比=%.2f%% 容量占比=%.2f%% —— >15%%:探测可用则升格必达,否则「二期补」", p, countShare, capShare)
		} else {
			t.Logf("厂商 %s: 磁盘数占比=%.2f%% 容量占比=%.2f%% —— ≤15%%:维持尽力而为,记录理由", p, countShare, capShare)
		}
	}
}

// enumerateProviderDisks 枚举单厂商全部账号的全部 region 云硬盘并聚合。
// 地域性资源:逐账号逐 region 枚举(账号 regions 与 NAS_PROBE_<VENDOR>_REGIONS
// 取并集);ProbeErr 仅在「一次成功枚举都没有」时置位(该厂商不计入分母)。
func enumerateProviderDisks(ctx context.Context, t *testing.T, provider string, accounts []ProbeAccount) ProviderUsage {
	usage := ProviderUsage{Provider: provider}
	if len(accounts) == 0 {
		usage.ProbeErr = "无活跃账号"
		return usage
	}
	anySuccess := false
	firstErr := ""
	for _, acc := range accounts {
		regions := mergeProbeRegions(acc.Regions, EnvRegions(probeVendorEnv(provider), ""))
		if len(regions) == 0 {
			if firstErr == "" {
				firstErr = fmt.Sprintf("账号 %d 未配置 region", acc.ID)
			}
			continue
		}
		for _, region := range regions {
			disks, err := listProviderDisks(ctx, provider, acc, region)
			if err != nil {
				if firstErr == "" {
					firstErr = fmt.Sprintf("账号 %d region %s 枚举失败: %v", acc.ID, region, err)
				}
				t.Logf("[枚举失败] 厂商=%s 账号=%s(%s) region=%s: %v", provider, acc.Name, MaskAK(acc.AK), region, err)
				continue
			}
			anySuccess = true
			for _, d := range disks {
				t.Logf("[明细] 厂商=%s 账号=%s region=%s disk_id=%s size=%dGB status=%s instance=%s",
					provider, acc.Name, region, d.DiskID, d.Size, d.Status, d.InstanceID)
			}
			part := DisksToUsage(provider, disks)
			usage.Instances += part.Instances
			usage.CapacityGB += part.CapacityGB
			usage.ZeroCap += part.ZeroCap
		}
	}
	if !anySuccess && firstErr != "" {
		usage.ProbeErr = firstErr
	}
	return usage
}

// listProviderDisks 按厂商分发只读磁盘枚举(直建适配器,不经工厂注册表)。
func listProviderDisks(ctx context.Context, provider string, acc ProbeAccount, region string) ([]types.DiskInstance, error) {
	logger := elog.DefaultLogger
	switch provider {
	case "aliyun":
		return aliyun.NewDiskAdapter(acc.AK, acc.SK, region, logger).ListInstances(ctx, region)
	case "tencent":
		return tencent.NewDiskAdapter(acc.AK, acc.SK, region, logger).ListInstances(ctx, region)
	case "huawei":
		return huawei.NewDiskAdapter(acc.AK, acc.SK, region, logger).ListInstances(ctx, region)
	case "volcano":
		return volcano.NewDiskAdapter(acc.AK, acc.SK, region, logger).ListInstances(ctx, region)
	case "aws":
		return aws.NewDiskAdapter(acc.AK, acc.SK, region, logger).ListInstances(ctx, region)
	default:
		return nil, fmt.Errorf("未知厂商: %s", provider)
	}
}
