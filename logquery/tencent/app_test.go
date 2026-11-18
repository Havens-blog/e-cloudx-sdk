package tencent

import (
	"strings"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// TestAppEntry appEntry 表格驱动:别名/大小写/时间量纲/兜底拼接/不丢弃。
func TestAppEntry(t *testing.T) {
	meta := logquery.LogMeta{
		Cloud:       domain.CloudProviderTencent,
		AccountID:   "1001",
		AccountName: "jlc-tencent",
		Region:      "ap-guangzhou",
		ResourceID:  "topic-app-1",
		Source:      "ap-guangzhou/topic-app-1",
	}
	isoMs := time.Date(2026, 11, 18, 20, 0, 0, 0, time.FixedZone("CST", 8*3600)).UnixMilli()

	cases := []struct {
		name    string
		raw     map[string]string
		wantNil bool
		check   func(t *testing.T, e *logquery.AppLogEntry)
	}{
		{
			name: "标准 JSON 全字段",
			raw: map[string]string{
				"__TIMESTAMP__": "1789490755123",
				"level":         "ERROR",
				"msg":           "order failed",
				"trace_id":      "abc123",
				"exception":     "java.lang.NullPointerException: x",
				"service":       "order-service",
				"pod":           "order-1",
			},
			check: func(t *testing.T, e *logquery.AppLogEntry) {
				if e.Timestamp != 1789490755123 {
					t.Errorf("Timestamp = %d, want 1789490755123", e.Timestamp)
				}
				if e.Level != "error" {
					t.Errorf("Level = %q, want error(归一小写)", e.Level)
				}
				if e.Message != "order failed" {
					t.Errorf("Message = %q", e.Message)
				}
				if e.TraceID != "abc123" {
					t.Errorf("TraceID = %q", e.TraceID)
				}
				if e.Exception != "java.lang.NullPointerException: x" {
					t.Errorf("Exception = %q", e.Exception)
				}
				if e.Service != "order-service" {
					t.Errorf("Service = %q", e.Service)
				}
				if got := e.Raw["pod"]; got != "order-1" {
					t.Errorf("Raw[pod] = %v, want order-1(Raw 全量保留)", got)
				}
				if e.Meta != meta {
					t.Errorf("Meta = %+v", e.Meta)
				}
			},
		},
		{
			name: "大小写混用键",
			raw: map[string]string{
				"Level":   "Warn",
				"Msg":     "slow query",
				"TraceId": "t-1",
			},
			check: func(t *testing.T, e *logquery.AppLogEntry) {
				if e.Level != "warn" {
					t.Errorf("Level = %q, want warn", e.Level)
				}
				if e.Message != "slow query" {
					t.Errorf("Message = %q", e.Message)
				}
				if e.TraceID != "t-1" {
					t.Errorf("TraceID = %q, want t-1", e.TraceID)
				}
			},
		},
		{
			name: "只有 Time 秒字符串",
			raw: map[string]string{
				"Time": "1789490755",
			},
			check: func(t *testing.T, e *logquery.AppLogEntry) {
				if e.Timestamp != 1789490755000 {
					t.Errorf("Timestamp = %d, want 1789490755000(秒→毫秒)", e.Timestamp)
				}
				if e.Level != "" {
					t.Errorf("Level = %q, want 空", e.Level)
				}
				// Time 本身是已知时间字段:别名有命中,故不触发未知字段兜底拼接。
				if e.Message != "" {
					t.Errorf("Message = %q, want 空(时间字段命中不算未知行)", e.Message)
				}
			},
		},
		{
			name: "毫秒数字按原值",
			raw: map[string]string{
				"timestamp": "1789490755123",
				"msg":       "x",
			},
			check: func(t *testing.T, e *logquery.AppLogEntry) {
				if e.Timestamp != 1789490755123 {
					t.Errorf("Timestamp = %d, want 原样毫秒", e.Timestamp)
				}
			},
		},
		{
			name: "时间串 ISO8601 走 ParseTimeMs",
			raw: map[string]string{
				"@timestamp": "2026-11-18T20:00:00+08:00",
				"msg":        "x",
			},
			check: func(t *testing.T, e *logquery.AppLogEntry) {
				if e.Timestamp != isoMs {
					t.Errorf("Timestamp = %d, want %d", e.Timestamp, isoMs)
				}
			},
		},
		{
			name: "只有 content 没有 level",
			raw: map[string]string{
				"content":   "hello",
				"timestamp": "1789490755000",
			},
			check: func(t *testing.T, e *logquery.AppLogEntry) {
				if e.Message != "hello" {
					t.Errorf("Message = %q, want hello", e.Message)
				}
				if e.Level != "" {
					t.Errorf("Level = %q, want 空(不得编造级别)", e.Level)
				}
			},
		},
		{
			name: "别名优先级 msg 优先于 message",
			raw: map[string]string{
				"msg":     "parsed body",
				"message": "[raw line] parsed body",
			},
			check: func(t *testing.T, e *logquery.AppLogEntry) {
				if e.Message != "parsed body" {
					t.Errorf("Message = %q, want msg 列优先", e.Message)
				}
			},
		},
		{
			name: "exception 别名 stack",
			raw: map[string]string{
				"msg":   "boom",
				"stack": "at com.example.Main(Main.java:1)",
			},
			check: func(t *testing.T, e *logquery.AppLogEntry) {
				if e.Exception != "at com.example.Main(Main.java:1)" {
					t.Errorf("Exception = %q", e.Exception)
				}
			},
		},
		{
			name: "服务名回退 __TOPIC__",
			raw: map[string]string{
				"msg":       "x",
				"__TOPIC__": "svc-topic",
			},
			check: func(t *testing.T, e *logquery.AppLogEntry) {
				if e.Service != "svc-topic" {
					t.Errorf("Service = %q, want svc-topic", e.Service)
				}
			},
		},
		{
			name: "无任何已知字段不丢弃",
			raw: map[string]string{
				"foo": "1",
				"bar": "2",
			},
			check: func(t *testing.T, e *logquery.AppLogEntry) {
				if e.Message != "bar=2 foo=1" {
					t.Errorf("Message = %q, want 按 key 排序的 k=v 串", e.Message)
				}
				if e.Level != "" {
					t.Errorf("Level = %q, want 空", e.Level)
				}
				if e.Service != "" || e.TraceID != "" || e.Exception != "" {
					t.Errorf("未知字段不应映射出业务语义: %+v", e)
				}
			},
		},
		{
			name: "兜底拼接串截断 500 字符",
			raw: map[string]string{
				"k": strings.Repeat("x", 600),
			},
			check: func(t *testing.T, e *logquery.AppLogEntry) {
				if got := len([]rune(e.Message)); got != appFixedMessageLimit {
					t.Errorf("len(Message runes) = %d, want %d", got, appFixedMessageLimit)
				}
			},
		},
		{
			name: "eo 特征冲突不特判仍映射 app",
			raw: map[string]string{
				"RequestHost":            "www.example.com",
				"EdgeResponseStatusCode": "200",
				"msg":                    "访问日志里混入的业务日志",
				"level":                  "INFO",
			},
			check: func(t *testing.T, e *logquery.AppLogEntry) {
				// app 映射不做 eo/waf 特判:源枚举已把这类 topic 归到 eo/waf,
				// 走到 appEntry 的行按字段尽力映射,不丢弃。
				if e.Message != "访问日志里混入的业务日志" {
					t.Errorf("Message = %q", e.Message)
				}
				if e.Level != "info" {
					t.Errorf("Level = %q, want info", e.Level)
				}
			},
		},
		{
			name: "waf 特征冲突不特判仍映射 app",
			raw: map[string]string{
				"request_path": "/api/login",
				"waf_action":   "block",
				"msg":          "业务日志",
			},
			check: func(t *testing.T, e *logquery.AppLogEntry) {
				if e.Message != "业务日志" {
					t.Errorf("Message = %q", e.Message)
				}
			},
		},
		{
			name:    "空 map 返回 nil",
			raw:     map[string]string{},
			wantNil: true,
		},
		{
			name:    "nil map 返回 nil",
			raw:     nil,
			wantNil: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := appEntry(meta, c.raw)
			if c.wantNil {
				if e != nil {
					t.Fatalf("appEntry = %#v, want nil", e)
				}
				return
			}
			if e == nil {
				t.Fatal("appEntry returned nil")
			}
			ae, ok := e.(*logquery.AppLogEntry)
			if !ok {
				t.Fatalf("entry type = %T, want *logquery.AppLogEntry", e)
			}
			c.check(t, ae)
		})
	}
}

