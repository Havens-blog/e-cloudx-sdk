// app.go 腾讯云 CLS 应用业务日志(LogTypeApp)。
//
// 与 EdgeOne/WAF 不同,CLS 里的应用业务日志由各业务自建投递(java
// logback/log4j、K8s 采集等),字段名与层级没有统一 schema:level 可能是
// level/severity/lvl,正文可能是 msg/message/content/log/body。因此映射层按
// key 别名、**大小写不敏感**地尽力取值(CLS 键随投递配置变化),取不到如实
// 留空,绝不编造级别;一条没有任何已知字段的行也保留,Message 回退为原始键值
// 拼接串(不丢数据,ADR D3 Raw 兜底)。
//
// 源枚举:t topic 分类为 "other"(既不是 EdgeOne 访问日志、也不是 WAF 访问
// 日志)的 CLS topic —— 应用日志是 "other" 的默认归属,已知 WAF stub 显式排除。
package tencent

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// appWAFStubTopicID 已知 WAF stub topic(wafSources 里以 enabled=false 列出,
// 见 provider.go)。它不是业务日志来源,应用日志枚举/查询都排除,避免把
// 一个 0 条的 WAF topic 当作应用源展示。
const appWAFStubTopicID = "a0bfd8ed-d7b1-480a-879b-3c143f7302b8"

// appFixedMessageLimit 无已知字段时回退拼接串保留的最大字符数(rune 计)。
const appFixedMessageLimit = 500

// appLevelKeys / appMessageKeys / ... 别名按优先级排列(先命中先取)。
var (
	appLevelKeys     = []string{"level", "severity", "lvl", "log_level", "loglevel"}
	appMessageKeys   = []string{"msg", "message", "content", "log", "body"}
	appExceptionKeys = []string{"exception", "stack", "stacktrace", "throwable", "error", "err"}
	appTraceKeys     = []string{"trace_id", "traceId", "traceid", "tid", "x_trace_id", "trace"}
	appServiceKeys   = []string{"service", "service_name", "app", "application", "app_name", "logger", "module"}
	appTimeKeys      = []string{"__TIMESTAMP__", "timestamp", "time", "@timestamp"}
)

// appSources 应用业务日志源:分类为 "other" 的 CLS topic,排除已知 WAF stub。
func (p *provider) appSources(ctx context.Context) ([]logquery.LogSource, error) {
	sources, err := p.sourcesByKind(ctx, "other", "应用日志",
		"CLS 中既不是 EdgeOne 访问日志也不是 WAF 日志的 topic，按应用业务日志查询（字段按 JSON 尽力映射）")
	if err != nil {
		return nil, err
	}
	return excludeAppStub(sources), nil
}

// excludeAppStub 过滤已知 WAF stub topic(纯函数,便于单测)。
func excludeAppStub(sources []logquery.LogSource) []logquery.LogSource {
	out := make([]logquery.LogSource, 0, len(sources))
	for _, s := range sources {
		if s.ResourceID == appWAFStubTopicID {
			continue
		}
		out = append(out, s)
	}
	return out
}

// appEntry 一条 CLS 应用日志 → 统一 AppLogEntry(别名大小写不敏感;
// 字段缺失如实留空;空 raw → nil)。
func appEntry(m logquery.LogMeta, raw map[string]string) logquery.LogEntry {
	if len(raw) == 0 {
		return nil
	}
	idx := appLowerIndex(raw)
	level := logquery.NormalizeLevel(appGet(idx, appLevelKeys...))
	message := appGet(idx, appMessageKeys...)
	exception := appGet(idx, appExceptionKeys...)
	traceID := appGet(idx, appTraceKeys...)
	service := appGet(idx, appServiceKeys...)
	if service == "" {
		service = appGet(idx, "__topic__") // 投递侧注入的 topic 名(没有则留空)
	}
	ts := appTimestamp(idx)
	// 所有别名都没命中(一条没有任何已知字段的行)时不丢弃:正文回退为原始
	// key=value 拼接串,级别保持空(不编造)。
	if message == "" && level == "" && exception == "" && traceID == "" && service == "" && ts <= 0 {
		message = appRawLine(raw)
	}
	return &logquery.AppLogEntry{
		Meta:      m,
		Timestamp: ts,
		Service:   service,
		Level:     level,
		Message:   message,
		Exception: exception,
		TraceID:   traceID,
		Raw:       toRawMap(raw),
	}
}

// appLowerIndex 建立小写键索引:同一小写键冲突时按原始键排序取首个非空值
// (确定性,不依赖 map 迭代顺序)。
func appLowerIndex(raw map[string]string) map[string]string {
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	idx := make(map[string]string, len(raw))
	for _, k := range keys {
		lk := strings.ToLower(k)
		if _, ok := idx[lk]; ok {
			continue
		}
		if v := strings.TrimSpace(raw[k]); v != "" {
			idx[lk] = raw[k]
		}
	}
	return idx
}

// appGet 按别名优先级取首个非空值(别名已是小写)。
func appGet(idx map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := idx[strings.ToLower(k)]; v != "" {
			return v
		}
	}
	return ""
}

// appTimestamp 时间戳归一为 Unix 毫秒:依次尝试 __TIMESTAMP__(CLS 注入,ms) →
// timestamp/time/@timestamp(纯数字按量级判断,<1e12 视为秒;其余交给
// ParseTimeMs 解析时间串)。
func appTimestamp(idx map[string]string) int64 {
	for _, k := range appTimeKeys {
		if v := idx[strings.ToLower(k)]; v != "" {
			if ms := appTimeValueMs(v); ms > 0 {
				return ms
			}
		}
	}
	return 0
}

// appTimeValueMs 单个时间值 → Unix 毫秒。纯数字(<1e12 视为秒,兼容
// "秒.小数")自行换算;非数字走 logquery.ParseTimeMs(RFC3339/ISO8601 等)。
func appTimeValueMs(v string) int64 {
	s := strings.TrimSpace(v)
	if s == "" {
		return 0
	}
	if numericLike(s) {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || f <= 0 {
			return 0
		}
		if f < 1e12 {
			return int64(math.Round(f * 1000))
		}
		return int64(math.Round(f))
	}
	return logquery.ParseTimeMs(s)
}

// numericLike 纯数字(允许一个小数点;时间值不会有符号/科学计数)。
func numericLike(s string) bool {
	if s == "" {
		return false
	}
	dot := false
	for i, c := range s {
		switch {
		case c >= '0' && c <= '9':
		case c == '.' && i > 0 && !dot:
			dot = true
		default:
			return false
		}
	}
	return true
}

// appRawLine 无已知字段时的兜底正文:按 key 排序拼 "k=v"(截断到 500 字符)。
func appRawLine(raw map[string]string) string {
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(raw[k])
	}
	return truncateRunes(b.String(), appFixedMessageLimit)
}

// truncateRunes 按字符(而非字节)截断,防中文被切半。
func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit])
}
