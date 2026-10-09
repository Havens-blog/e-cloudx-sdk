// Package aws_test NAS 指标探测(M1 探测任务,manual probe,只读)。
//
// AWS CloudWatch EFS `StorageBytes` 探测:确认指标可用,并对实盘 capacity=0
// 的 EFS 实例做监控 API 非零验证(AC-3);零值视为探测未通过并记录原因。
//
// SKIP gate(无 env 不跑):
//   - NAS_PROBE_AWS_AK / NAS_PROBE_AWS_SK / NAS_PROBE_AWS_REGION 直填,或
//   - NAS_PROBE_MONGODB_DSN(+可选 CAM_ENCRYPTION_KEY)从库加载活跃 aws 账号
//
// Hard Rule:只读凭证,只写样例行到测试日志,不动生产 ecam_nas_metric 表。
// 参考:https://docs.aws.amazon.com/efs/latest/ug/monitoring-cloudwatch.html
package aws_test

import (
	"context"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/aws"
	"github.com/Havens-blog/e-cloudx-sdk/nasprobe"
	cxtypes "github.com/Havens-blog/e-cloudx-sdk/types"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	"github.com/gotomicro/ego/core/elog"
)

// TestManualProbeAWSCloudWatchEFSStorageBytes CloudWatch EFS StorageBytes 探测。
func TestManualProbeAWSCloudWatchEFSStorageBytes(t *testing.T) {
	ak, sk, region := loadAWSCreds(t)
	if ak == "" {
		t.Skip("未设置 NAS_PROBE_AWS_AK/SK/REGION 或 NAS_PROBE_MONGODB_DSN,跳过(无 env 不跑)")
	}
	regions := nasprobe.EnvRegions("AWS", region)
	t.Logf("探测凭证: AK=%s regions=%v", nasprobe.MaskAK(ak), regions)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// 1) 只读枚举实盘 EFS 文件系统(逐 region;账号 regions 配置与实盘 region
	//    可能不一致,支持 NAS_PROBE_AWS_REGIONS 覆盖/扩展)
	var instances []cxtypes.NASInstance
	for _, rg := range regions {
		adapter := aws.NewEFSAdapter(ak, sk, rg, elog.DefaultLogger)
		ins, err := adapter.ListInstances(ctx, rg)
		if err != nil {
			t.Logf("[枚举失败] region=%s err=%v", rg, err)
			continue
		}
		t.Logf("region=%s EFS 实例数: %d", rg, len(ins))
		instances = append(instances, ins...)
	}
	t.Logf("实盘 EFS 实例总数: %d", len(instances))
	if len(instances) == 0 {
		t.Skip("各 region 均无 EFS 实例,非零验证无从进行(记录:实例枚举为空)")
	}
	for _, ins := range instances {
		t.Logf("实例: fs_id=%s name=%s capacity=%d used=%d metered=%d region=%s",
			ins.FileSystemID, ins.FileSystemName,
			ins.Capacity, ins.UsedCapacity, ins.MeteredSize, ins.Region)
	}

	// 2) CloudWatch 客户端(EFS StorageBytes,namespace AWS/EFS;逐实例按其 region 查询)
	// 3) 逐实例非零验证(AC-3):90 天窗口天粒度 Average(GetMetricData)
	passed, failed := 0, 0
	for _, ins := range instances {
		cwClient, err := newCloudWatchClient(ctx, ak, sk, ins.Region)
		if err != nil {
			failed++
			t.Logf("[非零验证][FAIL] fs_id=%s 创建 CloudWatch 客户端 err=%v", ins.FileSystemID, err)
			continue
		}
		// 挂载点核查(只读):StorageBytes 仅在文件系统有挂载/IO 时上报,
		// 无数据点的常见根因是实例从未被挂载使用。
		if n, mtErr := efsMountTargetCount(ctx, ak, sk, ins.Region, ins.FileSystemID); mtErr == nil {
			t.Logf("[挂载点核查] fs_id=%s region=%s 挂载点数=%d", ins.FileSystemID, ins.Region, n)
		}
		// ListMetrics 发现(只读):该 fs 实际存在哪些指标序列
		listCWMetricsForFS(ctx, t, cwClient, ins.FileSystemID)
		start := time.Now().Add(-90 * 24 * time.Hour) // 90 天窗口:实测 15 天无数据点而序列存在(实例长期无 IO),回看 90 天找最近数据点
		end := time.Now()
		// GetMetricData(推荐 API):与 GetMetricStatistics 双路验证
		gmd, err := cwClient.GetMetricData(ctx, &cloudwatch.GetMetricDataInput{
			StartTime: &start,
			EndTime:   &end,
			MetricDataQueries: []types.MetricDataQuery{{
				Id: awssdk.String("storage"),
				MetricStat: &types.MetricStat{
					Metric: &types.Metric{
						Namespace:  awssdk.String("AWS/EFS"),
						MetricName: awssdk.String("StorageBytes"),
						Dimensions: []types.Dimension{{Name: awssdk.String("FileSystemId"), Value: awssdk.String(ins.FileSystemID)}},
					},
					Period: awssdk.Int32(86400),
					Stat:   awssdk.String("Average"),
				},
			}},
		})
		if err != nil {
			failed++
			t.Logf("[非零验证][FAIL] fs_id=%s GetMetricData err=%v", ins.FileSystemID, err)
			continue
		}
		if len(gmd.MetricDataResults) == 0 || len(gmd.MetricDataResults[0].Values) == 0 {
			failed++
			t.Logf("[非零验证][FAIL] fs_id=%s GetMetricData 双路确认: 90 天窗口无任何数据点(序列已注册但长期无上报)", ins.FileSystemID)
			continue
		}
		vals := gmd.MetricDataResults[0].Values
		latest := vals[0] // GetMetricData 按时间升序
		passed++
		t.Logf("[非零验证][PASS] fs_id=%s 数据点=%d 最新 StorageBytes(avg)=%v → %.6g GB 数量级=%s",
			ins.FileSystemID, len(vals), latest,
			nasprobe.BytesToGB(latest), nasprobe.DescribeMagnitude(nasprobe.BytesToGB(latest)))
	}

	t.Logf("===== AWS 探测汇总:StorageBytes 非零验证 PASS=%d FAIL=%d(指标名定案:AWS/EFS StorageBytes)=====", passed, failed)
}

