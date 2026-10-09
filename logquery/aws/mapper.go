// Package aws AWS 日志查询 provider(Phase 3)。
//
// Phase 0 实测(source-status.md):
//   - WAFv2(CLOUDFRONT scope):Firehose -> S3 JSON Lines(gz),
//     路径 AWSLogs/<account>/WAFLogs/cloudfront/<acl>/YYYY/MM/DD/;
//   - CloudFront 标准日志:S3 TSV(gz,#Fields 头驱动),按域名前缀分目录;
//   - ALB 访问日志大多未开启,不纳入(需云控制台逐 LB 开启)。
// 字段对照 field-mapping.md §二.6/§三.9。
package aws

import (
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// mapWAFJSON 一条 AWS WAFv2 记录(JSON)-> WAFLogEntry(field-mapping.md §二.6)。
// timestamp 已是 ms;headers 是数组需查找 host/user-agent;
// terminatingRuleId=Default_Action 亦即放行(归一为 allow)。
func mapWAFJSON(m logquery.LogMeta, raw map[string]any) *logquery.WAFLogEntry {
	hr, _ := raw["httpRequest"].(map[string]any)
	e := &logquery.WAFLogEntry{
		Meta:      m,
		Timestamp: logquery.ParseTimeMs(raw["timestamp"]),
		Action:    logquery.NormalizeAction(logquery.Str(raw["action"])),
		RuleID:    logquery.Str(raw["terminatingRuleId"]),
		Raw:       raw,
	}
	if hr != nil {
		e.ClientIP = logquery.Str(hr["clientIp"])
		e.Geo = logquery.Str(hr["country"])
		e.URI = buildWAFURI(hr)
		e.Method = logquery.Str(hr["httpMethod"])
		e.Host, e.UserAgent = headerLookup(hr["headers"])
	}
	// 放行语义归一:Default_Action/无 terminating 规则 = allow
	if strings.EqualFold(e.RuleID, "Default_Action") {
		e.RuleID = ""
		if e.Action == "" || e.Action == "allow" {
			e.Action = "allow"
		}
	}
	if rid := logquery.Str(raw["httpSourceId"]); rid != "" {
		e.Meta.ResourceID = rid // CloudFront distribution ID
	}
	// httpRequest 完整请求对象(含 headers/cookie)已在上方抽出 ClientIP/URI/
	// Host/UA,裁掉只影响明细抽屉的原始 JSON,列表列不受影响。
	logquery.TrimRawHeaderDump(raw)
	return e
}

// buildWAFURI uri + args 拼接(args 已 URL 编码,原样保留)。
func buildWAFURI(hr map[string]any) string {
	uri := logquery.Str(hr["uri"])
	if a := logquery.Str(hr["args"]); a != "" && uri != "" {
		if strings.Contains(a, "=") && !strings.Contains(a, "?") {
			uri += "?" + a
		}
	}
	return uri
}

// headerLookup 从 WAF headers 数组取 host / user-agent(键大小写不敏感)。
func headerLookup(headersRaw any) (host, ua string) {
	arr, ok := headersRaw.([]any)
	if !ok {
		return "", ""
	}
	for _, item := range arr {
		h, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := logquery.Str(h["name"])
		value := logquery.Str(h["value"])
		switch strings.ToLower(name) {
		case "host":
			host = value
		case "user-agent":
			ua = value
		}
	}
	return host, ua
}

// mapCloudFrontLine 一条 CloudFront 标准日志行 -> CDNLogEntry(field-mapping.md §三.9)。
//
// fields 为 #Fields 头声明的列名(头驱动,勿写死列位);
// 无时区标记,CloudFront 均为 UTC;脏行(列数不符)降级整行进 Raw。
func mapCloudFrontLine(m logquery.LogMeta, fields []string, line string) *logquery.CDNLogEntry {
	cols := strings.Split(line, "\t")
	r := map[string]any{"_line": line}
	e := &logquery.CDNLogEntry{Meta: m, Raw: r}
	if len(cols) != len(fields) {
		return e // 脏行:统一字段留空,Raw 保底
	}
	for i, c := range cols {
		r[fields[i]] = c
	}
	get := func(name string) string {
		if v, ok := r[name].(string); ok {
			if v == "-" {
				return ""
			}
			return v
		}
		return ""
	}
	e.Timestamp = parseCloudFrontTime(get("date"), get("time"))
	e.Host = get("x-host-header") // x-host-header 为请求域名(CDN 统一模型用 Host 列)
	e.ClientIP = get("c-ip")
	e.Method = get("cs-method")
	e.URL = buildCFURL(get("x-host-header"), get("cs-uri-stem"), get("cs-uri-query"))
	e.Status = int(logquery.Int(get("sc-status")))
	e.BytesSent = logquery.Int(get("sc-bytes"))
	// time-taken 秒(float,样本 0.885)
	e.LatencyMs = secFloatToMs(get("time-taken"))
	e.CacheHit = logquery.NormalizeCacheHit(get("x-edge-result-type"))
	e.Referer = get("cs(Referer)")
	e.UserAgent = get("cs(User-Agent)")
	e.EdgeNode = get("x-edge-location")
	e.RequestID = get("x-edge-request-id")
	return e
}

// parseCloudFrontTime date + time(UTC)-> Unix ms。
func parseCloudFrontTime(date, tm string) int64 {
	if date == "" || tm == "" {
		return 0
	}
	// "2026-04-29" + "22:57:56" 均为 UTC(CloudFront 标准日志无时区标记)
	return logquery.ParseTimeMs(date + "T" + tm + "Z")
}

// buildCFURL host + stem + query 拼接(stem 已 URL 编码,原样保留)。
func buildCFURL(host, stem, query string) string {
	if host == "" {
		if stem == "" {
			return ""
		}
		if query != "" {
			return stem + "?" + query
		}
		return stem
	}
	u := url.URL{Host: host, Path: stem}
	if query != "" {
		u.RawQuery = query
	}
	return u.String()
}

// secFloatToMs 秒(float 字符串)-> 毫秒(四舍五入)。
func secFloatToMs(s string) int64 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return int64(math.Round(f * 1000))
}
