// Package huawei 华为云 LTS 日志查询 provider(Phase 2)。
//
// Phase 0 实测(source-status.md):cn-south-1 两个日志组实时流动:
//   - hwyun-waf-logs:hwyun-waf-logs-attack(WAF 攻击,content 为 JSON)、
//     hwyun-waf-logs-access(WAF 访问,content 为 JSON);
//   - eda-prod-elb:eda-prod-gw-*-elb(ELB 访问,content 为空格分隔文本行)。
// 字段对照 field-mapping.md §一.2/§二.4。
package huawei

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// mapperKind 华为侧原始日志形态(LTS group/stream -> 统一模型的映射键)。
type mapperKind string

const (
	kindWAFAttack mapperKind = "waf_attack" // WAF 攻击日志(content JSON)
	kindWAFAccess mapperKind = "waf_access" // WAF 访问日志(content JSON)
	kindELB       mapperKind = "elb"        // ELB 访问日志(content 空格分隔文本)
)

// classify 按 group/stream 名分类日志形态(流为动态枚举,按命名后缀判定;
// 未识别组合返回 ok=false,不产出条目)。
func classify(group, stream string) (mapperKind, bool) {
	switch group {
	case "hwyun-waf-logs":
		if strings.HasSuffix(stream, "-attack") {
			return kindWAFAttack, true
		}
		if strings.HasSuffix(stream, "-access") {
			return kindWAFAccess, true
		}
	case "eda-prod-elb":
		if strings.HasSuffix(stream, "-elb") {
			return kindELB, true
		}
	}
	return "", false
}

// mapContent 按 kind 把一条 LTS content 映射为统一模型。
// WAF:content 为 JSON;ELB:content 为空格分隔文本。
// 解析失败降级:统一字段留空,Raw 保底(不报错,缺失容忍)。
func mapContent(kind mapperKind, m logquery.LogMeta, content string) logquery.LogEntry {
	switch kind {
	case kindWAFAttack, kindWAFAccess:
		raw := map[string]any{}
		if err := json.Unmarshal([]byte(content), &raw); err != nil {
			return &logquery.WAFLogEntry{Meta: m, Raw: map[string]any{"content": content}}
		}
		if kind == kindWAFAttack {
			return mapWAFAttack(m, raw)
		}
		return mapWAFAccess(m, raw)
	case kindELB:
		return mapELBLine(m, content)
	default:
		return nil
	}
}

// mapWAFAttack WAF 攻击日志 -> WAFLogEntry(field-mapping.md §二.4)。
func mapWAFAttack(m logquery.LogMeta, raw map[string]any) *logquery.WAFLogEntry {
	// attack-time ISO Z 优先(事件时刻);time_iso8601 为 +08:00 本地展示时间
	ts := logquery.ParseTimeMs(raw["attack-time"])
	if ts == 0 {
		ts = logquery.ParseTimeMs(raw["time_iso8601"])
	}
	return &logquery.WAFLogEntry{
		Meta:      m,
		Timestamp: ts,
		ClientIP:  logquery.Str(raw["remote_ip"]),
		Host:      firstNonEmpty(raw["host"], raw["http_host"]),
		URI:       logquery.Str(raw["raw_uri"]),
		Method:    logquery.Str(raw["method"]),
		RuleID:    logquery.Str(raw["rule"]),
		RuleName:  logquery.Str(raw["rule_name"]),
		Action:    logquery.NormalizeAction(logquery.Str(raw["action"])),
		// level 数字 1~5 -> low/medium/high
		Severity: logquery.NormalizeSeverity(raw["level"]),
		Status:   int(logquery.Int(raw["status"])),
		UserAgent: extractUA(raw["header"]),
		Geo:      logquery.Str(raw["geo_str"]),
		Raw:      raw,
	}
}

// mapWAFAccess WAF 访问日志 -> WAFLogEntry(访问流无规则/动作,Action=pass)。
func mapWAFAccess(m logquery.LogMeta, raw map[string]any) *logquery.WAFLogEntry {
	// waf-time ISO Z 优先;time_iso8601 兜底
	ts := logquery.ParseTimeMs(raw["waf-time"])
	if ts == 0 {
		ts = logquery.ParseTimeMs(raw["time_iso8601"])
	}
	uri := logquery.Str(raw["url"])
	if a := logquery.Str(raw["args"]); a != "" && uri != "" {
		uri += "?" + a
	}
	return &logquery.WAFLogEntry{
		Meta:      m,
		Timestamp: ts,
		ClientIP:  firstNonEmpty(raw["remote_ip"], raw["sip"]),
		Host:      firstNonEmpty(raw["http_host"], raw["web_tag"]),
		URI:       uri,
		Method:    logquery.Str(raw["method"]),
		Action:    "pass",
		Status:    int(logquery.Int(raw["response_code"])),
		UserAgent: logquery.Str(raw["user_agent"]),
		Raw:       raw,
	}
}

