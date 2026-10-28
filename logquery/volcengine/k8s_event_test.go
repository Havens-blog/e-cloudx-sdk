// K8s 事件(火山 VKE)映射/下推/源判定单测(2026-10-28 TLS 实测样本锚定)。
package volcengine

import (
	"strings"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// k8sEventSampleRow 实测样本(ve-jlc-prod-box-k8s_event-collector,
// __time__ 为毫秒:int64 与字符串两种传入都要能解析)。
func k8sEventSampleRow() map[string]any {
	return map[string]any{
		"__time__":               int64(1793177719000),
		"cluster_id":             "cd2jeha4qp3hhlidnul0g",
		"count":                  "12",
		"firstTimestamp":         "2026-10-23T13:37:11.000Z",
		"lastTimestamp":          "2026-10-28T08:55:19.000Z",
		"involvedObject_kind":    "Pod",
		"involvedObject_name":    "elmo-buyer-web-131-66dc5fb4bd-n6lh2",
		"involvedObject_uid":     "c693857e-8f9f-47a0-a2ed-08fa48d6ffc0",
		"message":                "Started container elmo-buyer-web",
		"metadata_name":          "elmo-buyer-web-131-66dc5fb4bd-n6lh2.18e12c1d4b8db8bb",
		"metadata_namespace":     "prod-elmo",
		"metadata_uid":           "0dc130bb-157d-42b1-8be6-d58fb1f742de",
		"reason":                 "Started",
		"source_component":       "kubelet",
		"source_host":            "10.201.5.108",
		"type":                   "Normal",
	}
}

func TestIsK8sEventTopic(t *testing.T) {
	// 实测两种命名约定
	for _, name := range []string{
		"prod-k8s-smt-event-log", "prod-k8s-cnc-event-log", "prod-k8s-finance-event-log",
		"prod-k8s-pcb-event-log", "ve-jlc-prod-box-k8s_event-collector",
		"ve-jlc-prod-arch-k8s_event-collector", "ve-jlc-fat-k8s_event-collector",
	} {
		if !isK8sEventTopic(name) {
			t.Errorf("%s 应判为 K8s 事件 topic", name)
		}
	}
	// 不应误收
	for _, name := range []string{
		"jlc-data-collect-service-k8s-business", // 名字含 k8s 但是 app 业务流
		"fat-k8s_jlcops_com_https_access",       // 访问日志
		"tos-log-topic",                         // 其他 event 无关
		"apmplus-server-log",
	} {
		if isK8sEventTopic(name) {
			t.Errorf("%s 不应判为 K8s 事件 topic", name)
		}
	}
}

func TestMapK8sEventVolc(t *testing.T) {
	meta := logquery.LogMeta{ResourceID: "ve-jlc-prod-box-k8s_event-collector", Region: "cn-guangzhou"}
	e := mapK8sEvent(meta, k8sEventSampleRow())
	if e.ClusterID != "cd2jeha4qp3hhlidnul0g" {
		t.Errorf("ClusterID = %q", e.ClusterID)
	}
	if e.Namespace != "prod-elmo" || e.Kind != "Pod" || e.Object != "elmo-buyer-web-131-66dc5fb4bd-n6lh2" {
		t.Errorf("对象维度 = %s/%s/%s", e.Namespace, e.Kind, e.Object)
	}
	if e.Reason != "Started" {
		t.Errorf("Reason = %q", e.Reason)
	}
	if e.Level != "info" {
		t.Errorf("type=Normal 应归一 info,得到 %q", e.Level)
	}
	if e.Count != 12 {
		t.Errorf("Count = %d(字符串 count 应解析)", e.Count)
	}
	if e.Timestamp != 1793177719000 {
		t.Errorf("Timestamp = %d(毫秒应为原值)", e.Timestamp)
	}
	if e.FirstSeen == 0 || e.LastSeen == 0 {
		t.Errorf("FirstSeen/LastSeen = %d/%d", e.FirstSeen, e.LastSeen)
	}
	if e.Message != "Started: Started container elmo-buyer-web" {
		t.Errorf("Message = %q", e.Message)
	}
	// 火山 schema 无 clusterName:集群列以 cluster_id 兜底(不空白)
	if e.Cluster != e.ClusterID || e.Cluster == "" {
		t.Errorf("Cluster 应回退 cluster_id,得到 cluster=%q id=%q", e.Cluster, e.ClusterID)
	}
}

func TestMapK8sEventVolcWarning(t *testing.T) {
	raw := k8sEventSampleRow()
	raw["type"] = "Warning"
	raw["reason"] = "FailedGetScale"
	raw["count"] = "not-a-number" // 解析失败留 0,不 panic
	e := mapK8sEvent(logquery.LogMeta{ResourceID: "t"}, raw)
	if e.Level != "warning" {
		t.Errorf("Warning 应归一 warning,得到 %q", e.Level)
	}
	if e.Count != 0 {
		t.Errorf("非法 count 应留 0,得到 %d", e.Count)
	}
}

func TestK8sEventPushdownQuery(t *testing.T) {
	q, ok := k8sEventPushdownQuery("*", []logquery.FieldFilter{
		{Field: "level", Op: "eq", Value: "warning"},
		{Field: "reason", Op: "eq", Value: "OOMKilled"},
	})
	if !ok {
		t.Fatal("level/reason 等值应可下推")
	}
	if !strings.Contains(q, "type:Warning") {
		t.Errorf("level 应翻译为火山 type 原值: %s", q)
	}
	if !strings.Contains(q, "reason:OOMKilled") {
		t.Errorf("reason 下推缺失: %s", q)
	}
	// 不可下推过滤 -> pushable=false(聚合层整源跳过)
	if _, ok := k8sEventPushdownQuery("*", []logquery.FieldFilter{{Field: "namespace", Op: "contains", Value: "prod"}}); ok {
		t.Error("namespace contains 不可下推,应返回 false")
	}
}

func TestK8sEventScanDims(t *testing.T) {
	for _, d := range []string{"level", "reason", "namespace", "kind", "object", "cluster"} {
		if !k8sEventScanDims[d] {
			t.Errorf("维度 %s 应在采样支持集内", d)
		}
	}
	if k8sEventScanDims["message"] {
		t.Error("message 不应作为聚合维度")
	}
}
