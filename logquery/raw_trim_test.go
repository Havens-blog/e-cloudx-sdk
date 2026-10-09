package logquery

import "testing"

func TestTrimRawHeaderDump(t *testing.T) {
	raw := map[string]any{
		"request_headers_all":       `{"x":"...2MB...","cookie":"..."}`,
		"http_cookie":               "a=b; c=d; ...",
		"response_header":           `{"content-type":"...","set-cookie":"..."}`,
		"httpRequest":               map[string]any{"uri": "/x", "headers": []any{}},
		"Cookie":                    "x=1",
		"AkamaiSiemRequestHeaders":  "CEF request headers dump",
		"AkamaiSiemResponseHeaders": "CEF response headers dump",
		"host":                      "www.jlc.com", // 非转储字段须保留
		"status":                    "200",
	}
	if !TrimRawHeaderDump(raw) {
		t.Fatal("expected changed=true")
	}
	for _, k := range []string{
		"request_headers_all", "http_cookie", "response_header",
		"httpRequest", "Cookie", "AkamaiSiemRequestHeaders", "AkamaiSiemResponseHeaders",
	} {
		if _, ok := raw[k]; ok {
			t.Errorf("key %q should be trimmed, still present", k)
		}
	}
	for _, k := range []string{"host", "status"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("key %q should be preserved, missing", k)
		}
	}
	if TrimRawHeaderDump(map[string]any{"host": "x"}) {
		t.Error("expected changed=false when no dump keys present")
	}
}

func TestIsRawHeaderDumpKey(t *testing.T) {
	for _, k := range []string{"request_headers_all", "http_cookie", "response_header", "httpRequest", "Cookie", "AkamaiSiemRequestHeaders", "AkamaiSiemResponseHeaders"} {
		if !IsRawHeaderDumpKey(k) {
			t.Errorf("IsRawHeaderDumpKey(%q) = false, want true", k)
		}
	}
	if IsRawHeaderDumpKey("host") || IsRawHeaderDumpKey("status") || IsRawHeaderDumpKey("request_uri") {
		t.Error("ordinary fields must not be classified as dump keys")
	}
}