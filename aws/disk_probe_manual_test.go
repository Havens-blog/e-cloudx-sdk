// Package aws_test Disk 指标探测(disk-ops-insight M1 探测任务,manual probe,只读)。
//
// AWS CloudWatch `AWS/EBS` 云盘指标探测(AC-2):VolumeReadBytes/VolumeWriteBytes/
// VolumeIdleTime 实盘非零验证;并验证使用率派生公式
// usage% = (1 - VolumeIdleTime/统计周期秒数) × 100(AC-5 派生口径决策点)。
// VolumeIdleTime 以 Sum 统计天粒度聚合(统计周期内无读写请求的秒数)。
//
// SKIP gate(无 env 不跑):
//   - NAS_PROBE_AWS_AK / NAS_PROBE_AWS_SK / NAS_PROBE_AWS_REGION 直填,或
//   - NAS_PROBE_MONGODB_DSN(+可选 CAM_ENCRYPTION_KEY)从库加载活跃 aws 账号
//
// Hard Rule:只读凭证,只写样例行到测试日志,不动生产表。
// 参考:https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/using_cloudwatch_ebs.html
package aws_test

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/aws"
	"github.com/Havens-blog/e-cloudx-sdk/nasprobe"
	cxtypes "github.com/Havens-blog/e-cloudx-sdk/types"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/gotomicro/ego/core/elog"
)

// ebsMetricNamespace CloudWatch EBS namespace(官方指标)。
const ebsMetricNamespace = "AWS/EBS"

// ebsDimVolumeID CloudWatch 维度名(云盘级)。
const ebsDimVolumeID = "VolumeId"

