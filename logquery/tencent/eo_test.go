package tencent

import (
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// 实测样本(2026-09-17 eo-log topic):attachment.forface3d.com 304 请求。
func TestEOJSONMap(t *testing.T) {
	logJson := `{"RequestHost":"attachment.forface3d.com","EdgeResponseStatusCode":"304",
		"RequestMethod":"GET","ClientIP":"120.230.148.30","RequestUrl":"/forfaceModelResource/erp/1.png",
		"RequestUrlQueryString":"fileAccessId=123&fType=png","EdgeResponseBytes":"88",
		"EdgeCacheStatus":"hit","EdgeResponseTime":"6","RequestReferer":"https://servicewechat.com/wxe/19/page-frame.html",
		"RequestUA":"Mozilla/5.0 (Linux)","EdgeServerIP":"43.145.17.247","RequestID":"11507175855918138611",
		"__TIMESTAMP__":"1789490755000","SecurityModule":"-","ClientRegion":"CN"}`
	raw := eoLogJsonMap(logJson)
	if raw["RequestHost"] != "attachment.forface3d.com" {
		t.Fatalf("eoLogJsonMap parse failed: %v", raw)
	}

	e := eoLog(logquery.LogMeta{ResourceID: "topic-x"}, raw)
	if e == nil {
		t.Fatal("eoLog returned nil")
	}
	if e.Timestamp != 1789490755000 {
		t.Errorf("Timestamp = %d, want 1789490755000", e.Timestamp)
	}
	if e.Host != "attachment.forface3d.com" || e.Meta.ResourceID != "attachment.forface3d.com" {
		t.Errorf("Host/ResourceID mismatch: %q / %q", e.Host, e.Meta.ResourceID)
	}
	if e.Status != 304 {
		t.Errorf("Status = %d, want 304", e.Status)
	}
	if e.BytesSent != 88 || e.LatencyMs != 6 {
		t.Errorf("BytesSent/LatencyMs = %d/%d, want 88/6", e.BytesSent, e.LatencyMs)
	}
	if e.CacheHit != "hit" {
		t.Errorf("CacheHit = %q, want hit", e.CacheHit)
	}
	if e.URL != "/forfaceModelResource/erp/1.png?fileAccessId=123&fType=png" {
		t.Errorf("URL = %q", e.URL)
	}
	if e.UserAgent != "Mozilla/5.0 (Linux)" || e.RequestID != "11507175855918138611" {
		t.Errorf("UA/RequestID mismatch")
	}
}

// 占位值("-"/"-1"/"null")容忍:不 panic,字段归零。
func TestEOLogPlaceholderTolerance(t *testing.T) {
	raw := map[string]string{
		"RequestHost": "-", "EdgeResponseStatusCode": "-", "EdgeResponseBytes": "-",
		"__TIMESTAMP__": "1789490755000", "EdgeCacheStatus": "-",
	}
	e := eoLog(logquery.LogMeta{}, raw)
	if e == nil {
		t.Fatal("eoLog nil on placeholder-only")
	}
	if e.Status != 0 || e.BytesSent != 0 || e.Host != "" {
		t.Errorf("placeholder should zero out: %+v", e)
	}
	if e.CacheHit != "-" {
		t.Errorf("CacheHit = %q, want - (placeholder passthrough)", e.CacheHit)
	}
}

// 无时间戳的日志丢弃(不展示)。
func TestEOLogNoTimestamp(t *testing.T) {
	if e := eoLog(logquery.LogMeta{}, map[string]string{"RequestHost": "x"}); e != nil {
		t.Error("eoLog should return nil without timestamp")
	}
}

// 维度编译:统一字段映射、原始字段透传、非法字符拒绝。
func TestEODimensionExpr(t *testing.T) {
	cases := []struct {
		dim string
		ok  bool
		out string
	}{
		{"", true, ""},
		{"host", true, "RequestHost"},
		{"status", true, "EdgeResponseStatusCode"},
		{"client_ip", true, "ClientIP"},
		{"SecurityModule", true, "SecurityModule"}, // 原始字段透传
		{"ClientRegion", true, "ClientRegion"},
		{"user agent", false, ""}, // 非法字符(空格)防注入
		{"a;drop", false, ""},
	}
	for _, c := range cases {
		got, ok := eoDimensionExpr(c.dim)
		if ok != c.ok || (ok && got != c.out) {
			t.Errorf("eoDimensionExpr(%q) = (%q,%v), want (%q,%v)", c.dim, got, ok, c.out, c.ok)
		}
	}
}
