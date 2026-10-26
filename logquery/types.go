// Package logquery 多云统一日志查询共享域(Phase 1,plan.md §4.1/§4.2)。
//
// 设计要点(ADR D1~D6):
//   - 采集适配与字段归一分离:Provider 只负责"把原始日志拿来",映射层负责
//     "变成统一模型",互不感知;
//   - 按日志类型分域建模:CDN/WAF/SLB 三个独立 schema,不做大一统大宽表;
//   - Raw 字段全量保留:字段映射永远滞后于云厂商变更,Raw 是兜底,
//     详情抽屉零信息丢失;
//   - 时间统一 Unix 毫秒 UTC;枚举小写归一;缺失容忍(UI 显 -)。
package logquery

import (
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
)

// LogType 日志类型(四域分治,ADR D2)。
type LogType string

const (
	// LogTypeCDN CDN 访问日志。
	LogTypeCDN LogType = "cdn"
	// LogTypeWAF WAF 攻击/拦截/访问日志。
	LogTypeWAF LogType = "waf"
	// LogTypeSLB 负载均衡访问日志(ALB/ELB 产品日志)。
	LogTypeSLB LogType = "slb"
	// LogTypeAccess 源站访问日志(自采 nginx/gateway 实例访问日志,非负载均衡
	// 产品日志;火山类映射复用 SLBLogEntry schema)。
	LogTypeAccess LogType = "access"
	// LogTypeApp 应用业务日志(自采 java 服务 logback/log4j 业务流,SLS
	// logstore 命名约定 <service>-business,每 store 单服务)。字段
	// service/level/message/timestamp 为 RCA 服务间消费的稳定契约
	// (OpsAgent 经 e-cam 联邦取错误日志做根因分析)。
	LogTypeApp LogType = "app"
)

// AllLogTypes 全部日志类型(注册/校验用)。
var AllLogTypes = []LogType{LogTypeCDN, LogTypeWAF, LogTypeSLB, LogTypeAccess, LogTypeApp}

// IsValidLogType 校验日志类型合法值。
func IsValidLogType(t LogType) bool {
	return slices.Contains(AllLogTypes, t)
}

// LogMeta 所有日志类型共有元数据。
type LogMeta struct {
	Cloud       domain.CloudProvider `json:"cloud"`        // 云厂商
	AccountID   string               `json:"account_id"`   // 平台内云账号 ID
	AccountName string               `json:"account_name"` // 展示用
	Region      string               `json:"region"`       // 日志源区域
	ResourceID  string               `json:"resource_id"`  // 域名 / LB 实例 ID / 分发 ID
	Source      string               `json:"source"`       // 源标识(SLS project/logstore 等),详情追溯
}

// LogEntry 统一日志条目接口:联邦归并排序(按时间戳)与 JSON 序列化的
// 最小契约;具体字段由三域 schema 各自定义。
type LogEntry interface {
	// GetTimestamp 事件时间(Unix 毫秒 UTC,已归一)。
	GetTimestamp() int64
	// GetMeta 日志元数据。
	GetMeta() LogMeta
}

// CDNLogEntry CDN 访问日志统一模型。
type CDNLogEntry struct {
	Meta      LogMeta        `json:"meta"`
	Timestamp int64          `json:"timestamp"` // Unix ms UTC
	ClientIP  string         `json:"client_ip"`
	Method    string         `json:"method"`
	URL       string         `json:"url"`
	Host      string         `json:"host"`
	Status    int            `json:"status"`
	BytesSent int64          `json:"bytes_sent"`
	CacheHit  string         `json:"cache_hit"` // 归一枚举: hit / miss / partial / error / "-"
	LatencyMs int64          `json:"latency_ms"`
	Referer   string         `json:"referer"`
	UserAgent string         `json:"user_agent"`
	EdgeNode  string         `json:"edge_node"` // POP / 边缘节点
	RequestID string         `json:"request_id"`
	Raw       map[string]any `json:"raw"`
}

// GetTimestamp 实现 LogEntry。
func (e *CDNLogEntry) GetTimestamp() int64 { return e.Timestamp }

// GetMeta 实现 LogEntry。
func (e *CDNLogEntry) GetMeta() LogMeta { return e.Meta }

// WAFLogEntry WAF 攻击/拦截/访问日志统一模型。
type WAFLogEntry struct {
	Meta      LogMeta        `json:"meta"`
	Timestamp int64          `json:"timestamp"` // Unix ms UTC
	ClientIP  string         `json:"client_ip"`
	Host      string         `json:"host"`
	URI       string         `json:"uri"`
	Method    string         `json:"method"`
	RuleID    string         `json:"rule_id"`
	RuleName  string         `json:"rule_name"`
	Action    string         `json:"action"`   // 归一枚举: block / alert / allow / pass / "-"
	Severity  string         `json:"severity"` // 归一: low / medium / high / "-"
	Status    int            `json:"status"`
	UserAgent string         `json:"user_agent"`
	Geo       string         `json:"geo"` // 国家/地区码
	Raw       map[string]any `json:"raw"`
}

