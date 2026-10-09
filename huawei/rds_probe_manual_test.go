// Package huawei_test RDS 指标探测(rds-ops-insight M1 探测任务,manual probe,只读)。
//
// 华为云 CES 云数据库 RDS 指标探测(AC-1):候选 namespace `SYS.RDS`
// (文档口径)——CPU/内存/磁盘/连接数指标发现 + 实盘非零验证。
// 文档口径与实盘不符时以实盘为准并记录(参照 NAS SYS.EFS 定案先例)。
// 内存口径(AC-5):华为文档口径为 mem_usedPercent(百分比直给),实盘验证。
//
// SKIP gate(无 env 不跑):
//   - NAS_PROBE_HUAWEI_AK / NAS_PROBE_HUAWEI_SK / NAS_PROBE_HUAWEI_REGION 直填,或
//   - NAS_PROBE_MONGODB_DSN(+可选 CAM_ENCRYPTION_KEY)从库加载活跃 huawei 账号
//
// Hard Rule:只读凭证,只写样例行到测试日志,不动生产表。
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
	cxtypes "github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	cesv1model "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1/model"
)

// rdsNamespaceCandidates RDS 相关 namespace 候选(文档口径 SYS.RDS 优先;
// 实盘 ListMetrics 确认哪个 namespace 真有序列——文档与实盘不符时以实盘为准)。
var rdsNamespaceCandidates = []string{"SYS.RDS"}

// rdsMetricKeywords RDS 四类指标筛选关键词。
var rdsMetricKeywords = []string{
	"cpu", "mem", "disk", "storage", // CPU/内存/磁盘
	"conn", "session", "threads", // 连接数
	"tps", "qps", // 负载参考
}

