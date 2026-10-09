// Package volcano_test NAS 指标探测(M1 探测任务,manual probe,只读)。
//
// 用真实账号跑一次 volcengine cloudmonitor GetMetricData,确认 NAS 容量/使用量
// 指标名;失败=记录错误+候选指标名,供 probe-report.md 给出「二期补」判定。
//
// 实测结论(2026-09-19,账号「集团-火山云」region=cn-guangzhou,355+ 组合):
//
//  1. 实际网关仅 cloudmonitor.<region>.volcengineapi.com 可用;其上只有
//     GetMetricData(Version 2018-01-01)注册。文档 6361 的元数据接口
//     ListNamespaces/ListMetrics/QueryMetricData(2021-06-30 家族)在该网关
//     一律返回 InvalidActionOrVersion(7 个候选版本 × 多签名名全部否定)。
//  2. GetMetricData 的 Period 必须是时长字符串("5m"/"1h");传秒数("3600")
//     报 ParamsValueError invalid period;传数字报「entire request invalid」。
//  3. 参数合法后,所有候选组合均报 metric not found:Namespace(FileNAS/
//     VEI_NAS/NAS/Vulcan_NAS/Vulcan_FileNAS/CFS/Vulcan_CFS/FileStorageNAS)×
//     SubNamespace × 指标名(TotalCapacity/UsedCapacity/CapacityUsage 等)×
//     维度名(FileSystemId/fs_id/file_system_id);用真实 ECS 实例 id 做的
//     阳性对照(Vulcan_ECS/Instance/CPUPercent)同样 metric not found ——
//     判定:该账号云产品监控指标注册表为空(云产品监控指标需「产品订阅」
//     开通,见文档 6408/114674),指标名本身无法经实盘收敛。
//
// 二期补判定与重试路径:开通云产品监控指标订阅后重跑本测试,按文档
// 6402/6408 候选定案 Namespace=FileNAS、MetricName=UsedCapacity/TotalCapacity/
// CapacityUsage、Dimension=FileSystemId。探测路径保留:
//
//  1. ListNamespaces 枚举命名空间(开通订阅后应可用);
//  2. ListMetrics 列出指标名/维度;
//  3. GetMetricData 用真实 fs_id 拉样例行;静态候选矩阵兜底。
//
// SKIP gate(无 env 不跑):
//   - NAS_PROBE_VOLC_AK / NAS_PROBE_VOLC_SK / NAS_PROBE_VOLC_REGION 直填凭证,或
//   - NAS_PROBE_MONGODB_DSN(+可选 CAM_ENCRYPTION_KEY)从库加载活跃 volcano 账号
//
// Hard Rule:只读凭证,只写样例行到测试日志,不动生产 ecam_nas_metric 表。
package volcano_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/nasprobe"
	"github.com/Havens-blog/e-cloudx-sdk/volcano"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/cloudmonitor"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/request"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// 候选矩阵兜底(发现失败时逐格试错留证据;见 proposal Key Risks)。
var (
	volcanoNamespaces   = []string{"NAS", "FileNAS", "Vulcan_NAS"}
	volcanoSubNamespace = []string{"nas", "file_nas", "fs"}
	// 容量/使用量候选指标名(探测报告须记录每个候选的响应,含错误)
	volcanoMetricNames = []string{
		"FileSystemCapacityUsed", "CapacityUsed", "UsedCapacity",
		"FileSystemTotalCapacity", "Capacity", "StorageUsed", "CapacityUsage",
	}
	volcanoDimNames = []string{"fs_id", "file_system_id", "FileSystemId"}
)

