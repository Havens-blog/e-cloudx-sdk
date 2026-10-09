// Package tencent_test OSS 指标探测(oss-ops-insight M1 探测任务,manual probe,只读)。
//
// 腾讯云 monitor `QCE/COS` namespace 的 bucket 级容量/对象数指标探测(AC-4):
//  1. DescribeBaseMetrics(Namespace=QCE/COS)发现指标元数据(指标名/维度/单位);
//  2. GetMonitorData 用实盘 bucket 查数据点,验证非零;
//  3. 失败归因:「指标订阅未开通/无数据上报」vs「指标不存在」,记录二期补路径
//     (参照 NAS volcengine 先例:归因而非笼统判失败)。
//
// 文档口径候选(腾讯云 COS 监控指标,文档 248/4418):StdStorage(标准存储容量)、
// NrStdStorage(标准存储对象数)、LowFreqStorage/LowFreqObjectNumber(低频)、
// ArchivedStorage(归档)等;维度 bucket(部分指标需 appid+bucket 双维)。
//
// SKIP gate(无 env 不跑):
//   - NAS_PROBE_TENCENT_AK / NAS_PROBE_TENCENT_SK / NAS_PROBE_TENCENT_REGION 直填,或
//   - NAS_PROBE_MONGODB_DSN(+可选 CAM_ENCRYPTION_KEY)从库加载活跃 tencent 账号
//
// Hard Rule:只读凭证,只写样例行到测试日志,不动生产表。
package tencent_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/nasprobe"
	"github.com/Havens-blog/e-cloudx-sdk/tencent"
	"github.com/gotomicro/ego/core/elog"
	cam "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cam/v20190116"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	monitor "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/monitor/v20180724"
)

// cosNamespace COS 云监控业务命名空间。
const cosNamespace = "QCE/COS"

// cosMetricCandidates 容量/对象数候选指标名(文档口径 + 发现结果择优)。
var cosMetricCandidates = []string{
	"StdStorage", "NrStdStorage", // 标准存储容量 / 对象数(文档口径)
	"StorageSize", "ObjectNumber", // 通用候选
	"LowFreqStorage", "ArchivedStorage", // 分层参考
}

