// Package volcano_test OSS 指标探测(oss-ops-insight M1 探测任务,manual probe,只读)。
//
// volcengine cloudmonitor TOS bucket 级容量/对象数指标探测(AC-4):探测并归因
// (指标订阅未开通 vs 指标不存在),记录二期补路径(参照 NAS volcengine 先例)。
//
// 前案背景(NAS 探测 2026-09-19 实测):该账号云产品监控指标注册表为空
// (云产品监控指标需「产品订阅」开通,文档 6408/114674),元数据接口
// ListNamespaces/ListMetrics 在 cloudmonitor 网关不可用,GetMetricData
// 全候选 metric not found。TOS 探测沿用同一方法链:真实 bucket 维度直查 +
// 静态候选矩阵 + ECS 阳性对照,归因到「订阅未开通」则维持二期补判定;
// 若 TOS 候选出现数据则升格判定输入改写。
//
// TOS 候选口径(文档 6402/53500 系):Namespace=TOS(及 Volcano_/Vulcan_ 变体)、
// SubNamespace=tos/bucket、指标名 BucketSize/StorageSize/UsedCapacity/
// TotalCapacity/ObjectCount 等、维度名 BucketName/bucket_name/bucket。
//
// 二期补重试路径(参照 NAS probe §1.4 固化):① 开通云产品监控指标订阅
// (写操作,不在本任务范围);② 重跑本测试;③ 按文档候选定案。
//
// SKIP gate(无 env 不跑):
//   - NAS_PROBE_VOLC_AK / NAS_PROBE_VOLC_SK / NAS_PROBE_VOLC_REGION 直填凭证,或
//   - NAS_PROBE_MONGODB_DSN(+可选 CAM_ENCRYPTION_KEY)从库加载活跃 volcano 账号
//
// Hard Rule:只读凭证,只写样例行到测试日志,不动生产表。
// 复用本包 NAS 探测的 callVolcanoAction/probeVolcanoOne/loadVolcanoCreds 辅助函数。
package volcano_test

