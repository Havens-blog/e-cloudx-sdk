// 聚合 SQL 构造(plan.md §8):SLS 检索|SQL 管道下推,窗口内真实统计。
// 纯函数,单测覆盖检索段拼接 / 分桶 / TopN / kind 字段映射。
package aliyun

import (
	"fmt"
	"strings"
)

// aggregateTopNExpr 按 kind 返回统计 TopN 分组表达式(空=该源无此维度,
// 跳过 TopN)。与前端统计图对齐:CDN=域名,WAF=规则名,SLB=host。
// 注意与 activeDomainExpr(host 维度,源选择粒度)是两个语义,勿混用。
func aggregateTopNExpr(kind mapperKind) string {
	switch kind {
	case kindDCDN:
		return "domain"
	case kindAkamaiCDN:
		return "reqHost"
	case kindCDNOffline:
		// PascalCase 转存无域名独立字段,从完整 URL 提取 host
		return "regexp_extract(RequestURL, '^(?:https?://)?([^/?]+)', 1)"
	case kindAkamaiWAF:
		return "name" // CEF 规则名字段(映射层的 rule_name)
	case kindALB:
		return "http_host"
	default:
		return "" // kindWAF3 访问流无规则维度
	}
}

// activeDomainExpr 按 kind 返回源选择粒度的 host 维度表达式(空=该源不是
// 混装 host 形态)。统计 TopN 的维度(如 Akamai WAF 的规则名 name)与源
// 选择的 host 维度(dhost)是两个语义——曾混用导致 Akamai WAF 域名枚举
// 按"规则名"分组,66 个 host 只出 2 个规则名。
func activeDomainExpr(kind mapperKind) string {
	switch kind {
	case kindDCDN:
		return "domain"
	case kindAkamaiCDN:
		return "reqHost"
	case kindCDNOffline:
		return "regexp_extract(RequestURL, '^(?:https?://)?([^/?]+)', 1)"
	case kindWAF3:
		return "host"
	case kindAkamaiWAF:
		return "dhost"
	default:
		return "" // kindALB 实例流单资源,非混装
	}
}

// buildAggregateSearchPart 管道前的检索段:kind 专属 __topic__ 过滤(与
// Search 的 buildQuery 同源,防聚合 project 噪声流)+ 用户检索式 + 混装源
// 域名过滤(Resources 与 Search 扇出同语义 = 域名清单;资源等于本源
// logstore/project 时视为整源查询,不加域名过滤)。
func buildAggregateSearchPart(kind mapperKind, userQuery string, resources []string, selfNames ...string) string {
	var parts []string
	if t := topicFilter(kind); t != "" {
		parts = append(parts, t)
	}
	if q := strings.TrimSpace(userQuery); q != "" && q != "*" {
		parts = append(parts, "("+q+")")
	}
	if field := domainField(kind); field != "" && len(resources) > 0 {
		self := make(map[string]bool, len(selfNames))
		for _, n := range selfNames {
			if n != "" {
				self[n] = true
			}
		}
		wholeSource := false
		var terms []string
		for _, r := range resources {
			if self[r] {
				wholeSource = true
			}
			if r != "" && !self[r] {
				terms = append(terms, field+": "+r)
			}
		}
		if !wholeSource && len(terms) > 0 {
			parts = append(parts, "("+strings.Join(terms, " or ")+")")
		}
	}
	return strings.Join(parts, " and ")
}

// appendSearchPart 拼接两段检索式(空段跳过;无段时为全量)。
func appendSearchPart(a, b string) string {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	switch {
	case a == "" && b == "":
		return ""
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + " and " + b
	}
}

// buildAggregateBucketSQL 分桶 SQL:t = 桶起点(Unix 秒),c = 桶内精确条数。
// Total 由分桶求和得出,不再单独跑 count(省一次全窗扫描)。
func buildAggregateBucketSQL(searchPart string, bucketSec int64) string {
	if searchPart == "" {
		searchPart = "*"
	}
	return fmt.Sprintf("%s | select __time__ - __time__ %% %d as t, count(1) as c group by t order by t limit 200",
		searchPart, bucketSec)
}

// buildAggregateTopNSQL TopN SQL:k = 分组维度表达式,n = 组内条数,
// v = 指标值(计数时 = n)。按 v 降序取前 limit(计数/字节/均值/分位同秩)。
func buildAggregateTopNSQL(searchPart, dimExpr, metric string, limit int) string {
	if searchPart == "" {
		searchPart = "*"
	}
	return fmt.Sprintf("%s | select %s as k, count(1) as n, %s as v group by k order by v desc limit %d",
		searchPart, dimExpr, metric, limit)
}
