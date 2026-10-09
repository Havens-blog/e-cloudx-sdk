// Package tencent_test Disk 指标探测(disk-ops-insight M1 探测任务,manual probe,只读)。
//
// 腾讯云 monitor `QCE/CBS` namespace 的云盘指标探测(AC-4):使用率/IOPS/吞吐
// 指标发现 + 实盘非零验证 + 失败归因(指标订阅未开通 vs 指标不存在 vs 维度
// 不合法),记录二期补路径。
//
// 文档口径候选(腾讯云 CBS 云硬盘监控指标):DiskReadTotal/DiskWriteTotal
// (吞吐 KB/s? 以元数据单位为准)、DiskReadIops/DiskWriteIops(IOPS)、
// DiskIoActiveTimePercent(IO 活跃时间占比=繁忙度)、CvmDiskUsage 等;
// 维度 diskId(云盘级)。
//
// SKIP gate(无 env 不跑):
//   - NAS_PROBE_TENCENT_AK / NAS_PROBE_TENCENT_SK / NAS_PROBE_TENCENT_REGION 直填,或
//   - NAS_PROBE_MONGODB_DSN(+可选 CAM_ENCRYPTION_KEY)从库加载活跃 tencent 账号
//
// Hard Rule:只读凭证,只写样例行到测试日志,不动生产表。
package tencent_test

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/nasprobe"
	"github.com/Havens-blog/e-cloudx-sdk/tencent"
	"github.com/gotomicro/ego/core/elog"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	monitor "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/monitor/v20180724"
)

// cbsNamespace CBS 云硬盘云监控业务命名空间。
const cbsNamespace = "QCE/CBS"

// cbsMetricCandidates 云盘候选指标名(文档口径 + 发现结果择优)。
var cbsMetricCandidates = []string{
	"DiskReadIops", "DiskWriteIops", // IOPS(次/秒)
	"DiskReadTotal", "DiskWriteTotal", // 吞吐(KB/s,以元数据单位为准)
	"DiskIoActiveTimePercent", "DiskTotalIoRatio", // 繁忙度(%)
	"DiskUsage", "CvmDiskUsage", // 使用率候选(文档差异,逐格试错)
}

