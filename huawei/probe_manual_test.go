// Package huawei_test NAS 指标探测(M1 探测任务,manual probe,只读)。
//
// 华为 CES 探测:按文件系统类型分别跑 `SYS.SFS`(普通 SFS)与
// `SYS.SFS_Turbo`(SFS Turbo)两个 namespace(Hard Rule:不得一刀切),
// 确认容量指标可用;并对实盘 capacity=0 的实例做监控 API 非零验证(AC-3)。
// 探测失败按「华为降级为尽力而为、SC-1 改写为 aliyun/aws 必达」处置并写入报告。
//
// 实测结论(2026-09-19,cn-south-1):文档 namespace SYS.SFS / SYS.SFS_Turbo
// 在该账号下 ListMetrics 均为空;实盘 5 个 SFS Turbo(HPC 型)指标全部上报在
// **SYS.EFS** namespace、维度 **efs_instance_id**,容量指标 **used_capacity**
// (单位 byte)与 used_capacity_percent(%),无 total_capacity 直接指标
// (总容量可由 used_capacity / used_capacity_percent 派生)。故本测试对三个
// namespace 全部独立探测并汇总,以 SYS.EFS 为实测定案。
//
// SKIP gate(无 env 不跑):
//   - NAS_PROBE_HUAWEI_AK / NAS_PROBE_HUAWEI_SK / NAS_PROBE_HUAWEI_REGION 直填,或
//   - NAS_PROBE_MONGODB_DSN(+可选 CAM_ENCRYPTION_KEY)从库加载活跃 huawei 账号
//
// Hard Rule:只读凭证,只写样例行到测试日志,不动生产 ecam_nas_metric 表。
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
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	cesv1 "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1"
	cesv1model "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1/model"
	cesv1region "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1/region"
)

// huaweiNamespaces 三个 namespace 逐一独立探测(Hard Rule:SYS.SFS 与
// SYS.SFS_Turbo 不得一刀切;SYS.EFS 为实盘发现的真实上报 namespace)。
var huaweiNamespaces = []string{"SYS.SFS", "SYS.SFS_Turbo", "SYS.EFS"}

// capacityKeyword 容量类指标名筛选关键词(发现阶段用,不依赖文档枚举)。
var capacityKeyword = []string{"cap", "size", "usage", "storage", "inode"}

// TestManualProbeHuaweiCESNamespaces 华为 CES namespace/容量指标探测 + 非零验证。
func TestManualProbeHuaweiCESNamespaces(t *testing.T) {
	ak, sk, region := loadHuaweiCreds(t)
	if ak == "" {
		t.Skip("未设置 NAS_PROBE_HUAWEI_AK/SK/REGION 或 NAS_PROBE_MONGODB_DSN,跳过(无 env 不跑)")
	}
	t.Logf("探测凭证: AK=%s region=%s", nasprobe.MaskAK(ak), region)

	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()

	// 1) 只读枚举实盘 SFS 文件系统(现有适配器:ListShares 走 sfsturbo API)
	adapter := huawei.NewSFSAdapter(ak, sk, region, elog.DefaultLogger)
	instances, err := adapter.ListInstances(ctx, region)
	if err != nil {
		t.Fatalf("枚举 SFS 实例失败(账号凭证或网络问题): %v", err)
	}
	t.Logf("实盘 SFS 实例数: %d", len(instances))
	fsIDs := make(map[string]string, len(instances)) // fs_id -> fs_name
	for _, ins := range instances {
		fsIDs[ins.FileSystemID] = ins.FileSystemName
		t.Logf("实例: fs_id=%s name=%s type=%s capacity=%d used=%d region=%s",
			ins.FileSystemID, ins.FileSystemName, ins.FileSystemType,
			ins.Capacity, ins.UsedCapacity, ins.Region)
	}

	// 2) CES v1 客户端(指标 region 用实例真实 region)
	cesClient, err := newCESv1Client(ak, sk, region)
	if err != nil {
		t.Fatalf("创建 CES 客户端失败: %v", err)
	}

	// 3) 三个 namespace 分别探测,判定互相独立
	for _, ns := range huaweiNamespaces {
		t.Logf("===== namespace=%s 探测开始(独立判定)=====", ns)
		metrics, err := listCESMetrics(ctx, cesClient, ns)
		if err != nil {
			t.Logf("[FAIL] namespace=%s ListMetrics 失败: %v", ns, err)
			continue
		}
		if len(metrics) == 0 {
			t.Logf("[FAIL] namespace=%s 无任何已上报指标(账号下无该类资源或该 namespace 未上报)", ns)
			continue
		}
		names := make([]string, 0, len(metrics))
		for name := range metrics {
			names = append(names, name)
		}
		sort.Strings(names)
		t.Logf("[OK] namespace=%s 发现 %d 个指标: %s", ns, len(metrics), strings.Join(names, ", "))

		// 4) 容量类指标 × 实盘 fs 维度 → 非零验证(AC-3 华为部分)
		verifyCESNonZero(ctx, t, cesClient, ns, metrics, fsIDs)
	}

	t.Log("===== 华为探测汇总:各 namespace 判定见上方各段(独立,不互相覆盖);实测定案 namespace=SYS.EFS 段 =====")
}

