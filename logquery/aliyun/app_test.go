package aliyun

import (
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// TestFilterCatalogStoresApp kindApp 只收 <service>-business 业务流
// (nacos/ALB/metrics/internal 流全部排除)。
func TestFilterCatalogStoresApp(t *testing.T) {
	stores := []string{
		"cnc-sorter-center-business",
		"core-access-gateway-business",
		"nacos-jlcerp-com",
		"jlc-lb-log",
		"alb-mvezktcft1d81uojx9",
		"internal-ml-log",
		"jlc-lb-log-metrics",
	}
	got := filterCatalogStores(kindApp, stores)
	want := []string{"cnc-sorter-center-business", "core-access-gateway-business"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// TestMapApp 实测 schema 映射:document_type 去 -business 得服务名,
// level 归一小写,msg 优先,message 兜底,exception/trace_id 如实透传。
func TestMapApp(t *testing.T) {
	meta := logquery.LogMeta{
		Cloud:      domain.CloudProviderAliyun,
		AccountID:  "1",
		Region:     "cn-shenzhen",
		ResourceID: "cnc-third-order-system-business",
		Source:     "jlc-prod-log/cnc-third-order-system-business",
	}
	raw := map[string]string{
		"__time__":      "1793015511",
		"document_type": "cnc-third-order-system-business",
		"level":         "ERROR",
		"msg":           "---请求失败--customize=",
		"message":       "[2026-10-26 19:51:51.610] [http-nio-8080-exec-4] 原始行",
		"exception":     "com.szjlc.framework.exception.common.BusinessException: x",
		"trace_id":      "1f5233c8b6a4e17f402685c0451fb6c2",
		"pod_namespace": "prod-cnc",
	}
	e := mapEntry(kindApp, meta, raw)
	ae, ok := e.(*logquery.AppLogEntry)
	if !ok {
		t.Fatalf("mapEntry kindApp 应返回 *AppLogEntry, got %T", e)
	}
	if ae.Service != "cnc-third-order-system" {
		t.Errorf("Service = %q, want cnc-third-order-system", ae.Service)
	}
	if ae.Level != "error" {
		t.Errorf("Level = %q, want error(归一小写)", ae.Level)
	}
	if ae.Message != raw["msg"] {
		t.Errorf("Message = %q, want msg 列", ae.Message)
	}
	if ae.Exception != raw["exception"] || ae.TraceID != raw["trace_id"] {
		t.Errorf("Exception/TraceID 未如实透传")
	}
	if ae.Timestamp != 1793015511000 {
		t.Errorf("Timestamp = %d, want 1793015511000(秒→毫秒)", ae.Timestamp)
	}
	if ae.Meta.ResourceID != "cnc-third-order-system-business" {
		t.Errorf("Meta.ResourceID = %q, 应为 logstore 名", ae.Meta.ResourceID)
	}
}

// TestMapAppFallbacks document_type 缺失时服务名回退 store 名;msg 缺失时
// message 兜底;level 缺失如实留空。
func TestMapAppFallbacks(t *testing.T) {
	meta := logquery.LogMeta{ResourceID: "dayu-ai-manage-business"}
	raw := map[string]string{
		"__time__": "1793015511",
		"message":  "raw line only",
	}
	e := mapEntry(kindApp, meta, raw)
	ae := e.(*logquery.AppLogEntry)
	if ae.Service != "dayu-ai-manage" {
		t.Errorf("Service 回退 store 名 = %q", ae.Service)
	}
	if ae.Message != "raw line only" {
		t.Errorf("Message 兜底 message 列 = %q", ae.Message)
	}
	if ae.Level != "" {
		t.Errorf("Level 缺失应留空, got %q", ae.Level)
	}
}

// TestEntryMatchesApp 统一字段筛选语义(service/level 键与 /types 字典对齐)。
func TestEntryMatchesApp(t *testing.T) {
	e := &logquery.AppLogEntry{
		Service: "cnc-sorter-center",
		Level:   "error",
		Message: "boom",
	}
	if !logquery.EntryMatches(e, []logquery.FieldFilter{
		{Field: "service", Op: "eq", Value: "cnc-sorter-center"},
		{Field: "level", Op: "eq", Value: "error"},
	}) {
		t.Fatalf("service+level 过滤应命中")
	}
	if logquery.EntryMatches(e, []logquery.FieldFilter{
		{Field: "service", Op: "eq", Value: "other-service"},
	}) {
		t.Fatalf("service 不匹配应剔除")
	}
	if logquery.EntryMatches(e, []logquery.FieldFilter{
		{Field: "level", Op: "eq", Value: "warn"},
	}) {
		t.Fatalf("level 不匹配应剔除")
	}
	if !logquery.EntryMatches(e, []logquery.FieldFilter{
		{Field: "message", Op: "contains", Value: "boo"},
	}) {
		t.Fatalf("message contains 应命中")
	}
}

// TestFilterSearchPartApp level/service 可下推为 SLS 检索段。
func TestFilterSearchPartApp(t *testing.T) {
	part, ok := filterSearchPart(kindApp, []logquery.FieldFilter{
		{Field: "level", Op: "eq", Value: "error"},
		{Field: "service", Op: "eq", Value: "cnc-sorter-center"},
	})
	if !ok {
		t.Fatalf("kindApp level/service 应可下推")
	}
	// level 大小写敏感索引:变体 OR 组加括号,与 service 条件 AND 连接
	// (filterSearchPart 整体再包一层括号)
	want := `((level: "error" or level: "ERROR") and document_type: "cnc-sorter-center")`
	if part != want {
		t.Fatalf("search part = %q, want %q", part, want)
	}
	// 不可下推字段(如 message 不在单列映射内)整源跳过
	if _, ok := filterSearchPart(kindApp, []logquery.FieldFilter{
		{Field: "message", Op: "contains", Value: "x"},
	}); ok {
		t.Fatalf("message 不应可下推")
	}
	// contains 语义不受变体展开影响(通配值原样)
	part2, ok := filterSearchPart(kindApp, []logquery.FieldFilter{
		{Field: "trace_id", Op: "prefix", Value: "1f52"},
	})
	if !ok || part2 != `(trace_id: "1f52*")` {
		t.Fatalf("prefix 下推 = %q (ok=%v)", part2, ok)
	}
}

// TestAggregateTopNExprApp 应用业务流无 SQL 可读服务列(document_type
// doc_value=false,group by 恒 null),默认 TopN 诚实跳过。
func TestAggregateTopNExprApp(t *testing.T) {
	if got := aggregateTopNExpr(kindApp); got != "" {
		t.Fatalf("kindApp 默认 TopN 应跳过(document_type 无 doc_value), got %q", got)
	}
}

// TestDimensionExprAppFilterOnly service/trace_id 仅可检索过滤(doc_value=false),
// 作为分组维度必须显式拒绝;level(SQL 可读)可用。
func TestDimensionExprAppFilterOnly(t *testing.T) {
	if _, ok := dimensionExpr(kindApp, "service"); ok {
		t.Fatalf("service 应被拒绝为分组维度(doc_value=false)")
	}
	if _, ok := dimensionExpr(kindApp, "trace_id"); ok {
		t.Fatalf("trace_id 应被拒绝为分组维度(doc_value=false)")
	}
	expr, ok := dimensionExpr(kindApp, "level")
	if !ok || expr != "level" {
		t.Fatalf("level 应可作为分组维度, got %q ok=%v", expr, ok)
	}
	// 透传维度不受影响
	if _, ok := dimensionExpr(kindApp, "pod_namespace"); !ok {
		t.Fatalf("透传维度应放行(查询期 TopNSkipReason 兜底)")
	}
}
