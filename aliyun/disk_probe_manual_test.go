// Package aliyun_test Disk 指标探测(disk-ops-insight M1 探测任务,manual probe,只读)。
//
// 阿里云 CMS `acs_ecs_dashboard` 云盘指标探测(AC-3):使用率/IOPS/吞吐实盘
// 非零验证。路径:
//  1. DescribeMetricMetaList(Namespace=acs_ecs_dashboard)发现磁盘类指标
//     (指标名/维度/单位);
//  2. 对实盘磁盘(枚举快照 size 最大优先)用 DescribeMetricList 按维度
//     (instanceId / diskId 两种形态逐格试)查数据点,验证非零;
//  3. 记录使用率口径(云盘级 vs 挂载实例级)与单位。
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
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/cms"
	"github.com/gotomicro/ego/core/elog"
)

// diskMetricCandidates 云盘类候选指标名(元数据发现为可靠路径,此处为兜底):
// DiskUtilization=磁盘使用率(%)/DiskReadIOPS+DiskWriteIOPS=IOPS(次/秒)/
// DiskReadBPS+DiskWriteBPS=吞吐(byte/s)。
var diskMetricCandidates = []string{
	"DiskUtilization", "DiskReadBPS", "DiskWriteBPS", "DiskReadIOPS", "DiskWriteIOPS",
	"DiskEcderaOccupiedRatio", "DiskUsedPercent",
}

// TestManualProbeAliyunACSEcsDiskMetrics CMS acs_ecs_dashboard 磁盘指标探测。
func TestManualProbeAliyunACSEcsDiskMetrics(t *testing.T) {
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

	// 1) 只读枚举实盘云硬盘(地域性资源,逐 region;regions 配置与实盘可能
	//    不一致,支持 NAS_PROBE_ALIYUN_REGIONS 覆盖/扩展)
	regions := nasprobe.EnvRegions("ALIYUN", region)
	adapter := aliyun.NewDiskAdapter(ak, sk, region, elog.DefaultLogger)
	var disks []diskProbeTarget
	for _, rg := range regions {
		list, err := adapter.ListInstances(ctx, rg)
		if err != nil {
			t.Logf("[枚举失败] region=%s err=%v", rg, err)
			continue
		}
		t.Logf("region=%s 磁盘数: %d", rg, len(list))
		for _, d := range list {
			disks = append(disks, diskProbeTarget{DiskID: d.DiskID, InstanceID: d.InstanceID, Size: d.Size, Region: rg, Status: d.Status})
		}
	}
	t.Logf("实盘磁盘总数: %d", len(disks))
	if len(disks) == 0 {
		t.Skip("各 region 均无磁盘,非零验证无从进行(记录:枚举为空)")
	}
	sort.Slice(disks, func(i, j int) bool { return disks[i].Size > disks[j].Size })
	if len(disks) > 5 {
		disks = disks[:5]
	}

	// 2) CMS 客户端 + acs_ecs_dashboard 磁盘类指标发现(元数据接口)
	client, err := newCMSTestClient(ak, sk, region)
	if err != nil {
		t.Fatalf("创建 CMS 客户端失败: %v", err)
	}
	metas, err := listACSEcsDiskMetricMetas(client)
	if err != nil {
		t.Logf("[发现] DescribeMetricMetaList 失败(走候选矩阵): %v", err)
	}
	discovered := make([]string, 0, len(metas))
	for name := range metas {
		lower := strings.ToLower(name)
		if strings.Contains(lower, "disk") {
			discovered = append(discovered, name)
		}
	}
	sort.Strings(discovered)
	t.Logf("[发现] acs_ecs_dashboard 磁盘类指标 %d 条", len(discovered))
	for _, name := range discovered {
		m := metas[name]
		t.Logf("[发现] metric=%s unit=%s dims=%s desc=%s", name, m.Unit, m.Dimensions, m.Description)
	}
	probeMetrics := discovered
	if len(probeMetrics) == 0 {
		probeMetrics = diskMetricCandidates
	}

	// 3) 指标 × 维度形态(instanceId / diskId)× 实盘磁盘 → 非零验证
	to := time.Now()
	from := to.Add(-3 * 24 * time.Hour)
	dimForms := []struct {
		key  string
		desc string
	}{{"instanceId", "挂载实例级"}, {"diskId", "云盘级"}}
	for _, form := range dimForms {
		passed, zero, empty := 0, 0, 0
		for _, d := range disks {
			dimVal := d.DiskID
			if form.key == "instanceId" {
				dimVal = d.InstanceID
				if dimVal == "" {
					continue
				}
			}
			for _, name := range probeMetrics {
				vals, err := describeOSSMetricDaily(client, "acs_ecs_dashboard", name, form.key, "86400", dimVal, from, to)
				if err != nil {
					t.Logf("[探测][ERR] dim=%s(%s) metric=%s 目标=%s 归因=%s err=%v",
						form.key, form.desc, name, dimVal, classifyOSSMetricError(err), err)
					continue
				}
				if len(vals) == 0 {
					empty++
					continue
				}
				latest := vals[len(vals)-1]
				if latest.Value == 0 {
					zero++
					t.Logf("[探测][零值] dim=%s metric=%s 目标=%s 最新值=0", form.key, name, dimVal)
					continue
				}
				passed++
				t.Logf("[非零验证][PASS] dim=%s(%s) metric=%s 目标=%s 数据点=%d 最新值=%v",
					form.key, form.desc, name, dimVal, len(vals), latest.Value)
			}
		}
		t.Logf("===== 维度形态 %s(%s) 汇总: PASS=%d 零值=%d 空窗口=%d =====", form.key, form.desc, passed, zero, empty)
	}
	t.Log("===== aliyun 磁盘探测完成(定案见 probe-report.md;使用率口径按带数据的维度形态判定)=====")
}

// diskProbeTarget 磁盘探测目标(从 DiskInstance 提取的最小字段)。
type diskProbeTarget struct {
	DiskID     string
	InstanceID string
	Size       int
	Region     string
	Status     string
}

// listACSEcsDiskMetricMetas DescribeMetricMetaList 分页发现 acs_ecs_dashboard
// 全部指标元数据(与 listACSOSSMetricMetas 同型,namespace 换为 ECS)。
func listACSEcsDiskMetricMetas(client *cms.Client) (map[string]ossMetricMeta, error) {
	out := map[string]ossMetricMeta{}
	for page := 1; page <= 20; page++ {
		request := cms.CreateDescribeMetricMetaListRequest()
		request.Namespace = "acs_ecs_dashboard"
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