// TestManualProbeVolcanoNASCloudMonitor volcengine cloudmonitor 指标名探测。
func TestManualProbeVolcanoNASCloudMonitor(t *testing.T) {
	ak, sk, region, fromDB := loadVolcanoCreds(t)
	if ak == "" {
		t.Skip("未设置 NAS_PROBE_VOLC_AK/SK/REGION 或 NAS_PROBE_MONGODB_DSN,跳过(无 env 不跑)")
	}
	t.Logf("探测凭证: AK=%s 来源=%s region=%s", nasprobe.MaskAK(ak), fromDB, region)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	// 1) 只读枚举实盘 NAS 实例(复用现有 NAS 适配器凭证/region 模式)
	adapter := volcano.NewNASAdapter(ak, sk, region, elog.DefaultLogger)
	instances, err := adapter.ListInstances(ctx, region)
	if err != nil {
		t.Fatalf("枚举 NAS 实例失败(账号凭证或网络问题): %v", err)
	}
	t.Logf("实盘 NAS 实例数: %d", len(instances))
	probeFS := ""
	if len(instances) > 0 {
		probeFS = instances[0].FileSystemID
		t.Logf("探测目标实例: fs_id=%s name=%s type=%s capacity=%d used=%d",
			probeFS, instances[0].FileSystemName, instances[0].FileSystemType,
			instances[0].Capacity, instances[0].UsedCapacity)
	} else {
		t.Logf("该账号无 NAS 实例:仅能验证 namespace 合法性,无法确认实例级指标")
	}

	// 2) cloudmonitor 客户端(与 CDN/NAS 同一 session 构造模式)
	sess, err := session.NewSession(volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(ak, sk, "")).
		WithRegion(region))
	if err != nil {
		t.Fatalf("创建 cloudmonitor 会话失败: %v", err)
	}
	client := cloudmonitor.New(sess)

	// 3) 元数据发现(官方推荐路径:ListNamespaces → ListMetrics)
	namespaces, discovered := discoverVolcanoNamespaces(ctx, t, client)
	metrics := map[string]volcanoMetricMeta{}
	if discovered {
		for _, ns := range namespaces {
			for k, v := range discoverVolcanoMetrics(ctx, t, client, ns) {
				metrics[k] = v
			}
		}
	}

	// 4) 拉样例数据(发现成功:按发现结果;失败:静态候选矩阵兜底)
	end := time.Now().Unix()
	start := end - 24*3600
	calls := 0
	hit := map[string]int{}
	if discovered && len(metrics) > 0 {
		for key, meta := range metrics {
			for _, dimName := range meta.dimNames {
				var hasData bool
				calls, hasData = probeVolcanoOne(ctx, t, client, meta.namespace, meta.subNamespace,
					key, dimName, probeFS, start, end, calls)
				if hasData {
					hit[meta.namespace+"|"+meta.subNamespace+"|"+key]++
				}
			}
		}
	} else {
		t.Log("[兜底] 元数据发现失败,走静态候选矩阵逐格试错")
		for _, ns := range volcanoNamespaces {
			for _, sub := range volcanoSubNamespace {
				nsOK := false
				for _, metric := range volcanoMetricNames {
					for _, dim := range volcanoDimNames {
						var hasData bool
						calls, hasData = probeVolcanoOne(ctx, t, client, ns, sub, metric, dim, probeFS, start, end, calls)
						if hasData {
							hit[ns+"|"+sub+"|"+metric]++
							nsOK = true
						}
					}
				}
				if nsOK {
					t.Logf("[定案候选] namespace=%s sub=%s 有指标返回数据", ns, sub)
				}
			}
		}
	}

	// 5) 汇总
	t.Log("===== volcengine 探测汇总 =====")
	if len(hit) == 0 {
		t.Log("结论: 全部候选组合均无数据返回 —— 按「二期补」判定素材记录(候选清单见日志)")
	} else {
		for k, v := range hit {
			t.Logf("结论: %s 返回数据点 %d 个", k, v)
		}
	}
	t.Logf("共发起只读探测调用 %d 次", calls)
}

// volcanoMetricMeta 发现的指标元数据(namespace/subnamespace/维度名)。
type volcanoMetricMeta struct {
	namespace    string
	subNamespace string
	dimNames     []string
}

// callVolcanoAction 发送任意 monitor/cloudmonitor Action(只读探测用)。
// 云监控当前版本为 2021-06-30、签名服务 monitor、region cn-north-1
// (文档 6361/1089804;SDK cloudmonitor 内置 2018-01-01 为旧版),
// 故对每个请求覆写 ClientInfo(APIVersion/SigningName/SigningRegion)。
func callVolcanoAction(ctx context.Context, client *cloudmonitor.CLOUDMONITOR, action string, params map[string]interface{}, variant string) (*map[string]interface{}, error) {
	if params == nil {
		params = map[string]interface{}{}
	}
	op := &request.Operation{
		Name:       action,
		HTTPMethod: "POST",
		HTTPPath:   "/",
	}
	output := &map[string]interface{}{}
	req := client.NewRequest(op, &params, output)
	// variant: "v2"=2021-06-30+monitor+cn-north-1;"v1"=SDK 默认 2018-01-01
	if variant == "v2" {
		req.ClientInfo.APIVersion = "2021-06-30"
		req.ClientInfo.SigningName = "monitor"
		req.ClientInfo.ServiceName = "monitor"
		req.ClientInfo.SigningRegion = "cn-north-1"
	}
	callCtx, callCancel := context.WithTimeout(ctx, 20*time.Second)
	defer callCancel()
	req.SetContext(callCtx)
	// 不覆写 Content-Type:SDK 对 POST+map 参数默认走 form 编码(query 传参),
	// 与云监控元数据接口(ListNamespaces/ListMetrics,文档 6361)的协议一致;
	// GetMetricData(2018-01-01)由 SDK 自带 JSON 头,不受影响。
	if err := req.Send(); err != nil {
		return nil, err
	}
	return output, nil
}

