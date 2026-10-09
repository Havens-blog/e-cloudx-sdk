package volcengine

import (
	"math"
	"strconv"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// mapAccessLog 火山访问日志 -> SLBLogEntry。分两种 schema:
//   - 火山 ALB(`*_lb_access`,有 loadbalancer_id/listener_id,无 method,请求在
//     request 字段)→ mapALBAccess;
//   - 自采 nginx/gateway(有 method/request_uri)→ mapNginxAccess。
func mapAccessLog(m logquery.LogMeta, raw map[string]any) *logquery.SLBLogEntry {
	if raw["loadbalancer_id"] != nil || raw["listener_id"] != nil {
		return mapALBAccess(m, raw)
	}
	return mapNginxAccess(m, raw)
}

// parseTimeField 取首个可解析的非零时间戳候选。__time__ 由 TLS 返回为 JSON
// number(int64),Str() 只认字符串会把数字丢成空,故这里直接用 ParseTimeMs 逐个
// 试(ParseTimeMs 本身按量级自适配 sec/ms,并容忍 string/int64/float64/nil)。
func parseTimeField(vs ...any) int64 {
	for _, v := range vs {
		if ts := logquery.ParseTimeMs(v); ts > 0 {
			return ts
		}
	}
	return 0
}

// mapALBAccess 火山 ALB 访问日志(schema:loadbalancer_id/listener_id/servergroup_id/
// vip/vport/request/request_id/upstream_status/ssl_protocol/ssl_cipher…)。
func mapALBAccess(m logquery.LogMeta, raw map[string]any) *logquery.SLBLogEntry {
	method, uri, proto := parseRequestLine(logquery.Str(raw["request"]))
	e := &logquery.SLBLogEntry{
		Meta:              m,
		Timestamp:         parseTimeField(raw["__time__"], raw["@timestamp"]),
		ClientIP:          logquery.Str(raw["remote_addr"]),
		ClientPort:        int(logquery.Int(raw["remote_port"])),
		Host:              logquery.Str(raw["http_host"]),
		Method:            method,
		URL:               uri,
		Protocol:          proto,
		Status:            int(logquery.Int(raw["status"])),
		RequestLength:     logquery.Int(raw["request_length"]),
		BytesSent:         logquery.Int(raw["bytes_sent"]),
		LatencyMs:         secondsToMs(logquery.Str(raw["request_time"])),
		UpstreamLatencyMs: secondsToMs(logquery.Str(raw["upstream_response_time"])),
		UpstreamStatus:    int(logquery.Int(raw["upstream_status"])),
		TLSProtocol:       logquery.Str(raw["ssl_protocol"]),
		TLSCipher:         logquery.Str(raw["ssl_cipher"]),
		RequestID:         logquery.Str(raw["request_id"]),
		ListenerPort:      int(logquery.Int(raw["vport"])),
		Raw:               raw,
	}
	if addr := logquery.Str(raw["upstream_addr"]); addr != "" && addr != "-" {
		ip, port := splitAddr(addr)
		e.TargetIP, e.TargetPort = ip, port
	}
	return e
}

// parseRequestLine 拆请求行 "GET /path HTTP/1.1" → method/uri/proto。
func parseRequestLine(line string) (method, uri, proto string) {
	parts := strings.Fields(strings.TrimSpace(line))
	switch len(parts) {
	case 3:
		return parts[0], parts[1], parts[2]
	case 2:
		return parts[0], parts[1], ""
	case 1:
		return parts[0], "", ""
	default:
		return "", "", ""
	}
}

// mapNginxAccess 火山自采 nginx/gateway 访问日志(采集管道展开,字段全字符串)。
//
// 时间戳:__time__(unix ms 字符串)优先,@timestamp(ISO)兜底;datetime 是节点本地
// 时间(+0800 标签下数值与 UTC 实际不一致),不作时间戳。protocol 仅含版本("1.1"),
// 拼 "HTTP/" 前缀对齐统一模型。request_time/upstream_response_time 为秒(字符串,
// 可能 "0.123"),转毫秒。
func mapNginxAccess(m logquery.LogMeta, raw map[string]any) *logquery.SLBLogEntry {
	protocol := logquery.Str(raw["protocol"])
	if protocol != "" && !strings.HasPrefix(protocol, "HTTP/") {
		protocol = "HTTP/" + protocol
	}
	e := &logquery.SLBLogEntry{
		Meta:              m,
		Timestamp:         parseTimeField(raw["__time__"], raw["@timestamp"]),
		ClientIP:          clientIP(raw),
		Host:              logquery.Str(raw["http_host"]),
		Method:            logquery.Str(raw["method"]),
		URL:               requestURI(raw),
		Protocol:          protocol,
		Status:            int(logquery.Int(raw["status"])),
		RequestLength:     logquery.Int(raw["request_length"]),
		BytesSent:         logquery.Int(raw["body_bytes_send"]),
		LatencyMs:         secondsToMs(logquery.Str(raw["request_time"])),
		UpstreamLatencyMs: secondsToMs(logquery.Str(raw["upstream_response_time"])),
		RequestID:         logquery.Str(firstNonEmpty(raw["upstream_trace_id"], raw["j_trace_id"])),
		Raw:               raw,
	}
	if addr := logquery.Str(raw["upstream_addr"]); addr != "" && addr != "-" {
		ip, port := splitAddr(addr)
		e.TargetIP, e.TargetPort = ip, port
	}
	return e
}

// clientIP 真实客户端 IP:x_forwarded_for 首段(反代注入)优先,cdn_src_ip 兜底,
// 再退回 remote_addr(直连对端)。
func clientIP(raw map[string]any) string {
	if xff := logquery.Str(raw["x_forwarded_for"]); xff != "" && xff != "-" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	if cdn := logquery.Str(raw["cdn_src_ip"]); cdn != "" && cdn != "-" {
		return cdn
	}
	return logquery.Str(raw["remote_addr"])
}

// requestURI 完整请求 URI:request_uri(path+query) 优先,uri+uri_param 兜底。
func requestURI(raw map[string]any) string {
	if u := logquery.Str(raw["request_uri"]); u != "" {
		return u
	}
	u := logquery.Str(raw["uri"])
	if p := logquery.Str(raw["uri_param"]); p != "" {
		u += "?" + p
	}
	return u
}

// secondsToMs 秒字符串("-"/空为 0)转毫秒,浮点四舍五入。
func secondsToMs(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int64(math.Round(f * 1000))
}

// splitAddr 拆后端地址 "ip:port"(多后端取首个)。
func splitAddr(addr string) (string, int) {
	addr = strings.TrimSpace(strings.Split(addr, ",")[0])
	i := strings.LastIndexByte(addr, ':')
	if i <= 0 {
		return addr, 0
	}
	port, _ := strconv.Atoi(addr[i+1:])
	return addr[:i], port
}

// firstNonEmpty 取首个非空值(用于字段回退)。
func firstNonEmpty(vs ...any) any {
	for _, v := range vs {
		if s := logquery.Str(v); s != "" && s != "-" {
			return v
		}
	}
	return vs[len(vs)-1]
}