// GetTimestamp 实现 LogEntry。
func (e *WAFLogEntry) GetTimestamp() int64 { return e.Timestamp }

// GetMeta 实现 LogEntry。
func (e *WAFLogEntry) GetMeta() LogMeta { return e.Meta }

// SLBLogEntry 负载均衡访问日志统一模型。
type SLBLogEntry struct {
	Meta              LogMeta        `json:"meta"`
	Timestamp         int64          `json:"timestamp"` // Unix ms UTC
	ClientIP          string         `json:"client_ip"`
	ClientPort        int            `json:"client_port"`
	TargetIP          string         `json:"target_ip"` // 后端(真实)服务器
	TargetPort        int            `json:"target_port"`
	ListenerPort      int            `json:"listener_port"`
	Protocol          string         `json:"protocol"`
	Method            string         `json:"method"`
	URL               string         `json:"url"`
	Host              string         `json:"host"`
	Status            int            `json:"status"`
	RequestLength     int64          `json:"request_length"`
	BytesSent         int64          `json:"bytes_sent"`
	LatencyMs         int64          `json:"latency_ms"`
	UpstreamLatencyMs int64          `json:"upstream_latency_ms"`
	UpstreamStatus    int            `json:"upstream_status"`
	TLSProtocol       string         `json:"tls_protocol"`
	TLSCipher         string         `json:"tls_cipher"`
	RequestID         string         `json:"request_id"`
	UserAgent         string         `json:"user_agent"`
	Raw               map[string]any `json:"raw"`
}

// GetTimestamp 实现 LogEntry。
func (e *SLBLogEntry) GetTimestamp() int64 { return e.Timestamp }

// GetMeta 实现 LogEntry。
func (e *SLBLogEntry) GetMeta() LogMeta { return e.Meta }

// AppLogEntry 应用业务日志统一模型(自采 java 业务流,<service>-business
// store 单服务一 store;service/level/message/timestamp 为 RCA 跨服务
// 消费契约,缺失如实留空不编造)。
type AppLogEntry struct {
	Meta      LogMeta        `json:"meta"`
	Timestamp int64          `json:"timestamp"` // Unix ms UTC
	Service   string         `json:"service"`   // 服务名(document_type 去 -business 后缀)
	Level     string         `json:"level"`     // 归一小写: error / warn / info / debug / ""
	Message   string         `json:"message"`   // 日志正文(msg 列;空回退原始行 message 列)
	Exception string         `json:"exception"` // 异常堆栈(ERROR 行常见;可为空)
	TraceID   string         `json:"trace_id"`  // 链路 ID(业务埋点,可为空)
	Raw       map[string]any `json:"raw"`       // pod/线程/location 等全量原始字段
}

// GetTimestamp 实现 LogEntry。
func (e *AppLogEntry) GetTimestamp() int64 { return e.Timestamp }

// GetMeta 实现 LogEntry。
func (e *AppLogEntry) GetMeta() LogMeta { return e.Meta }

// NormalizeLevel 应用日志级别归一(去空白 + 小写;空保持空,不占位编造)。
// 实测取值 INFO/WARN/ERROR/DEBUG(java logback 惯例),归一后供筛选与
// RCA 消费方按统一小写语义比较。
func NormalizeLevel(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ---------------------------------------------------------------------
// 归一化工具(映射层共用;单测覆盖,ADR D5/D6)
// ---------------------------------------------------------------------

// ParseTimeMs 多格式时间归一为 Unix 毫秒(ADR D5 硬规则)。
// 支持入参形态(Phase 0 实测全量覆盖):
//   - RFC3339/ISO8601 带时区:"2026-08-20T20:55:57+08:00"
//   - ISO8601 Z 尾:"2026-08-27T09:56:02.000Z"
//   - 纯数字字符串/数值:按量级自适应 sec/ms/µs/ns(如 Akamai _unixtimestamp_ 为纳秒)
//   - "秒.小数" 形态:"1787824562.04"(华为 ELB 首列)
//   - nginx 访问日志格式:"3/Sep/2026:11:23:01 +0800"(CDN 离线转存 Time)
//
// 解析失败返回 0(缺失容忍:字段可空,UI 显 -)。
func ParseTimeMs(v any) int64 {
	switch t := v.(type) {
	case int64:
		return scaleUnixToMs(t)
	case int:
		return scaleUnixToMs(int64(t))
	case json.Number: // 火山 TLS 等 SDK 用 UseNumber() 反序列化,数字以 json.Number 形态出现
		return ParseTimeMs(t.String())
	case float64:
		sec := int64(t)
		// 浮点尾数取整用四舍五入:0.800*1000 浮点表示为 799.99…,截断会丢 1ms
		frac := int64(math.Round((t - float64(sec)) * 1000))
		return scaleUnixToMs(sec) + frac
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0
		}
		// 纯数字(含小数点):量级自适应
		if isNumeric(s) {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return ParseTimeMs(f)
			}
			return 0
		}
		for _, layout := range []string{
			time.RFC3339Nano, time.RFC3339,
			"2006-01-02T15:04:05.000Z", "2006-01-02T15:04:05Z",
			"2/Jan/2006:15:04:05 -0700", // nginx 访问日志格式(CDN 离线转存 Time)
		} {
			if ts, err := time.Parse(layout, s); err == nil {
				return ts.UnixMilli()
			}
		}
		return 0
	default:
		return 0
	}
}

