// K8s 事件映射与下推编译单测(2026-10-27 ACK eventer 实测样本锚定)。
package aliyun

import (
	"strings"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// k8sEventSampleRow 2026-10-27 实测样本(k8s-log-ccfdfb63…/k8s-event)。
func k8sEventSampleRow(eventID string) map[string]string {
	return map[string]string{
		"__time__":    "1793105301",
		"clusterId":   "ccfdfb63dc5c4427a8271dd5ce96cbcff",
		"clusterName": "ali-jlc-transit-zone-k8s",
		"eventId":     eventID,
		"hostname":    "",
		"level":       "Warning",
	}
}

const k8sEventSampleJSON = `{
 "metadata": {"name": "async-limiting-demo-124.1884a9350f394ec5", "namespace": "prod-jlc-fdn"},
 "involvedObject": {"kind": "HorizontalPodAutoscaler", "namespace": "prod-jlc-fdn", "name": "async-limiting-demo-124"},
 "reason": "FailedGetScale",
 "message": "deployments/scale.apps \"async-limiting-demo-124\" not found",
 "source": {"component": "horizontal-pod-autoscaler"},
 "firstTimestamp": "2025-12-26T04:23:19Z",
 "lastTimestamp": "2026-10-27T12:48:21Z",
 "count": 1757566,
 "type": "Warning"
}`

func TestMapK8sEvent(t *testing.T) {
	meta := logquery.LogMeta{ResourceID: "k8s-log-ccfdfb63dc5c4427a8271dd5ce96cbcff", Source: "k8s-log-ccfdfb63dc5c4427a8271dd5ce96cbcff/k8s-event"}
	e := mapEntry(kindK8sEvent, meta, k8sEventSampleRow(k8sEventSampleJSON))
	ke, ok := e.(*logquery.K8sEventLogEntry)
	if !ok {
		t.Fatalf("期望 *K8sEventLogEntry,得到 %T", e)
	}
	if ke.ClusterID != "ccfdfb63dc5c4427a8271dd5ce96cbcff" {
		t.Errorf("ClusterID = %q", ke.ClusterID)
	}
	if ke.Cluster != "ali-jlc-transit-zone-k8s" {
		t.Errorf("Cluster = %q", ke.Cluster)
	}
	if ke.Namespace != "prod-jlc-fdn" || ke.Kind != "HorizontalPodAutoscaler" || ke.Object != "async-limiting-demo-124" {
		t.Errorf("对象维度 = %s/%s/%s", ke.Namespace, ke.Kind, ke.Object)
	}
	if ke.Reason != "FailedGetScale" {
		t.Errorf("Reason = %q", ke.Reason)
	}
	if ke.Level != "warning" {
		t.Errorf("Level = %q,期望 warning", ke.Level)
	}
	if ke.Count != 1757566 {
		t.Errorf("Count = %d", ke.Count)
	}
	if ke.Timestamp != 1793105301*1000 {
		t.Errorf("Timestamp = %d", ke.Timestamp)
	}
	if ke.FirstSeen == 0 || ke.LastSeen == 0 {
		t.Errorf("FirstSeen/LastSeen = %d/%d,应从 RFC3339 解析", ke.FirstSeen, ke.LastSeen)
	}
	want := `FailedGetScale: deployments/scale.apps "async-limiting-demo-124" not found`
	if ke.Message != want {
		t.Errorf("Message = %q,期望 %q", ke.Message, want)
	}
	if ke.Raw["clusterName"] != "ali-jlc-transit-zone-k8s" {
		t.Errorf("Raw 兜底缺失")
	}
}

func TestMapK8sEventNormalIsInfo(t *testing.T) {
	meta := logquery.LogMeta{ResourceID: "k8s-log-x"}
	raw := k8sEventSampleRow(`{"reason":"Started","message":"Started container","type":"Normal"}`)
	raw["level"] = "Normal"
	e := mapEntry(kindK8sEvent, meta, raw)
	ke := e.(*logquery.K8sEventLogEntry)
	if ke.Level != "info" {
		t.Errorf("Normal 应归一 info,得到 %q", ke.Level)
	}
	if ke.Message != "Started: Started container" {
		t.Errorf("Message = %q", ke.Message)
	}
}

func TestMapK8sEventParseFallback(t *testing.T) {
	meta := logquery.LogMeta{ResourceID: "k8s-log-x"}
	raw := k8sEventSampleRow(`{broken json`)
	e := mapEntry(kindK8sEvent, meta, raw)
	ke := e.(*logquery.K8sEventLogEntry)
	if ke.Reason != "" || ke.Namespace != "" {
		t.Errorf("解析失败应留空 typed 字段,得到 reason=%q ns=%q", ke.Reason, ke.Namespace)
	}
	if !strings.Contains(ke.Message, "broken json") {
		t.Errorf("Message 应回退原文,得到 %q", ke.Message)
	}
	if ke.Level != "warning" {
		t.Errorf("外层 level 兜底失效: %q", ke.Level)
	}
}

func TestFilterSearchPartK8s(t *testing.T) {
	part, ok := filterSearchPart(kindK8sEvent, []logquery.FieldFilter{
		{Field: "level", Op: "eq", Value: "warning"},
		{Field: "reason", Op: "contains", Value: "Pull"},
	})
	if !ok {
		t.Fatal("k8s 筛选应可下推")
	}
	if !strings.Contains(part, `level: "warning"`) {
		t.Errorf("level 下推缺失: %s", part)
	}
	if !strings.Contains(part, `eventId.reason: "*Pull*"`) {
		t.Errorf("reason JSON 子键点路径下推缺失: %s", part)
	}
	if _, ok := filterSearchPart(kindK8sEvent, []logquery.FieldFilter{{Field: "cluster", Op: "eq", Value: "c1"}}); !ok {
		t.Error("cluster(cluster_id) 应可下推")
	}
	if _, ok := filterSearchPart(kindK8sEvent, []logquery.FieldFilter{{Field: "message", Op: "eq", Value: "x"}}); ok {
		t.Error("message 无检索列映射,应不可下推(由明细层兜底)")
	}
}

func TestK8sDimensionExpr(t *testing.T) {
	expr, ok := dimensionExpr(kindK8sEvent, "reason")
	if !ok || !strings.Contains(expr, `json_extract_scalar(eventId, '$.reason')`) {
		t.Errorf("reason 分组表达式 = %q ok=%v", expr, ok)
	}
	expr, ok = dimensionExpr(kindK8sEvent, "namespace")
	if !ok || !strings.Contains(expr, "involvedObject.namespace") {
		t.Errorf("namespace 分组表达式 = %q ok=%v", expr, ok)
	}
	expr, ok = dimensionExpr(kindK8sEvent, "level")
	if !ok || expr != "level" {
		t.Errorf("level 应走单列,得到 %q ok=%v", expr, ok)
	}
	if aggregateTopNExpr(kindK8sEvent) != "level" {
		t.Errorf("k8s 缺省 TopN 维度应为 level")
	}
}

func TestEntryFieldValueK8sEvent(t *testing.T) {
	e := &logquery.K8sEventLogEntry{
		Cluster: "ali-jlc-transit-zone-k8s", ClusterID: "ccdf", Namespace: "prod",
		Kind: "Pod", Object: "web-0", Reason: "OOMKilled", Level: "warning", Count: 3,
	}
	// 经导出的 EntryMatches 逐字段验证(未导出 entryFieldValue 的等价路径)
	for field, want := range map[string]string{
		"cluster": "ali-jlc-transit-zone-k8s", "cluster_id": "ccdf", "namespace": "prod",
		"kind": "Pod", "object": "web-0", "reason": "OOMKilled", "level": "warning", "count": "3",
	} {
		if !logquery.EntryMatches(e, []logquery.FieldFilter{{Field: field, Op: "eq", Value: want}}) {
			t.Errorf("field %s 过滤 %q 未命中", field, want)
		}
	}
}