// discoverVolcanoMetadata 依次尝试 v2(2021-06-30)与 v1(2018-01-01)两种版本调元数据接口(只读)。
func discoverVolcanoMetadata(ctx context.Context, t *testing.T, client *cloudmonitor.CLOUDMONITOR, action string, params map[string]interface{}) (*map[string]interface{}, error) {
	var lastErr error
	for _, variant := range []string{"v2", "v1"} {
		out, err := callVolcanoAction(ctx, client, action, params, variant)
		if err == nil {
			t.Logf("[发现] %s 成功(variant=%s)", action, variant)
			return out, nil
		}
		lastErr = err
		t.Logf("[发现] %s 失败(variant=%s): %v", action, variant, err)
	}
	return nil, lastErr
}

// discoverVolcanoNamespaces ListNamespaces 枚举命名空间,筛 NAS 相关。
// 返回 (筛出的 namespace 清单, 是否发现成功)。
func discoverVolcanoNamespaces(ctx context.Context, t *testing.T, client *cloudmonitor.CLOUDMONITOR) ([]string, bool) {
	out, err := discoverVolcanoMetadata(ctx, t, client, "ListNamespaces", map[string]interface{}{
		"Limit": 100, "Offset": 0,
	})
	if err != nil {
		return nil, false
	}
	raw, _ := json.Marshal(out)
	preview := string(raw)
	if len(preview) > 2500 {
		preview = preview[:2500] + "..."
	}
	t.Logf("[发现] ListNamespaces 原始响应: %s", preview)

	var found []string
	scanNamespaceNames(raw, &found)
	if len(found) == 0 {
		t.Log("[发现] 响应未解析出 NAS 相关命名空间(原始响应已记录)")
		return nil, false
	}
	t.Logf("[发现] NAS 相关命名空间候选: %v", found)
	return found, true
}

// scanNamespaceNames 递归扫描 JSON,收集含 "nas" 字样的命名空间/名称值。
func scanNamespaceNames(node interface{}, found *[]string) {
	switch v := node.(type) {
	case map[string]interface{}:
		for k, val := range v {
			lk := strings.ToLower(k)
			if s, ok := val.(string); ok && (lk == "namespace" || lk == "name") && strings.Contains(strings.ToLower(s), "nas") {
				*found = append(*found, s)
				continue
			}
			scanNamespaceNames(val, found)
		}
	case []interface{}:
		for _, item := range v {
			scanNamespaceNames(item, found)
		}
	}
}

// discoverVolcanoMetrics ListMetrics 列出该 namespace 下指标与维度,筛容量类。
func discoverVolcanoMetrics(ctx context.Context, t *testing.T, client *cloudmonitor.CLOUDMONITOR, namespace string) map[string]volcanoMetricMeta {
	out, err := discoverVolcanoMetadata(ctx, t, client, "ListMetrics", map[string]interface{}{
		"Namespace": namespace,
		"Limit":     1000,
	})
	if err != nil {
		t.Logf("[发现] ListMetrics(%s) 失败: %v", namespace, err)
		return nil
	}
	raw, _ := json.Marshal(out)
	preview := string(raw)
	if len(preview) > 3000 {
		preview = preview[:3000] + "..."
	}
	t.Logf("[发现] ListMetrics(%s) 原始响应: %s", namespace, preview)

	result := map[string]volcanoMetricMeta{}
	var list []interface{}
	extractMetricArrays(raw, &list)
	for _, item := range list {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		metricName, _ := m["MetricName"].(string)
		if metricName == "" {
			metricName, _ = m["metric_name"].(string)
		}
		lower := strings.ToLower(metricName)
		if !strings.Contains(lower, "cap") && !strings.Contains(lower, "size") && !strings.Contains(lower, "usage") {
			continue
		}
		meta := volcanoMetricMeta{namespace: namespace}
		if sub, ok := m["SubNamespace"].(string); ok {
			meta.subNamespace = sub
		} else if subs, ok := m["SubNamespace"].([]interface{}); ok && len(subs) > 0 {
			meta.subNamespace, _ = subs[0].(string)
		}
		meta.dimNames = extractDimNames(m["Dimensions"])
		if len(meta.dimNames) == 0 {
			meta.dimNames = []string{"fs_id", "file_system_id", "FileSystemId"}
		}
		result[metricName] = meta
	}
	t.Logf("[发现] namespace=%s 容量类指标: %d 个", namespace, len(result))
	return result
}

