package aliyun

import (
	"strings"
	"testing"
)

// TestNonhitCountMetricExpr CDN 缓存分析 nonhit_count 指标按 CDN 类 kind 编译:
// DCDN hit_info 复合值取首段(镜像 mapper firstSegment 语义),Akamai/离线
// 按原始列关键字过滤;非 CDN 类 kind 不支持(显式跳过)。
func TestNonhitCountMetricExpr(t *testing.T) {
	dcdn, ok := metricSQLExpr(kindDCDN, "nonhit_count")
	if !ok || !strings.Contains(dcdn, "case when") || !strings.Contains(dcdn, "regexp_extract(hit_info, '^[^|,]+')") {
		t.Errorf("kindDCDN nonhit_count 编译错误: %q (ok=%v)", dcdn, ok)
	}
	for _, k := range []mapperKind{kindAkamaiCDN, kindCDNOffline} {
		expr, ok := metricSQLExpr(k, "nonhit_count")
		if !ok || !strings.Contains(expr, "case when") || !strings.Contains(expr, "like '%MISS%'") {
			t.Errorf("%s nonhit_count 编译错误: %q (ok=%v)", k, expr, ok)
		}
	}
	// 非 CDN 类 kind(WAF/ALB)无 cache_hit 列,显式不支持 → 源级跳过标注。
	if _, ok := metricSQLExpr(kindALB, "nonhit_count"); ok {
		t.Error("kindALB 不应支持 nonhit_count")
	}
	if _, ok := metricSQLExpr(kindWAF3, "nonhit_count"); ok {
		t.Error("kindWAF3 不应支持 nonhit_count")
	}
	// nonhit_bytes(域名级字节命中下钻):同判据未命中字节,then 字节列。
	dcdnBytes, ok := metricSQLExpr(kindDCDN, "nonhit_bytes")
	if !ok || !strings.Contains(dcdnBytes, "response_size") || !strings.Contains(dcdnBytes, "%MISS%") {
		t.Errorf("kindDCDN nonhit_bytes 编译错误: %q (ok=%v)", dcdnBytes, ok)
	}
	offBytes, ok := metricSQLExpr(kindCDNOffline, "nonhit_bytes")
	if !ok || !strings.Contains(offBytes, "ResponseSize") || !strings.Contains(offBytes, "%MISS%") {
		t.Errorf("kindCDNOffline nonhit_bytes 编译错误: %q (ok=%v)", offBytes, ok)
	}
	akBytes, ok := metricSQLExpr(kindAkamaiCDN, "nonhit_bytes")
	if !ok || !strings.Contains(akBytes, "%MISS%") {
		t.Errorf("kindAkamaiCDN nonhit_bytes 编译错误: %q (ok=%v)", akBytes, ok)
	}
	if metricIsWeighted("nonhit_bytes") {
		t.Error("nonhit_bytes 应为可加指标")
	}
	// 可加指标:跨源归并按值求和(不加权)。
	if metricIsWeighted("nonhit_count") {
		t.Error("nonhit_count 应为可加指标")
	}
	// TopN SQL 拼装含完整指标表达式(单维度聚合内完成未命中过滤)。
	sql := buildAggregateTopNSQL("domain: a.com", "domain", "sum(case when hit_info like '%MISS%' then 1 else 0 end)", 10)
	if !strings.Contains(sql, "group by k order by v desc limit 10") {
		t.Errorf("TopN SQL 拼装错误: %q", sql)
	}
}