// cesDim 单条指标序列的维度键值对。
type cesDim struct {
	Name  string
	Value string
}

// cesMetricMeta ListMetrics 发现的指标元数据(含全部维度序列;
// 同一指标多个实例各占一条序列,须保留全部,不能按维度键去重)。
type cesMetricMeta struct {
	Unit string
	Dims []cesDim
}

// newCESv1Client 创建 CES v1 客户端(region 非法时报错)。
func newCESv1Client(ak, sk, region string) (*cesv1.CesClient, error) {
	auth, err := basic.NewCredentialsBuilder().
		WithAk(ak).
		WithSk(sk).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}
	regionObj, err := cesv1region.SafeValueOf(region)
	if err != nil {
		return nil, fmt.Errorf("CES region %s 不在支持列表: %w", region, err)
	}
	client, err := cesv1.CesClientBuilder().
		WithRegion(regionObj).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建CES客户端失败: %w", err)
	}
	return cesv1.NewCesClient(client), nil
}

// listCESMetrics ListMetrics 发现该 namespace 下全部指标(分页聚合)。
func listCESMetrics(ctx context.Context, client *cesv1.CesClient, namespace string) (map[string]cesMetricMeta, error) {
	out := map[string]cesMetricMeta{}
	start := ""
	for page := 0; page < 25; page++ {
		request := &cesv1model.ListMetricsRequest{
			Namespace: &namespace,
			Limit:     nil,
		}
		if start != "" {
			request.Start = &start
		}
		response, err := client.ListMetrics(request)
		if err != nil {
			return nil, err
		}
		if response == nil || response.Metrics == nil || len(*response.Metrics) == 0 {
			break
		}
		for _, m := range *response.Metrics {
			meta := out[m.MetricName]
			meta.Unit = m.Unit
			for _, d := range m.Dimensions {
				if d.Name != nil && d.Value != nil {
					meta.Dims = append(meta.Dims, cesDim{Name: *d.Name, Value: *d.Value})
				}
			}
			out[m.MetricName] = meta
		}
		if response.MetaData == nil || response.MetaData.Marker == "" {
			break
		}
		start = response.MetaData.Marker
	}
	return out, nil
}

