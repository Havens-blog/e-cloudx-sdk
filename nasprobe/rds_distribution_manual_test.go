// Package nasprobe 实盘 RDS 实例按厂商分布统计(rds-ops-insight M1 探测任务,manual probe,只读)。
//
// 对 5 厂商活跃账号逐一只读枚举云数据库 RDS 实例(地域性资源,逐 region 枚举),
// 统计各厂商实例数/存储容量占比(双口径),作为「必达 vs 尽力而为」分组与升格/
// 降级判定的证据(AC-6);并输出引擎(engine)分布与规格分布,作为多引擎口径
// 确认(AC-5)与高危实例排查的输入。
//
// 复用本包通用分布工具(AggregateDistribution/Verdict/RDSToUsage)与只读
// 账号加载器(LoadProbeAccounts);RDS 枚举走各厂商现有 RDSAdapter
// ListInstances(aliyun AK/SK 直建;aws/huawei/tencent/volcano 经
// domain.CloudAccount 构造,region 逐账号 + NAS_PROBE_<VENDOR>_REGIONS 覆盖)。
//
// SKIP gate(无 env 不跑):NAS_PROBE_MONGODB_DSN(+可选 CAM_ENCRYPTION_KEY)。
//
// Hard Rule:只读凭证,只写统计行到测试日志,不动生产表。
package nasprobe

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/aliyun"
	"github.com/Havens-blog/e-cloudx-sdk/aws"
	"github.com/Havens-blog/e-cloudx-sdk/huawei"
	"github.com/Havens-blog/e-cloudx-sdk/tencent"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/volcano"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
)

// TestManualProbeRDSDistribution 实盘 RDS 实例数/存储/引擎/规格按厂商分布统计(AC-6)。
func TestManualProbeRDSDistribution(t *testing.T) {
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
	engines := map[string]map[string]int{} // provider -> engine -> count
	classes := map[string]map[string]int{} // provider -> class -> count
	for _, provider := range probeProviders {
		usage, insts := enumerateProviderRDS(ctx, t, provider, byProvider[provider])
		usages = append(usages, usage)
		engines[provider] = map[string]int{}
		classes[provider] = map[string]int{}
		for _, ins := range insts {
			e := strings.ToLower(strings.TrimSpace(ins.Engine))
			if e == "" {
				e = "(empty)"
			}
			engines[provider][e]++
			c := strings.TrimSpace(ins.DBInstanceClass)
			if c == "" {
				c = "(empty)"
			}
			classes[provider][c]++
		}
	}

	dist := AggregateDistribution(usages)
	t.Log("===== 实盘 RDS 实例按厂商分布(双口径)=====")
	t.Log("| 厂商 | 实例数 | 实例数占比(%) | 存储合计(GB) | 存储占比(%) | storage=0 数 | 枚举错误 |")
	t.Log("|------|--------|----------------|---------------|--------------|--------------|----------|")
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
	t.Logf("合计: 实例 %d, 存储 %.2f GB", dist.TotalInst, dist.TotalCapGB)

	// 引擎分布(AC-5 多引擎口径确认输入)。
	t.Log("===== 引擎分布(engine → 实例数)=====")
	for _, p := range probeProviders {
		if len(engines[p]) == 0 {
			continue
		}
		names := make([]string, 0, len(engines[p]))
		for e := range engines[p] {
			names = append(names, e)
		}
		sort.Strings(names)
		parts := make([]string, 0, len(names))
		for _, e := range names {
			parts = append(parts, fmt.Sprintf("%s=%d", e, engines[p][e]))
		}
		t.Logf("引擎分布 %s: %s", p, strings.Join(parts, ", "))
	}
	// 规格分布(规格水位证据,Urgency 量化口径输入)。
	t.Log("===== 规格分布(DBInstanceClass → 实例数,每厂商最多 10 类)=====")
	for _, p := range probeProviders {
		if len(classes[p]) == 0 {
			continue
		}
		type kv struct {
			k string
			v int
		}
		list := make([]kv, 0, len(classes[p]))
		for c, n := range classes[p] {
			list = append(list, kv{c, n})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].v > list[j].v })
		if len(list) > 10 {
			list = list[:10]
		}
		parts := make([]string, 0, len(list))
		for _, x := range list {
			parts = append(parts, fmt.Sprintf("%s=%d", x.k, x.v))
		}
		t.Logf("规格分布 %s: %s", p, strings.Join(parts, ", "))
	}

	// 升格判定输入(tencent/volcengine 任一占比 >15% 且探测可用 → 升格必达;
	// >15% 但探测不可用 → 二期补;≤15% → 维持尽力而为并记录理由)。
	// 探测可用性由各厂商 rds_probe_manual_test 结果回填(见探测报告)。
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
			t.Logf("厂商 %s: 实例数占比=%.2f%% 存储占比=%.2f%% —— >15%%:探测可用则升格必达,否则「二期补」", p, countShare, capShare)
		} else {
			t.Logf("厂商 %s: 实例数占比=%.2f%% 存储占比=%.2f%% —— ≤15%%:维持尽力而为,记录理由", p, countShare, capShare)
		}
	}
}

