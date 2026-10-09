// Package aliyun_test RDS 指标探测(rds-ops-insight M1 探测任务,manual probe,只读)。
//
// 阿里云 CMS `acs_rds_dashboard` 云数据库指标探测(AC-3):CPU/内存/磁盘/连接数
// 实盘非零验证(AC-5:内存口径——aliyun MemoryUsage 文档口径为百分比直给,
// 实盘验证;多引擎 mysql/pg/mariadb/sqlserver 口径一致性逐引擎采样)。
//
// SKIP gate(无 env 不跑):
//   - NAS_PROBE_ALIYUN_AK / NAS_PROBE_ALIYUN_SK / NAS_PROBE_ALIYUN_REGION 直填,或
//   - NAS_PROBE_MONGODB_DSN(+可选 CAM_ENCRYPTION_KEY)从库加载活跃 aliyun 账号
//
// Hard Rule:只读凭证,只写样例行到测试日志,不动生产表。
package aliyun_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/aliyun"
	"github.com/Havens-blog/e-cloudx-sdk/nasprobe"
	cxtypes "github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/cms"
	"github.com/gotomicro/ego/core/elog"
)

// rdsMetricNamespace 阿里云 RDS 云监控业务命名空间(文档口径候选)。
const rdsMetricNamespace = "acs_rds_dashboard"

// rdsMetricKeywords RDS 四类指标筛选关键词(元数据发现为可靠路径,候选为兜底):
// CPU / 内存 / 磁盘 / 连接。
var rdsMetricKeywords = []string{"cpu", "memory", "disk", "connection", "iops"}

// rdsMetricCandidates RDS 四指标候选名(文档口径;元数据发现失败时兜底)。
var rdsMetricCandidates = []string{
	"CpuUsage", "MemoryUsage", "DiskUsage", "ConnectionUsage",
	"MySQL_NetworkInOut", "PgSQL_CpuUsage",
}

// TestManualProbeAliyunACSRDSMetrics CMS acs_rds_dashboard RDS 指标探测(AC-3/AC-5)。
func TestManualProbeAliyunACSRDSMetrics(t *testing.T) {
	ak, sk, region := loadAliyunCreds(t)
	if ak == "" {
		t.Skip("未设置 NAS_PROBE_ALIYUN_AK/SK/REGION 或 NAS_PROBE_MONGODB_DSN,跳过(无 env 不跑)")
	}
	if region == "" {
		region = "cn-hangzhou"
	}
	t.Logf("探测凭证: AK=%s region=%s", nasprobe.MaskAK(ak), region)

	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()

	// 1) 只读枚举实盘 RDS 实例(地域性资源,逐 region),按引擎分组采样
	regions := nasprobe.EnvRegions("ALIYUN", region)
	adapter := aliyun.NewRDSAdapter(ak, sk, region, elog.DefaultLogger)
	var instances []cxtypes.RDSInstance
	for _, rg := range regions {
		list, err := adapter.ListInstances(ctx, rg)
		if err != nil {
			t.Logf("[枚举失败] region=%s err=%v", rg, err)
			continue
		}
		t.Logf("region=%s RDS 实例数: %d", rg, len(list))
		instances = append(instances, list...)
	}
	t.Logf("实盘 RDS 实例总数: %d", len(instances))
	if len(instances) == 0 {
		t.Skip("各 region 均无 RDS 实例,非零验证无从进行(记录:枚举为空)")
	}
	targets := pickRDSEngineSamples(instances, 5)
	t.Logf("采样目标(逐引擎优先,最多 5 实例): %s", rdsTargetsSummary(targets))

	// 2) CMS 客户端 + acs_rds_dashboard 指标发现(元数据接口)
	client, err := newCMSTestClient(ak, sk, region)
	if err != nil {
		t.Fatalf("创建 CMS 客户端失败: %v", err)
	}
	metas, err := listACSRDSMetricMetas(client)
	if err != nil {
		t.Logf("[发现] DescribeMetricMetaList 失败(走候选矩阵): %v", err)
	}
	discovered := make([]string, 0, len(metas))
	for name := range metas {
		lower := strings.ToLower(name)
		for _, kw := range rdsMetricKeywords {
			if strings.Contains(lower, kw) {
				discovered = append(discovered, name)
				break
			}
		}
	}
	sort.Strings(discovered)
	t.Logf("[发现] %s CPU/内存/磁盘/连接类指标 %d 条", rdsMetricNamespace, len(discovered))
	for _, name := range discovered {
		m := metas[name]
		t.Logf("[发现] metric=%s unit=%s dims=%s desc=%s", name, m.Unit, m.Dimensions, m.Description)
	}
	probeMetrics := discovered
	if len(probeMetrics) == 0 {
		probeMetrics = rdsMetricCandidates
	}

	// 3) 指标 × 实盘实例(维度 instanceId)→ DescribeMetricList 非零验证
	to := time.Now()
	from := to.Add(-3 * 24 * time.Hour)
	passed, zero, empty := 0, 0, 0
	memoryByEngine := map[string][]float64{} // engine → MemoryUsage 样本(AC-5 内存口径)
	for _, ins := range targets {
		for _, name := range probeMetrics {
			vals, err := describeOSSMetricDaily(client, rdsMetricNamespace, name, "instanceId", "86400", ins.InstanceID, from, to)
			if err != nil {
				t.Logf("[探测][ERR] instance=%s engine=%s metric=%s 归因=%s err=%v",
					ins.InstanceID, ins.Engine, name, classifyOSSMetricError(err), err)
				continue
			}
			if len(vals) == 0 {
				empty++
				continue
			}
			latest := vals[len(vals)-1]
			if latest.Value == 0 {
				zero++
				t.Logf("[探测][零值] instance=%s metric=%s 最新值=0", ins.InstanceID, name)
				continue
			}
			passed++
			t.Logf("[非零验证][PASS] instance=%s engine=%s metric=%s unit=%s 数据点=%d 最新值=%v",
				ins.InstanceID, ins.Engine, name, metas[name].Unit, len(vals), latest.Value)
			if strings.EqualFold(name, "MemoryUsage") {
				memoryByEngine[strings.ToLower(ins.Engine)] = append(memoryByEngine[strings.ToLower(ins.Engine)], latest.Value)
			}
		}
	}
	t.Logf("===== aliyun RDS 探测汇总: PASS=%d 零值=%d 空窗口=%d =====", passed, zero, empty)

	// 4) 内存口径与多引擎口径记录(AC-5)
	t.Log("===== 内存口径判定(AC-5)=====")
	for engine, samples := range memoryByEngine {
		inRange := true
		for _, v := range samples {
			if !nasprobe.CheckUsagePercentRange(v) {
				inRange = false
			}
		}
		t.Logf("引擎 %s MemoryUsage 样本=%v 全部 0~100=%v —— %s", engine, samples, inRange,
			map[bool]string{true: "百分比直给口径成立(无需换算)", false: "非百分比口径,须换算(以实盘为准)"}[inRange])
	}
	if len(memoryByEngine) == 0 {
		t.Log("MemoryUsage 无非零样本:内存口径无法确认,记录「以实盘复测为准」")
	}
	t.Log("===== 多引擎口径记录(AC-5)=====")
	t.Log("各引擎样本指标名与数值见上方 [非零验证]/[零值] 行;引擎间指标名差异(如 MySQL_/PgSQL_ 前缀)以 [发现] 清单为准,T3 按 engine 分派判定输入。")
}

