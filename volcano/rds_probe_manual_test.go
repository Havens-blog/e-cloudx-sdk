// Package volcano_test RDS 指标探测(rds-ops-insight M1 探测任务,manual probe,只读)。
//
// volcengine cloudmonitor 云数据库指标名探测(AC-4):RDS namespace/指标候选
// 矩阵(Namespace × SubNamespace × 指标名 × 维度名)逐格试;元数据接口
// ListNamespaces/ListMetrics 发现 RDS 相关命名空间;真实 ECS 指标阳性对照;
// 失败归因(指标订阅未开通 vs 指标不存在),二期补重试路径固化在测试注释
// (参照 NAS/OSS/Disk 前案)。
//
// SKIP gate(无 env 不跑):
//   - NAS_PROBE_VOLC_AK / NAS_PROBE_VOLC_SK / NAS_PROBE_VOLC_REGION 直填,或
//   - NAS_PROBE_MONGODB_DSN(+可选 CAM_ENCRYPTION_KEY)从库加载活跃 volcano 账号
//
// Hard Rule:只读凭证,只写样例行到测试日志,不动生产表。
package volcano_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/nasprobe"
	cxtypes "github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/volcano"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/cloudmonitor"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// volcanoRDSNamespaceCandidates RDS namespace 候选(文档 6361 云产品形态推定)。
var volcanoRDSNamespaceCandidates = []string{
	"Volcano_RDS", "Vulcan_RDS", "RDS", "RDS_MySQL", "Volcano_MySQL", "MySQL",
}

// volcanoRDSSubNamespaces SubNamespace 候选。
var volcanoRDSSubNamespaces = []string{"rds", "mysql", "instance", "db_instance"}

// volcanoRDSMetricNames RDS 四类候选指标名(CPU/内存/磁盘/连接)。
var volcanoRDSMetricNames = []string{
	"CPUUtilization", "CpuUsage", "CPUPercent", "CpuPercent",
	"MemoryUsage", "MemUsage", "MemoryPercent", "MemPercent",
	"DiskUsage", "StorageUsage", "StorageUsedPercent",
	"ConnectionCount", "Connections", "ConnectionNum", "ActiveConnections",
}

// volcanoRDSDimNames RDS 维度名候选。
var volcanoRDSDimNames = []string{"instance_id", "InstanceId", "rds_id", "db_instance_id", "ResourceID"}

