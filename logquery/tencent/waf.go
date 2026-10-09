// 腾讯云 WAF 访问日志映射(CLS 投递)。
//
// 列名已按 topic(ap-shanghai waf_access_logtopic)真实索引字段校准
// (2026-09-20):动作/规则按模块分列(cc_*/antiscan_*/waf_*/final_*/scene_*/
// acl_*),主机 host/matched_host,方法 request_method,状态 status 等;
// 值形态(枚举)待投递开启后真实日志校准(field-mapping.md §四惯例)。
package tencent

import (
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// wafFieldMap 统一字段 → WAF 原始列(聚合/筛选下推,**须单一列**):
// 动作/规则取核心模块列(透传允许其他模块列 raw group by)。
var wafFieldMap = map[string]string{
	"host":        "host",
	"client_ip":   "client_ip",
	"method":      "request_method",
	"uri":         "request_path",
	"status":      "status",
	"rule_id":     "waf_rule_id",
	"rule_name":   "rule_name",
	"action":      "waf_action",
	"attack_type": "waf_rule_type",
}

// wafMetricExpr WAF 聚合指标(访问日志无字节/耗时列,仅计数)。
var wafMetricExpr = map[string]string{"count": "count(*)"}

// wafDimensionExpr 分组维度编译(同 EO:统一字段映射优先,否则透传,非法拒绝)。
func wafDimensionExpr(dim string) (string, bool) {
	if dim == "" {
		return "", true
	}
	if col, ok := wafFieldMap[dim]; ok {
		return col, true
	}
	if identifierLike(dim) {
		return dim, true
	}
	return "", false
}

// wafLog 一条 WAF 访问日志 → 统一 WAF 模型(字段缺失/占位容忍;Raw 全量保留)。
func wafLog(m logquery.LogMeta, raw map[string]string) *logquery.WAFLogEntry {
	ts := logquery.Int(raw["__TIMESTAMP__"])
	if ts <= 0 {
		ts = logquery.Int(raw["time"])
		if ts > 0 && ts < 1e12 {
			ts *= 1000 // 腾讯 WAF time 为 Unix 秒(兜底换算)
		}
	}
	if ts <= 0 {
		return nil
	}
	// 主机/客户端 IP/方法/URI/状态 按真实列多候选回退(索引确认列名;防个别字段缺位)
	host := firstNonEmpty(raw, "host", "matched_host")
	if host != "" {
		m.ResourceID = host // 混装 topic 的选择粒度收敛到域名
	}
	uri := firstNonEmpty(raw, "request_path", "request_uri", "request")
	if qs := firstNonEmpty(raw, "req_query_string", "query_string", "querystring"); qs != "" && !strings.Contains(uri, "?") {
		uri += "?" + qs
	}
	status := int(logquery.Int(firstNonEmpty(raw, "status", "resp_status")))
	// 动作/规则按模块分列:最终处置(final_*) > WAF 规则(waf_*) > CC/场景/ACL 模块,
	// > 兼容旧 action 字段。值枚举(拦截/观察/放行)待已投递日志校准。
	action := normalizeWAFAction(firstNonEmpty(raw, "final_action", "waf_action", "cc_action", "acl_action", "scene_action", "action"))
	severity := logquery.NormalizeSeverity(firstNonEmpty(raw, "severity", "rule_level", "level"))
	geo := joinNonEmpty(firstNonEmpty(raw, "client_country", "country", "country_name"), firstNonEmpty(raw, "client_province", "province"))
	return &logquery.WAFLogEntry{
		Meta:      m,
		Timestamp: ts,
		ClientIP:  firstNonEmpty(raw, "client_ip", "real_client_ip", "src_ip", "remote_addr", "src"),
		Host:      host,
		URI:       uri,
		Method:    firstNonEmpty(raw, "request_method", "method"),
		RuleID:    firstNonEmpty(raw, "final_rule_id", "waf_rule_id", "cc_rule_id", "acl_rule_id", "rule_id"),
		RuleName:  firstNonEmpty(raw, "rule_name"),
		Action:    action,
		Severity:  severity,
		Status:    status,
		Geo:       geo,
		Raw:       toRawMap(raw),
	}
}

// normalizeWAFAction 腾讯 WAF action → 统一枚举:
// attack(拦截)→ block,observe(观察)→ alert,pass(放行)→ pass;
// 其余交给通用归一(小写直返,兜底不丢语义)。
func normalizeWAFAction(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "attack", "block", "intercept", "deny", "reject":
		return "block"
	case "observe", "alert", "log":
		return "alert"
	case "pass", "allow", "default":
		return "pass"
	default:
		return logquery.NormalizeAction(s)
	}
}

// hasWAFFields topic 采样判定为 WAF 访问日志(特征字段;与 EO/Kong 区分)。
// 真实索引含按模块分列的规则/动作:cc_rule_*、antiscan_*、waf_rule_*、
// bypass_matched_ids、final_action 等任一即判 WAF。
func hasWAFFields(raw map[string]string) bool {
	if firstNonEmpty(raw, "request_path", "request_uri", "req_uri", "request") == "" {
		return false
	}
	return raw["waf_action"] != "" || raw["final_action"] != "" || raw["waf_rule_id"] != "" ||
		raw["cc_rule_id"] != "" || raw["antiscan_rule_id"] != "" || raw["rule_name"] != "" ||
		raw["bypass_matched_ids"] != "" || raw["severity"] != ""
}

// firstNonEmpty 依次取首个非空字段(多候选容错)。
func firstNonEmpty(raw map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := raw[k]; v != "" && v != "-" {
			return v
		}
	}
	return ""
}

// joinNonEmpty 非空片段拼接(地理等)。
func joinNonEmpty(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "/")
}
