// Package huawei_test OSS 指标探测(oss-ops-insight M1 探测任务,manual probe,只读)。
//
// 华为 CES `SYS.OBS` namespace 的 bucket 级容量/对象数指标探测(AC-1):
// 文档口径 namespace=SYS.OBS 需实盘验证(NAS 的 SYS.SFS_Turbo 实盘 0 上报先例
// 证明文档不可全信);发现维度与指标名后对实盘 bucket 做非零验证,
// ≥1 个真实 bucket 非零数据点通过;若文档口径与实盘不符,以实盘为准并记录。
//
// 实测结论(2026-09-20,实盘运行后回填,见 probe-report §1.2):
// 候选 namespace SYS.OBS 的实盘 ListMetrics/维度/指标名以本测试输出为准;
// 若文档口径与实盘不符,以实盘为准并记录(参照 NAS 的 SYS.SFS_Turbo 先例)。
//
// SKIP gate(无 env 不跑):
//   - NAS_PROBE_HUAWEI_AK / NAS_PROBE_HUAWEI_SK / NAS_PROBE_HUAWEI_REGION 直填,或
//   - NAS_PROBE_MONGODB_DSN(+可选 CAM_ENCRYPTION_KEY)从库加载活跃 huawei 账号
//
// Hard Rule:只读凭证,只写样例行到测试日志,不动生产表。
// 复用本包 NAS 探测的 newCESv1Client / loadHuaweiCreds 辅助函数。
package huawei_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/huawei"
	"github.com/Havens-blog/e-cloudx-sdk/nasprobe"
	"github.com/gotomicro/ego/core/elog"
	cesv1model "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1/model"
)

// ossCapacityKeyword 容量/对象类指标名筛选关键词(发现阶段用)。
var ossCapacityKeyword = []string{"cap", "size", "storage", "object", "count", "num"}