// scaleUnixToMs 按量级把 Unix 时间戳归一到毫秒。
func scaleUnixToMs(v int64) int64 {
	switch {
	case v <= 0:
		return 0
	case v < 1e11: // 秒(~2001~5138 年)
		return v * 1000
	case v < 1e14: // 毫秒
		return v
	case v < 1e17: // 微秒
		return v / 1e3
	default: // 纳秒
		return v / 1e6
	}
}

// isNumeric 判断字符串是否为纯数字(允许一个小数点)。
func isNumeric(s string) bool {
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
	return len(s) > 0
}

// NormalizeAction WAF 动作归一(小写收敛;各云 BLOCK/intercept/ban 等同义)。
func NormalizeAction(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	switch v {
	case "intercept", "ban", "deny", "blocked", "reject", "challenge":
		return "block"
	case "observe", "log":
		return "alert"
	case "ok", "default_action", "default":
		return "allow"
	default:
		return v // block/alert/allow/pass 及空串直接小写返回
	}
}

// NormalizeCacheHit CDN 缓存命中归一(TCP_HIT/HIT/0 等;语义不足返回 "-")。
func NormalizeCacheHit(s string) string {
	v := strings.TrimSpace(s)
	if v == "" || v == "-" {
		return "-"
	}
	upper := strings.ToUpper(v)
	switch {
	case strings.Contains(upper, "HIT") && !strings.Contains(upper, "MISS"),
		v == "0": // Akamai 码表:0=hit(fixture 实测样本)
		return "hit"
	case strings.Contains(upper, "MISS"), strings.Contains(upper, "TCP_MISS"):
		return "miss"
	case strings.Contains(upper, "PARTIAL"), strings.Contains(upper, "REFRESH"):
		return "partial"
	case strings.Contains(upper, "ERROR"):
		return "error"
	default:
		return strings.ToLower(v)
	}
}

// NormalizeSeverity WAF 严重度归一(数字/文本统一 low/medium/high)。
func NormalizeSeverity(v any) string {
	s := ""
	switch t := v.(type) {
	case string:
		s = strings.TrimSpace(t)
	case int, int64, float64:
		s = strconv.FormatInt(toInt64(v), 10)
	default:
		return "-"
	}
	switch strings.ToLower(s) {
	case "1", "2", "low", "info", "informational":
		return "low"
	case "3", "4", "medium", "warn", "warning":
		return "medium"
	case "5", "6", "7", "high", "crit", "critical", "emerg", "alert":
		return "high"
	case "8", "9", "10", "veryhigh", "extreme":
		return "high"
	default:
		return "-"
	}
}

// toInt64 宽松数值转换(string/int/float -> int64;失败 0)。
func toInt64(v any) int64 {
	switch t := v.(type) {
	case int:
		return int64(t)
	case int32:
		return int64(t)
	case int64:
		return t
	case float32:
		return int64(t)
	case float64:
		return int64(t)
	case json.Number: // UseNumber() 反序列化出的数字(见 ParseTimeMs 注释)
		return toInt64(t.String())
	case string:
		// "0.027" / "1,234" / "403" 等字符串数值
		s := strings.TrimSpace(t)
		if i := strings.IndexByte(s, ','); i > 0 { // "3.097, 3.072" 取首段
			s = strings.TrimSpace(s[:i])
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int64(f)
		}
		return 0
	default:
		return 0
	}
}

// Str 宽松取字符串(数值/nil -> "";供映射层从 map[string]any 取值)。
// json.Number 为 UseNumber() 反序列化出的数字字符串,直接还原本体。
func Str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case nil:
		return ""
	default:
		return ""
	}
}

// Int 宽松取整(见 toInt64;缺失/非法返回 0,由调用方决定是否展示)。
func Int(v any) int64 { return toInt64(v) }

// DashIfEmpty 空值显示占位(UI 显 -;映射层统一在序列化前不处理,
// 此函数供字段字典/前端对齐语义用)。
func DashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