// TestAppSourcesExcludeWAFStub 应用源必须排除已知 WAF stub topic。
func TestAppSourcesExcludeWAFStub(t *testing.T) {
	in := []logquery.LogSource{
		{ResourceID: "topic-a", Name: "应用日志 / a"},
		{ResourceID: appWAFStubTopicID, Name: "WAF stub"},
		{ResourceID: "topic-b", Name: "应用日志 / b"},
	}
	got := excludeAppStub(in)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2(WAF stub 已排除): %+v", len(got), got)
	}
	if got[0].ResourceID != "topic-a" || got[1].ResourceID != "topic-b" {
		t.Fatalf("过滤后顺序/内容错误: %+v", got)
	}
	for _, s := range got {
		if s.ResourceID == appWAFStubTopicID {
			t.Fatalf("WAF stub 未被排除: %+v", s)
		}
	}
	// 空输入不 panic(返回空切片,不返回 nil 也不影响上层)。
	if out := excludeAppStub(nil); len(out) != 0 {
		t.Fatalf("nil 输入 len = %d, want 0", len(out))
	}
}

// TestKindForLogType app 走 "other" 分类 + appEntry 映射器;CDN/WAF 逐字不变。
func TestKindForLogType(t *testing.T) {
	appP := &provider{logType: logquery.LogTypeApp}
	kind, mapper := appP.kindForLogType()
	if kind != "other" {
		t.Fatalf("app kind = %q, want other", kind)
	}
	if mapper == nil {
		t.Fatal("app mapper = nil")
	}
	e := mapper(logquery.LogMeta{}, map[string]string{"msg": "x"})
	if _, ok := e.(*logquery.AppLogEntry); !ok {
		t.Fatalf("app mapper 返回 %T, want *AppLogEntry", e)
	}

	if k, _ := (&provider{logType: logquery.LogTypeWAF}).kindForLogType(); k != "waf" {
		t.Errorf("waf kind = %q, want waf(不得改变)", k)
	}
	if k, _ := (&provider{logType: logquery.LogTypeCDN}).kindForLogType(); k != "eo" {
		t.Errorf("cdn kind = %q, want eo(不得改变)", k)
	}
}

// TestAppTimeValueMs 时间量纲判断:数字 <1e12 视为秒,≥1e12 视为毫秒,
// "秒.小数" 保留毫秒精度;非数字走 ParseTimeMs。
func TestAppTimeValueMs(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"1789490755", 1789490755000},     // 秒
		{"1789490755.123", 1789490755123}, // 秒+小数(四舍五入)
		{"1789490755123", 1789490755123},  // 毫秒
		{"999999999999", 999999999999000}, // 边界前(<1e12 仍按秒)
		{"2026-11-18T20:00:00+08:00", time.Date(2026, 11, 18, 20, 0, 0, 0, time.FixedZone("CST", 8*3600)).UnixMilli()},
		{"", 0},
		{"   ", 0},
		{"not-a-time", 0},
	}
	for _, c := range cases {
		if got := appTimeValueMs(c.in); got != c.want {
			t.Errorf("appTimeValueMs(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
