package volcengine

import (
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
)

func TestMapAccessLog(t *testing.T) {
	m := logquery.LogMeta{Cloud: domain.CloudProviderVolcengine, Region: "cn-guangzhou", ResourceID: "topic-x"}
	raw := map[string]any{
		"__time__":               "1790143079000",        // unix ms(字符串)
		"@timestamp":             "2026-09-23T13:57:59Z", // 兜底(此处 __time__ 优先)
		"http_host":              "jlc.com",
		"method":                 "GET",
		"request_uri":            "/api/cam/xx?a=1",
		"uri":                    "/api/cam/xx",
		"uri_param":              "a=1",
		"protocol":               "1.1",
		"status":                 "200",
		"request_length":         "1234",
		"body_bytes_send":        "2574",
		"request_time":           "0.012",
		"upstream_response_time": "0.010",
		"upstream_addr":          "10.0.0.5:8080",
		"remote_addr":            "127.0.0.1",
		"x_forwarded_for":        "183.29.134.73, 10.0.0.1",
		"cdn_src_ip":             "-",
		"upstream_trace_id":      "abc123",
		"j_trace_id":             "xyz",
	}
	e := mapAccessLog(m, raw)
	if e.Timestamp != 1790143079000 {
		t.Errorf("timestamp = %d, want 1790143079000", e.Timestamp)
	}
	if e.ClientIP != "183.29.134.73" {
		t.Errorf("client_ip = %q, want 183.29.134.73(x_forwarded_for 首段)", e.ClientIP)
	}
	if e.Host != "jlc.com" || e.Method != "GET" || e.URL != "/api/cam/xx?a=1" {
		t.Errorf("host/method/url = %q/%q/%q", e.Host, e.Method, e.URL)
	}
	if e.Protocol != "HTTP/1.1" {
		t.Errorf("protocol = %q, want HTTP/1.1", e.Protocol)
	}
	if e.Status != 200 || e.RequestLength != 1234 || e.BytesSent != 2574 {
		t.Errorf("status/reqlen/bytes = %d/%d/%d", e.Status, e.RequestLength, e.BytesSent)
	}
	if e.LatencyMs != 12 || e.UpstreamLatencyMs != 10 {
		t.Errorf("latency/upstream = %d/%d ms, want 12/10", e.LatencyMs, e.UpstreamLatencyMs)
	}
	if e.TargetIP != "10.0.0.5" || e.TargetPort != 8080 {
		t.Errorf("target = %s:%d, want 10.0.0.5:8080", e.TargetIP, e.TargetPort)
	}
	if e.RequestID != "abc123" {
		t.Errorf("request_id = %q, want abc123(upstream_trace_id 优先)", e.RequestID)
	}
}

func TestMapAccessLog_Fallbacks(t *testing.T) {
	m := logquery.LogMeta{Cloud: domain.CloudProviderVolcengine}
	// 无 x_forwarded_for / upstream_addr / request_uri 时各回退路径
	e := mapAccessLog(m, map[string]any{
		"__time__":  "0", // scaleUnixToMs(0)=0
		"http_host": "h", "status": "302", "method": "POST",
		"uri": "/x", "uri_param": "b=2",
		"protocol":      "HTTP/2", // 已带前缀不再拼
		"remote_addr":   "10.1.1.1",
		"upstream_addr": "-",
	})
	if e.URL != "/x?b=2" {
		t.Errorf("url = %q, want /x?b=2(uri+uri_param 兜底)", e.URL)
	}
	if e.Protocol != "HTTP/2" {
		t.Errorf("protocol = %q, want HTTP/2(已带前缀)", e.Protocol)
	}
	if e.ClientIP != "10.1.1.1" {
		t.Errorf("client_ip = %q, want 10.1.1.1(remote_addr 兜底)", e.ClientIP)
	}
	if e.TargetIP != "" || e.TargetPort != 0 {
		t.Errorf("target = %s:%d, want empty(upstream=-)", e.TargetIP, e.TargetPort)
	}
}

