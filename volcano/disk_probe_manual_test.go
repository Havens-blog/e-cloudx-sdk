// Package volcano_test Disk 指标探测(disk-ops-insight M1 探测任务,manual probe,只读)。
//
// volcengine cloudmonitor 云盘指标名探测(AC-4):云盘容量/使用率/IOPS/吞吐
// 指标候选矩阵(Namespace × SubNamespace × 指标名 × 维度名)逐格试;真实
// ECS 指标(Vulcan_ECS/CPUPercent)作阳性对照;失败归因(指标订阅未开通
// vs 指标不存在),二期补重试路径固化在测试注释(参照 NAS/OSS 前案)。
//
// SKIP gate(无 env 不跑):
//   - NAS_PROBE_VOLC_AK / NAS_PROBE_VOLC_SK / NAS_PROBE_VOLC_REGION 直填,或
//   - NAS_PROBE_MONGODB_DSN(+可选 CAM_ENCRYPTION_KEY)从库加载活跃 volcano 账号
//
// Hard Rule:只读凭证,只写样例行到测试日志,不动生产表。
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

// volcanoDiskNamespaceCandidates 云盘 namespace 候选(EBS/块存储形态)。
var volcanoDiskNamespaceCandidates = []string{
	"Vulcan_EBS", "Volcano_EBS", "VEI_EBS", "EBS", "Vulcan_Storage_EBS",
}

// volcanoDiskSubNamespaces SubNamespace 候选。
var volcanoDiskSubNamespaces = []string{"ebs", "volume", "disk", "block_storage"}

// volcanoDiskMetricNames 云盘容量/IO 类候选指标名。
var volcanoDiskMetricNames = []string{
	"VolumeUsedCapacity", "UsedCapacity", "CapacityUsed",
	"VolumeTotalCapacity", "Capacity", "VolumeSize",
	"VolumeReadIOPS", "VolumeWriteIOPS", "IOPS",
	"VolumeReadBPS", "VolumeWriteBPS", "Throughput",
	"VolumeUsedPercent", "UsagePercent",
}

// volcanoDiskDimNames 云盘维度名候选。
var volcanoDiskDimNames = []string{"volume_id", "disk_id", "VolumeId", "DiskId"}

// TestManualProbeVolcanoEBSCloudMonitor volcengine cloudmonitor 云盘指标名探测。
func TestManualProbeVolcanoEBSCloudMonitor(t *testing.T) {
	ak, sk, region, fromDB := loadVolcanoCreds(t)
	if ak == "" {
		t.Skip("未设置 NAS_PROBE_VOLC_AK/SK/REGION 或 NAS_PROBE_MONGODB_DSN,跳过(无 env 不跑)")
	}
	t.Logf("探测凭证: AK=%s 来源=%s region=%s", nasprobe.MaskAK(ak), fromDB, region)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	// 1) 只读枚举实盘云硬盘(复用现有 Disk 适配器凭证/region 模式)
	adapter := volcano.NewDiskAdapter(ak, sk, region, elog.DefaultLogger)
	disks, err := adapter.ListInstances(ctx, region)
	if err != nil {
		t.Fatalf("枚举磁盘失败(账号凭证或网络问题): %v", err)
	}
	t.Logf("实盘磁盘数: %d", len(disks))
	probeVolume := ""
	if len(disks) > 0 {
		probeVolume = disks[0].DiskID
		t.Logf("探测目标磁盘: volume_id=%s name=%s size=%dGB status=%s",
			probeVolume, disks[0].DiskName, disks[0].Size, disks[0].Status)
	} else {
		t.Logf("该账号无磁盘:仅能验证 namespace 合法性,无法确认实例级指标")
	}

	// 2) cloudmonitor 客户端(与 CDN/NAS 同一 session 构造模式)
	sess, err := session.NewSession(volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(ak, sk, "")).
		WithRegion(region))
	if err != nil {
		t.Fatalf("创建 cloudmonitor 会话失败: %v", err)
	}
	client := cloudmonitor.New(sess)

	// 3) 候选矩阵逐格探测(NAS 前案:真实 ECS 指标同为 metric not found,
	//    注册表为空特征 → 根因「云产品监控指标订阅未开通」)
	calls, hits := probeVolcanoDiskMatrix(ctx, t, client, probeVolume)
	t.Logf("===== volcengine 磁盘探测汇总: 候选组合调用 %d 次, 命中(有数据点)=%d =====", calls, hits)

	// 4) 阳性对照(复用 OSS/NAS 探测先例的 volcanoPositiveControl:真实 ECS
	//    指标 Vulkan_ECS/Instance/CPUPercent,区分「注册表空」vs「维度错」)
	lastErrKind := volcanoPositiveControl(ctx, t, client, &calls)
	if hits == 0 {
		t.Logf("[根因判定] 真实磁盘候选全部无数据;阳性对照结论=%s", lastErrKind)
		t.Log("若阳性对照同样 metric not found —— 账号云产品监控指标注册表为空(订阅未开通),非指标名错误")
	}
}

// probeVolcanoDiskMatrix 候选矩阵逐格探测,返回(调用数, 命中数)。
func probeVolcanoDiskMatrix(ctx context.Context, t *testing.T, client *cloudmonitor.CLOUDMONITOR, volumeID string) (calls, hits int) {
	start := time.Now().Add(-24 * time.Hour).Unix()
	end := time.Now().Unix()
	for _, ns := range volcanoDiskNamespaceCandidates {
		for _, sub := range volcanoDiskSubNamespaces {
			for _, metric := range volcanoDiskMetricNames {
				for _, dim := range volcanoDiskDimNames {
					calls++
					c, hit := probeVolcanoOne(ctx, t, client, ns, sub, metric, dim, volumeID, start, end, calls)
					calls = c
					if hit {
						hits++
					}
				}
			}
		}
	}
	return calls, hits
}
