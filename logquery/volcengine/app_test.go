package volcengine

import (
	"strings"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// TestIsAppTopic 名字分型:-business 结尾是应用业务日志;business 出现在中段的
// 访问日志(域名段)不是;访问日志后缀与 app 不相交(isAccessTopic 已排除)。
func TestIsAppTopic(t *testing.T) {
	yes := []string{"zxh-smt-order-service-business", "smt-match-es-business", "Final-Business", "fat-v2-business"}
	for _, n := range yes {
		if !isAppTopic(n) {
			t.Fatalf("%s 应判定为 app topic", n)
		}
	}
	no := []string{"dev-ai-app-business_xxx_https_access", "nginx-access", "core-access-gateway_lb_access", "app-business-log-proxy"}
	for _, n := range no {
		if isAppTopic(n) {
			t.Fatalf("%s 不应判定为 app topic", n)
		}
	}
	// 与访问日志分型互斥:同一名不是 app 就是 access,不得双归属
	for _, n := range yes {
		if isAccessTopic(n) {
			t.Fatalf("%s 不得同时命中访问日志分型", n)
		}
	}
}

// TestMapApp 火山 schema 映射:document_type 去 -business 得服务名;level 归一
// 小写;msg 优先 message 兜底;__time__ ms 时间戳;exception/trace_id 透传。
func TestMapApp(t *testing.T) {
	raw := map[string]any{
		"document_type": "zxh-smt-order-service-business",
		"level":         "ERROR",
		"msg":           "订单回调处理失败",
		"exception":     "java.lang.RuntimeException: boom",
		"trace_id":      "087694d7e09d446c94a492084f317f2e",
		"__time__":      int64(1793064389483),
		"pod_name":      "zxh-smt-order-service-89-5787648dfc-xqd8c",
	}
	m := logquery.LogMeta{ResourceID: "zxh-smt-order-service-business", Region: "cn-guangzhou"}
	e := mapApp(m, raw)
	if e.Service != "zxh-smt-order-service" {
		t.Fatalf("service = %q, want zxh-smt-order-service", e.Service)
	}
	if e.Level != "error" {
		t.Fatalf("level = %q, want error(归一小写)", e.Level)
	}
	if e.Message != "订单回调处理失败" {
		t.Fatalf("message = %q", e.Message)
	}
	if e.Timestamp != 1793064389483 {
		t.Fatalf("timestamp = %d, want 1793064389483", e.Timestamp)
	}
	if e.Exception == "" || e.TraceID == "" {
		t.Fatalf("exception/trace_id 应透传: %q / %q", e.Exception, e.TraceID)
	}
	if !strings.EqualFold(e.Level, "error") {
		t.Fatalf("ERROR 级别归一后应为 error, got %q", e.Level)
	}
}

// TestMapAppFallbacks 兜底链:document_type 缺失回退源 topic 名;msg 缺失回退
// message 原始行;level 未知值留空不编造。
func TestMapAppFallbacks(t *testing.T) {
	m := logquery.LogMeta{ResourceID: "smt-match-es-business"}
	e := mapApp(m, map[string]any{"message": "[2026-10-27 09:26:29] raw line", "level": "WARN"})
	if e.Service != "smt-match-es" {
		t.Fatalf("service 兜底应为源 topic 名去后缀, got %q", e.Service)
	}
	if e.Message != "[2026-10-27 09:26:29] raw line" {
		t.Fatalf("message 应回退原始行, got %q", e.Message)
	}
	if e.Level != "warn" {
		t.Fatalf("level = %q, want warn", e.Level)
	}
	e2 := mapApp(m, map[string]any{"level": "VERBOSE"})
	if e2.Level != "verbose" {
		t.Fatalf("level 归一为 trim+lowercase, got %q", e2.Level)
	}
	if strings.EqualFold(e2.Level, "error") {
		t.Fatalf("verbose 不应判为 error")
	}
}

// TestAppPushdownQuery level 等值下推单形态(TLS 大小写不敏感,实测证);
// 与既有 query 合并;非等值/其他字段不可下推;neq 不冒险留明细层。
func TestAppPushdownQuery(t *testing.T) {
	// level=error → 单形态大写
	q, ok := appPushdownQuery("*", []logquery.FieldFilter{{Field: "level", Op: "eq", Value: "error"}})
	if !ok || q != "level:ERROR" {
		t.Fatalf("level=error 下推 = %q ok=%v, want level:ERROR/true", q, ok)
	}
	// op 空视同 eq
	q, ok = appPushdownQuery("", []logquery.FieldFilter{{Field: "level", Value: "warn"}})
	if !ok || q != "level:WARN" {
		t.Fatalf("op 空 = %q ok=%v", q, ok)
	}
	// 与业务 query 合并(and)
	q, _ = appPushdownQuery("service-error", []logquery.FieldFilter{{Field: "level", Op: "eq", Value: "error"}})
	if q != "service-error and level:ERROR" {
		t.Fatalf("合并 = %q", q)
	}
	// contains 不可下推 → 聚合层据此整源跳过
	_, ok = appPushdownQuery("*", []logquery.FieldFilter{{Field: "message", Op: "contains", Value: "超时"}})
	if ok {
		t.Fatal("message contains 不应可下推")
	}
	// neq 不冒险(未实测 not 语法),留明细层
	_, ok = appPushdownQuery("*", []logquery.FieldFilter{{Field: "level", Op: "neq", Value: "error"}})
	if ok {
		t.Fatal("level neq 不应可下推(not 语法未实测)")
	}
}

// TestAppServiceOf 服务名提取边界。
func TestAppServiceOf(t *testing.T) {
	cases := map[string]string{
		"zxh-smt-order-service-business": "zxh-smt-order-service",
		"final-business":                 "final",
		"plain":                          "plain",
	}
	for in, want := range cases {
		if got := appServiceOf(in); got != want {
			t.Fatalf("appServiceOf(%q) = %q, want %q", in, got, want)
		}
	}
	if !strings.HasSuffix("x-business", "-business") {
		t.Fatal("sanity")
	}
}

// TestAppPushdownService service 等值下推为 document_type 项(实测证:TLS
// key-value 支持 document_type: 与 AND 组合);带 -business 后缀的源名不重复
// 补后缀;neq 不可下推。
func TestAppPushdownService(t *testing.T) {
	q, ok := appPushdownQuery("*", []logquery.FieldFilter{
		{Field: "service", Op: "eq", Value: "cmi-service"},
		{Field: "level", Op: "eq", Value: "error"},
	})
	if !ok || q != "document_type:cmi-service-business and level:ERROR" {
		t.Fatalf("service+level 下推 = %q ok=%v", q, ok)
	}
	// 源名(已带 -business)原样
	q, _ = appPushdownQuery("", []logquery.FieldFilter{{Field: "service", Value: "smt-match-es-business"}})
	if q != "document_type:smt-match-es-business" {
		t.Fatalf("带后缀源名 = %q", q)
	}
	// service neq 不可下推
	_, ok = appPushdownQuery("*", []logquery.FieldFilter{{Field: "service", Op: "neq", Value: "x"}})
	if ok {
		t.Fatal("service neq 不应可下推")
	}
}

// TestAppResourceFilter 服务等值 → 资源(topic 名)收敛集;混合不可收敛过滤时
// allPushable=false 但可收敛部分仍返回。
func TestAppResourceFilter(t *testing.T) {
	want, ok := appResourceFilter([]logquery.FieldFilter{{Field: "service", Op: "eq", Value: "cmi-service"}})
	if !ok || !want["cmi-service-business"] || len(want) != 1 {
		t.Fatalf("单服务收敛 = %v ok=%v", want, ok)
	}
	want, ok = appResourceFilter([]logquery.FieldFilter{{Field: "service", Op: "neq", Value: "x"}})
	if ok || want != nil {
		t.Fatalf("纯 neq 应无收敛集 ok=%v want=%v", ok, want)
	}
	if want, _ := appResourceFilter(nil); want != nil {
		t.Fatal("空过滤应返回 nil(不收敛)")
	}
}