// verifyCESNonZero 对容量类指标逐 fs 查询数据点,验证非零且数量级正确。
// fsIDs: 实盘 fs_id -> fs_name;namespace 无维度序列时不执行。
func verifyCESNonZero(ctx context.Context, t *testing.T, client *cesv1.CesClient,
	namespace string, metrics map[string]cesMetricMeta, fsIDs map[string]string) {
	capMetrics := make([]string, 0, len(metrics))
	for name := range metrics {
		lower := strings.ToLower(name)
		for _, kw := range capacityKeyword {
			if strings.Contains(lower, kw) {
				capMetrics = append(capMetrics, name)
				break
			}
		}
	}
	sort.Strings(capMetrics)
	if len(capMetrics) == 0 {
		t.Logf("[FAIL] namespace=%s 未发现容量类指标(关键词 cap/size/usage/storage/inode)", namespace)
		return
	}
	sort.Strings(capMetrics)

	to := time.Now().UnixMilli()
	from := to - 3*24*3600*1000 // 近 3 天,天粒度

	passed, failed := 0, 0
	for _, metric := range capMetrics {
		meta := metrics[metric]
		for _, dim := range meta.Dims {
			dimName, dimValue := dim.Name, dim.Value
			fsName := fsIDs[dimValue]
			if _, known := fsIDs[dimValue]; !known {
				continue // 只对实盘枚举到的实例做非零验证
			}
			request := &cesv1model.ShowMetricDataRequest{
				Namespace:  namespace,
				MetricName: metric,
				Dim0:       dimName + "," + dimValue,
				Filter:     cesv1model.GetShowMetricDataRequestFilterEnum().AVERAGE,
				Period:     cesv1model.GetShowMetricDataRequestPeriodEnum().E_86400,
				From:       from,
				To:         to,
			}

			resp, err := client.ShowMetricData(request)
			if err != nil {
				failed++
				t.Logf("[非零验证][FAIL] ns=%s metric=%s dim=%s/%s(%s) err=%v",
					namespace, metric, dimName, dimValue, fsName, err)
				continue
			}
			points := 0
			if resp != nil && resp.Datapoints != nil {
				points = len(*resp.Datapoints)
			}
			if points == 0 {
				failed++
				t.Logf("[非零验证][FAIL] ns=%s metric=%s dim=%s/%s(%s) 3 天窗口无数据点(实例未挂载或指标未上报)",
					namespace, metric, dimName, dimValue, fsName)
				continue
			}
			latest := (*resp.Datapoints)[points-1]
			v := 0.0
			if latest.Average != nil {
				v = *latest.Average
			} else if latest.Max != nil {
				v = *latest.Max
			} else if latest.Sum != nil {
				v = *latest.Sum
			}
			unit := ""
			if latest.Unit != nil {
				unit = *latest.Unit
			}
			if v == 0 {
				failed++
				t.Logf("[非零验证][FAIL] ns=%s metric=%s dim=%s/%s(%s) 最新值=0(零值=探测未通过)",
					namespace, metric, dimName, dimValue, fsName)
				continue
			}
			// byte 单位换算 GB;百分比单位原样判定
			display := fmt.Sprintf("%v %s", v, unit)
			if unit == "byte" || unit == "bytes" || unit == "Byte" {
				display = fmt.Sprintf("%v byte = %.4f GB", v, nasprobe.BytesToGB(v))
			}
			passed++
			t.Logf("[非零验证][PASS] ns=%s metric=%s dim=%s/%s(%s) 数据点=%d 最新值=%s",
				namespace, metric, dimName, dimValue, fsName, points, display)
		}
	}
	t.Logf("[非零验证汇总] namespace=%s PASS=%d FAIL=%d", namespace, passed, failed)
}

// loadHuaweiCreds 直填凭证优先,否则从库加载活跃 huawei 账号。
func loadHuaweiCreds(t *testing.T) (ak, sk, region string) {
	if ak = nasprobe.EnvAK("HUAWEI"); ak != "" {
		if sk = nasprobe.EnvSK("HUAWEI"); sk != "" {
			return ak, sk, nasprobe.EnvRegion("HUAWEI")
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
		if a.Provider == "huawei" {
			rg := ""
			if len(a.Regions) > 0 {
				rg = a.Regions[0]
			}
			return a.AK, a.SK, rg
		}
	}
	return "", "", ""
}
