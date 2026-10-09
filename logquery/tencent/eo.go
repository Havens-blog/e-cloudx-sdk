// EdgeOne 访问日志映射:统一字段 → EO 原始列(实测 2026-09-17,eo-log topic)。
// 字段与 edgeone 推送的访问日志 schema 对齐(ClientIP/RequestHost/
// EdgeResponseStatusCode/EdgeResponseBytes/EdgeCacheStatus 等)。
package tencent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// eoFieldMap 统一字段 → EdgeOne 原始列(检索/聚合可下推;语义照 eoLog 映射)。
var eoFieldMap = map[string]string{
	"host":       "RequestHost",
	"status":     "EdgeResponseStatusCode",
	"method":     "RequestMethod",
	"client_ip":  "ClientIP",
	"url":        "RequestUrl",
	"uri":        "RequestUrl",
	"bytes_sent": "EdgeResponseBytes",
	"cache_hit":  "EdgeCacheStatus",
	"latency_ms": "EdgeResponseTime",
	"referer":    "RequestReferer",
	"user_agent": "RequestUA",
	"request_id": "RequestID",
	"edge_node":  "EdgeServerIP",
	"geo":        "ClientRegion",
}

// eoMetricExpr 聚合指标(CLS 分析 SQL;实测 count/sum/avg/approx_percentile 可用)。
// nonhit_count(CDN 缓存分析,任务 2):未命中请求计数 —— 命中归类口径的
// 未命中 = miss+error,SQL 侧按 EdgeCacheStatus 关键字过滤(含 MISS/ERROR),
// 与明细层 NormalizeCacheHit 同判(单一映射源),不另起归一路径。
var eoMetricExpr = map[string]string{
	"count":        "count(*)",
	"sum_bytes":    "sum(EdgeResponseBytes)",
	"avg_latency":  "avg(EdgeResponseTime)",
	"p99_latency":  "approx_percentile(EdgeResponseTime, 0.99)",
	"nonhit_count": "sum(case when EdgeCacheStatus like '%MISS%' or EdgeCacheStatus like '%ERROR%' then 1 else 0 end)",
	"nonhit_bytes": "sum(case when EdgeCacheStatus like '%MISS%' or EdgeCacheStatus like '%ERROR%' then EdgeResponseBytes else 0 end)",
}

// eoDimensionExpr 分组维度编译:统一字段映射优先,否则合法标识符原样透传
// (EdgeOne 原始字段,如 SecurityModule / ClientRegion);非法字符拒绝(防注入)。
func eoDimensionExpr(dim string) (string, bool) {
	if dim == "" {
		return "", true
	}
	if col, ok := eoFieldMap[dim]; ok {
		return col, true
	}
	if identifierLike(dim) {
		return dim, true
	}
	return "", false
}

// eoLogJsonMap 解析 CLS LogJson(JSON 字符串)为 map[string]string(值统一字符化)。
func eoLogJsonMap(logJson string) map[string]string {
	if logJson == "" {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(logJson), &raw); err != nil {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[k] = fmt.Sprint(v)
	}
	return out
}

// eoLog 一条 EdgeOne 日志 → 统一 CDN 模型(Raw 全量保留,ADR D3)。
// 时间戳取 __TIMESTAMP__(ms 字符串);字段缺失/占位("-"/"-1"/"null")容忍为空。
func eoLog(m logquery.LogMeta, raw map[string]string) *logquery.CDNLogEntry {
	ts := logquery.Int(raw["__TIMESTAMP__"])
	if ts <= 0 && raw["Time"] != "" { // 兜底 LogInfo.Time(ms)
		ts = logquery.Int(raw["Time"])
	}
	if ts <= 0 {
		return nil // 无时间戳的日志不展示
	}
	url := raw["RequestUrl"]
	if qs := raw["RequestUrlQueryString"]; qs != "" && !strings.Contains(url, "?") {
		url += "?" + qs
	}
	host := raw["RequestHost"]
	if host != "" && host != "-" {
		m.ResourceID = host // 混装 topic 的选择粒度收敛到域名(与 DCDN 一致)
	} else {
		host = ""
	}
	e := &logquery.CDNLogEntry{
		Meta:      m,
		Timestamp: ts,
		ClientIP:  raw["ClientIP"],
		Method:    raw["RequestMethod"],
		Host:      host,
		URL:       url,
		Status:    int(logquery.Int(raw["EdgeResponseStatusCode"])),
		BytesSent: logquery.Int(raw["EdgeResponseBytes"]),
		// EdgeResponseTime 已是 ms(与边缘日志量纲一致,勿再换算)
		LatencyMs: logquery.Int(raw["EdgeResponseTime"]),
		Referer:   raw["RequestReferer"],
		UserAgent: raw["RequestUA"],
		EdgeNode:  raw["EdgeServerIP"],
		RequestID: raw["RequestID"],
		Raw:       toRawMap(raw),
	}
	e.CacheHit = logquery.NormalizeCacheHit(raw["EdgeCacheStatus"])
	return e
}

// toRawMap string map → any map(与 aliyun mapper 的 Raw 形态一致)。
func toRawMap(raw map[string]string) map[string]any {
	r := make(map[string]any, len(raw))
	for k, v := range raw {
		r[k] = v
	}
	return r
}

// identifierLike 合法 SQL 标识符(字母数字下划线;不以数字开头,防注入)。
func identifierLike(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		switch {
		case c == '_':
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
