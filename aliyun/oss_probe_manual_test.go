// Package aliyun_test OSS 指标探测(oss-ops-insight M1 探测任务,manual probe,只读)。
//
// 阿里云 CMS `acs_oss` namespace 的 bucket 级容量/对象数指标探测(AC-3):
//  1. DescribeMetricMetaList(Namespace=acs_oss)发现该 namespace 全部指标
//     (指标名/维度/单位)——比静态候选矩阵可靠;
//  2. 对实盘 bucket(枚举快照 storage>0 优先)用 DescribeMetricList 按维度
//     查数据点,验证非零且数量级正确;
//  3. 对主容量指标回看 30 天窗口,输出 bucket 增速(高增长近失证据)。
//
// SKIP gate(无 env 不跑):
//   - NAS_PROBE_ALIYUN_AK / NAS_PROBE_ALIYUN_SK / NAS_PROBE_ALIYUN_REGION 直填,或
//   - NAS_PROBE_MONGODB_DSN(+可选 CAM_ENCRYPTION_KEY)从库加载活跃 aliyun 账号
//
// Hard Rule:只读凭证,只写样例行到测试日志,不动生产表。
package aliyun_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/aliyun"
	"github.com/Havens-blog/e-cloudx-sdk/nasprobe"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/cms"
	"github.com/gotomicro/ego/core/elog"
)

// capacityKeywords 容量/对象类指标名筛选关键词(发现结果分类用)。
var (
	ossCapacityKeywords = []string{"size", "storage", "cap"}
	ossObjectKeywords   = []string{"object", "count"}
)

// ossNamespaceCandidates CMS namespace 候选(实盘验证):
//   - acs_oss_dashboard:现行文档口径(2026-03 文档「Access monitoring data」),
//     计量类指标 Period=3600、窗口 ≤31 天、维度 {"BucketName":...};
//   - acs_oss:旧版文档口径(实盘 400 证明多数指标未注册,仅 StorageUtilization
//     等旧指标残留注册,403 维度形态不合法)。
var ossNamespaceCandidates = []string{"acs_oss_dashboard", "acs_oss"}

// ossMetricCandidates 容量/对象数候选指标名(实盘逐格试错,记录每个候选的响应):
// Metering* 为 acs_oss_dashboard 文档口径(MeteringStorageUtilization=存储用量 byte);
// 其余为旧版/通用候选,失败归因「指标不存在」留证据。
var ossMetricCandidates = []string{
	"MeteringStorageUtilization", "MeteringObjectCount", "MeteringObjectNumber",
	"StorageUtilization", "ObjectCount",
}