// extractMetricArrays 递归找 JSON 内「指标列表数组」(元素为含 MetricName 的对象)。
func extractMetricArrays(node interface{}, out *[]interface{}) {
	switch v := node.(type) {
	case map[string]interface{}:
		for _, val := range v {
			if arr, ok := val.([]interface{}); ok && len(arr) > 0 {
				if m, ok := arr[0].(map[string]interface{}); ok {
					if _, has := m["MetricName"]; has {
						*out = append(*out, arr...)
						continue
					}
					if _, has := m["metric_name"]; has {
						*out = append(*out, arr...)
						continue
					}
				}
			}
			extractMetricArrays(val, out)
		}
	case []interface{}:
		for _, item := range v {
			extractMetricArrays(item, out)
		}
	}
}

// extractDimNames 从 Dimensions 字段提取维度名(容错多种结构)。
func extractDimNames(v interface{}) []string {
	var out []string
	switch dims := v.(type) {
	case []interface{}:
		for _, d := range dims {
			switch dd := d.(type) {
			case map[string]interface{}:
				if n, ok := dd["Name"].(string); ok && n != "" {
					out = append(out, n)
				} else if n, ok := dd["name"].(string); ok && n != "" {
					out = append(out, n)
				}
			case string:
				out = append(out, dd)
			}
		}
	}
	return out
}

// probeVolcanoOne 单格探测:返回(累计调用数, 是否有数据点)。
func probeVolcanoOne(
	ctx context.Context, t *testing.T, client *cloudmonitor.CLOUDMONITOR,
	ns, sub, metric, dim, fsID string, start, end int64, calls int,
) (int, bool) {
	calls++
	if calls > 300 { // 防失控:候选矩阵全量上限
		return calls, false
	}

	input := &cloudmonitor.GetMetricDataInput{
		Namespace:    volcengine.String(ns),
		SubNamespace: volcengine.String(sub),
		MetricName:   volcengine.String(metric),
		StartTime:    volcengine.Int32(int32(start)),
		EndTime:      volcengine.Int32(int32(end)),
		// Period 不传:实测传 "3600" 报 ParamsValueError invalid period,
		// 由服务端按区间自适应粒度(CDN DescribeEdgeData 同策略)。
	}
	if fsID != "" {
		input.Instances = []*cloudmonitor.InstanceForGetMetricDataInput{{
			Dimensions: []*cloudmonitor.DimensionForGetMetricDataInput{
				{Name: volcengine.String(dim), Value: volcengine.String(fsID)},
			},
		}}
	}

	callCtx, callCancel := context.WithTimeout(ctx, 15*time.Second)
	defer callCancel()
	output, err := client.GetMetricDataWithContext(callCtx, input)
	if err != nil {
		msg := err.Error()
		if len(msg) > 160 {
			msg = msg[:160]
		}
		t.Logf("[无数据] ns=%s sub=%s metric=%s dim=%s err=%s", ns, sub, metric, dim, msg)
		return calls, false
	}
	points := 0
	if output != nil && output.Data != nil && len(output.Data.MetricDataResults) > 0 {
		for _, r := range output.Data.MetricDataResults {
			points += len(r.DataPoints)
		}
		if points > 0 {
			unit := ""
			if output.Data.Unit != nil {
				unit = *output.Data.Unit
			}
			dp := output.Data.MetricDataResults[0].DataPoints[0]
			t.Logf("[命中] ns=%s sub=%s metric=%s dim=%s 数据点=%d unit=%s 样例值=%v 样例时间戳=%v",
				ns, sub, metric, dim, points, unit, volcengine.Float64Value(dp.Value), dp.Timestamp)
			return calls, true
		}
	}
	t.Logf("[空数据] ns=%s sub=%s metric=%s dim=%s 调用成功但无数据点", ns, sub, metric, dim)
	return calls, false
}

// loadVolcanoCreds 直填凭证优先,否则从库加载活跃 volcano 账号。
func loadVolcanoCreds(t *testing.T) (ak, sk, region, source string) {
	if ak = nasprobe.EnvAK("VOLC"); ak != "" {
		if sk = nasprobe.EnvSK("VOLC"); sk != "" {
			return ak, sk, nasprobe.EnvRegion("VOLC"), "env"
		}
		return "", "", "", ""
	}
	if !nasprobe.ProbeDSNEnabled() {
		return "", "", "", ""
	}
	accounts, err := nasprobe.LoadProbeAccounts()
	if err != nil {
		t.Logf("从库加载账号失败(将跳过): %v", err)
		return "", "", "", ""
	}
	for _, a := range accounts {
		if a.Provider == "volcano" {
			rg := ""
			if len(a.Regions) > 0 {
				rg = a.Regions[0]
			}
			return a.AK, a.SK, rg, fmt.Sprintf("db(account_id=%d,name=%s)", a.ID, a.Name)
		}
	}
	return "", "", "", ""
}