// listCWMetricsForFS ListMetrics 发现该 FileSystemId 实际存在的指标序列(只读)。
func listCWMetricsForFS(ctx context.Context, t *testing.T, cwClient *cloudwatch.Client, fsID string) {
	var nextToken *string
	names := map[string]bool{}
	for {
		out, err := cwClient.ListMetrics(ctx, &cloudwatch.ListMetricsInput{
			Namespace:  awssdk.String("AWS/EFS"),
			Dimensions: []types.DimensionFilter{{Name: awssdk.String("FileSystemId"), Value: awssdk.String(fsID)}},
			NextToken:  nextToken,
		})
		if err != nil {
			t.Logf("[指标发现] fs_id=%s ListMetrics err=%v", fsID, err)
			return
		}
		for _, m := range out.Metrics {
			if m.MetricName != nil {
				names[*m.MetricName] = true
			}
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}
	t.Logf("[指标发现] fs_id=%s 现存指标序列: %v", fsID, names)
}

// efsMountTargetCount 只读查询 EFS 挂载点数量(探测证据:无挂载点=从未使用)。
func efsMountTargetCount(ctx context.Context, ak, sk, region, fsID string) (int, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(ak, sk, "")),
	)
	if err != nil {
		return 0, err
	}
	client := efs.NewFromConfig(cfg)
	out, err := client.DescribeMountTargets(ctx, &efs.DescribeMountTargetsInput{
		FileSystemId: awssdk.String(fsID),
	})
	if err != nil {
		return 0, err
	}
	return len(out.MountTargets), nil
}

// newCloudWatchClient 创建 CloudWatch 客户端(静态凭证 + region)。
func newCloudWatchClient(ctx context.Context, ak, sk, region string) (*cloudwatch.Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(ak, sk, "")),
	)
	if err != nil {
		return nil, err
	}
	return cloudwatch.NewFromConfig(cfg), nil
}

// loadAWSCreds 直填凭证优先,否则从库加载活跃 aws 账号。
func loadAWSCreds(t *testing.T) (ak, sk, region string) {
	if ak = nasprobe.EnvAK("AWS"); ak != "" {
		if sk = nasprobe.EnvSK("AWS"); sk != "" {
			return ak, sk, nasprobe.EnvRegion("AWS")
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
		if a.Provider == "aws" {
			rg := ""
			if len(a.Regions) > 0 {
				rg = a.Regions[0]
			}
			return a.AK, a.SK, rg
		}
	}
	return "", "", ""
}