// enumerateProviderRDS 枚举单厂商全部账号的全部 region RDS 实例并聚合。
// 地域性资源:逐账号逐 region 枚举(账号 regions 与 NAS_PROBE_<VENDOR>_REGIONS
// 取并集);ProbeErr 仅在「一次成功枚举都没有」时置位(该厂商不计入分母)。
func enumerateProviderRDS(ctx context.Context, t *testing.T, provider string, accounts []ProbeAccount) (ProviderUsage, []types.RDSInstance) {
	usage := ProviderUsage{Provider: provider}
	var all []types.RDSInstance
	if len(accounts) == 0 {
		usage.ProbeErr = "无活跃账号"
		return usage, nil
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
			instances, err := listProviderRDS(ctx, provider, acc, region)
			if err != nil {
				if firstErr == "" {
					firstErr = fmt.Sprintf("账号 %d region %s 枚举失败: %v", acc.ID, region, err)
				}
				t.Logf("[枚举失败] 厂商=%s 账号=%s(%s) region=%s: %v", provider, acc.Name, MaskAK(acc.AK), region, err)
				continue
			}
			anySuccess = true
			for _, ins := range instances {
				t.Logf("[明细] 厂商=%s 账号=%s region=%s rds_id=%s engine=%s class=%s storage=%dGB status=%s",
					provider, acc.Name, region, ins.InstanceID, ins.Engine, ins.DBInstanceClass, ins.Storage, ins.Status)
			}
			all = append(all, instances...)
		}
	}
	part := RDSToUsage(provider, all)
	usage.Instances = part.Instances
	usage.CapacityGB = part.CapacityGB
	usage.ZeroCap = part.ZeroCap
	if !anySuccess && firstErr != "" {
		usage.ProbeErr = firstErr
	}
	return usage, all
}

// listProviderRDS 按厂商分发只读 RDS 枚举(直建适配器,不经工厂注册表;
// aws/huawei/tencent/volcano 的 RDSAdapter 经 domain.CloudAccount 构造,
// AccessKeySecret 传探测解密后的明文——与生产 toDomain 解密后同口径)。
func listProviderRDS(ctx context.Context, provider string, acc ProbeAccount, region string) ([]types.RDSInstance, error) {
	logger := elog.DefaultLogger
	switch provider {
	case "aliyun":
		return aliyun.NewRDSAdapter(acc.AK, acc.SK, region, logger).ListInstances(ctx, region)
	case "tencent":
		return tencent.NewRDSAdapter(probeCloudAccount(acc), region, logger).ListInstances(ctx, region)
	case "huawei":
		return huawei.NewRDSAdapter(probeCloudAccount(acc), region, logger).ListInstances(ctx, region)
	case "volcano":
		return volcano.NewRDSAdapter(probeCloudAccount(acc), region, logger).ListInstances(ctx, region)
	case "aws":
		return aws.NewRDSAdapter(probeCloudAccount(acc), region, logger).ListInstances(ctx, region)
	default:
		return nil, fmt.Errorf("未知厂商: %s", provider)
	}
}

// probeCloudAccount 探测账号 → domain.CloudAccount(仅填 RDS 适配器所需字段;
// AccessKeySecret 为解密后的明文,探测进程内存态,不落盘)。
func probeCloudAccount(acc ProbeAccount) *domain.CloudAccount {
	return &domain.CloudAccount{
		ID:              acc.ID,
		Name:            acc.Name,
		AccessKeyID:     acc.AK,
		AccessKeySecret: acc.SK,
		Regions:         acc.Regions,
	}
}
