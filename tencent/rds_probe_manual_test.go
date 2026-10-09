// Package tencent_test RDS 指标探测(rds-ops-insight M1 探测任务,manual probe,只读)。
//
// 腾讯云 monitor `QCE/CDB` namespace 的云数据库 MySQL 指标探测(AC-4):
// CPU/内存/磁盘/连接数指标发现 + 实盘非零验证 + 失败归因(指标订阅未开通
// vs 指标不存在 vs 维度不合法),记录二期补路径。
//
// 文档口径候选(腾讯云 CDB 云数据库监控指标):CpuUsage(%)/MemoryUsage(%)/
// DiskUsage(%)是百分比直给;连接数 KeepAlive/ConnectionNum / SlowQueries 等。
// 注意 QCE/CDB 仅覆盖 MySQL 主实例(引擎口径差异记录到报告,AC-5)。
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
	cxtypes "github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	monitor "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/monitor/v20180724"
)

// cdbNamespace CDB 云数据库云监控业务命名空间(文档口径)。
const cdbNamespace = "QCE/CDB"

// cdbMetricCandidates 云数据库候选指标名(文档口径;元数据发现择优)。
var cdbMetricCandidates = []string{
	"CpuUsage",    // CPU 使用率(%)
	"MemoryUsage", // 内存使用率(%)
	"DiskUsage",   // 磁盘使用率(%)
	"KeepAlive",   // 连接数(文档口径之一)
	"ConnectionNum",
	"SlowQueries",
	"QpsUseRate",
}