// TestManualProbeTencentQCECOSMetrics monitor QCE/COS 指标发现 + 实盘探测与归因。
func TestManualProbeTencentQCECOSMetrics(t *testing.T) {
	ak, sk, region := loadTencentCreds(t)
	if ak == "" {
		t.Skip("未设置 NAS_PROBE_TENCENT_AK/SK/REGION 或 NAS_PROBE_MONGODB_DSN,跳过(无 env 不跑)")
	}
	t.Logf("探测凭证: AK=%s region=%s", nasprobe.MaskAK(ak), region)

	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()

	// 1) 只读枚举实盘 COS bucket(全局服务,region 传空)
	adapter := tencent.NewCOSAdapter(ak, sk, region, elog.DefaultLogger)
	buckets, err := adapter.ListBuckets(ctx, "")
	if err != nil {
		t.Fatalf("枚举 COS bucket 失败(账号凭证或网络问题): %v", err)
	}
	t.Logf("实盘 COS bucket 数: %d", len(buckets))
	if len(buckets) == 0 {
		t.Skip("账号无 COS bucket,探测记「空集,无数据可采」(参照 NAS tencent 先例)")
	}
	for _, b := range buckets {
		t.Logf("bucket: %s region=%s storage=%d byte(%.4f GB) objects=%d",
			b.BucketName, b.Region, b.StorageSize, nasprobe.BytesToGB(float64(b.StorageSize)), b.ObjectCount)
	}

	// 2) DescribeBaseMetrics 发现 QCE/COS 指标元数据(任取一个 bucket 的 region 建客户端)
	probeRegion := buckets[0].Region
	monitorClient := newTencentMonitorClient(ak, sk, probeRegion)
	metas := discoverCOSMetrics(t, monitorClient)
	names := make([]string, 0, len(metas))
	for name := range metas {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 0 {
		t.Logf("[发现] namespace=%s 指标元数据 %d 条:", cosNamespace, len(names))
		for _, name := range names {
			m := metas[name]
			t.Logf("[发现] metric=%s unit=%s dims=%v", name, m.unit, m.dims)
		}
	} else {
		t.Log("[发现] DescribeBaseMetrics 返回空 —— 记录归因:元数据不可得,走候选矩阵")
	}

	// 3) GetMonitorData 逐 bucket 逐候选指标探测(维度双形态:bucket / appid+bucket)
	appid := resolveTencentAppID(t, ak, sk)
	endT := time.Now()
	startT := endT.Add(-3 * 24 * time.Hour)
	type hit struct{ metric, dimForm string }
	hits := map[hit]int{}
	errs := map[string]string{} // 错误样本归因
	probed := 0
	probeBuckets := buckets
	if len(probeBuckets) > 3 {
		probeBuckets = probeBuckets[:3]
	}
	for _, b := range probeBuckets {
		client := newTencentMonitorClient(ak, sk, b.Region)
		for _, metric := range mergeCandidates(names, cosMetricCandidates) {
			for _, dimForm := range []string{"bucket", "appid+bucket"} {
				if dimForm == "appid+bucket" && appid == "" {
					continue
				}
				probed++
				dataPoints, err := getCOSMetricDaily(client, metric, appid, b.BucketName, dimForm, startT, endT)
				if err != nil {
					key := classifyCOSError(err)
					errs[key] = err.Error()
					t.Logf("[探测][ERR] bucket=%s metric=%s dim=%s 归因=%s err=%v", b.BucketName, metric, dimForm, key, err)
					continue
				}
				if len(dataPoints) == 0 {
					t.Logf("[探测][空] bucket=%s metric=%s dim=%s 3 天窗口无数据点", b.BucketName, metric, dimForm)
					continue
				}
				hits[hit{metric, dimForm}]++
				latest := dataPoints[len(dataPoints)-1]
				unit := ""
				if m, ok := metas[metric]; ok {
					unit = m.unit
				}
				display := fmt.Sprintf("%v %s", latest, unit)
				if strings.Contains(strings.ToLower(unit), "byte") || strings.EqualFold(unit, "mb") {
					display = fmt.Sprintf("%v %s", latest, unit)
				}
				t.Logf("[探测][PASS] bucket=%s metric=%s dim=%s 数据点=%d 最新值=%s", b.BucketName, metric, dimForm, len(dataPoints), display)
			}
		}
	}

	// 4) 汇总与归因(AC-4)
	t.Log("===== tencent 探测汇总 =====")
	if len(hits) == 0 {
		t.Log("结论: 全部候选组合均无数据 —— 归因样本见上方 [ERR](区分「指标订阅未开通/无上报」vs「指标不存在」),按「二期补」判定素材记录")
	} else {
		for k, n := range hits {
			t.Logf("结论: metric=%s dim=%s 有数据点(bucket 样本 %d 个)", k.metric, k.dimForm, n)
		}
	}
	for k, msg := range errs {
		t.Logf("归因样本[%s]: %s", k, truncateErr(msg))
	}
	t.Logf("共发起只读探测调用 %d 次", probed)
}

// cosMetricMeta 单条指标元数据。
type cosMetricMeta struct {
	unit string
	dims []string
}

// discoverCOSMetrics DescribeBaseMetrics 发现 QCE/COS 全部指标(候选名逐个 + 空名全量)。
func discoverCOSMetrics(t *testing.T, client *monitor.Client) map[string]cosMetricMeta {
	out := map[string]cosMetricMeta{}
	request := monitor.NewDescribeBaseMetricsRequest()
	request.Namespace = common.StringPtr(cosNamespace)
	request.MetricName = common.StringPtr("")
	response, err := client.DescribeBaseMetrics(request)
	if err != nil {
		t.Logf("[发现] DescribeBaseMetrics 全量失败: %v", err)
		return out
	}
	if response == nil || response.Response == nil {
		return out
	}
	for _, m := range response.Response.MetricSet {
		if m == nil || m.MetricName == nil {
			continue
		}
		meta := cosMetricMeta{}
		if m.Unit != nil {
			meta.unit = *m.Unit
		}
		for _, d := range m.Dimensions {
			if d != nil && d.Dimensions != nil {
				for _, dn := range d.Dimensions {
					if dn != nil {
						meta.dims = append(meta.dims, *dn)
					}
				}
			}
		}
		out[*m.MetricName] = meta
	}
	return out
}

// mergeCandidates 候选名与发现结果合并(发现优先,保序去重)。
func mergeCandidates(discovered, fallback []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(discovered)+len(fallback))
	for _, n := range append(append([]string{}, discovered...), fallback...) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// getCOSMetricDaily GetMonitorData 查询单 bucket 单指标天粒度数据点。
// dimForm="bucket" 单维;dimForm="appid+bucket" 双维(文档顺序 appid 在前)。
func getCOSMetricDaily(client *monitor.Client, metricName, appid, bucket, dimForm string, startT, endT time.Time) ([]float64, error) {
	request := monitor.NewGetMonitorDataRequest()
	request.Namespace = common.StringPtr(cosNamespace)
	request.MetricName = common.StringPtr(metricName)
	request.Period = common.Uint64Ptr(86400)
	dims := []*monitor.Dimension{{Name: common.StringPtr("bucket"), Value: common.StringPtr(bucket)}}
	if dimForm == "appid+bucket" {
		dims = []*monitor.Dimension{
			{Name: common.StringPtr("appid"), Value: common.StringPtr(appid)},
			{Name: common.StringPtr("bucket"), Value: common.StringPtr(bucket)},
		}
	}
	request.Instances = []*monitor.Instance{{Dimensions: dims}}
	request.StartTime = common.StringPtr(startT.Format("2006-01-02T15:04:05+08:00"))
	request.EndTime = common.StringPtr(endT.Format("2006-01-02T15:04:05+08:00"))

	response, err := client.GetMonitorData(request)
	if err != nil {
		return nil, err
	}
	if response == nil || response.Response == nil || len(response.Response.DataPoints) == 0 {
		return nil, nil
	}
	for _, dp := range response.Response.DataPoints {
		if dp == nil || len(dp.Values) == 0 {
			continue
		}
		vals := make([]float64, 0, len(dp.Values))
		for _, v := range dp.Values {
			if v != nil {
				vals = append(vals, *v)
			}
		}
		return vals, nil
	}
	return nil, nil
}

// classifyCOSError 探测错误粗归因(订阅未开通 vs 指标不存在 vs 参数/鉴权)。
func classifyCOSError(err error) string {
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "authfailure"), strings.Contains(lower, "signature"), strings.Contains(lower, "unauthorized"):
		return "鉴权失败"
	case strings.Contains(lower, "invalidparameter"), strings.Contains(lower, "dimension"), strings.Contains(lower, "diam"):
		return "维度/参数不合法(指标可能存在,维度形态不对)"
	case strings.Contains(lower, "notfound"), strings.Contains(lower, "not found"), strings.Contains(lower, "unsupported"), strings.Contains(lower, "nonexistent"):
		return "指标不存在(或未注册)"
	case strings.Contains(lower, "limitexceeded"), strings.Contains(lower, "throttl"):
		return "限流"
	default:
		return "其他(详见错误文本)"
	}
}