// TestManualProbeAWSCloudWatchEBSMetrics CloudWatch EBS 磁盘指标探测。
func TestManualProbeAWSCloudWatchEBSMetrics(t *testing.T) {
	ak, sk, region := loadAWSCreds(t)
	if ak == "" {
		t.Skip("未设置 NAS_PROBE_AWS_AK/SK/REGION 或 NAS_PROBE_MONGODB_DSN,跳过(无 env 不跑)")
	}
	regions := nasprobe.EnvRegions("AWS", region)
	t.Logf("探测凭证: AK=%s regions=%v", nasprobe.MaskAK(ak), regions)

	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()

	// 1) 只读枚举实盘 EBS 卷(地域性资源,逐 region)
	var volumes []cxtypes.DiskInstance
	for _, rg := range regions {
		adapter := aws.NewDiskAdapter(ak, sk, rg, elog.DefaultLogger)
		list, err := adapter.ListInstances(ctx, rg)
		if err != nil {
			t.Logf("[枚举失败] region=%s err=%v", rg, err)
			continue
		}
		t.Logf("region=%s EBS 卷数: %d", rg, len(list))
		volumes = append(volumes, list...)
	}
	t.Logf("实盘 EBS 卷总数: %d", len(volumes))
	if len(volumes) == 0 {
		t.Skip("各 region 均无 EBS 卷,非零验证无从进行(记录:枚举为空)")
	}
	// 非零验证目标:容量最大 5 个卷(有挂载使用的概率最高)
	sort.Slice(volumes, func(i, j int) bool { return volumes[i].Size > volumes[j].Size })
	if len(volumes) > 5 {
		volumes = volumes[:5]
	}

	// 2) 三关键指标 × 实盘卷 → GetMetricData 非零验证 + 派生使用率
	const windowSeconds = 86400
	metricCases := []struct {
		name string
		stat string
		desc string
	}{
		{"VolumeReadBytes", "Sum", "读吞吐(byte/日)"},
		{"VolumeWriteBytes", "Sum", "写吞吐(byte/日)"},
		{"VolumeIdleTime", "Sum", "空闲秒数(使用率派生输入)"},
		{"VolumeReadOps", "Sum", "读 IOPS 派生输入(次/日)"},
		{"VolumeWriteOps", "Sum", "写 IOPS 派生输入(次/日)"},
	}
	passed, failed := 0, 0
	derivOK, derivFail := 0, 0
	to := time.Now()
	from := to.Add(-3 * 24 * time.Hour)
	for _, vol := range volumes {
		cwClient, err := newCloudWatchClient(ctx, ak, sk, vol.Region)
		if err != nil {
			t.Logf("[非零验证][FAIL] volume=%s 创建 CloudWatch 客户端 err=%v", vol.DiskID, err)
			continue
		}
		for _, mc := range metricCases {
			vals, err := getCWEBSum(ctx, cwClient, mc.name, mc.stat, vol.DiskID, from, to)
			if err != nil {
				failed++
				t.Logf("[非零验证][FAIL] volume=%s metric=%s err=%v", vol.DiskID, mc.name, err)
				continue
			}
			if len(vals) == 0 {
				failed++
				t.Logf("[非零验证][FAIL] volume=%s metric=%s 3 天窗口无数据点(未挂载或未上报)", vol.DiskID, mc.name)
				continue
			}
			latest := vals[len(vals)-1]
			if latest == 0 && mc.name != "VolumeIdleTime" {
				zeroNote := "(零值:磁盘无 IO,零值例外打标路径)"
				if mc.stat == "Sum" {
					zeroNote = "(零值:当日无该方向 IO)"
				}
				t.Logf("[探测][零值] volume=%s metric=%s 最新值=0 %s", vol.DiskID, mc.name, zeroNote)
				continue
			}
			passed++
			t.Logf("[非零验证][PASS] volume=%s metric=%s(%s,%s) 数据点=%d 最新=%v",
				vol.DiskID, mc.name, mc.stat, mc.desc, len(vals), latest)
			// 3) 派生使用率验证(仅 VolumeIdleTime):usage% = (1-idle/86400)×100
			if mc.name == "VolumeIdleTime" {
				usage, ok := nasprobe.DiskUsagePercentFromIdle(latest, windowSeconds)
				if !ok || !nasprobe.CheckUsagePercentRange(usage) {
					derivFail++
					t.Logf("[派生验证][FAIL] volume=%s idle=%v/%ds → usage 不可计算或越界", vol.DiskID, latest, windowSeconds)
					continue
				}
				derivOK++
				t.Logf("[派生验证][PASS] volume=%s idle=%.0fs/%ds → usage%%=%.2f(繁忙占比口径,非容量水位)", vol.DiskID, latest, windowSeconds, usage)
			}
		}
	}
	t.Logf("===== aws 磁盘探测汇总: 指标非零 PASS=%d FAIL(含错误/空窗口)=%d;派生使用率 PASS=%d FAIL=%d =====",
		passed, failed, derivOK, derivFail)
	t.Log("口径注记:EBS 无直接容量使用率指标,派生公式给出的是「繁忙时间占比」;")
	t.Log("若需容量水位需从 OS 侧(挂载实例)采集 —— 探测报告按 AC-5 记录该口径差异。")
}

// getCWEBSum GetMetricData 单卷单指标窗口查询(日粒度聚合)。
func getCWEBSum(ctx context.Context, cwClient *cloudwatch.Client, metricName, stat, volumeID string, from, to time.Time) ([]float64, error) {
	out, err := cwClient.GetMetricData(ctx, &cloudwatch.GetMetricDataInput{
		StartTime: &from,
		EndTime:   &to,
		MetricDataQueries: []cwtypes.MetricDataQuery{{
			Id: awssdk.String("m"),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace:  awssdk.String(ebsMetricNamespace),
					MetricName: awssdk.String(metricName),
					Dimensions: []cwtypes.Dimension{{
						Name:  awssdk.String(ebsDimVolumeID),
						Value: awssdk.String(volumeID),
					}},
				},
				Period: awssdk.Int32(86400),
				Stat:   awssdk.String(stat),
			},
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("GetMetricData(%s) 失败: %w", metricName, err)
	}
	if len(out.MetricDataResults) == 0 {
		return nil, nil
	}
	return out.MetricDataResults[0].Values, nil
}