// TestManualProbeAliyunACSOSSMetrics CMS acs_oss 指标发现 + 实盘非零验证。
func TestManualProbeAliyunACSOSSMetrics(t *testing.T) {
	ak, sk, region := loadAliyunCreds(t)
	if ak == "" {
		t.Skip("未设置 NAS_PROBE_ALIYUN_AK/SK/REGION 或 NAS_PROBE_MONGODB_DSN,跳过(无 env 不跑)")
	}
	if region == "" {
		region = "cn-hangzhou" // CMS endpoint 兜底(指标查询与 bucket 所在 region 无关)
	}
	t.Logf("探测凭证: AK=%s region=%s", nasprobe.MaskAK(ak), region)

	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()

	// 1) 只读枚举实盘 OSS bucket(全局服务,region 传空)
	adapter := aliyun.NewOSSAdapter(ak, sk, region, elog.DefaultLogger)
	buckets, err := adapter.ListBuckets(ctx, "")
	if err != nil {
		t.Fatalf("枚举 OSS bucket 失败(账号凭证或网络问题): %v", err)
	}
	t.Logf("实盘 OSS bucket 数: %d", len(buckets))
	if len(buckets) == 0 {
		t.Skip("账号无 OSS bucket,非零验证无从进行(记录:枚举为空)")
	}
	// 非零验证目标:取枚举快照 storage 最大的前 3 个 bucket(数据点最可能非零)
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].StorageSize > buckets[j].StorageSize })
	probeTargets := make([]string, 0, 3)
	for _, b := range buckets {
		if len(probeTargets) >= 3 {
			break
		}
		probeTargets = append(probeTargets, b.BucketName)
	}
	t.Logf("非零验证目标 bucket(枚举快照最大 3 个): %v", probeTargets)

	// 2) CMS 客户端 + acs_oss 指标发现(元数据接口 + 候选矩阵兜底)
	client, err := newCMSTestClient(ak, sk, region)
	if err != nil {
		t.Fatalf("创建 CMS 客户端失败: %v", err)
	}
	metas, err := listACSOSSMetricMetas(client)
	if err != nil {
		t.Logf("[发现] DescribeMetricMetaList 失败(走候选矩阵): %v", err)
		metas = map[string]ossMetricMeta{}
	}
	names := make([]string, 0, len(metas))
	for name := range metas {
		names = append(names, name)
	}
	sort.Strings(names)
	t.Logf("[发现] namespace=acs_oss 指标元数据 %d 条(DescribeMetricMetaList)", len(names))
	for _, name := range names {
		m := metas[name]
		t.Logf("[发现] metric=%s unit=%s dims=%s desc=%s", name, m.Unit, m.Dimensions, m.Description)
	}
	var probeMetrics []string
	if len(names) > 0 {
		probeMetrics = filterOSSMetrics(names, append(append([]string{}, ossCapacityKeywords...), ossObjectKeywords...))
	}
	if len(probeMetrics) == 0 {
		t.Log("[发现] 元数据接口对 acs_oss 无指标(0 条)—— 以文档口径(namespace=acs_oss_dashboard/Metering*)+候选矩阵逐格试错")
		probeMetrics = ossMetricCandidates
	}

	// 3) namespace × 候选指标 × 实盘 bucket → 逐格探测与非零验证(AC-3)
	// 口径:acs_oss_dashboard 维度 BucketName、计量类 Period=3600;
	//      acs_oss(旧版)维度 bucket、Period=86400。
	passed, failed := 0, 0
	to := time.Now()
	from := to.Add(-3 * 24 * time.Hour)
	errSamples := map[string]string{}
	type hitKey struct{ ns, metric, dim string }
	hits := map[hitKey]int{}
	for _, ns := range ossNamespaceCandidates {
		dimKey := "bucket"
		period := "86400"
		if ns == "acs_oss_dashboard" {
			dimKey = "BucketName"
			period = "3600"
		}
		for _, bucket := range probeTargets {
			for _, name := range probeMetrics {
				vals, err := describeOSSMetricDaily(client, ns, name, dimKey, period, bucket, from, to)
				if err != nil {
					failed++
					key := classifyOSSMetricError(err)
					if _, seen := errSamples[key]; !seen {
						errSamples[key] = err.Error()
					}
					t.Logf("[探测][ERR] ns=%s bucket=%s metric=%s 归因=%s err=%v", ns, bucket, name, key, err)
					continue
				}
				if len(vals) == 0 {
					t.Logf("[探测][空] ns=%s bucket=%s metric=%s 3 天窗口无数据点", ns, bucket, name)
					continue
				}
				latest := vals[len(vals)-1]
				if latest.Value == 0 {
					t.Logf("[探测][零值] ns=%s bucket=%s metric=%s 最新值=0(零值例外打标路径)", ns, bucket, name)
					continue
				}
				passed++
				hits[hitKey{ns, name, dimKey}]++
				t.Logf("[非零验证][PASS] ns=%s bucket=%s metric=%s 数据点=%d 最新值=%v 数量级=%s",
					ns, bucket, name, len(vals), latest.Value,
					nasprobe.DescribeMagnitude(nasprobe.BytesToGB(latest.Value)))
			}
		}
	}
	for k, msg := range errSamples {
		t.Logf("[归因样本][%s]: %s", k, msg)
	}
	for k, n := range hits {
		t.Logf("[定案候选] namespace=%s metric=%s dim=%s 有非零数据(bucket 样本 %d 个)", k.ns, k.metric, k.dim, n)
	}

	// 4) 主容量指标 30 天窗口 → bucket 增速(高增长近失证据)
	probeOSSBucketGrowth(ctx, t, client, probeMetrics, probeTargets)

	t.Logf("===== aliyun 探测汇总:acs_oss* 非零验证 PASS=%d FAIL(含调用错误)=%d(空数据点不计 FAIL)=====", passed, failed)
}