// mapELBLine ELB 访问日志(空格分隔文本) -> SLBLogEntry。
//
// 列位按 Phase 0 实测样本(0-based;引号/方括号为单 token):
//
//	0 msec | 1 track uuid | 2 [time_local] | 3 listener_name | 4 client ip:port
//	5 status | 6 "METHOD URL PROTO" | 7 body_bytes_sent | 8 bytes_sent | 9 request_length
//	10 request_time(秒,总耗时) | 11 "upstream_status" | 12 "upstream_connect_time"
//	13 "upstream_header_time" | 14 "upstream_response_time" | 15 "upstream_addr"
//	16 UA | 17 referer | 18 xff | 19+ loadbalancer_<uuid> ...
//
// 列数随格式版本浮动:解析失败统一降级整行进 Raw。
func mapELBLine(m logquery.LogMeta, line string) *logquery.SLBLogEntry {
	raw := map[string]any{"content": line}
	e := &logquery.SLBLogEntry{Meta: m, Raw: raw}
	toks := tokenizeELBLine(line)
	if len(toks) < 11 {
		return e // 列数不足,统一字段留空(Raw 已保底)
	}
	e.Timestamp = logquery.ParseTimeMs(toks[2]) // [2026-08-27T17:56:02+08:00]
	if e.Timestamp == 0 {
		e.Timestamp = logquery.ParseTimeMs(toks[0]) // msec 秒.毫秒兜底
	}
	e.ClientIP, e.ClientPort = splitIPPort(toks[4])
	e.Status = int(logquery.Int(toks[5]))
	if method, u, proto, ok := splitRequestLine(toks[6]); ok {
		e.Method, e.Protocol = method, proto
		e.URL = u
		if pu, err := url.Parse(u); err == nil && pu.Host != "" {
			e.Host = pu.Host
		}
	}
	e.BytesSent = logquery.Int(toks[7])
	e.RequestLength = logquery.Int(toks[9])
	e.LatencyMs = secondsToMs(toks[10])
	if len(toks) > 15 {
		e.UpstreamStatus = int(logquery.Int(toks[11]))
		// upstream_response_time(14)优先,connect/header 亦可作参考
		e.UpstreamLatencyMs = secondsToMs(toks[14])
		e.TargetIP, e.TargetPort = splitIPPort(toks[15])
	}
	if len(toks) > 16 {
		e.UserAgent = toks[16]
	}
	// loadbalancer_<uuid> 出现在 19 之后(列数随版本浮动,扫描而非定死)
	for i := 19; i < len(toks) && i < 40; i++ {
		if s := toks[i]; strings.HasPrefix(s, "loadbalancer_") {
			// 资源标识落在 Meta 上(统一模型约定)
			e.Meta.ResourceID = strings.TrimPrefix(s, "loadbalancer_")
			break
		}
	}
	_ = e
	return e
}

// tokenizeELBLine 行分词:引号段/方括号段为单 token,其余按空格切。
func tokenizeELBLine(line string) []string {
	var toks []string
	i := 0
	for i < len(line) {
		for i < len(line) && line[i] == ' ' {
			i++
		}
		if i >= len(line) {
			break
		}
		switch line[i] {
		case '"':
			if j := strings.IndexByte(line[i+1:], '"'); j >= 0 {
				toks = append(toks, line[i+1:i+1+j])
				i += j + 2
			} else {
				toks = append(toks, line[i+1:])
				i = len(line)
			}
		case '[':
			if j := strings.IndexByte(line[i:], ']'); j >= 0 {
				toks = append(toks, line[i+1:i+j])
				i += j + 1
			} else {
				toks = append(toks, line[i+1:])
				i = len(line)
			}
		default:
			if j := strings.IndexByte(line[i:], ' '); j >= 0 {
				toks = append(toks, line[i:i+j])
				i += j
			} else {
				toks = append(toks, line[i:])
				i = len(line)
			}
		}
	}
	return toks
}

// splitIPPort "ip:port" 拆分(IPv6 带端口场景不做,Phase 0 样本均为 v4)。
func splitIPPort(s string) (string, int) {
	if i := strings.LastIndexByte(s, ':'); i > 0 {
		return s[:i], int(logquery.Int(s[i+1:]))
	}
	return s, 0
}

// splitRequestLine "GET https://… HTTP/1.1" -> (method, url, proto)。
func splitRequestLine(s string) (method, u, proto string, ok bool) {
	parts := strings.Fields(s)
	if len(parts) == 3 {
		return parts[0], parts[1], parts[2], true
	}
	if len(parts) == 2 { // 无协议版本
		return parts[0], parts[1], "", true
	}
	return "", "", "", false
}

// secondsToMs 秒(字符串)-> 毫秒(四舍五入)。
func secondsToMs(s string) int64 {
	if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
		return int64(f*1000 + 0.5)
	}
	return 0
}

// extractUA 从 header JSON 串提取 user-agent(攻击流 UA 藏在 header 字段;
// 键大小写随引擎版本浮动:user-agent / User-Agent,做不敏感匹配)。
func extractUA(headerRaw any) string {
	s := logquery.Str(headerRaw)
	if s == "" {
		return ""
	}
	var h map[string]any
	if err := json.Unmarshal([]byte(s), &h); err != nil {
		return ""
	}
	for k, v := range h {
		if strings.EqualFold(k, "user-agent") {
			return logquery.Str(v)
		}
	}
	return ""
}

// firstNonEmpty 多字段回退。
func firstNonEmpty(vals ...any) string {
	for _, v := range vals {
		if s := logquery.Str(v); s != "" {
			return s
		}
	}
	return ""
}
