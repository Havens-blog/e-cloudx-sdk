package aliyun

import (
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// mapperKind 阿里侧原始日志形态(SLS project/logstore 组合 -> 统一模型的映射键)。
// Phase 0 实测 9 类源,field-mapping.md 逐源对照。
type mapperKind string

const (
	kindALB        mapperKind = "alb"         // 阿里 ALB 访问日志(topic=alb_layer7_access_log;含聚合 LB logstore 同 schema)
	kindWAF3       mapperKind = "waf3"        // 阿里 WAF3.0 访问日志(topic=waf_access_log)
	kindDCDN       mapperKind = "dcdn"        // DCDN 边缘实时日志(CDN 实时投递(监控)同构,复用)
	kindCDNOffline mapperKind = "cdn_offline" // CDN 离线转存(独立 PascalCase schema,域名在 RequestURL)
	kindAkamaiCDN  mapperKind = "akamai_cdn"  // Akamai CDN(自采,69 字段)
	kindAkamaiWAF  mapperKind = "akamai_waf"  // Akamai WAF(自采,CEF 展开为字段)
)

// mapEntry 按 kind 把一条 SLS 原始日志映射为统一模型(Raw 全量保留,ADR D3)。
// m 提供元数据;解析失败的字段留空/零值,不报错(缺失容忍)。
func mapEntry(kind mapperKind, m logquery.LogMeta, raw map[string]string) logquery.LogEntry {
	// Raw 全量保留(string -> any 直接转,保持可序列化)
	r := make(map[string]any, len(raw))
	for k, v := range raw {
		r[k] = v
	}
	// WAF 头/cookie 全量转储体量最大且只服务明细抽屉,typed 字段已从 raw
	// (string 表)单独抽取,裁 r 不影响列表列(AWS 侧同理,含 httpRequest)。
	if kind == kindWAF3 || kind == kindAkamaiWAF {
		logquery.TrimRawHeaderDump(r)
	}
	switch kind {
	case kindALB:
		return mapALB(m, r, raw)
	case kindWAF3:
		return mapWAF3(m, r, raw)
	case kindDCDN:
		return mapDCDN(m, r, raw)
	case kindCDNOffline:
		return mapCDNOffline(m, r, raw)
	case kindAkamaiCDN:
		return mapAkamaiCDN(m, r, raw)
	case kindAkamaiWAF:
		return mapAkamaiWAF(m, r, raw)
	default:
		return nil
	}
}

// mapALB 阿里 ALB 访问日志 -> SLBLogEntry(field-mapping.md §一.1)。
func mapALB(m logquery.LogMeta, r map[string]any, raw map[string]string) *logquery.SLBLogEntry {
	e := &logquery.SLBLogEntry{
		Meta:      m,
		Timestamp: logquery.ParseTimeMs(raw["time"]),
		ClientIP:  raw["client_ip"],
		Method:    raw["request_method"],
		Protocol:  raw["server_protocol"],
		Host:      pickHost(raw["http_host"], raw["host"]),
		URL:       buildURL(raw["scheme"], raw["http_host"], raw["request_uri"]),
		Status:    int(logquery.Int(raw["status"])),
		// request_time 为秒(字符串,多后端逗号分隔取首段)
		LatencyMs:         secondsToMs(raw["request_time"]),
		UpstreamLatencyMs: secondsToMs(raw["upstream_response_time"]),
		UpstreamStatus:    int(logquery.Int(raw["upstream_status"])),
		RequestLength:     logquery.Int(raw["request_length"]),
		BytesSent:         logquery.Int(raw["body_bytes_sent"]),
		TLSProtocol:       raw["ssl_protocol"],
		TLSCipher:         raw["ssl_cipher"],
		RequestID:         raw["request_traceid"], // 聚合 LB 无此字段,容忍为空
		Raw:               r,
	}
	// client_port 拆自 vip 侧;upstream_addr "172.19.152.31:6090" 拆 host:port(多后端取首个)
	ip, port := splitAddr(raw["upstream_addr"])
	e.TargetIP, e.TargetPort = ip, port
	e.ListenerPort = int(logquery.Int(raw["slb_vport"]))
	if rid, ok := raw["app_lb_id"]; ok {
		m.ResourceID = rid
		e.Meta = m
	}
	return e
}

// mapWAF3 阿里 WAF3.0 访问日志 -> WAFLogEntry(field-mapping.md §二.3)。
// 访问流无规则/动作字段:Action 视作 pass,规则留空。
func mapWAF3(m logquery.LogMeta, r map[string]any, raw map[string]string) *logquery.WAFLogEntry {
	// real_client_ip > src_ip > remote_addr 三级回退(field-mapping.md)
	clientIP := raw["real_client_ip"]
	if clientIP == "" {
		clientIP = raw["src_ip"]
	}
	if clientIP == "" {
		clientIP = raw["remote_addr"]
	}
	uri := raw["request_uri"]
	if q := raw["querystring"]; q != "" && !strings.Contains(uri, "?") {
		uri += "?" + q
	}
	if host := pickHost(raw["host"], raw["matched_host"]); host != "" {
		m.ResourceID = host // 混装 store 的选择粒度是 host(与 DCDN 一致)
	}
	return &logquery.WAFLogEntry{
		Meta:      m,
		Timestamp: logquery.ParseTimeMs(raw["start_time"]),
		ClientIP:  clientIP,
		Host:      pickHost(raw["host"], raw["matched_host"]),
		URI:       uri,
		Method:    raw["request_method"],
		Action:    "pass",
		Status:    int(logquery.Int(raw["status"])),
		UserAgent: raw["http_user_agent"],
		Geo:       raw["region"],
		// request_time_msec 已是 ms(多后端逗号分隔,取首段)
		Raw: r,
	}
}

// mapDCDN DCDN 边缘实时日志 / CDN 实时投递(监控) -> CDNLogEntry(field-mapping.md §三.7)。
func mapDCDN(m logquery.LogMeta, r map[string]any, raw map[string]string) *logquery.CDNLogEntry {
	if raw["domain"] != "" {
		m.ResourceID = raw["domain"]
	}
	e := &logquery.CDNLogEntry{
		Meta:      m,
		Timestamp: logquery.ParseTimeMs(raw["unixtime"]),
		ClientIP:  raw["client_ip"],
		Method:    raw["method"],
		Host:      raw["domain"],
		URL:       buildURL(raw["scheme"], raw["domain"], raw["uri"], raw["uri_param"]),
		Status:    int(logquery.Int(raw["return_code"])),
		BytesSent: logquery.Int(raw["response_size"]),
		// request_time 已是 ms(边缘日志量纲;fixture 实测 2~10001,按秒解读会是小时级)
		LatencyMs: logquery.Int(raw["request_time"]),
		Referer:   buildURL(raw["refer_protocol"], raw["refer_domain"], raw["refer_uri"]),
		UserAgent: raw["user_agent"],
		// via_info "cache15.l2eu95-4[161,0], ens-cache5.cn9564[236,0]":
		// 节点间分隔为 ", "(逗号+空格),节点内数组逗号无空格,按 ", " 切
		EdgeNode:  firstSegment(raw["via_info"], ", "),
		RequestID: raw["uuid"],
		Raw:       r,
	}
	// hit_info "-,WS|CHARGE|NOTLAST":取 "|" 首段再取 "," 首段做命中率判据
	e.CacheHit = logquery.NormalizeCacheHit(firstSegment(firstSegment(raw["hit_info"], "|"), ","))
	return e
}

// mapCDNOffline CDN 离线转存 -> CDNLogEntry(field-mapping.md §三.10)。
// 独立 PascalCase schema,与 DCDN 完全不同(Phase 0 误判同构,2026-09-04 实测独立);
// 域名无独立字段,藏在 RequestURL 里。RequestTime 实测 78~194 与 API 耗时吻合,
// 按 ms 语义映射。ProxyIP(回源代理)无统一字段对应,留 Raw。
func mapCDNOffline(m logquery.LogMeta, r map[string]any, raw map[string]string) *logquery.CDNLogEntry {
	host := domainFromURL(raw["RequestURL"])
	if host != "" {
		m.ResourceID = host // 转存任务即站点,ResourceID 收敛到域名(与 DCDN/Akamai 一致)
	}
	referer := raw["Referer"]
	if referer == "-" {
		referer = ""
	}
	return &logquery.CDNLogEntry{
		Meta: m,
		// Time 为 nginx 格式 "3/Sep/2026:11:23:01 +0800"(ParseTimeMs 已支持)
		Timestamp: logquery.ParseTimeMs(raw["Time"]),
		ClientIP:  raw["RemoteIP"],
		Method:    raw["HTTPMethod"],
		Host:      host,
		URL:       raw["RequestURL"],
		Status:    int(logquery.Int(raw["HTTPStatus"])),
		BytesSent: logquery.Int(raw["ResponseSize"]),
		LatencyMs: logquery.Int(raw["RequestTime"]),
		Referer:   referer,
		UserAgent: raw["UserAgent"],
		CacheHit:  logquery.NormalizeCacheHit(raw["HitInfo"]),
		Raw:       r,
	}
}

// domainFromURL 从完整 URL 提取 host(去端口;解析失败容忍为空)。
func domainFromURL(u string) string {
	if u == "" {
		return ""
	}
	if parsed, err := url.Parse(u); err == nil {
		return parsed.Hostname()
	}
	return ""
}

// mapAkamaiCDN Akamai CDN(自采) -> CDNLogEntry(field-mapping.md §三.8)。
func mapAkamaiCDN(m logquery.LogMeta, r map[string]any, raw map[string]string) *logquery.CDNLogEntry {
	e := &logquery.CDNLogEntry{
		Meta: m,
		// reqTimeSec 名为 time 实为请求时刻戳(秒.毫秒;Phase 0 实测)
		Timestamp: logquery.ParseTimeMs(raw["reqTimeSec"]),
		ClientIP:  raw["cliIP"],
		Method:    raw["reqMethod"],
		Host:      raw["reqHost"],
		URL:       buildURLFromPath(raw["reqHost"], raw["reqPath"]),
		Status:    int(logquery.Int(raw["statusCode"])),
		BytesSent: logquery.Int(raw["bytes"]),
		LatencyMs: logquery.Int(raw["turnAroundTimeMSec"]),
		Referer:   raw["referer"],
		// UA 为 URL 编码串("%20"),展示前解码
		UserAgent: urlDecode(raw["UA"]),
		EdgeNode:  raw["cp"],
		RequestID: raw["reqId"],
		Raw:       r,
	}
	if raw["reqHost"] != "" {
		e.Meta.ResourceID = raw["reqHost"]
	}
	e.CacheHit = logquery.NormalizeCacheHit(raw["cacheStatus"])
	return e
}

// mapAkamaiWAF Akamai WAF(自采,CEF 展开字段) -> WAFLogEntry(field-mapping.md §二.5)。
func mapAkamaiWAF(m logquery.LogMeta, r map[string]any, raw map[string]string) *logquery.WAFLogEntry {
	// start 为 unix 秒;_unixtimestamp_ 为纳秒(仅校验用,勿直接当秒)
	ts := logquery.ParseTimeMs(raw["start"])
	if ts == 0 {
		ts = logquery.ParseTimeMs(raw["_unixtimestamp_"])
	}
	uri := raw["request"]
	if uri == "" {
		uri = buildURLFromPath(raw["dhost"], raw["dpath"])
	}
	if raw["dhost"] != "" {
		m.ResourceID = raw["dhost"] // 混装 store 的选择粒度是 host
	}
	return &logquery.WAFLogEntry{
		Meta:      m,
		Timestamp: ts,
		ClientIP:  raw["src"],
		Host:      raw["dhost"],
		URI:       uri,
		Method:    raw["requestMethod"],
		RuleID:    raw["cs1"], // cs1Label=Rules
		RuleName:  raw["name"],
		Action:    logquery.NormalizeAction(raw["act"]),
		Severity:  logquery.NormalizeSeverity(raw["severity"]),
		Raw:       r,
	}
}

// ---------------------------------------------------------------------
// 映射辅助(字段级转换;单测覆盖)
// ---------------------------------------------------------------------

// pickHost 多 host 字段回退(http_host 优先)。
func pickHost(fields ...string) string {
	for _, f := range fields {
		if f != "" {
			return f
		}
	}
	return ""
}

// buildURL scheme+host+uri(+param) 拼接;空 host 容忍。
func buildURL(parts ...string) string {
	scheme, host, uri := "", "", ""
	if len(parts) > 0 {
		scheme = parts[0]
	}
	if len(parts) > 1 {
		host = parts[1]
	}
	if len(parts) > 2 {
		uri = parts[2]
	}
	if len(parts) > 3 && parts[3] != "" && uri != "" && !strings.Contains(uri, "?") {
		uri += "?" + parts[3]
	}
	if host == "" {
		return uri
	}
	if scheme == "" {
		return host + uri
	}
	return scheme + "://" + host + uri
}

// buildURLFromPath host+path 拼接(无 scheme 源)。
func buildURLFromPath(host, path string) string {
	if host == "" {
		return path
	}
	return host + path
}

// secondsToMs 秒(字符串,多值逗号分隔取首段)-> 毫秒;小数秒四舍五入到 ms。
func secondsToMs(s string) int64 {
	first := firstSegment(s, ",")
	if first == "" {
		return 0
	}
	// 小数秒需保留精度("0.009" 不能截断为 0 秒),parse float 后换算
	if i := strings.IndexByte(first, '.'); i >= 0 {
		if f, err := strconv.ParseFloat(strings.TrimSpace(first), 64); err == nil {
			return int64(math.Round(f * 1000))
		}
	}
	return logquery.Int(first) * 1000
}

// splitAddr "ip:port" 拆分(多值取首个)。
func splitAddr(s string) (string, int) {
	first := firstSegment(s, ",")
	if first == "" {
		return "", 0
	}
	if i := strings.LastIndexByte(first, ':'); i > 0 {
		return first[:i], int(logquery.Int(first[i+1:]))
	}
	return first, 0
}

// firstSegment 按 sep 切分取首段(trim)。
func firstSegment(s, sep string) string {
	if s == "" {
		return ""
	}
	if i := strings.Index(s, sep); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// urlDecode 展示前解码(Akamai UA 为 URL 编码串)。
func urlDecode(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	if d, err := url.QueryUnescape(s); err == nil {
		return d
	}
	return s
}
