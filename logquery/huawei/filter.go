// 华为 LTS 字段筛选/聚合维度/指标能力(诚实标注式最小集)。
// LTS 分析 SQL 能力有限(内测标注),且非结构化 WAF 流 content 列不可解析
// (field-mapping.md 实测)——字段筛选只对结构化 ELB 流 + host 列下推。
// 其余字段/维度/指标:明细由 EntryMatches 兜底;聚合显式标注跳过
// (TopNSkipReason / SkipFilterReason),不给静默错数。
package huawei

import (
	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// filterWhere 编译字段筛选为 LTS 检索段前缀(仅 ELB host 支持下推)。
// 返回 检索段(空=无下推),ok=false=字段不可下推(调用方跳过该源)。
func filterWhere(kind mapperKind, filters []logquery.FieldFilter) (string, bool) {
	if len(filters) == 0 {
		return "", true
	}
	if kind != kindELB {
		return "", false
	}
	// LTS 检索语法 key:value(host 为 ELB 分析列,实测可检索);只接受 host eq。
	var parts []string
	for _, f := range filters {
		if f.Field != "host" || (f.Op != "" && f.Op != "eq") || f.Value == "" {
			return "", false
		}
		parts = append(parts, `host: "`+f.Value+`"`)
	}
	return joinSearchParts(parts), true
}

// dimensionExpr 聚合分组维度(仅 ELB host;其余返回 false → TopNSkipReason)。
func dimensionExpr(kind mapperKind, dim string) (string, bool) {
	if dim == "" {
		return "", true // 缺省回退 kind 默认维度(ELB host)
	}
	if kind == kindELB && dim == "host" {
		return "host", true
	}
	return "", false
}

// metricSQLExpr 聚合指标(仅 count;avg/分位/字节 LTS 未验证,诚实拒绝)。
func metricSQLExpr(kind mapperKind, metric string) (string, bool) {
	if metric == "" || metric == "count" {
		return "count(1)", true
	}
	return "", false
}

// joinSearchParts 拼接检索段(全部非空,原样串联)。
func joinSearchParts(parts []string) string {
	out := ""
	for _, p := range parts {
		if out == "" {
			out = p
		} else {
			out += " and " + p
		}
	}
	return out
}