// TestManualProbeHuaweiCesoOBSMetrics CES SYS.OBS 指标发现 + bucket 非零验证。
func TestManualProbeHuaweiCesoOBSMetrics(t *testing.T) {
	ak, sk, region := loadHuaweiCreds(t)
	if ak == "" {
		t.Skip("未设置 NAS_PROBE_HUAWEI_AK/SK/REGION 或 NAS_PROBE_MONGODB_DSN,跳过(无 env 不跑)")
	}
	t.Logf("探测凭证: AK=%s region=%s", nasprobe.MaskAK(ak), region)

	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()

	// 1) 只读枚举实盘 OBS bucket(全局服务,region 传空;容量快照走 GetBucketStats)
	adapter := huawei.NewOBSAdapter(ak, sk, region, elog.DefaultLogger)
	buckets, err := adapter.ListBuckets(ctx, "")
	if err != nil {
		t.Fatalf("枚举 OBS bucket 失败(账号凭证或网络问题): %v", err)
	}
	t.Logf("实盘 OBS bucket 数: %d", len(buckets))
	if len(buckets) == 0 {
		t.Skip("账号无 OBS bucket,非零验证无从进行(记录:枚举为空)")
	}
	bucketSet := map[string]bool{}
	for _, b := range buckets {
		bucketSet[b.BucketName] = true
		t.Logf("bucket: %s region=%s storage=%d byte(%.4f GB) objects=%d",
			b.BucketName, b.Region, b.StorageSize, nasprobe.BytesToGB(float64(b.StorageSize)), b.ObjectCount)
	}

	// 2) CES ListMetrics:SYS.OBS namespace 指标发现(文档口径实盘验证)
	cesClient, err := newCESv1Client(ak, sk, region)
	if err != nil {
		t.Fatalf("创建 CES 客户端失败: %v", err)
	}
	const obsNamespace = "SYS.OBS"
	metrics, err := listCESMetrics(ctx, cesClient, obsNamespace)
	if err != nil {
		t.Fatalf("ListMetrics(namespace=%s) 失败: %v", obsNamespace, err)
	}
	if len(metrics) == 0 {
		t.Fatalf("[定案] namespace=%s 实盘无任何指标上报 —— 文档口径与实盘不符(同 SYS.SFS_Turbo 先例),以实盘为准记录并另寻 namespace", obsNamespace)
	}
	names := make([]string, 0, len(metrics))
	for name := range metrics {
		names = append(names, name)
	}
	sort.Strings(names)
	t.Logf("[OK] namespace=%s 发现 %d 个指标: %s", obsNamespace, len(names), strings.Join(names, ", "))

	// 3) 容量/对象类指标 × 实盘 bucket 维度 → 非零验证(AC-1)
	// 维度名定案(报告引用):对容量指标第一条序列记录 ListMetrics 返回的维度名
	for _, metric := range []string{"capacity_total", "object_num_all"} {
		if meta, ok := metrics[metric]; ok && len(meta.Dims) > 0 {
			t.Logf("[维度] %s 维度名=%s(样例值=%s)", metric, meta.Dims[0].Name, meta.Dims[0].Value)
		}
	}
	passed, failed := 0, 0
	to := time.Now().UnixMilli()
	from := to - 3*24*3600*1000
	verifiedBuckets := map[string]bool{}
	for _, metric := range names {
		lower := strings.ToLower(metric)
		isCap := false
		for _, kw := range ossCapacityKeyword {
			if strings.Contains(lower, kw) {
				isCap = true
				break
			}
		}
		if !isCap {
			continue
		}
		meta := metrics[metric]
		seenDim := map[string]bool{}
		for _, dim := range meta.Dims {
			if !bucketSet[dim.Value] || seenDim[dim.Value] {
				continue // 只对实盘枚举到的 bucket 验证,每 bucket 每指标一次
			}
			seenDim[dim.Value] = true
			request := &cesv1model.ShowMetricDataRequest{
				Namespace:  obsNamespace,
				MetricName: metric,
				Dim0:       dim.Name + "," + dim.Value,
				Filter:     cesv1model.GetShowMetricDataRequestFilterEnum().AVERAGE,
				Period:     cesv1model.GetShowMetricDataRequestPeriodEnum().E_86400,
				From:       from,
				To:         to,
			}
			resp, err := cesClient.ShowMetricData(request)
			if err != nil {
				failed++
				t.Logf("[非零验证][FAIL] metric=%s bucket=%s err=%v", metric, dim.Value, err)
				continue
			}
			points := 0
			if resp != nil && resp.Datapoints != nil {
				points = len(*resp.Datapoints)
			}
			if points == 0 {
				t.Logf("[非零验证][空] metric=%s bucket=%s 3 天窗口无数据点", metric, dim.Value)
				continue
			}
			latest := (*resp.Datapoints)[points-1]
			v := 0.0
			if latest.Average != nil {
				v = *latest.Average
			} else if latest.Max != nil {
				v = *latest.Max
			} else if latest.Sum != nil {
				v = *latest.Sum
			}
			unit := ""
			if latest.Unit != nil {
				unit = *latest.Unit
			}
			if v == 0 {
				failed++
				t.Logf("[非零验证][FAIL] metric=%s bucket=%s 最新值=0(零值=探测未通过)", metric, dim.Value)
				continue
			}
			display := ""
			if strings.Contains(strings.ToLower(unit), "byte") {
				display = fmt.Sprintf("≈ %.4f GB", nasprobe.BytesToGB(v))
			} else {
				display = "原值口径"
			}
			passed++
			verifiedBuckets[dim.Value] = true
			t.Logf("[非零验证][PASS] metric=%s bucket=%s 数据点=%d 最新值=%v %s (%s) 数量级=%s",
				metric, dim.Value, points, v, unit, display,
				nasprobe.DescribeMagnitude(nasprobe.BytesToGB(v)))
		}
	}
	t.Logf("[非零验证汇总] namespace=%s PASS=%d FAIL=%d 验证通过 bucket 数=%d(≥1 即 AC-1 通过)",
		obsNamespace, passed, failed, len(verifiedBuckets))
}
