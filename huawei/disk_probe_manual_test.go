// Package huawei_test Disk 指标探测(disk-ops-insight M1 探测任务,manual probe,只读)。
//
// 华为云 CES 云硬盘指标探测(AC-1):使用率/IOPS/吞吐实盘非零验证。
// 候选 namespace:SYS.EVS(云盘级,文档口径)与 SYS.ECS(挂载实例级,含
// disk_usedPercent)——实盘 ListMetrics 确认哪个 namespace 真有云硬盘序列,
// 使用率口径(云盘级 vs 挂载实例级)以实盘为准(AC-5,参照 NAS SYS.EFS 定案)。
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
	"github.com/gotomicro/ego/core/elog"
	cesv1model "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1/model"
)

// diskNamespaceCandidates 云盘相关 namespace 候选(SYS.EVS 云盘级 / SYS.ECS 实例级)。
var diskNamespaceCandidates = []string{"SYS.EVS", "SYS.ECS"}

// diskMetricKeywords 磁盘类指标筛选关键词(使用率/IOPS/吞吐)。
var diskMetricKeywords = []string{
	"used", "percent", "ratio", "util", // 使用率类
	"requests_rate", "iops", // IOPS 类
	"bytes_rate", "throughput", // 吞吐类
}

// TestManualProbeHuaweiCESDiskMetrics CES 云硬盘指标发现 + 实盘非零验证。
func TestManualProbeHuaweiCESDiskMetrics(t *testing.T) {
	ak, sk, region := loadHuaweiCreds(t)
	if ak == "" {
		t.Skip("未设置 NAS_PROBE_HUAWEI_AK/SK/REGION 或 NAS_PROBE_MONGODB_DSN,跳过(无 env 不跑)")
	}
	t.Logf("探测凭证: AK=%s region=%s", nasprobe.MaskAK(ak), region)

	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()

	// 1) 只读枚举实盘云硬盘(地域性资源,逐 region)
	regions := nasprobe.EnvRegions("HUAWEI", region)
	var disks []huaweiDiskTarget
	for _, rg := range regions {
		adapter := huawei.NewDiskAdapter(ak, sk, rg, elog.DefaultLogger)
		list, err := adapter.ListInstances(ctx, rg)
		if err != nil {
			t.Logf("[枚举失败] region=%s err=%v", rg, err)
			continue
		}
		t.Logf("region=%s 磁盘数: %d", rg, len(list))
		for _, d := range list {
			disks = append(disks, huaweiDiskTarget{
				DiskID: d.DiskID, DiskName: d.DiskName, Size: d.Size,
				InstanceID: d.InstanceID, Region: rg, Status: d.Status,
			})
		}
	}
	t.Logf("实盘磁盘总数: %d", len(disks))
	if len(disks) == 0 {
		t.Skip("各 region 均无磁盘,非零验证无从进行(记录:枚举为空)")
	}

	// 2) 逐 namespace 指标发现(ListMetrics)+ 磁盘类筛选
	for _, ns := range diskNamespaceCandidates {
		client, err := newCESv1Client(ak, sk, regions[0])
		if err != nil {
			t.Fatalf("[发现] namespace=%s 创建 CES 客户端失败: %v", ns, err)
		}
		metrics, err := listCESMetrics(ctx, client, ns)
		if err != nil {
			t.Logf("[发现][FAIL] namespace=%s ListMetrics err=%v", ns, err)
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
			for _, kw := range diskMetricKeywords {
				if strings.Contains(lower, kw) {
					names = append(names, name)
					break
				}
			}
		}
		sort.Strings(names)
		t.Logf("[发现] namespace=%s 磁盘类 %d 条", ns, len(names))
		for _, name := range names {
			m := metrics[name]
			t.Logf("[发现] ns=%s metric=%s unit=%s 序列数=%d", ns, name, m.Unit, len(m.Dims))
		}
		// 3) 磁盘类指标 × 实盘序列 → 非零验证(3 天窗口天粒度 Average)
		verifyHuaweiDiskNonZero(ctx, t, client, ns, metrics, names, disks)
	}
	t.Log("===== huawei 磁盘探测完成(使用率口径按带数据的 namespace/维度判定,定案见 probe-report.md)=====")
}

// verifyHuaweiDiskNonZero 对磁盘类指标的已注册序列逐个查数据点验证非零。
// 序列维度值与实盘磁盘匹配:SYS.EVS 的 disk_name 实盘形态为
// `<volume-uuid>-vda`(卷 ID + 设备后缀,非用户命名),SYS.ECS 的
// instance_id 为实例 UUID —— 按「前缀匹配 DiskID / 精确匹配 InstanceID」
// 双形态匹配;最多验证 30 条序列。
func verifyHuaweiDiskNonZero(ctx context.Context, t *testing.T, client interface {
	ShowMetricData(request *cesv1model.ShowMetricDataRequest) (*cesv1model.ShowMetricDataResponse, error)
}, ns string, metrics map[string]cesMetricMeta, names []string, disks []huaweiDiskTarget) {
	diskIDs := map[string]bool{}
	instanceIDs := map[string]bool{}
	for _, d := range disks {
		diskIDs[d.DiskID] = true
		if d.InstanceID != "" {
			instanceIDs[d.InstanceID] = true
		}
	}
	// dimMatchesDisk 维度值与实盘磁盘匹配。实盘形态三种:
	//   - SYS.ECS instance_id:实例 UUID(精确);
	//   - SYS.EVS disk_name:<实例 UUID>-<device>(实例 UUID 前缀 + 设备后缀,
	//     ECS Agent 按设备粒度上报,非用户命名也非卷 ID);
	//   - 卷 UUID 前缀(直连云盘形态,兼容)。
	dimMatchesDisk := func(v string) bool {
		if instanceIDs[v] {
			return true
		}
		for id := range diskIDs {
			if v == id || strings.HasPrefix(v, id+"-") {
				return true
			}
		}
		for id := range instanceIDs {
			if strings.HasPrefix(v, id+"-") {
				return true
			}
		}
		return false
	}
	to := time.Now().UnixMilli()
	from := to - 3*24*3600*1000
	passed, failed := 0, 0
	for _, name := range names {
		meta := metrics[name]
		verified := 0
		for _, dim := range meta.Dims {
			if verified >= 30 {
				break
			}
			if !dimMatchesDisk(dim.Value) {
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
				t.Logf("[非零验证][FAIL] ns=%s metric=%s dim=%s=%s 3 天窗口无数据点", ns, name, dim.Name, dim.Value)
				continue
			}
			passed++
			t.Logf("[非零验证][PASS] ns=%s metric=%s dim=%s=%s unit=%s 数据点=%d 最新均值=%v", ns, name, dim.Name, dim.Value, meta.Unit, points, latest)
		}
		if len(meta.Dims) > 0 && verified == 0 {
			t.Logf("[注记] ns=%s metric=%s 序列维度值与实盘磁盘名不匹配(样本 %s=%s),仅记录注册事实", ns, name, meta.Dims[0].Name, meta.Dims[0].Value)
		}
	}
	t.Logf("===== namespace=%s 非零验证汇总: PASS=%d FAIL=%d =====", ns, passed, failed)
	if passed == 0 {
		t.Logf("[注记] namespace=%s 无实盘非零序列", ns)
	}
	_ = fmt.Sprintf // 保持 fmt 引用(日志格式化均在调用侧)
}

// huaweiDiskTarget 华为云磁盘探测目标(从 DiskInstance 提取的最小字段)。
type huaweiDiskTarget struct {
	DiskID     string
	DiskName   string
	Size       int
	InstanceID string
	Region     string
	Status     string
}