// TestManualProbeVolcanoRDSMetrics volcengine cloudmonitor RDS 指标名探测(AC-4)。
func TestManualProbeVolcanoRDSMetrics(t *testing.T) {
	ak, sk, region, fromDB := loadVolcanoCreds(t)
	if ak == "" {
		t.Skip("未设置 NAS_PROBE_VOLC_AK/SK/REGION 或 NAS_PROBE_MONGODB_DSN,跳过(无 env 不跑)")
	}
	t.Logf("探测凭证: AK=%s 来源=%s region=%s", nasprobe.MaskAK(ak), fromDB, region)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	// 1) 只读枚举实盘 RDS 实例(地域性资源;逐 region)
	regions := nasprobe.EnvRegions("VOLC", region)
	var instances []cxtypes.RDSInstance
	for _, rg := range regions {
		adapter := volcano.NewRDSAdapter(&domain.CloudAccount{AccessKeyID: ak, AccessKeySecret: sk}, rg, elog.DefaultLogger)
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
	probeInstance := ""
	if len(instances) > 0 {
		probeInstance = instances[0].InstanceID
		t.Logf("探测目标实例: instance_id=%s engine=%s storage=%dGB status=%s",
			probeInstance, instances[0].Engine, instances[0].Storage, instances[0].Status)
	} else {
		t.Log("该账号无 RDS 实例:仅能验证 namespace 合法性,无法确认实例级指标")
	}

	// 2) cloudmonitor 客户端(与 CDN/NAS/Disk 同一 session 构造模式)
	sess, err := session.NewSession(volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(ak, sk, "")).
		WithRegion(region))
	if err != nil {
		t.Fatalf("创建 cloudmonitor 会话失败: %v", err)
	}
	client := cloudmonitor.New(sess)

	// 3) 元数据发现:ListNamespaces 扫 RDS/MySQL 相关命名空间(只读)
	calls := 0
	if nsList, ok := discoverVolcanoRDSNamespaces(ctx, t, client, &calls); ok && len(nsList) > 0 {
		t.Logf("[发现] RDS/MySQL 相关命名空间候选: %v", nsList)
	}

	// 4) 候选矩阵逐格探测(真实实例维度值;NAS/OSS/Disk 前案同型)
	start := time.Now().Add(-24 * time.Hour).Unix()
	end := time.Now().Unix()
	total, hits := 0, 0
	for _, ns := range volcanoRDSNamespaceCandidates {
		for _, sub := range volcanoRDSSubNamespaces {
			for _, metric := range volcanoRDSMetricNames {
				for _, dim := range volcanoRDSDimNames {
					c, hit := probeVolcanoOne(ctx, t, client, ns, sub, metric, dim, probeInstance, start, end, calls)
					calls = c
					total++
					if hit {
						hits++
						t.Logf("[定案候选] namespace=%s sub=%s metric=%s dim=%s 有数据", ns, sub, metric, dim)
					}
				}
			}
		}
	}
	t.Logf("===== volcengine RDS 探测汇总: 候选组合调用 %d/%d 次, 命中(有数据点)=%d =====", calls, total*6, hits)

	// 5) 阳性对照:真实 ECS 指标(区分「注册表空」vs「维度错」;前案同根因验证)
	lastErrKind := volcanoPositiveControl(ctx, t, client, &calls)
	if hits == 0 {
		t.Logf("[根因判定] 真实 RDS 实例候选全部无数据;阳性对照结论=%s", lastErrKind)
		t.Log("若阳性对照同样 metric not found —— 账号云产品监控指标注册表为空(订阅未开通),非指标名错误")
	}
	// 二期补重试路径(固化,参照 NAS probe §1.4 / Disk probe §1.5):
	// ① 控制台开通「云产品监控指标订阅」(写操作,超出本任务只读边界);
	// ② 重跑 TestManualProbeVolcanoRDSMetrics;
	// ③ 命中后按实盘指标名收敛定案(候选矩阵已在脚本,元数据 ListMetrics 同步复核)。
}

// discoverVolcanoRDSNamespaces ListNamespaces 枚举命名空间,筛 RDS/MySQL 相关。
func discoverVolcanoRDSNamespaces(ctx context.Context, t *testing.T, client *cloudmonitor.CLOUDMONITOR, calls *int) ([]string, bool) {
	out, err := discoverVolcanoMetadata(ctx, t, client, "ListNamespaces", map[string]interface{}{
		"Limit": 100, "Offset": 0,
	})
	if err != nil {
		return nil, false
	}
	raw, _ := json.Marshal(out)
	var found []string
	scanRDSNamespaceNames(raw, &found)
	return found, true
}

// scanRDSNamespaceNames 递归扫描 JSON,收集含 rds/mysql 字样的命名空间/名称值
// (与 NAS 探测 scanNamespaceNames 同型,关键词换为数据库类)。
func scanRDSNamespaceNames(node interface{}, found *[]string) {
	switch v := node.(type) {
	case map[string]interface{}:
		for k, val := range v {
			lk := strings.ToLower(k)
			if s, ok := val.(string); ok && (lk == "namespace" || lk == "name") {
				ls := strings.ToLower(s)
				if strings.Contains(ls, "rds") || strings.Contains(ls, "mysql") {
					*found = append(*found, s)
				}
				continue
			}
			scanRDSNamespaceNames(val, found)
		}
	case []interface{}:
		for _, item := range v {
			scanRDSNamespaceNames(item, found)
		}
	}
}

var _ = fmt.Sprintf // 保持 fmt 引用(日志格式化均在调用侧)