// classifyOSSMetricError CMS 指标查询错误粗归因(指标不存在 vs 维度不合法 vs 其他)。
func classifyOSSMetricError(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not exist"), strings.Contains(msg, "notexist"), strings.Contains(msg, "no metric"):
		return "指标不存在(namespace/指标名未注册)"
	case strings.Contains(msg, "dimension"), strings.Contains(msg, "parameter"):
		return "维度/参数不合法(指标可能存在,维度形态不对)"
	case strings.Contains(msg, "signature"), strings.Contains(msg, "forbidden"), strings.Contains(msg, "auth"):
		return "鉴权/权限"
	default:
		return "其他"
	}
}

// ossMetricMeta 单条指标元数据(发现结果)。
type ossMetricMeta struct {
	Unit        string
	Dimensions  string
	Description string
}

// listACSOSSMetricMetas DescribeMetricMetaList 分页发现 acs_oss 全部指标元数据。
func listACSOSSMetricMetas(client *cms.Client) (map[string]ossMetricMeta, error) {
	out := map[string]ossMetricMeta{}
	for page := 1; page <= 20; page++ {
		request := cms.CreateDescribeMetricMetaListRequest()
		request.Namespace = "acs_oss"
		request.PageNumber = requests.Integer(fmt.Sprintf("%d", page))
		request.PageSize = "100"
		response, err := client.DescribeMetricMetaList(request)
		if err != nil {
			return nil, fmt.Errorf("DescribeMetricMetaList 失败: %w", err)
		}
		if response == nil {
			break
		}
		for _, r := range response.Resources.Resource {
			out[r.MetricName] = ossMetricMeta{Unit: r.Unit, Dimensions: r.Dimensions, Description: r.Description}
		}
		total := 0
		fmt.Sscanf(response.TotalCount, "%d", &total)
		if len(response.Resources.Resource) == 0 || page*100 >= total {
			break
		}
	}
	return out, nil
}

// filterOSSMetrics 按关键词筛选指标名(小写匹配)。
func filterOSSMetrics(names []string, keywords []string) []string {
	var out []string
	for _, name := range names {
		lower := strings.ToLower(name)
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				out = append(out, name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// ossDatapoint 单个指标数据点。
type ossDatapoint struct {
	Timestamp int64
	Value     float64
}

// describeOSSMetricDaily DescribeMetricList 查询单 bucket 单指标数据点
// (period 由调用方按 namespace 口径传入:acs_oss_dashboard 计量类 3600,旧版 86400)。
func describeOSSMetricDaily(client *cms.Client, namespace, metricName, dimKey, period, bucket string, from, to time.Time) ([]ossDatapoint, error) {
	request := cms.CreateDescribeMetricListRequest()
	request.Namespace = namespace
	request.MetricName = metricName
	request.Period = period
	request.Length = "1000"
	request.Dimensions = fmt.Sprintf(`{%q:%q}`, dimKey, bucket)
	request.StartTime = fmt.Sprintf("%d", from.UnixMilli())
	request.EndTime = fmt.Sprintf("%d", to.UnixMilli())
	response, err := client.DescribeMetricList(request)
	if err != nil {
		return nil, fmt.Errorf("DescribeMetricList(%s) 失败: %w", metricName, err)
	}
	if response == nil {
		return nil, nil
	}
	return parseOSSDatapoints(response.Datapoints)
}

// parseOSSDatapoints 解析 DescribeMetricList 的 Datapoints JSON(大小写不敏感,
// 与 NAS 适配器 parseCMSDatapoints 同口径)。
func parseOSSDatapoints(raw string) ([]ossDatapoint, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "[]" {
		return nil, nil
	}
	var items []map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
		return nil, fmt.Errorf("解析 Datapoints JSON 失败: %w", err)
	}
	points := make([]ossDatapoint, 0, len(items))
	for _, item := range items {
		var dp ossDatapoint
		for k, v := range item {
			f, ok := ossNumber(v)
			if !ok {
				continue
			}
			switch strings.ToLower(k) {
			case "timestamp":
				dp.Timestamp = int64(f)
			case "value", "average", "maximum":
				dp.Value = f
			}
		}
		if dp.Value != 0 || dp.Timestamp != 0 {
			points = append(points, dp)
		}
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Timestamp < points[j].Timestamp })
	return points, nil
}

// ossNumber 数值提取(float64/json.Number/字符串三形态兜底)。
func ossNumber(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		var f float64
		_, err := fmt.Sscanf(strings.TrimSpace(n), "%g", &f)
		return f, err == nil
	default:
		return 0, false
	}
}

