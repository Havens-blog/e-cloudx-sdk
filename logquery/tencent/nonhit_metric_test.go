package tencent

import (
	"strings"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// TestEONonhitCountMetric CDN 缓存分析 nonhit_count 指标(EdgeOne
// EdgeCacheStatus 关键字过滤;归一口径与明细层 NormalizeCacheHit 同判)。
func TestEONonhitCountMetric(t *testing.T) {
	expr, ok := eoMetricExpr["nonhit_count"]
	if !ok || !strings.Contains(expr, "EdgeCacheStatus") || !strings.Contains(expr, "case when") {
		t.Fatalf("eoMetricExpr nonhit_count 编译错误: %q (ok=%v)", expr, ok)
	}
	if !strings.Contains(expr, "like '%MISS%'") || !strings.Contains(expr, "like '%ERROR%'") {
		t.Errorf("未命中口径应过滤 miss+error: %q", expr)
	}
	// 可加指标:联邦/提供方归并按值求和(不加权)。
	if logquery.MetricIsWeighted("nonhit_count") {
		t.Error("nonhit_count 应为可加指标")
	}
	if !logquery.IsValidAggregateMetric("nonhit_count") {
		t.Error("nonhit_count 应在合法聚合指标白名单内")
	}
	// nonhit_bytes(域名级字节命中下钻):then 字节列 EdgeResponseBytes。
	exprBytes, ok := eoMetricExpr["nonhit_bytes"]
	if !ok || !strings.Contains(exprBytes, "EdgeResponseBytes") || !strings.Contains(exprBytes, "case when") {
		t.Fatalf("eoMetricExpr nonhit_bytes 编译错误: %q (ok=%v)", exprBytes, ok)
	}
	if logquery.MetricIsWeighted("nonhit_bytes") {
		t.Error("nonhit_bytes 应为可加指标")
	}
	if !logquery.IsValidAggregateMetric("nonhit_bytes") {
		t.Error("nonhit_bytes 应在合法聚合指标白名单内")
	}
}