// TestManualProbeTencentQCEDBMetrics monitor QCE/CDB 指标发现 + 实盘探测与归因(AC-4)。
func TestManualProbeTencentQCEDBMetrics(t *testing.T) {
	ak, sk, region := loadTencentCreds(t)
	if ak == "" {
		t.Skip("未设置 NAS_PROBE_TENCENT_AK/SK/REGION 或 NAS_PROBE_MONGODB_DSN,跳过(无 env 不跑)")
	}
	t.Logf("探测凭证: AK=%s region=%s", nasprobe.MaskAK(ak), region)

	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()

	// 1) 只读枚举实盘 RDS 实例(地域性资源,逐 region)
	regions := nasprobe.EnvRegions("TENCENT", region)
	var instances []cxtypes.RDSInstance
	for _, rg := range regions {
		adapter := tencent.NewRDSAdapter(&domain.CloudAccount{AccessKeyID: ak, AccessKeySecret: sk}, rg, elog.DefaultLogger)
		list, err := adapter.ListInstances(ctx, rg)
		if err != nil {
			t.Logf("[枚举失败] region=%s err=%v", rg, err)
			continue
		}
		t.Logf("region=%s RDS 实例数: %d", rg, len(list))
		instances = append(instances, list...)
	}
	t.Logf("实盘 RDS 实例总数: %d", len(instances))
	engineParts := map[string]int{}
	for _, ins := range instances {
		engineParts[strings.ToLower(ins.Engine)]++
	}
	t.Logf("引擎分布: %v", engineParts)
	if len(instances) == 0 {
		// 无实例:非零验证无从进行,但元数据发现可先行归因
		// (QCE/CDB 指标是否注册 → 「订阅未开通」vs「namespace 不存在」)
		t.Log("[归因前置] 实盘无 RDS 实例,仅做 DescribeBaseMetrics 元数据归因")
	} else {
		sort.Slice(instances, func(i, j int) bool { return instances[i].Storage > instances[j].Storage })
		if len(instances) > 5 {
			instances = instances[:5]
		}
	}
	instanceIDs := make([]string, 0, len(instances))
	for _, ins := range instances {
		instanceIDs = append(instanceIDs, ins.InstanceID)
	}

	// 2) DescribeBaseMetrics 发现 QCE/CDB 全部指标(元数据接口)
	monitorClient := newTencentMonitorClient(ak, sk, regions[0])
	metas := discoverCDBMetrics(t, monitorClient)
	names := make([]string, 0, len(metas))
	for name := range metas {
		names = append(names, name)
	}
	sort.Strings(names)
	t.Logf("[发现] namespace=%s 指标元数据 %d 条", cdbNamespace, len(names))
	for _, name := range names {
		m := metas[name]
		t.Logf("[发现] metric=%s unit=%s dims=%v", name, m.unit, m.dims)
	}
	probeMetrics := mergeCandidates(discoverDBMetrics(names), cdbMetricCandidates)

	// 3) 候选指标 × 实盘实例(维度 uInstanceId)→ GetMonitorData 非零验证 + 归因
	startT := time.Now().Add(-3 * 24 * time.Hour)
	endT := time.Now()
	passed, zero, empty := 0, 0, 0
	errSamples := map[string]string{}
	memorySamples := []float64{}
	for _, instanceID := range instanceIDs {
		for _, name := range probeMetrics {
			vals, err := getCDBMetricDaily(monitorClient, name, instanceID, startT, endT)
			if err != nil {
				key := classifyCBSMetricError(err)
				if _, seen := errSamples[key]; !seen {
					errSamples[key] = err.Error()
				}
				t.Logf("[探测][ERR] instance=%s metric=%s 归因=%s err=%v", instanceID, name, key, err)
				continue
			}
			if len(vals) == 0 {
				empty++
				continue
			}
			latest := vals[len(vals)-1]
			if latest == 0 {
				zero++
				t.Logf("[探测][零值] instance=%s metric=%s 最新值=0", instanceID, name)
				continue
			}
			passed++
			t.Logf("[非零验证][PASS] instance=%s metric=%s 数据点=%d 最新值=%v", instanceID, name, len(vals), latest)
			if strings.EqualFold(name, "MemoryUsage") {
				memorySamples = append(memorySamples, latest)
			}
		}
	}
	for k, msg := range errSamples {
		t.Logf("[归因样本][%s]: %s", k, msg)
	}
	// 内存口径(AC-5)
	if len(memorySamples) > 0 {
		inRange := true
		for _, v := range memorySamples {
			if !nasprobe.CheckUsagePercentRange(v) {
				inRange = false
			}
		}
		t.Logf("[内存口径] MemoryUsage 样本 %v 全部 0~100=%v —— %s", memorySamples, inRange,
			map[bool]string{true: "百分比直给口径成立(无需换算)", false: "非百分比口径,须换算"}[inRange])
	}
	// 阳性对照(只读):QCE/CVM CpuUsage —— 区分「账号监控权限」vs「QCE/CDB 未注册」
	posOK := false
	cvmClient := monitorClient
	if len(instances) > 0 {
		vals, err := getCVMPositiveMetric(cvmClient, instances[0].InstanceID, startT, endT)
		if err != nil {
			t.Logf("[阳性对照][ERR] err=%v", err)
		} else if len(vals) > 0 && vals[len(vals)-1] != 0 {
			posOK = true
			t.Logf("[阳性对照][PASS] QCE/CVM CpuUsage 最新值=%v —— 云监控可用", vals[len(vals)-1])
		}
	}
	t.Logf("===== tencent RDS 探测汇总: PASS=%d 零值=%d 空窗口=%d 阳性对照(QCE/CVM)=%v(定案与二期路径见 probe-report.md)=====",
		passed, zero, empty, posOK)
}

// discoverDBMetrics 从发现结果中筛数据库类指标(小写含 cpu/mem/disk/conn 关键词)。
func discoverDBMetrics(names []string) []string {
	kws := []string{"cpu", "mem", "disk", "conn", "keepalive", "qps"}
	var out []string
	for _, name := range names {
		lower := strings.ToLower(name)
		for _, kw := range kws {
			if strings.Contains(lower, kw) {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

// discoverCDBMetrics DescribeBaseMetrics 发现 QCE/CDB 全部指标(与 CBS 同型)。
func discoverCDBMetrics(t *testing.T, client *monitor.Client) map[string]cosMetricMeta {
	out := map[string]cosMetricMeta{}
	request := monitor.NewDescribeBaseMetricsRequest()
	request.Namespace = common.StringPtr(cdbNamespace)
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

// getCDBMetricDaily GetMonitorData 查询单实例单指标天粒度数据点(维度 uInstanceId)。
func getCDBMetricDaily(client *monitor.Client, metricName, instanceID string, startT, endT time.Time) ([]float64, error) {
	request := monitor.NewGetMonitorDataRequest()
	request.Namespace = common.StringPtr(cdbNamespace)
	request.MetricName = common.StringPtr(metricName)
	request.Period = common.Uint64Ptr(86400)
	request.Instances = []*monitor.Instance{{
		Dimensions: []*monitor.Dimension{
			{Name: common.StringPtr("uInstanceId"), Value: common.StringPtr(instanceID)},
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
