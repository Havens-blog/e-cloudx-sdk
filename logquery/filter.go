// 结构化字段筛选(plan.md §9 扩展):按 /types 字段字典构建多条件
// (eq/neq/contains/prefix) AND 叠加。统一语义定义在**归一化字段**上:
//   - 明细(Search):映射后逐条过滤(采样量级,各云一致,不依赖搜索索引);
//   - 聚合(Aggregate):能下推的源编译进检索/分析 SQL,不能下推的源
//     显式标注跳过(与"不支持聚合"同款诚实姿势,不静默给错数)。
package logquery

import (
	"slices"
	"strconv"
	"strings"
)

// FieldFilter 结构化字段筛选条件。Field 为 /types 字段字典的 key。
type FieldFilter struct {
	Field string `json:"field"` // client_ip / host / status / rule_name ...
	Op    string `json:"op"`    // eq / neq / contains / prefix
	Value string `json:"value"`
}

// fieldFilterOps 合法操作符集合(未知 op 在请求层即拒绝)。
var fieldFilterOps = []string{"eq", "neq", "contains", "prefix"}

// IsValidFieldFilterOp 校验操作符(空 = eq,缺失容忍)。
func IsValidFieldFilterOp(op string) bool {
	if op == "" {
		return true
	}
	return slices.Contains(fieldFilterOps, op)
}

// ValidFieldFilterOps 合法操作符清单(前端下拉)。
func ValidFieldFilterOps() []string { return append([]string(nil), fieldFilterOps...) }

// EntryMatches 判断条目是否命中全部字段条件(合约:统一字段语义,
// 数值/枚举按字符串化后比较,eq 精确匹配)。
func EntryMatches(e LogEntry, filters []FieldFilter) bool {
	if len(filters) == 0 {
		return true
	}
	for _, f := range filters {
		v, ok := entryFieldValue(e, f.Field)
		if !ok || !matchOp(v, f) {
			return false
		}
	}
	return true
}

// matchOp 单条件匹配(未知 op 视作 eq,由上层校验保证)。
func matchOp(value string, f FieldFilter) bool {
	switch f.Op {
	case "neq":
		return value != f.Value
	case "contains":
		return strings.Contains(value, f.Value)
	case "prefix":
		return strings.HasPrefix(value, f.Value)
	default: // "" / eq
		return value == f.Value
	}
}

// entryFieldValue 取统一条目字段的字符串化值(与 /types 字段字典键对齐;
// 缺失字段返回 false 不匹配 — 过滤语义:条件不适用即剔除)。
func entryFieldValue(e LogEntry, field string) (string, bool) {
	switch v := e.(type) {
	case *CDNLogEntry:
		switch field {
		case "client_ip":
			return v.ClientIP, true
		case "method":
			return v.Method, true
		case "url":
			return v.URL, true
		case "host":
			return v.Host, true
		case "status":
			return strconv.FormatInt(int64(v.Status), 10), true
		case "bytes_sent":
			return strconv.FormatInt(v.BytesSent, 10), true
		case "cache_hit":
			return v.CacheHit, true
		case "latency_ms":
			return strconv.FormatInt(v.LatencyMs, 10), true
		case "referer":
			return v.Referer, true
		case "user_agent":
			return v.UserAgent, true
		case "edge_node":
			return v.EdgeNode, true
		case "request_id":
			return v.RequestID, true
		}
	case *WAFLogEntry:
		switch field {
		case "client_ip":
			return v.ClientIP, true
		case "host":
			return v.Host, true
		case "uri":
			return v.URI, true
		case "method":
			return v.Method, true
		case "rule_name":
			return v.RuleName, true
		case "rule_id":
			return v.RuleID, true
		case "action":
			return v.Action, true
		case "severity":
			return v.Severity, true
		case "status":
			return strconv.FormatInt(int64(v.Status), 10), true
		case "user_agent":
			return v.UserAgent, true
		case "geo":
			return v.Geo, true
		}
	case *SLBLogEntry:
		switch field {
		case "client_ip":
			return v.ClientIP, true
		case "method":
			return v.Method, true
		case "url":
			return v.URL, true
		case "host":
			return v.Host, true
		case "status":
			return strconv.FormatInt(int64(v.Status), 10), true
		case "target_ip":
			return v.TargetIP, true
		case "latency_ms":
			return strconv.FormatInt(v.LatencyMs, 10), true
		case "upstream_latency_ms":
			return strconv.FormatInt(v.UpstreamLatencyMs, 10), true
		case "upstream_status":
			return strconv.FormatInt(int64(v.UpstreamStatus), 10), true
		case "bytes_sent":
			return strconv.FormatInt(v.BytesSent, 10), true
		case "tls_protocol":
			return v.TLSProtocol, true
		}
	}
	return "", false
}