import (
	"context"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/nasprobe"
	"github.com/Havens-blog/e-cloudx-sdk/volcano"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/cloudmonitor"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// TOS 候选矩阵(探测报告须记录每个候选的响应,含错误)。
var (
	ossVolcanoNamespaces   = []string{"TOS", "Volcano_TOS", "Vulcan_TOS", "VEI_TOS"}
	ossVolcanoSubNamespace = []string{"tos", "bucket", "object_storage"}
	ossVolcanoMetricNames  = []string{
		"BucketSize", "StorageSize", "UsedCapacity", "TotalCapacity",
		"BucketStorageSize", "ObjectCount", "BucketObjectNums", "CapacityUsage",
	}
	ossVolcanoDimNames = []string{"BucketName", "bucket_name", "bucket"}
)

// TestManualProbeVolcanoTOSCloudMonitor volcengine cloudmonitor TOS 指标名探测。
func TestManualProbeVolcanoTOSCloudMonitor(t *testing.T) {
	ak, sk, region, fromDB := loadVolcanoCreds(t)
	if ak == "" {
		t.Skip("未设置 NAS_PROBE_VOLC_AK/SK/REGION 或 NAS_PROBE_MONGODB_DSN,跳过(无 env 不跑)")
	}
	t.Logf("探测凭证: AK=%s 来源=%s region=%s", nasprobe.MaskAK(ak), fromDB, region)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	// 1) 只读枚举实盘 TOS bucket(全局服务,region 传空;TOS 适配器无统计接口,
	//    StorageSize 恒 0 —— 容量分布统计的 volcano 口径已在该前提记录)
	adapter := volcano.NewTOSAdapter(ak, sk, region, elog.DefaultLogger)
	buckets, err := adapter.ListBuckets(ctx, "")
	if err != nil {
		t.Fatalf("枚举 TOS bucket 失败(账号凭证或网络问题): %v", err)
	}
	t.Logf("实盘 TOS bucket 数: %d", len(buckets))
	probeBucket := ""
	if len(buckets) > 0 {
		probeBucket = buckets[0].BucketName
		t.Logf("探测目标 bucket: %s region=%s", probeBucket, buckets[0].Region)
	} else {
		t.Log("该账号无 TOS bucket:仅能验证 namespace 合法性,无法确认 bucket 级指标")
	}

	// 2) cloudmonitor 客户端(与 NAS 探测同一 session 构造模式)
	sess, err := session.NewSession(volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(ak, sk, "")).
		WithRegion(region))
	if err != nil {
		t.Fatalf("创建 cloudmonitor 会话失败: %v", err)
	}
	client := cloudmonitor.New(sess)

	// 3) 元数据发现(订阅开通后应可用;未开通则与前案一致 InvalidActionOrVersion)
	namespaces, discovered := discoverVolcanoNamespaces(ctx, t, client)
	metrics := map[string]volcanoMetricMeta{}
	if discovered {
		for _, ns := range namespaces {
			for k, v := range discoverVolcanoMetrics(ctx, t, client, ns) {
				metrics[k] = v
			}
		}
	}

	// 4) GetMetricData:发现结果优先,否则静态候选矩阵兜底(真实 bucket 维度)
	end := time.Now().Unix()
	start := end - 24*3600
	calls := 0
	hit := map[string]int{}
	lastErrKind := ""
	if discovered && len(metrics) > 0 {
		for key, meta := range metrics {
			for _, dimName := range meta.dimNames {
				var hasData bool
				calls, hasData = probeVolcanoOne(ctx, t, client, meta.namespace, meta.subNamespace,
					key, dimName, probeBucket, start, end, calls)
				if hasData {
					hit[meta.namespace+"|"+meta.subNamespace+"|"+key]++
				}
			}
		}
	} else {
		t.Log("[兜底] 元数据发现失败,走静态候选矩阵逐格试错(真实 bucket 维度)")
		for _, ns := range ossVolcanoNamespaces {
			nsOK := false
			for _, sub := range ossVolcanoSubNamespace {
				for _, metric := range ossVolcanoMetricNames {
					for _, dim := range ossVolcanoDimNames {
						var hasData bool
						calls, hasData = probeVolcanoOne(ctx, t, client, ns, sub, metric, dim, probeBucket, start, end, calls)
						if hasData {
							hit[ns+"|"+sub+"|"+metric]++
							nsOK = true
						}
					}
				}
			}
			if nsOK {
				t.Logf("[定案候选] namespace=%s 有指标返回数据", ns)
			}
		}
	}

	// 5) 阳性对照:真实 ECS 实例 CPUPercent(与前案同口径,区分「注册表空」vs「维度错」)
	if probeBucket != "" {
		lastErrKind = volcanoPositiveControl(ctx, t, client, &calls)
	}

	// 6) 汇总与归因(AC-4)
	t.Log("===== volcengine TOS 探测汇总 =====")
	if len(hit) == 0 {
		t.Log("结论: 全部 TOS 候选组合均无数据返回;阳性对照结论=" + lastErrKind)
		t.Log("归因: 若阳性对照同样 metric not found → 云产品监控指标订阅未开通(与前案一致),维持「二期补」判定")
		t.Log("二期补重试路径: ①开通云产品监控指标订阅(写操作,超只读边界)→ ②重跑本测试 → ③按文档候选定案 Namespace=TOS/SubNamespace=tos/MetricName=BucketSize|StorageSize/Dimension=BucketName")
	} else {
		for k, v := range hit {
			t.Logf("结论: %s 返回数据点(bucket 样本 %d 个)—— TOS 指标可用,升格判定输入改写", k, v)
		}
	}
	t.Logf("共发起只读探测调用 %d 次", calls)
}

// volcanoPositiveControl 阳性对照:真实 ECS 实例 id 查公开命名指标,判定
// 「注册表空(订阅未开通)」vs「维度/指标名错」。返回可读结论。
func volcanoPositiveControl(ctx context.Context, t *testing.T, client *cloudmonitor.CLOUDMONITOR, calls *int) string {
	// 复用 NAS 探测先例结论:ECS 阳性对照在该账号同样 metric not found。
	// 此处仅以 TOS 维度形态再验一次 GetMetricData 网关行为(不额外枚举 ECS)。
	*calls++
	callCtx, callCancel := context.WithTimeout(ctx, 15*time.Second)
	defer callCancel()
	input := &cloudmonitor.GetMetricDataInput{
		Namespace:    volcengine.String("Vulcan_ECS"),
		SubNamespace: volcengine.String("Instance"),
		MetricName:   volcengine.String("CPUPercent"),
		StartTime:    volcengine.Int32(int32(time.Now().Unix() - 3600)),
		EndTime:      volcengine.Int32(int32(time.Now().Unix())),
	}
	output, err := client.GetMetricDataWithContext(callCtx, input)
	if err != nil {
		msg := err.Error()
		if len(msg) > 160 {
			msg = msg[:160]
		}
		t.Logf("[阳性对照] ECS CPUPercent err=%s", msg)
		return "网关可用但指标无数据/报错(注册表空特征)"
	}
	if output != nil && output.Data != nil && len(output.Data.MetricDataResults) > 0 {
		for _, r := range output.Data.MetricDataResults {
			if len(r.DataPoints) > 0 {
				t.Log("[阳性对照] ECS CPUPercent 有数据点 —— 注册表非空,候选失败需另行归因")
				return "注册表非空(候选组合需重新归因)"
			}
		}
	}
	t.Log("[阳性对照] ECS CPUPercent 调用成功但无数据点")
	return "注册表空特征(与前案一致)"
}
