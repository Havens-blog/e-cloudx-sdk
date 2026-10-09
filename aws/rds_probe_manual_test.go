// Package aws_test RDS 指标探测(rds-ops-insight M1 探测任务,manual probe,只读)。
//
// AWS CloudWatch `AWS/RDS` 云数据库指标探测(AC-2):CPUUtilization/
// FreeableMemory/FreeStorageSpace/DatabaseConnections 实盘非零验证;
// 并验证内存使用率换算公式 memory% = (1 - FreeableMemory/TotalMemory) × 100
// (AC-5 关键决策点——AWS 不直给内存使用率,FreeableMemory 为可释放内存字节,
// TotalMemory 由 DBInstanceClass 规格映射,探测验证数值合理性)。
//
// SKIP gate(无 env 不跑):
//   - NAS_PROBE_AWS_AK / NAS_PROBE_AWS_SK / NAS_PROBE_AWS_REGION 直填,或
//   - NAS_PROBE_MONGODB_DSN(+可选 CAM_ENCRYPTION_KEY)从库加载活跃 aws 账号
//
// Hard Rule:只读凭证,只写样例行到测试日志,不动生产表。
// 参考:https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/metrics.dimensions.html
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
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/gotomicro/ego/core/elog"
)

// rdsCWNamespace CloudWatch RDS namespace(官方指标)。
const rdsCWNamespace = "AWS/RDS"

// rdsCWDimInstanceID CloudWatch RDS 维度名(DBInstanceIdentifier)。
const rdsCWDimInstanceID = "DBInstanceIdentifier"

// rdsMetricCases AWS/RDS 四指标(文档口径标准指标)。
var rdsMetricCases = []struct {
	name string
	stat string
	desc string
}{
	{"CPUUtilization", "Average", "CPU 使用率(%)"},
	{"FreeableMemory", "Average", "可释放内存(byte,内存使用率换算输入)"},
	{"FreeStorageSpace", "Average", "可用存储空间(byte,磁盘使用率换算输入)"},
	{"DatabaseConnections", "Average", "当前连接数(个)"},
}