// TestManualProbeTencentQCECBSMetrics monitor QCE/CBS 指标发现 + 实盘探测与归因。
func TestManualProbeTencentQCECBSMetrics(t *testing.T) {
	ak, sk, region := loadTencentCreds(t)
	if ak == "" {
		t.Skip("未设置 NAS_PROBE_TENCENT_AK/SK/REGION 或 NAS_PROBE_MONGODB_DSN,跳过(无 env 不跑)")
	}
	t.Logf("探测凭证: AK=%s region=%s", nasprobe.MaskAK(ak), region)

	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()

	// 1) 只读枚举实盘云硬盘(地域性资源,逐 region)
	regions := nasprobe.EnvRegions("TENCENT", region)
	var tencentProbeInstances []tencentDiskTarget
	for _, rg := range regions {
		adapter := tencent.NewDiskAdapter(ak, sk, rg, elog.DefaultLogger)
		list, err := adapter.ListInstances(ctx, rg)
		if err != nil {
			t.Logf("[枚举失败] region=%s err=%v", rg, err)
			continue
		}
		t.Logf("region=%s 磁盘数: %d", rg, len(list))
		for _, d := range list {
			tencentProbeInstances = append(tencentProbeInstances, tencentDiskTarget{DiskID: d.DiskID, InstanceID: d.InstanceID})
		}
	}
	t.Logf("实盘磁盘总数: %d", len(tencentProbeInstances))
	if len(tencentProbeInstances) == 0 {
		t.Skip("各 region 均无磁盘,非零验证无从进行(记录:枚举为空)")
	}
	sort.Slice(tencentProbeInstances, func(i, j int) bool { return tencentProbeInstances[i].DiskID < tencentProbeInstances[j].DiskID })
	if len(tencentProbeInstances) > 5 {
		tencentProbeInstances = tencentProbeInstances[:5]
	}
	diskIDs := make([]string, 0, len(tencentProbeInstances))
	for _, d := range tencentProbeInstances {
		diskIDs = append(diskIDs, d.DiskID)
	}

	// 2) DescribeBaseMetrics 发现 QCE/CBS 全部指标(元数据接口)
	monitorClient := newTencentMonitorClient(ak, sk, regions[0])
	metas := discoverCBSMetrics(t, monitorClient)
	names := make([]string, 0, len(metas))
	for name := range metas {
		names = append(names, name)
	}
	sort.Strings(names)
	t.Logf("[发现] namespace=%s 指标元数据 %d 条", cbsNamespace, len(names))
	for _, name := range names {
		m := metas[name]
		t.Logf("[发现] metric=%s unit=%s dims=%v", name, m.unit, m.dims)
	}
	probeMetrics := mergeCandidates(discoverDiskMetrics(names), cbsMetricCandidates)

	// 3) 候选指标 × 实盘磁盘(维度 diskId)→ GetMonitorData 非零验证 + 归因
	startT := time.Now().Add(-3 * 24 * time.Hour)
	endT := time.Now()
	passed, zero, empty := 0, 0, 0
	errSamples := map[string]string{}
	for _, diskID := range diskIDs {
		for _, name := range probeMetrics {
			vals, err := getCBSMetricDaily(monitorClient, name, diskID, startT, endT)
			if err != nil {
				key := classifyCBSMetricError(err)
				if _, seen := errSamples[key]; !seen {
					errSamples[key] = err.Error()
				}
				t.Logf("[探测][ERR] disk=%s metric=%s 归因=%s err=%v", diskID, name, key, err)
				continue
			}
			if len(vals) == 0 {
				empty++
				continue
			}
			latest := vals[len(vals)-1]
			if latest == 0 {
				zero++
				t.Logf("[探测][零值] disk=%s metric=%s 最新值=0(可能当日无 IO)", diskID, name)
				continue
			}
			passed++
			t.Logf("[非零验证][PASS] disk=%s metric=%s 数据点=%d 最新值=%v", diskID, name, len(vals), latest)
		}
	}
	for k, msg := range errSamples {
		t.Logf("[归因样本][%s]: %s", k, msg)
	}
	// 4) 阳性对照(只读):QCE/CVM CpuUsage(unInstanceId 维度,实盘挂载实例)——
	//    区分「QCE/CBS namespace 未注册/未订阅」vs「候选指标名错误」
	posOK := false
	for _, d := range tencentProbeInstances {
		if d.InstanceID == "" {
			continue
		}
		vals, err := getCVMPositiveMetric(monitorClient, d.InstanceID, startT, endT)
		if err != nil {
			t.Logf("[阳性对照][ERR] instance=%s err=%v", d.InstanceID, err)
			continue
		}
		if len(vals) > 0 && vals[len(vals)-1] != 0 {
			posOK = true
			t.Logf("[阳性对照][PASS] QCE/CVM CpuUsage instance=%s 最新值=%v —— 云监控可用,QCE/CBS 磁盘指标未注册/未订阅", d.InstanceID, vals[len(vals)-1])
			break
		}
	}
	if passed == 0 && !posOK {
		t.Log("[归因判定] CBS 磁盘候选全部 invalid 且 QCE/CVM 阳性对照也无数据 —— 需复核账号监控订阅状态;若阳性对照可用则为 QCE/CBS 指标未注册(二期补:控制台开通云硬盘监控或核对文档指标名)")
	}
	t.Logf("===== tencent 磁盘探测汇总: PASS=%d 零值=%d 空窗口=%d 阳性对照=%v(定案与二期路径见 probe-report.md)=====", passed, zero, empty, posOK)
}

// discoverDiskMetrics 从发现结果中筛磁盘类指标(小写含 disk/iops 关键词)。
func discoverDiskMetrics(names []string) []string {
	var out []string
	for _, name := range names {
		lower := strings.ToLower(name)
		if strings.Contains(lower, "disk") || strings.Contains(lower, "iops") {
			out = append(out, name)
		}
	}
	return out
}

