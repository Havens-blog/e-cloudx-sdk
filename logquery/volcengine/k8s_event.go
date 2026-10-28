// k8s_event.go 火山 VKE K8s 事件日志(kindK8sEventVolc,LogTypeK8sEvent)。
//
// 实测(2026-10-28,probe-applog -provider volcano -topic ... -full):
// cn-guangzhou 共 10 个 K8s 事件 topic,两种命名约定并存:
//   - prod-k8s-{smt,cnc,finance,pcb}-event-log         (4,prod 集群)
//   - ve-jlc-{prod-box,prod-arch,prod-forface,fat,dev,uat}-k8s_event-collector (6)
// schema 完全同构(SLB/ACK 差异:火山为**扁平字段**,非 eventId 嵌套 JSON):
//
//	cluster_id / count(字符串) / firstTimestamp / lastTimestamp(ISO,毫秒精度)
//	involvedObject_kind|_name|_uid / metadata_name|_namespace|_uid
//	reason(如 Started/Info) / message / type(Normal|Warning) / source_component
//	__time__(**毫秒**,与阿里 SLS 秒不同,实测 1793177719000)
//
// 与阿里的口径差异:火山 TLS 无 SQL 分组引擎(describehistogramv1 仅原生时间
// 分桶),因此 TopN 走采样路径(TopNNote 标注口径),与 app 类型同模式。
package volcengine

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// isK8sEventTopic K8s 事件 topic 判定:两种命名约定,均已实测在网。
// 注意与访问日志分型不相交(_k8s_event-collector / -event-log 后缀,
// 且访问日志为 *_access/_error 后缀)。
func isK8sEventTopic(topicName string) bool {
	n := strings.ToLower(strings.TrimSpace(topicName))
	if strings.HasSuffix(n, "-k8s_event-collector") || strings.HasSuffix(n, "_k8s_event-collector") {
		return true
	}
	// prod-k8s-<site>-event-log:必须同时含 k8s 与结尾 event-log,避免误收
	// 其他产品的 event-log(如 tos-log-topic 类)
	return strings.Contains(n, "k8s") && strings.HasSuffix(n, "-event-log")
}

// k8sEventTopics 列出 K8s 事件 topic(与访问/app 共用 topics 清单缓存)。
// want 非空时按 topic 名收敛(资源语义 = topic 名;cluster_id 在数据行内)。
func (p *provider) k8sEventTopics(ctx context.Context, want map[string]bool) []topicRef {
	var out []topicRef
	for _, region := range p.enumRegions() {
		for _, t := range p.listTopicsCached(ctx, region) {
			if t == nil || t.TopicID == "" || !isK8sEventTopic(t.TopicName) {
				continue
			}
			if len(want) > 0 && !want[t.TopicName] {
				continue
			}
			out = append(out, topicRef{region: region, topicID: t.TopicID, topicName: t.TopicName})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].topicName < out[j].topicName })
	return out
}

// mapK8sEvent 火山 K8s 事件 -> K8sEventLogEntry(扁平字段直取)。
// level 归一:type=Normal->info、Warning->warning;type 缺失时回退外层 level。
// count 为字符串(火山原样),解析失败留 0。
func mapK8sEvent(m logquery.LogMeta, raw map[string]any) *logquery.K8sEventLogEntry {
	level := logquery.NormalizeLevel(logquery.Str(raw["type"]))
	if level == "" {
		level = logquery.NormalizeLevel(logquery.Str(raw["level"]))
	}
	if level == "normal" {
		level = "info"
	}
	count := int64(0)
	if c := logquery.Str(raw["count"]); c != "" {
		if v, err := strconv.ParseInt(strings.TrimSpace(c), 10, 64); err == nil {
			count = v
		}
	}
	reason := logquery.Str(raw["reason"])
	message := logquery.Str(raw["message"])
	// 与阿里侧同构:message 组合 "reason: message"(reason 缺失时仅 message)
	combined := message
	if reason != "" && message != "" {
		combined = reason + ": " + message
	} else if reason != "" {
		combined = reason
	}
	clusterID := logquery.Str(raw["cluster_id"])
	if clusterID == "" {
		clusterID = m.ResourceID
	}
	return &logquery.K8sEventLogEntry{
		Meta:      m,
		Timestamp: parseTimeField(raw["__time__"]),
		ClusterID: clusterID,
		// 火山事件无 clusterName 字段(实测 schema),集群名缺省留空,
		// cluster 维度回退 cluster_id(消费方可按 resource 名读 topic)
		Namespace: logquery.Str(raw["metadata_namespace"]),
		Kind:      logquery.Str(raw["involvedObject_kind"]),
		Object:    logquery.Str(raw["involvedObject_name"]),
		Reason:    reason,
		Level:     level,
		Count:     count,
		FirstSeen: parseTimeField(raw["firstTimestamp"]),
		LastSeen:  parseTimeField(raw["lastTimestamp"]),
		Message:   combined,
		Raw:       raw,
	}
}

// k8sEventPushdownQuery 检索式下推:火山 TLS key-value 检索大小写不敏感,
// 单形态即可。可下推:level(type 字段,值大小写不敏感)、reason 等值。
// 其余过滤留在明细层 EntryMatches(pushable=false 时聚合整源跳过以防失真)。
func k8sEventPushdownQuery(query string, filters []logquery.FieldFilter) (string, bool) {
	pushable := true
	var terms []string
	for _, f := range filters {
		switch {
		case strings.EqualFold(f.Field, "level") && (f.Op == "eq" || f.Op == ""):
			// 归一值 -> 火山原值:info -> Normal,warning -> Warning,
			// error -> Warning(火山事件只有 Normal|Warning 两态)
			terms = append(terms, "type:"+k8sEventTypeOf(f.Value))
		case strings.EqualFold(f.Field, "reason") && (f.Op == "eq" || f.Op == ""):
			terms = append(terms, "reason:"+strings.TrimSpace(f.Value))
		default:
			pushable = false
		}
	}
	if len(terms) == 0 {
		return query, pushable
	}
	q := strings.TrimSpace(query)
	if q == "" || q == "*" {
		return strings.Join(terms, " and "), pushable
	}
	return q + " and " + strings.Join(terms, " and "), pushable
}

// k8sEventTypeOf SDK 归一 level -> 火山 type 原值(检索下推用)。
func k8sEventTypeOf(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "warning", "warn", "error", "critical", "fatal":
		return "Warning"
	case "info", "normal", "debug", "trace":
		return "Normal"
	default:
		return strings.TrimSpace(level)
	}
}

// k8sEventScanDims 采样型 TopN 支持的维度(扁平字段直取,与 mapper 同源)。
var k8sEventScanDims = map[string]bool{
	"level": true, "reason": true, "namespace": true, "kind": true,
	"object": true, "cluster": true, "cluster_id": true,
}