func TestMapALBAccess(t *testing.T) {
	m := logquery.LogMeta{Cloud: domain.CloudProviderVolcengine, Region: "cn-guangzhou"}
	raw := map[string]any{
		"__time__":               int64(1790143079000), // TLS 返回 JSON number,非字符串
		"http_host":              "nacos.jlcops.com",
		"listener_id":            "lsn-abc",
		"loadbalancer_id":        "alb-xyz",
		"request":                "GET /nacos/v1/cs/configs HTTP/1.1",
		"request_id":             "req-123",
		"protocol_type":          "http",
		"status":                 "200",
		"request_length":         "512",
		"bytes_sent":             "1024",
		"request_time":           "0.015",
		"upstream_response_time": "0.010",
		"upstream_status":        "200",
		"upstream_addr":          "10.0.0.8:8080",
		"remote_addr":            "203.0.113.5",
		"remote_port":            "44321",
		"ssl_protocol":           "TLSv1.3",
		"ssl_cipher":             "TLS_AES_128_GCM_SHA256",
		"vport":                  "8080",
	}
	e := mapAccessLog(m, raw)
	if e.Method != "GET" || e.URL != "/nacos/v1/cs/configs" || e.Protocol != "HTTP/1.1" {
		t.Errorf("method/url/proto = %q/%q/%q, want parsed from request line", e.Method, e.URL, e.Protocol)
	}
	if e.ClientIP != "203.0.113.5" || e.ClientPort != 44321 {
		t.Errorf("client = %s:%d, want 203.0.113.5:44321", e.ClientIP, e.ClientPort)
	}
	if e.Host != "nacos.jlcops.com" || e.Status != 200 || e.BytesSent != 1024 || e.RequestLength != 512 {
		t.Errorf("host/status/bytes/reqlen = %q/%d/%d/%d", e.Host, e.Status, e.BytesSent, e.RequestLength)
	}
	if e.LatencyMs != 15 || e.UpstreamLatencyMs != 10 || e.UpstreamStatus != 200 {
		t.Errorf("latency/upstream = %d/%d/%d", e.LatencyMs, e.UpstreamLatencyMs, e.UpstreamStatus)
	}
	if e.TLSProtocol != "TLSv1.3" || e.TLSCipher != "TLS_AES_128_GCM_SHA256" {
		t.Errorf("tls = %q/%q", e.TLSProtocol, e.TLSCipher)
	}
	if e.RequestID != "req-123" || e.ListenerPort != 8080 {
		t.Errorf("reqid/port = %q/%d", e.RequestID, e.ListenerPort)
	}
	if e.TargetIP != "10.0.0.8" || e.TargetPort != 8080 {
		t.Errorf("target = %s:%d, want 10.0.0.8:8080", e.TargetIP, e.TargetPort)
	}
}

func TestParseTimeField(t *testing.T) {
	cases := []struct {
		name string
		args []any
		want int64
	}{
		{"numeric int64 毫秒", []any{int64(1790228758000), nil}, 1790228758000},
		{"numeric float64 毫秒", []any{float64(1790228758000)}, 1790228758000},
		{"string 毫秒", []any{"1790228758000"}, 1790228758000},
		{"string 秒回退", []any{nil, "1790228758"}, 1790228758000},
		{"首个非零优先", []any{int64(1790228758000), "2026-09-24T13:46:01Z"}, 1790228758000},
		{"全空", []any{nil, ""}, 0},
	}
	for _, c := range cases {
		if got := parseTimeField(c.args...); got != c.want {
			t.Errorf("parseTimeField(%v) = %d, want %d(%s)", c.args, got, c.want, c.name)
		}
	}
}

func TestSourceLabel(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"fat-nacos_jlcerp_com_lb_access", "ALB"},             // 火山 ALB 产品日志
		{"prod_dayu-access-gateway-limited_lb_access", "ALB"}, // 火山 ALB(限流)
		{"www_jlc_com_https_access", "Nginx"},                 // 自采 fqdn nginx
		{"ai-mobile-web-nginx-access", "Nginx"},               // 自采 nginx
		{"jsjlc-access-gateway-gateway-access", "Gateway"},    // 自采 gateway
	}
	for _, c := range cases {
		if got := sourceLabel(c.name); got != c.want {
			t.Errorf("sourceLabel(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestIsALBAccessTopic(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"fat-nacos_jlcerp_com_lb_access", true},             // 火山 ALB
		{"prod_dayu-access-gateway-limited_lb_access", true}, // 火山 ALB(限流)
		{"www_jlc_com_https_access", false},                  // 自采 fqdn nginx,非 ALB
		{"ai-mobile-web-nginx-access", false},                // 自采 nginx
		{"jsjlc-access-gateway-gateway-access", false},       // 自采 gateway
	}
	for _, c := range cases {
		if got := isALBAccessTopic(c.name); got != c.want {
			t.Errorf("isALBAccessTopic(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIsAccessTopic(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"www_jlc_com_https_access", true},
		{"ai-mobile-web-nginx-access", true},
		{"jsjlc-access-gateway-gateway-access", true},
		{"fat-nacos_jlcerp_com_lb_access", true},              // 火山 ALB 访问日志
		{"prod_dayu-access-gateway-limited_lb_access", true},  // 火山 ALB(限流)
		{"forface-model-viewer-tomcat-access", false},         // 应用服务器访问,非边缘
		{"ai-app-core-service-business", false},               // 应用业务日志
		{"core-access-gateway-business", false},               // 网关业务日志(Java message 型,非访问日志)
		{"jlc-app-deploy-access-business", false},             // 应用部署业务日志
		{"dev-ai-app-business_jlcerp_com_https_access", true}, // business 是域名段(下划线),仍是边缘 https 访问
		{"www_jlc_com_https_error", false},                    // 错误日志
	}
	for _, c := range cases {
		if got := isAccessTopic(c.name); got != c.want {
			t.Errorf("isAccessTopic(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