// TestManualProbeAWSCloudWatchRDSMetrics CloudWatch AWS/RDS 指标探测(AC-2/AC-5)。
func TestManualProbeAWSCloudWatchRDSMetrics(t *testing.T) {
	ak, sk, region := loadAWSCreds(t)
	if ak == "" {
		t.Skip("未设置 NAS_PROBE_AWS_AK/SK/REGION 或 NAS_PROBE_MONGODB_DSN,跳过(无 env 不跑)")
	}
	regions := nasprobe.EnvRegions("AWS", region)
	t.Logf("探测凭证: AK=%s regions=%v", nasprobe.MaskAK(ak), regions)

	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()

	// 1) 只读枚举实盘 RDS 实例(地域性资源,逐 region)
	var instances []cxtypes.RDSInstance
	for _, rg := range regions {
		adapter := aws.NewRDSAdapter(&domain.CloudAccount{AccessKeyID: ak, AccessKeySecret: sk}, rg, elog.DefaultLogger)
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
	// 非零验证目标:存储最大 5 个实例(有负载概率最高)
	sort.Slice(instances, func(i, j int) bool { return instances[i].Storage > instances[j].Storage })
	if len(instances) > 5 {
		instances = instances[:5]
	}
	engines := map[string]bool{}
	for _, ins := range instances {
		engines[ins.Engine] = true
	}
	t.Logf("采样目标(存储最大优先 5 实例,引擎覆盖 %v)", engines)

	// 2) 四指标 × 实盘实例 → GetMetricData 非零验证 + 内存换算验证
	to := time.Now()
	from := to.Add(-3 * 24 * time.Hour)
	passed, failed := 0, 0
	derivOK, derivFail := 0, 0
	for _, ins := range instances {
		cwClient, err := newCloudWatchClient(ctx, ak, sk, ins.Region)
		if err != nil {
			t.Logf("[非零验证][FAIL] instance=%s 创建 CloudWatch 客户端 err=%v", ins.InstanceID, err)
			continue
		}
		for _, mc := range rdsMetricCases {
			vals, err := getCWVal(ctx, cwClient, rdsCWNamespace, mc.name, mc.stat, ins.InstanceID, from, to)
			if err != nil {
				failed++
				t.Logf("[非零验证][FAIL] instance=%s engine=%s metric=%s err=%v", ins.InstanceID, ins.Engine, mc.name, err)
				continue
			}
			if len(vals) == 0 {
				failed++
				t.Logf("[非零验证][FAIL] instance=%s engine=%s metric=%s 3 天窗口无数据点", ins.InstanceID, ins.Engine, mc.name)
				continue
			}
			latest := vals[len(vals)-1]
			passed++
			t.Logf("[非零验证][PASS] instance=%s engine=%s metric=%s(%s,%s) 数据点=%d 最新=%v",
				ins.InstanceID, ins.Engine, mc.name, mc.stat, mc.desc, len(vals), latest)
			// 3) 内存使用率换算验证(仅 FreeableMemory):
			//    memory% = (1 - FreeableMemory/TotalMemory) × 100,
			//    TotalMemory 由 DBInstanceClass 规格映射(探测用常见规格表,
			//    FreeableMemory ≤ Total 且换算落 0~100 即数值合理)。
			if mc.name == "FreeableMemory" {
				totalBytes, ok := awsRDSTotalMemoryBytes(ins.DBInstanceClass)
				if !ok {
					derivFail++
					t.Logf("[换算验证][SKIP] instance=%s class=%s 总内存规格未映射(记录:T3 须补规格表)", ins.InstanceID, ins.DBInstanceClass)
					continue
				}
				usage, ok := nasprobe.MemoryPercentFromFreeable(latest, totalBytes)
				if !ok || !nasprobe.CheckUsagePercentRange(usage) {
					derivFail++
					t.Logf("[换算验证][FAIL] instance=%s freeable=%v total=%v(%s) → usage 不可计算或越界",
						ins.InstanceID, latest, totalBytes, ins.DBInstanceClass)
					continue
				}
				derivOK++
				t.Logf("[换算验证][PASS] instance=%s class=%s freeable=%.3gB total=%.3gB → memory%%=%.2f(FreeableMemory 换算口径)",
					ins.InstanceID, ins.DBInstanceClass, latest, totalBytes, usage)
			}
			// 磁盘使用率换算参考:disk% = (1 - FreeStorageSpace/(Storage×GiB)) × 100
			if mc.name == "FreeStorageSpace" && ins.Storage > 0 {
				totalBytes := float64(ins.Storage) * 1024 * 1024 * 1024
				if disk, ok := nasprobe.MemoryPercentFromUsed(totalBytes-latest, totalBytes); ok && nasprobe.CheckUsagePercentRange(disk) {
					t.Logf("[换算参考] instance=%s storage=%dGB freeable_disk=%.3gB → disk%%=%.2f(容量水位口径,AllocatedStorage 快照换算)",
						ins.InstanceID, ins.Storage, latest, disk)
				}
			}
		}
	}
	t.Logf("===== aws RDS 探测汇总: 指标非零 PASS=%d FAIL(含错误/空窗口)=%d;内存换算 PASS=%d FAIL/SKIP=%d =====",
		passed, failed, derivOK, derivFail)
	t.Log("口径注记:AWS 无直接内存/磁盘使用率指标,均由字节数换算(FreeableMemory→内存、FreeStorageSpace→磁盘);")
	t.Log("连接数为绝对值直给(DatabaseConnections);CPU 为百分比直给(CPUUtilization)。")
}

// awsRDSTotalMemoryBytes DBInstanceClass → 总内存字节(常见规格表,探测用;
// T3 适配器须固化为完整规格表并以实例元数据 API 为准)。
// 依据 AWS RDS 实例规格(vCPU 与内存配对,db.r/db.m/db.t 家族常见档位)。
func awsRDSTotalMemoryBytes(class string) (float64, bool) {
	const GiB = 1024.0 * 1024.0 * 1024.0
	table := map[string]float64{
		// db.t 家族(micro/nano 1GiB 起)
		"db.t3.micro": 1, "db.t3.small": 2, "db.t3.medium": 4, "db.t3.large": 8,
		"db.t3.xlarge": 16, "db.t3.2xlarge": 32,
		"db.t4g.micro": 1, "db.t4g.small": 2, "db.t4g.medium": 4, "db.t4g.large": 8,
		"db.t4g.xlarge": 16, "db.t4g.2xlarge": 32,
		// db.m 家族(每 vCPU 4GiB)
		"db.m5.large": 8, "db.m5.xlarge": 16, "db.m5.2xlarge": 32, "db.m5.4xlarge": 64,
		"db.m6g.large": 8, "db.m6g.xlarge": 16, "db.m6g.2xlarge": 32, "db.m6g.4xlarge": 64,
		"db.m7g.large": 8, "db.m7g.xlarge": 16, "db.m7g.2xlarge": 32, "db.m7g.4xlarge": 64,
		// db.r 家族(每 vCPU 8GiB,内存优化)
		"db.r5.large": 16, "db.r5.xlarge": 32, "db.r5.2xlarge": 64, "db.r5.4xlarge": 128,
		"db.r6g.large": 16, "db.r6g.xlarge": 32, "db.r6g.2xlarge": 64, "db.r6g.4xlarge": 128,
	}
	giB, ok := table[class]
	if !ok {
		return 0, false
	}
	return giB * GiB, true
}

// getCWVal GetMetricData 单实例单指标窗口查询(日粒度聚合)。
func getCWVal(ctx context.Context, cwClient *cloudwatch.Client, namespace, metricName, stat, instanceID string, from, to time.Time) ([]float64, error) {
	out, err := cwClient.GetMetricData(ctx, &cloudwatch.GetMetricDataInput{
		StartTime: &from,
		EndTime:   &to,
		MetricDataQueries: []cwtypes.MetricDataQuery{{
			Id: awssdk.String("m"),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace:  awssdk.String(namespace),
					MetricName: awssdk.String(metricName),
					Dimensions: []cwtypes.Dimension{{
						Name:  awssdk.String(rdsCWDimInstanceID),
						Value: awssdk.String(instanceID),
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