// discoverCBSMetrics DescribeBaseMetrics 发现 QCE/CBS 全部指标(与 COS 同型)。
func discoverCBSMetrics(t *testing.T, client *monitor.Client) map[string]cosMetricMeta {
	out := map[string]cosMetricMeta{}
	request := monitor.NewDescribeBaseMetricsRequest()
	request.Namespace = common.StringPtr(cbsNamespace)
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

// getCBSMetricDaily GetMonitorData 查询单磁盘单指标天粒度数据点(维度 diskId)。
func getCBSMetricDaily(client *monitor.Client, metricName, diskID string, startT, endT time.Time) ([]float64, error) {
	request := monitor.NewGetMonitorDataRequest()
	request.Namespace = common.StringPtr(cbsNamespace)
	request.MetricName = common.StringPtr(metricName)
	request.Period = common.Uint64Ptr(86400)
	request.Instances = []*monitor.Instance{{
		Dimensions: []*monitor.Dimension{
			{Name: common.StringPtr("diskId"), Value: common.StringPtr(diskID)},
		},
	}}
	request.StartTime = common.StringPtr(startT.Format("2006-01-02T15:04:05+08:00"))
	request.EndTime = common.StringPtr(endT.Format("2006-01-02T15:04:05+08:00"))

	response, err := client.GetMonitorData(request)
	if err != nil {
		return nil, err
	}
	if response == nil || response.Response == nil || len(response.Response.DataPoints) == 0 {
		return nil, nil
	}
	var out []float64
	for _, dp := range response.Response.DataPoints {
		if dp == nil {
			continue
		}
		for _, v := range dp.Values {
			if v != nil {
				out = append(out, *v)
			}
		}
	}
	return out, nil
}

// getCVMPositiveMetric 阳性对照查询:QCE/CVM CpuUsage(unInstanceId 维度)。
func getCVMPositiveMetric(client *monitor.Client, instanceID string, startT, endT time.Time) ([]float64, error) {
	request := monitor.NewGetMonitorDataRequest()
	request.Namespace = common.StringPtr("QCE/CVM")
	request.MetricName = common.StringPtr("CpuUsage")
	request.Period = common.Uint64Ptr(86400)
	request.Instances = []*monitor.Instance{{
		Dimensions: []*monitor.Dimension{
			{Name: common.StringPtr("unInstanceId"), Value: common.StringPtr(instanceID)},
		},
	}}
	request.StartTime = common.StringPtr(startT.Format("2006-01-02T15:04:05+08:00"))
	request.EndTime = common.StringPtr(endT.Format("2006-01-02T15:04:05+08:00"))
	response, err := client.GetMonitorData(request)
	if err != nil {
		return nil, err
	}
	if response == nil || response.Response == nil {
		return nil, nil
	}
	var out []float64
	for _, dp := range response.Response.DataPoints {
		if dp == nil {
			continue
		}
		for _, v := range dp.Values {
			if v != nil {
				out = append(out, *v)
			}
		}
	}
	return out, nil
}

// tencentDiskTarget 腾讯云磁盘探测目标(从 DiskInstance 提取的最小字段)。
type tencentDiskTarget struct {
	DiskID     string
	InstanceID string
}

// classifyCBSMetricError 腾讯 monitor 错误粗归因(订阅/不存在/维度/其他)。
func classifyCBSMetricError(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "notfound"), strings.Contains(msg, "not found"), strings.Contains(msg, "unsupported"):
		return "指标/namespace 不存在"
	case strings.Contains(msg, "dimension"), strings.Contains(msg, "invalidparameter"), strings.Contains(msg, "param"):
		return "维度/参数不合法(指标可能存在,维度形态不对)"
	case strings.Contains(msg, "authfailure"), strings.Contains(msg, "unauthorized"), strings.Contains(msg, "denied"):
		return "鉴权/权限(可能指标未订阅开通)"
	default:
		return "其他"
	}
}