// truncateErr 错误样本截断(日志可读性)。
func truncateErr(msg string) string {
	msg = strings.ReplaceAll(msg, "\n", " ")
	if len(msg) > 200 {
		return msg[:200] + "..."
	}
	return msg
}

// resolveTencentAppID CAM GetUserAppId 解析 APPID(QCE/COS 双维探测用)。
func resolveTencentAppID(t *testing.T, ak, sk string) string {
	credential := common.NewCredential(ak, sk)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "cam.tencentcloudapi.com"
	client, err := cam.NewClient(credential, "", cpf)
	if err != nil {
		t.Logf("[APPID] 创建 CAM 客户端失败: %v", err)
		return ""
	}
	resp, err := client.GetUserAppId(cam.NewGetUserAppIdRequest())
	if err != nil || resp == nil || resp.Response == nil || resp.Response.AppId == nil {
		t.Logf("[APPID] 获取失败(双维探测将跳过): %v", err)
		return ""
	}
	return fmt.Sprintf("%d", *resp.Response.AppId)
}

// newTencentMonitorClient 创建 monitor 客户端(按 bucket region)。
func newTencentMonitorClient(ak, sk, region string) *monitor.Client {
	credential := common.NewCredential(ak, sk)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "monitor.tencentcloudapi.com"
	client, err := monitor.NewClient(credential, region, cpf)
	if err != nil {
		return nil
	}
	return client
}

// loadTencentCreds 直填凭证优先,否则从库加载活跃 tencent 账号。
func loadTencentCreds(t *testing.T) (ak, sk, region string) {
	if ak = nasprobe.EnvAK("TENCENT"); ak != "" {
		if sk = nasprobe.EnvSK("TENCENT"); sk != "" {
			return ak, sk, nasprobe.EnvRegion("TENCENT")
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
		if a.Provider == "tencent" {
			rg := ""
			if len(a.Regions) > 0 {
				rg = a.Regions[0]
			}
			return a.AK, a.SK, rg
		}
	}
	return "", "", ""
}
