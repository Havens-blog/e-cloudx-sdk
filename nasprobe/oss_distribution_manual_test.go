// Package nasprobe 实盘 OSS bucket 按厂商分布统计(oss-ops-insight M1 探测任务,manual probe,只读)。
//
// 对 5 厂商活跃账号逐一只读枚举 OSS bucket,统计各厂商 bucket 数/容量占比
// (双口径),作为「必达 vs 尽力而为」分组与升格/降级判定的证据(AC-5):
// 占比 >15% 且探测可用 → 升格必达候选;否则维持尽力而为并记录理由。
//
// 复用本包通用分布工具(AggregateDistribution/Verdict/BytesToGB)与只读账号
// 加载器(LoadProbeAccounts);bucket 枚举走各厂商现有 OSS 适配器 ListBuckets
// (全局服务,region 参数传空返回全部 bucket)。
//
// 口径说明(探测报告引用):bucket 容量取自各适配器枚举时的 GetBucketStats
// 快照(资产表同源);volcano TOS 适配器 GetBucketStats 不返回统计(TOS API
// 无直接统计接口),其容量口径为 0 —— 容量占比仅具相对参考意义,bucket 数
// 占比是可靠口径(与 NAS 探测「资产表容量不可信,实例数占比可靠」同型)。
//
// SKIP gate(无 env 不跑):NAS_PROBE_MONGODB_DSN(+可选 CAM_ENCRYPTION_KEY)。
//
// Hard Rule:只读凭证,只写统计行到测试日志,不动生产表。
package nasprobe

import (
	"context"
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

// TestManualProbeOSSBucketDistribution 实盘 OSS bucket 数/容量按厂商分布统计。
func TestManualProbeOSSBucketDistribution(t *testing.T) {
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
		var buckets []types.OSSBucket
		for _, acc := range byProvider[provider] {
			list, err := listProviderOSSBuckets(ctx, provider, acc)
			if err != nil {
				t.Logf("[枚举失败] 厂商=%s 账号=%s(%s): %v", provider, acc.Name, MaskAK(acc.AK), err)
				continue
			}
			for _, b := range list {
				t.Logf("[明细] 厂商=%s 账号=%s bucket=%s region=%s storage=%d byte(%.4f GB) objects=%d",
					provider, acc.Name, b.BucketName, b.Region,
					b.StorageSize, BytesToGB(float64(b.StorageSize)), b.ObjectCount)
			}
			buckets = append(buckets, list...)
		}
		usages = append(usages, OSSBucketsToUsage(provider, buckets))
	}

	dist := AggregateDistribution(usages)

	// 双口径占比:bucket 数占比 + 容量占比(分列,互不替代)
	t.Log("===== 实盘 OSS bucket 按厂商分布(双口径)=====")
	t.Log("| 厂商 | bucket数 | bucket数占比(%) | 容量合计(GB) | 容量占比(%) | storage=0 数 |")
	t.Log("|------|----------|------------------|---------------|--------------|--------------|")
	for _, u := range dist.Usages {
		bucketShare := 0.0
		capShare := 0.0
		if dist.TotalInst > 0 {
			bucketShare = float64(u.Instances) / float64(dist.TotalInst) * 100
		}
		if dist.TotalCapGB > 0 {
			capShare = u.CapacityGB / dist.TotalCapGB * 100
		}
		t.Logf("| %s | %d | %.2f | %.2f | %.2f | %d |",
			u.Provider, u.Instances, bucketShare, u.CapacityGB, capShare, u.ZeroCap)
	}
	t.Logf("合计: bucket %d, 容量 %.2f GB", dist.TotalInst, dist.TotalCapGB)

	// 升格判定输入(>15% 且探测可用 → 升格必达候选;>15% 但探测不可用 → 二期补;
	// ≤15% → 维持尽力而为并记录理由)。探测可用性由各厂商 oss_probe_manual_test
	// 结果回填(见各探测日志与 probe-report.md),本测试只输出占比与判定输入。
	t.Log("===== 升格判定输入(tencent / volcengine)=====")
	for _, p := range []string{"tencent", "volcano"} {
		u := providerUsage(usages, p)
		bucketShare, capShare := 0.0, 0.0
		if dist.TotalInst > 0 {
			bucketShare = float64(u.Instances) / float64(dist.TotalInst) * 100
		}
		if dist.TotalCapGB > 0 {
			capShare = u.CapacityGB / dist.TotalCapGB * 100
		}
		if bucketShare > 15 || capShare > 15 {
			t.Logf("厂商 %s: bucket数占比=%.2f%% 容量占比=%.2f%% —— >15%%:探测可用则升格必达候选,否则「二期补」", p, bucketShare, capShare)
		} else {
			t.Logf("厂商 %s: bucket数占比=%.2f%% 容量占比=%.2f%% —— ≤15%%:维持尽力而为,记录理由", p, bucketShare, capShare)
		}
	}
}

// providerUsage 取指定厂商的统计行(未找到返回零值行)。
func providerUsage(usages []ProviderUsage, provider string) ProviderUsage {
	for _, u := range usages {
		if u.Provider == provider {
			return u
		}
	}
	return ProviderUsage{Provider: provider}
}

// listProviderOSSBuckets 按厂商分发只读 bucket 枚举(直建适配器,不经工厂注册表)。
// OSS 为全局服务:region 传空返回账号全部 bucket;构造器的 defaultRegion 仅作
// 客户端 endpoint 兜底(取账号首个 region)。
func listProviderOSSBuckets(ctx context.Context, provider string, acc ProbeAccount) ([]types.OSSBucket, error) {
	logger := elog.DefaultLogger
	defaultRegion := ""
	if len(acc.Regions) > 0 {
		defaultRegion = acc.Regions[0]
	}
	switch provider {
	case "aliyun":
		return aliyun.NewOSSAdapter(acc.AK, acc.SK, defaultRegion, logger).ListBuckets(ctx, "")
	case "tencent":
		return tencent.NewCOSAdapter(acc.AK, acc.SK, defaultRegion, logger).ListBuckets(ctx, "")
	case "huawei":
		return huawei.NewOBSAdapter(acc.AK, acc.SK, defaultRegion, logger).ListBuckets(ctx, "")
	case "volcano":
		return volcano.NewTOSAdapter(acc.AK, acc.SK, defaultRegion, logger).ListBuckets(ctx, "")
	case "aws":
		return aws.NewS3Adapter(acc.AK, acc.SK, defaultRegion, logger).ListBuckets(ctx, "")
	default:
		return nil, nil
	}
}