// probeOSSBucketGrowth 对探测目标 bucket 用主容量指标回看 30 天,输出增速与
// 高增长判定(proposal Urgency「近 30 天存储量增速 > X% 高增长 bucket」近失证据)。
func probeOSSBucketGrowth(ctx context.Context, t *testing.T, client *cms.Client, names []string, buckets []string) {
	capMetrics := filterOSSMetrics(names, ossCapacityKeywords)
	if len(capMetrics) == 0 {
		t.Log("[增速] acs_oss 未发现容量类指标,30 天增速无法计算")
		return
	}
	metric := capMetrics[0]
	const growthNamespace = "acs_oss_dashboard"
	const growthPeriod = "3600"
	to := time.Now()
	from := to.Add(-30 * 24 * time.Hour)
	highGrowth := 0
	for _, bucket := range buckets {
		vals, err := describeOSSMetricDaily(client, growthNamespace, metric, "BucketName", growthPeriod, bucket, from, to)
		if err != nil || len(vals) < 2 {
			t.Logf("[增速] bucket=%s metric=%s 数据点不足(窗口 %d 点),增速不可计算", bucket, metric, len(vals))
			continue
		}
		growth, ok := nasprobe.OSSGrowthPercent(vals[0].Value, vals[len(vals)-1].Value)
		if nasprobe.IsHighGrowth(growth, ok) {
			highGrowth++
		}
		t.Logf("[增速] bucket=%s metric=%s 首点=%.4g 末点=%.4g 增速=%.2f%%(ok=%v) 高增长=%v",
			bucket, metric, vals[0].Value, vals[len(vals)-1].Value, growth, ok,
			nasprobe.IsHighGrowth(growth, ok))
	}
	t.Logf("[增速汇总] 高增长 bucket 数(>30%% 月增速)= %d", highGrowth)
}

// newCMSTestClient 创建 CMS 探测客户端(与 NAS 适配器同域名字典)。
func newCMSTestClient(ak, sk, region string) (*cms.Client, error) {
	credential := credentials.NewAccessKeyCredential(ak, sk)
	config := sdk.NewConfig()
	config.Scheme = "https"
	client, err := cms.NewClientWithOptions(region, config, credential)
	if err != nil {
		return nil, err
	}
	client.Domain = fmt.Sprintf("metrics.%s.aliyuncs.com", region)
	return client, nil
}

// loadAliyunCreds 直填凭证优先,否则从库加载活跃 aliyun 账号。
func loadAliyunCreds(t *testing.T) (ak, sk, region string) {
	if ak = nasprobe.EnvAK("ALIYUN"); ak != "" {
		if sk = nasprobe.EnvSK("ALIYUN"); sk != "" {
			return ak, sk, nasprobe.EnvRegion("ALIYUN")
		}
		return "", "", ""
	}
	if !nasprobe.ProbeDSNEnabled() {
		return "", "", ""
	}
	accounts, err := nasprobe.LoadProbeAccounts()
	if err != nil {
		t.Logf("从库加载账号失败(将跳过): %v", err)
		return "", "", ""
	}
	for _, a := range accounts {
		if a.Provider == "aliyun" {
			rg := ""
			if len(a.Regions) > 0 {
				rg = a.Regions[0]
			}
			return a.AK, a.SK, rg
		}
	}
	return "", "", ""
}
