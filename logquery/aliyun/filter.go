// 阿里云字段筛选/分组维度/聚合指标的列映射与检索段编译。
// 归一化字段 → SLS 原始列(mapper.go 同源;只收录单一列、无链式回退的字段,
// 保证 eq/前缀/包含语义可精确下推)。缓存命中/边缘节点等跨字段归一字段
// 不在列映射内 — 明细由 EntryMatches 兜底,聚合下推时该字段(源)显式跳过。
package aliyun

import (
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// dimColumnExpr 归一化维度/筛选项 → SLS 列表达式(key 为 /types 字段字典键)。
// 约束:只能是**单一列**(可作 `col: value` 检索下推,也能 select/group by)。
// 多列拼接(如 DCDN referer)或需运算的字段(latency 秒→ms)不收在此表,
// 见 dimGroupExpr(仅分组维度)。字段语义照 mapper.go 同源(勿漂移)。
var dimColumnExpr = map[mapperKind]map[string]string{
	kindDCDN: {
		"host": "domain", "status": "return_code", "method": "method",
		"client_ip": "client_ip", "uri": "uri",
		// /types 字典键是 url(WAF 才是 uri);缺失时 url 落到透传列 → group by url
		// 对 DCDN/ALB 报"列不存在",聚合"不支持"。补齐别名让 URL 维度/筛选可下推。
		"url":        "uri",
		"bytes_sent": "response_size", "latency_ms": "request_time",
		"user_agent": "user_agent", "edge_node": "via_info", "request_id": "uuid",
	},
	kindCDNOffline: {
		"host":   "regexp_extract(RequestURL, '^(?:https?://)?([^/?]+)', 1)",
		"status": "HTTPStatus", "method": "HTTPMethod", "client_ip": "RemoteIP",
		"url": "RequestURL", "bytes_sent": "ResponseSize", "latency_ms": "RequestTime",
		"user_agent": "UserAgent", "referer": "Referer",
	},
	kindAkamaiCDN: {
		"host": "reqHost", "status": "statusCode", "method": "reqMethod",
		"client_ip": "cliIP", "bytes_sent": "bytes", "latency_ms": "turnAroundTimeMSec",
		"user_agent": "UA", "edge_node": "cp", "request_id": "reqId", "referer": "referer",
	},
	kindWAF3: {
		"host": "host", "status": "status", "method": "request_method",
		// WAF3 索引属性用的是 request_path(request_uri 未建分析与筛选键:
		// 曾致聚合/sources 筛选下推整源失败)
		"uri": "request_path", "client_ip": "real_client_ip",
		"user_agent": "http_user_agent", "geo": "region",
	},
	kindAkamaiWAF: {
		"host": "dhost", "rule_name": "name", "rule_id": "cs1",
	},
	kindALB: {
		"host": "http_host", "status": "status", "method": "request_method",
		"uri": "request_uri", "client_ip": "client_ip", "upstream_status": "upstream_status",
		"url": "request_uri", "bytes_sent": "body_bytes_sent", "tls_protocol": "ssl_protocol",
	},
}

// dimGroupExpr 仅分组维度表达式(不可作检索下推列,聚合 TopN 专用):
// 字段语义对齐 mapper 的量纲换算(ALB request_time 为秒 → ×1000 得 ms,
// 与 secondsToMs 同源)。收录归一化后无法单列下推的字段(如 CDN cache_hit
// 需从 hit_info 归一)——分组直接按原始列,筛选取值仍走明细逐条归一。
var dimGroupExpr = map[mapperKind]map[string]string{
	kindALB: {
		"latency_ms":          "request_time * 1000",
		"upstream_latency_ms": "upstream_response_time * 1000",
	},
	kindDCDN: {
		"cache_hit": "hit_info",
		// uri_host 组合键:域名|路径(供 URI 未命中归属域名;单维度聚合内把
		// 域名与路径拼成一个分组键,避免 host×uri 二维交叉。域名不含 "|",引擎
		// 按首个 "|" 拆分即无损还原 host/path)。
		"uri_host": "concat(domain, '|', uri)",
	},
	kindAkamaiCDN: {
		"cache_hit": "cacheStatus",
	},
	kindCDNOffline: {
		"cache_hit": "HitInfo",
		// 离线转存 RequestURL 本含 scheme://host/path,URI 归属域名可直接从
		// 完整 URL 解析(与 url→RequestURL 同列,uri_host 仅作统一入口)。
		"uri_host": "RequestURL",
	},
	kindAkamaiWAF: {
		"action": "act", "severity": "severity",
	},
}

// internalDim 内部组合维度(仅编排层 URI 归属下钻使用,不暴露到 /types 可聚合
// 字段字典,避免用户下拉出现无中文标签的中间组合键)。
var internalDim = map[string]bool{"uri_host": true}

// metricExpr 聚合指标按 kind 编译(空=该 kind 不支持该指标)。
// 单位对齐 mapper:ALB request_time 为秒(secondsToMs 换算),×1000 补回 ms。
// nonhit_count(CDN 缓存分析,任务 2):未命中请求计数 —— 命中归类口径的
// 未命中 = miss+error,SQL 侧按原始列关键字过滤(miss:含 MISS;error:含
// ERROR),与明细层 NormalizeCacheHit 同判;DCDN hit_info 为复合值
// ("-,WS|CHARGE|NOTLAST"),regexp_extract 取首段('|' 与 ',' 前缀)镜像
// mapper 的 firstSegment(firstSegment(hit_info,"|"),",") 预处理,归一仍由
// 消费侧共用 NormalizeCacheHit 完成,不另起归一路径。
var metricExpr = map[mapperKind]map[string]string{
	kindDCDN: {
		"count":        "count(1)",
		"sum_bytes":    "sum(response_size)",
		"avg_latency":  "avg(request_time)",
		"p99_latency":  "approx_percentile(request_time, 0.99)",
		"nonhit_count": "sum(case when regexp_extract(hit_info, '^[^|,]+') like '%MISS%' or regexp_extract(hit_info, '^[^|,]+') like '%ERROR%' then 1 else 0 end)",
		"nonhit_bytes": "sum(case when regexp_extract(hit_info, '^[^|,]+') like '%MISS%' or regexp_extract(hit_info, '^[^|,]+') like '%ERROR%' then response_size else 0 end)",
	},
	kindCDNOffline: {
		"count":        "count(1)",
		"sum_bytes":    "sum(ResponseSize)",
		"avg_latency":  "avg(RequestTime)",
		"p99_latency":  "approx_percentile(RequestTime, 0.99)",
		"nonhit_count": "sum(case when HitInfo like '%MISS%' or HitInfo like '%ERROR%' then 1 else 0 end)",
		"nonhit_bytes": "sum(case when HitInfo like '%MISS%' or HitInfo like '%ERROR%' then ResponseSize else 0 end)",
	},
	kindAkamaiCDN: {
		"count":        "count(1)",
		"sum_bytes":    "sum(bytes)",
		"avg_latency":  "avg(turnAroundTimeMSec)",
		"p99_latency":  "approx_percentile(turnAroundTimeMSec, 0.99)",
		"nonhit_count": "sum(case when cacheStatus like '%MISS%' or cacheStatus like '%ERROR%' then 1 else 0 end)",
		"nonhit_bytes": "sum(case when cacheStatus like '%MISS%' or cacheStatus like '%ERROR%' then bytes else 0 end)",
	},
	kindALB: {
		"count":       "count(1)",
		"sum_bytes":   "sum(body_bytes_sent)",
		"avg_latency": "avg(request_time) * 1000",
		"p99_latency": "approx_percentile(request_time, 0.99) * 1000",
	},
	// kindWAF3/kindAkamaiWAF:无 latency/bytes 指标列,仅 count
	kindWAF3:      {"count": "count(1)"},
	kindAkamaiWAF: {"count": "count(1)"},
}

// filterSearchPart 编译字段筛选为 SLS 检索段(多条件 AND;返回 false =
// 存在该 kind 无法下推的字段,调用方显式标注跳过该源)。
// 语法对齐 SLS 键检索:数字字面量不引号,其余短语引号;contain 用通配 *v*,
// prefix 用 v*,neq 用 not。
func filterSearchPart(kind mapperKind, filters []logquery.FieldFilter) (string, bool) {
	cols := dimColumnExpr[kind]
	var parts []string
	for _, f := range filters {
		col, ok := cols[f.Field]
		if !ok || f.Value == "" {
			return "", false
		}
		// eq 数字字面量不引号(数值检索更稳);其余统一短语引号(规避
		// SLS 词法空格/冒号等),末端追加 SLS 通配符(*val* / val*)。
		val := f.Value
		if f.Op == "contains" {
			val = "*" + val + "*"
		} else if f.Op == "prefix" {
			val = val + "*"
		}
		if !numericLiteral(val) || strings.ContainsAny(val, "*") {
			val = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(val) + `"`
		}
		switch f.Op {
		case "neq":
			parts = append(parts, "not "+col+": "+val)
		default: // eq / contains / prefix
			parts = append(parts, col+": "+val)
		}
	}
	if len(parts) == 0 {
		return "", true
	}
	return "(" + strings.Join(parts, " and ") + ")", true
}

// numericLiteral 数字字面量(含小数/负号)不加引号,SLS 数值检索更稳。
func numericLiteral(s string) bool {
	if s == "" {
		return false
	}
	dot := false
	for i, c := range s {
		switch {
		case c >= '0' && c <= '9':
		case c == '.' && i > 0 && !dot:
			dot = true
		case c == '-' && i == 0:
		default:
			return false
		}
	}
	return true
}

// dimensionExpr 分组维度编译。四级:
//  1. 归一化字段优先(dimColumnExpr,如 waf3 client_ip → real_client_ip);
//  2. 维度专用表达式(dimGroupExpr,如 ALB latency_ms → request_time*1000);
//  3. 否则合法标识符**原样透传**为 SLS 列(group by 任意原始字段,
//     例:real_client_ip / user_agent —— 用户可直接按云上原始列聚合);
//  4. 含非法字符(空格/分号/管道等,防 SQL 注入)返回 false → 显式跳过。
//
// SLS 分析 SQL 对索引字段可直接 select+group by;列名不存在时查询报错,
// 由调用方转为 TopNSkipReason 可见提示,不静默。
func dimensionExpr(kind mapperKind, dim string) (string, bool) {
	if dim == "" {
		return "", true
	}
	if expr, ok := dimColumnExpr[kind][dim]; ok {
		return expr, true
	}
	if expr, ok := dimGroupExpr[kind][dim]; ok {
		return expr, true
	}
	if identifierLike(dim) {
		return dim, true
	}
	return "", false
}

// briefErr 截取错误摘要(防长错误串进 TopNSkipReason 撑爆 UI)。
func briefErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}

// identifierLike 合法 SLS/SQL 标识符(字母数字下划线点;不以数字开头,
// 不落任何分隔符/引号)。
func identifierLike(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		switch {
		case c == '_' || c == '.':
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// metricSQLExpr 聚合指标编译;不支持的 (kind, metric) 返回 false。
func metricSQLExpr(kind mapperKind, metric string) (string, bool) {
	if metric == "" {
		metric = "count"
	}
	expr, ok := metricExpr[kind][metric]
	return expr, ok
}

// metricIsCount 指标是否为计数(count 语义)。
func metricIsCount(metric string) bool { return metric == "" || metric == "count" }

// metricIsWeighted avg/p99 等非可加指标:跨源归并需按 count 加权均值;
// count/sum_bytes/nonhit_count 可加直接求和。
func metricIsWeighted(metric string) bool {
	switch metric {
	case "", "count", "sum_bytes", "nonhit_count", "nonhit_bytes":
		return false
	default:
		return true
	}
}
