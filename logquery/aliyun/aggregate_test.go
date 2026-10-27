package aliyun

import (
	"strings"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// TestBuildAggregateSearchPart 检索段拼接:topic 过滤 / 用户检索式 / 混装源域名过滤。
func TestBuildAggregateSearchPart(t *testing.T) {
	cases := []struct {
		name       string
		kind       mapperKind
		query      string
		resources  []string
		selfNames  []string
		wantSubstr []string
		wantNot    []string
	}{
		{
			name: "waf3 topic filter", kind: kindWAF3,
			wantSubstr: []string{"__topic__:waf_access_log"},
		},
		{
			name: "alb topic + user query", kind: kindALB, query: "status: 500",
			wantSubstr: []string{"__topic__:alb_layer7_access_log", "(status: 500)"},
		},
		{
			name: "dcdn domain resources", kind: kindDCDN, resources: []string{"a.com", "b.com"},
			wantSubstr: []string{"(domain: a.com or domain: b.com)"},
		},
		{
			name: "akamai reqHost resources", kind: kindAkamaiCDN, resources: []string{"rs.jlcpcb.com"},
			wantSubstr: []string{"reqHost: rs.jlcpcb.com"},
		},
		{
			name: "non-mixed ignores resources", kind: kindALB, resources: []string{"a.com"},
			wantNot: []string{"a.com"},
		},
		{
			name: "waf3 host resources", kind: kindWAF3, resources: []string{"a.com", "b.com"},
			wantSubstr: []string{"__topic__:waf_access_log", "(host: a.com or host: b.com)"},
		},
		{
			name: "whole-source self name skips domain filter", kind: kindDCDN,
			query: "", resources: []string{"dcdn-edge-rtlog"},
			selfNames:  []string{"dcdn-edge-rtlog", "dcdn-edge-rtlog-cn-42d9825f"},
			wantSubstr: []string{"*"},
			wantNot:    []string{"domain:"},
		},
		{
			name: "empty query no user term", kind: kindDCDN,
			wantSubstr: []string{"*"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildAggregateSearchPart(c.kind, c.query, c.resources, c.selfNames...)
			if got == "" {
				got = "*"
			}
			for _, s := range c.wantSubstr {
				if !strings.Contains(got, s) {
					t.Errorf("search part %q missing %q", got, s)
				}
			}
			for _, s := range c.wantNot {
				if strings.Contains(got, s) {
					t.Errorf("search part %q should not contain %q", got, s)
				}
			}
		})
	}
}

// TestBuildAggregateBucketSQL 分桶 SQL:取模步长与 order by 存在;空检索段回退 *。
func TestBuildAggregateBucketSQL(t *testing.T) {
	got := buildAggregateBucketSQL("__topic__:x", 300)
	for _, want := range []string{"__time__ - __time__ % 300", "group by t", "order by t", "limit 200"} {
		if !strings.Contains(got, want) {
			t.Errorf("bucket sql %q missing %q", got, want)
		}
	}
	if got := buildAggregateBucketSQL("", 60); !strings.HasPrefix(got, "* |") {
		t.Errorf("empty search part should fall back to *: %q", got)
	}
}

// TestBuildAggregateTopNSQL TopN SQL:维度/指标表达式与降序 limit。
func TestBuildAggregateTopNSQL(t *testing.T) {
	got := buildAggregateTopNSQL("*", "domain", "count(1)", 10)
	for _, want := range []string{"select domain as k", "count(1) as n", "group by k", "order by v desc", "limit 10"} {
		if !strings.Contains(got, want) {
			t.Errorf("topn sql %q missing %q", got, want)
		}
	}
}

// TestFilterSearchPart 检索段编译:eq 数值不引号/文本引号、prefix 通配、
// neq 反衬、不可下推字段返回 false。
func TestFilterSearchPart(t *testing.T) {
	cases := []struct {
		kind   mapperKind
		filter logquery.FieldFilter
		want   string
		ok     bool
	}{
		{kindDCDN, logquery.FieldFilter{Field: "status", Op: "eq", Value: "404"}, "(return_code: 404)", true},
		{kindDCDN, logquery.FieldFilter{Field: "host", Op: "eq", Value: "www.jlc.com"}, `(domain: "www.jlc.com")`, true},
		{kindDCDN, logquery.FieldFilter{Field: "host", Op: "prefix", Value: "static"}, `(domain: "static*")`, true},
		{kindDCDN, logquery.FieldFilter{Field: "host", Op: "neq", Value: "a.com"}, `(not domain: "a.com")`, true},
		{kindCDNOffline, logquery.FieldFilter{Field: "host", Op: "eq", Value: "x"}, "(regexp_extract(RequestURL, '^(?:https?://)?([^/?]+)', 1): \"x\")", true},
		{kindDCDN, logquery.FieldFilter{Field: "cache_hit", Op: "eq", Value: "hit"}, "", false}, // 跨字段归一,不可下推
	}
	for _, c := range cases {
		got, ok := filterSearchPart(c.kind, []logquery.FieldFilter{c.filter})
		if ok != c.ok || got != c.want {
			t.Errorf("filterSearchPart(%v, %+v) = %q, ok=%v; want %q, %v", c.kind, c.filter, got, ok, c.want, c.ok)
		}
	}
}

// TestDimensionExprPassthrough 维度三级解析:归一化映射优先、合法标识符透传
// (按任意云上原始列聚合,如 real_client_ip)、非法字符(SQL 注入面)拒绝。
func TestDimensionExprPassthrough(t *testing.T) {
	cases := []struct {
		kind mapperKind
		dim  string
		want string
		ok   bool
	}{
		{kindWAF3, "client_ip", "real_client_ip", true},      // 归一化映射优先
		{kindWAF3, "real_client_ip", "real_client_ip", true}, // 原始列透传
		{kindDCDN, "user_agent", "user_agent", true},         // 任意列名
		{kindDCDN, "status; drop table", "", false},          // 注入面
		{kindDCDN, "host | select", "", false},
		{kindDCDN, "", "", true}, // 缺省回退语义
	}
	for _, c := range cases {
		got, ok := dimensionExpr(c.kind, c.dim)
		if got != c.want || ok != c.ok {
			t.Errorf("dimensionExpr(%v, %q) = %q, ok=%v; want %q, %v", c.kind, c.dim, got, ok, c.want, c.ok)
		}
	}
}

// TestMetricSQLExpr 指标编译:count 全 kind,avg/p99 仅带 latency 列的 kind。
func TestMetricSQLExpr(t *testing.T) {
	for _, kind := range []mapperKind{kindDCDN, kindCDNOffline, kindAkamaiCDN, kindALB} {
		if _, ok := metricSQLExpr(kind, "p99_latency"); !ok {
			t.Errorf("metric p99_latency should be supported for %v", kind)
		}
	}
	if _, ok := metricSQLExpr(kindWAF3, "avg_latency"); ok {
		t.Errorf("kindWAF3 has no latency column, avg_latency should be unsupported")
	}
	if _, ok := metricSQLExpr(kindDCDN, "sum_bytes"); !ok {
		t.Errorf("sum_bytes should be supported for dcdn")
	}
}

// TestAggregateTopNExpr 统计 TopN 维度映射(CDN=域名/转存=URL 提取/
// Akamai CDN=reqHost/Akamai WAF=规则名/WAF3 访问流跳过/ALB=host)。
func TestAggregateTopNExpr(t *testing.T) {
	cases := map[mapperKind]string{
		kindDCDN:       "domain",
		kindAkamaiCDN:  "reqHost",
		kindCDNOffline: "regexp_extract(RequestURL",
		kindAkamaiWAF:  "name",
		kindALB:        "http_host",
		kindWAF3:       "", // 访问流无规则维度,跳过 TopN
	}
	for kind, want := range cases {
		got := aggregateTopNExpr(kind)
		if want == "" {
			if got != "" {
				t.Errorf("kind %q expr = %q, want empty", kind, got)
			}
			continue
		}
		if !strings.Contains(got, want) {
			t.Errorf("kind %q expr = %q, want containing %q", kind, got, want)
		}
	}
}

// TestActiveDomainExpr 源选择粒度的 host 维度(与统计 TopN 维度是两个语义:
// Akamai WAF 源选择=dhost,统计 TopN=规则名 name;曾混用导致域名枚举按
// 规则名分组,66 个 host 只出 2 个规则名)。
func TestActiveDomainExpr(t *testing.T) {
	cases := map[mapperKind]string{
		kindDCDN:       "domain",
		kindAkamaiCDN:  "reqHost",
		kindCDNOffline: "regexp_extract(RequestURL",
		kindWAF3:       "host",
		kindAkamaiWAF:  "dhost",
		kindALB:        "", // 实例流单资源,非混装
	}
	for kind, want := range cases {
		got := activeDomainExpr(kind)
		if want == "" {
			if got != "" {
				t.Errorf("kind %q expr = %q, want empty", kind, got)
			}
			continue
		}
		if !strings.Contains(got, want) {
			t.Errorf("kind %q expr = %q, want containing %q", kind, got, want)
		}
	}
}

// TestAggregateBucketRoundTrip 分桶行解析(t 秒 -> ms;c 求和 = Total)。
func TestAggregateBucketRoundTrip(t *testing.T) {
	// 走 aggregateStore 太重(SLS client),这里直接验证行的语义约定:
	// t/c 键名由 SQL builder 决定,此处锁定键名防漂移。
	sql := buildAggregateBucketSQL("*", 60)
	if !strings.Contains(sql, " as t") || !strings.Contains(sql, "as c") {
		t.Fatalf("bucket sql must alias t/c: %q", sql)
	}
	// PickBucketSec 对齐语义(共享包)
	if got := logquery.PickBucketSec(0, 6*3600_000); got != 300 {
		t.Errorf("PickBucketSec(6h) = %d, want 300(6h/300s=72 桶 ≤100)", got)
	}
	if got := logquery.PickBucketSec(0, 30*1000); got != 60 {
		t.Errorf("PickBucketSec(30s) = %d, want 60", got)
	}
	if got := logquery.PickBucketSec(0, 30*86400_000); got != 86400 {
		t.Errorf("PickBucketSec(30d) = %d, want 86400(封顶)", got)
	}
}

// TestScanDimension 扫描型维度判定:kindApp 的 service/trace_id 走采样分组,
// 其余维度/kind 不走。
func TestScanDimension(t *testing.T) {
	if !scanDimension(kindApp, "service") || !scanDimension(kindApp, "trace_id") {
		t.Fatal("kindApp service/trace_id 应为扫描型")
	}
	if scanDimension(kindApp, "level") || scanDimension(kindDCDN, "domain") {
		t.Fatal("非扫描型维度误判")
	}
}