// pickRDSEngineSamples 逐引擎优先采样(每引擎至少 1 实例,不足补齐):
// 保证多引擎口径确认(AC-5)有每引擎样本;同引擎内取存储最大优先(有负载概率高)。
func pickRDSEngineSamples(instances []cxtypes.RDSInstance, limit int) []cxtypes.RDSInstance {
	byEngine := map[string][]cxtypes.RDSInstance{}
	order := []string{}
	for _, ins := range instances {
		e := strings.ToLower(strings.TrimSpace(ins.Engine))
		if e == "" {
			e = "(empty)"
		}
		if _, seen := byEngine[e]; !seen {
			order = append(order, e)
		}
		byEngine[e] = append(byEngine[e], ins)
	}
	for _, e := range order {
		list := byEngine[e]
		sort.Slice(list, func(i, j int) bool { return list[i].Storage > list[j].Storage })
	}
	var out []cxtypes.RDSInstance
	// 第一轮:每引擎取 1 个
	for _, e := range order {
		if len(out) >= limit {
			break
		}
		out = append(out, byEngine[e][0])
	}
	// 第二轮:补齐名额(存储最大优先)
	rest := make([]cxtypes.RDSInstance, 0, len(instances))
	for _, e := range order {
		rest = append(rest, byEngine[e]...)
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].Storage > rest[j].Storage })
	for _, ins := range rest {
		if len(out) >= limit {
			break
		}
		dup := false
		for _, o := range out {
			if o.InstanceID == ins.InstanceID {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, ins)
		}
	}
	return out
}

// rdsTargetsSummary 采样目标摘要(日志用)。
func rdsTargetsSummary(targets []cxtypes.RDSInstance) string {
	parts := make([]string, 0, len(targets))
	for _, ins := range targets {
		parts = append(parts, fmt.Sprintf("%s(%s,%dGB)", ins.InstanceID, ins.Engine, ins.Storage))
	}
	return strings.Join(parts, "; ")
}

// listACSRDSMetricMetas DescribeMetricMetaList 分页发现 acs_rds_dashboard
// 全部指标元数据(与 listACSEcsDiskMetricMetas 同型,namespace 换为 RDS)。
func listACSRDSMetricMetas(client *cms.Client) (map[string]ossMetricMeta, error) {
	out := map[string]ossMetricMeta{}
	for page := 1; page <= 20; page++ {
		request := cms.CreateDescribeMetricMetaListRequest()
		request.Namespace = rdsMetricNamespace
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