// TestManualProbeHuaweiCESRDSMetrics CES SYS.RDS 指标发现 + 实盘非零验证(AC-1)。
func TestManualProbeHuaweiCESRDSMetrics(t *testing.T) {
	ak, sk, region := loadHuaweiCreds(t)
	if ak == "" {
		t.Skip("未设置 NAS_PROBE_HUAWEI_AK/SK/REGION 或 NAS_PROBE_MONGODB_DSN,跳过(无 env 不跑)")
	}
	t.Logf("探测凭证: AK=%s region=%s", nasprobe.MaskAK(ak), region)

	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()

	// 1) 只读枚举实盘 RDS 实例(地域性资源,逐 region)
	regions := nasprobe.EnvRegions("HUAWEI", region)
	var instances []cxtypes.RDSInstance
	for _, rg := range regions {
		adapter := huawei.NewRDSAdapter(&domain.CloudAccount{AccessKeyID: ak, AccessKeySecret: sk, Regions: regions}, rg, elog.DefaultLogger)
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
	engineParts := map[string]int{}
	instancesByID := map[string]cxtypes.RDSInstance{}
	for _, ins := range instances {
		engineParts[strings.ToLower(ins.Engine)]++
		instancesByID[ins.InstanceID] = ins
	}
	t.Logf("引擎分布: %v", engineParts)

	// 2) 逐 namespace 指标发现(ListMetrics)+ RDS 类筛选
	for _, ns := range rdsNamespaceCandidates {
		client, err := newCESv1Client(ak, sk, regions[0])
		if err != nil {
			t.Fatalf("[发现] namespace=%s 创建 CES 客户端失败: %v", ns, err)
		}
		metrics, err := listCESMetrics(ctx, client, ns)
		if err != nil {
			t.Logf("[发现][FAIL] namespace=%s ListMetrics err=%v(文档口径与实盘不符时以实盘为准并记录)", ns, err)
			continue
		}
		allNames := make([]string, 0, len(metrics))
		for name := range metrics {
			allNames = append(allNames, name)
		}
		sort.Strings(allNames)
		t.Logf("[发现] namespace=%s 全部指标 %d 条: %v", ns, len(allNames), allNames)
		names := make([]string, 0, len(metrics))
		for name := range metrics {
			lower := strings.ToLower(name)
			for _, kw := range rdsMetricKeywords {
				if strings.Contains(lower, kw) {
					names = append(names, name)
					break
				}
			}
		}
		sort.Strings(names)
		t.Logf("[发现] namespace=%s RDS 类 %d 条", ns, len(names))
		for _, name := range names {
			m := metrics[name]
			t.Logf("[发现] ns=%s metric=%s unit=%s 序列数=%d", ns, name, m.Unit, len(m.Dims))
		}
		// 3) RDS 类指标 × 实盘序列 → 非零验证(3 天窗口天粒度 Average)
		verifyHuaweiRDSNonZero(ctx, t, client, ns, metrics, names, instancesByID)
	}
	t.Log("===== huawei RDS 探测完成(namespace/内存口径按带数据的序列判定,定案见 probe-report.md)=====")
}

// verifyHuaweiRDSNonZero 对 RDS 类指标的已注册序列逐个查数据点验证非零。
// 序列维度值与实盘实例匹配:SYS.RDS 的 instance_id 为实例 ID(精确匹配);
// 最多验证 30 条序列。
func verifyHuaweiRDSNonZero(ctx context.Context, t *testing.T, client interface {
	ShowMetricData(request *cesv1model.ShowMetricDataRequest) (*cesv1model.ShowMetricDataResponse, error)
}, ns string, metrics map[string]cesMetricMeta, names []string, instancesByID map[string]cxtypes.RDSInstance) {
	to := time.Now().UnixMilli()
	from := to - 3*24*3600*1000
	passed, failed := 0, 0
	memorySamples := []float64{}
	for _, name := range names {
		meta := metrics[name]
		verified := 0
		for _, dim := range meta.Dims {
			if verified >= 30 {
				break
			}
			ins, match := instancesByID[dim.Value]
			if !match {
				continue
			}
			verified++
			request := &cesv1model.ShowMetricDataRequest{
				Namespace:  ns,
				MetricName: name,
				Dim0:       dim.Name + "," + dim.Value,
				Filter:     cesv1model.GetShowMetricDataRequestFilterEnum().AVERAGE,
				Period:     cesv1model.GetShowMetricDataRequestPeriodEnum().E_86400,
				From:       from,
				To:         to,
			}
			resp, err := client.ShowMetricData(request)
			if err != nil {
				failed++
				t.Logf("[非零验证][FAIL] ns=%s metric=%s dim=%s=%s err=%v", ns, name, dim.Name, dim.Value, err)
				continue
			}
			points := 0
			latest := 0.0
			if resp != nil && resp.Datapoints != nil && len(*resp.Datapoints) > 0 {
				dps := *resp.Datapoints
				points = len(dps)
				if dps[points-1].Average != nil {
					latest = *dps[points-1].Average
				}
			}
			if points == 0 {
				failed++
				t.Logf("[非零验证][FAIL] ns=%s metric=%s dim=%s=%s engine=%s 3 天窗口无数据点",
					ns, name, dim.Name, dim.Value, ins.Engine)
				continue
			}
			passed++
			t.Logf("[非零验证][PASS] ns=%s metric=%s dim=%s=%s engine=%s unit=%s 数据点=%d 最新均值=%v",
				ns, name, dim.Name, dim.Value, ins.Engine, meta.Unit, points, latest)
			if strings.Contains(strings.ToLower(name), "mem") {
				memorySamples = append(memorySamples, latest)
			}
		}
		if len(meta.Dims) > 0 && verified == 0 {
			t.Logf("[注记] ns=%s metric=%s 序列维度值与实盘实例不匹配(样本 %s=%s),仅记录注册事实",
				ns, name, meta.Dims[0].Name, meta.Dims[0].Value)
		}
	}
	t.Logf("===== namespace=%s 非零验证汇总: PASS=%d FAIL=%d =====", ns, passed, failed)
	// 内存口径(AC-5):mem_* 指标数值全部落在 0~100 → 百分比直给口径成立。
	if len(memorySamples) > 0 {
		inRange := true
		for _, v := range memorySamples {
			if !nasprobe.CheckUsagePercentRange(v) {
				inRange = false
			}
		}
		t.Logf("[内存口径] mem 类指标样本 %v 全部 0~100=%v —— %s", memorySamples, inRange,
			map[bool]string{true: "百分比直给口径成立(无需换算)", false: "非百分比口径,须换算"}[inRange])
	}
	_ = fmt.Sprintf // 保持 fmt 引用(日志格式化均在调用侧)
}
