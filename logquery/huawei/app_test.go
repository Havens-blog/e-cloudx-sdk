package huawei

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// testAppMS 固定毫秒时间戳(JSON 数值形态,避免测试里重复魔法数字)。
const testAppMS int64 = 1793064389483

// appEntry 断言 mapAppContent 返回 *logquery.AppLogEntry。
func appEntry(t *testing.T, e logquery.LogEntry) *logquery.AppLogEntry {
	t.Helper()
	if e == nil {
		t.Fatal("mapAppContent 返回 nil,期望 AppLogEntry")
	}
	out, ok := e.(*logquery.AppLogEntry)
	if !ok {
		t.Fatalf("类型 = %T,期望 *logquery.AppLogEntry", e)
	}
	return out
}

// TestMapAppContentJSON JSON 对象映射:字段别名、大小写、缺字段回退、
// 时间戳多形态(取值优先 __time__,解析失败留 0 由上层保留 LTS 时间)。
func TestMapAppContentJSON(t *testing.T) {
	rfc3339 := time.Date(2026, 8, 27, 12, 0, 0, 123_000_000, time.UTC).UnixMilli()
	meta := logquery.LogMeta{ResourceID: "app-order-stream"}
	tests := []struct {
		name          string
		content       string
		wantLevel     string
		wantMessage   string
		wantException string
		wantTrace     string
		wantService   string
		wantTS        int64
	}{
		{
			name:          "logback-full",
			content:       `{"@timestamp":"2026-08-27T12:00:00.123Z","__time__":1793064389483,"level":"ERROR","msg":"order callback failed","exception":"java.lang.RuntimeException: boom","trace_id":"0123456789abcdef0123456789abcdef","service":"zxh-smt-order-service","pod_name":"order-0"}`,
			wantLevel:     "error",
			wantMessage:   "order callback failed",
			wantException: "java.lang.RuntimeException: boom",
			wantTrace:     "0123456789abcdef0123456789abcdef",
			wantService:   "zxh-smt-order-service",
			wantTS:        testAppMS,
		},
		{
			name:          "case-insensitive-keys",
			content:       `{"LEVEL":"WARN","Msg":"mixed case","TraceId":"tid-123456","Service_Name":"svc-a","Stack":"at x.y.Z","loglevel":"debug","body":"fallback body"}`,
			wantLevel:     "warn",
			wantMessage:   "mixed case",
			wantException: "at x.y.Z",
			wantTrace:     "tid-123456",
			wantService:   "svc-a",
			wantTS:        0,
		},
		{
			name:        "missing-fields-service-falls-back-to-stream",
			content:     `{"msg":"only message"}`,
			wantLevel:   "",
			wantMessage: "only message",
			wantService: "app-order-stream", // meta.ResourceID = 流名
		},
		{
			name:        "timestamp-string-ms",
			content:     `{"timestamp":"1793064389483","message":"via timestamp"}`,
			wantMessage: "via timestamp",
			wantService: "app-order-stream",
			wantTS:      testAppMS,
		},
		{
			name:        "timestamp-rfc3339-at-prefix",
			content:     `{"@timestamp":"2026-08-27T12:00:00.123Z","message":"via at-timestamp"}`,
			wantMessage: "via at-timestamp",
			wantService: "app-order-stream",
			wantTS:      rfc3339,
		},
		{
			name:        "timestamp-unparsable-stays-zero",
			content:     `{"timestamp":"not-a-time","message":"bad ts"}`,
			wantMessage: "bad ts",
			wantService: "app-order-stream",
			wantTS:      0,
		},
		{
			name:        "invalid-json-falls-back-to-text",
			content:     `{"level":"ERROR","msg":"truncated"`,
			wantLevel:   "",
			wantMessage: `{"level":"ERROR","msg":"truncated"`,
			wantService: "app-order-stream",
		},
		{
			name:        "json-array-falls-back-to-text",
			content:     `[1,2,3]`,
			wantMessage: "[1,2,3]",
			wantService: "app-order-stream",
		},
		{
			name:        "json-with-bom",
			content:     "\ufeff{\"level\":\"INFO\",\"msg\":\"bom tolerated\"}",
			wantLevel:   "info",
			wantMessage: "bom tolerated",
			wantService: "app-order-stream",
		},
		{
			// 数值型 severity(pino/bunyan 风格)不映射为级别:留空,原始值仍在 Raw。
			name:        "numeric-severity-not-mapped",
			content:     `{"level":30,"msg":"pino numeric level"}`,
			wantLevel:   "",
			wantMessage: "pino numeric level",
			wantService: "app-order-stream",
		},
		{
			// 其余别名:severity / log / err / trace / application。
			name:          "alias-variants",
			content:       `{"severity":"CRITICAL","log":"alt message","err":"E1","trace":"abc-123-xyz","application":"svc-app"}`,
			wantLevel:     "critical",
			wantMessage:   "alt message",
			wantException: "E1",
			wantTrace:     "abc-123-xyz",
			wantService:   "svc-app",
			wantTS:        0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := appEntry(t, mapAppContent(meta, tt.content))
			if e.Level != tt.wantLevel {
				t.Errorf("level = %q, want %q", e.Level, tt.wantLevel)
			}
			if e.Message != tt.wantMessage {
				t.Errorf("message = %q, want %q", e.Message, tt.wantMessage)
			}
			if e.Exception != tt.wantException {
				t.Errorf("exception = %q, want %q", e.Exception, tt.wantException)
			}
			if e.TraceID != tt.wantTrace {
				t.Errorf("trace_id = %q, want %q", e.TraceID, tt.wantTrace)
			}
			if e.Service != tt.wantService {
				t.Errorf("service = %q, want %q", e.Service, tt.wantService)
			}
			if e.Timestamp != tt.wantTS {
				t.Errorf("timestamp = %d, want %d", e.Timestamp, tt.wantTS)
			}
			if e.Raw == nil {
				t.Error("Raw 不应为空(JSON 整份保留)")
			}
			if e.Meta.ResourceID != meta.ResourceID {
				t.Errorf("meta 未透传: %+v", e.Meta)
			}
		})
	}
}

// TestMapAppContentRaw JSON 与纯文本的 Raw 保底形态。
func TestMapAppContentRaw(t *testing.T) {
	meta := logquery.LogMeta{ResourceID: "app-order-stream"}

	js := appEntry(t, mapAppContent(meta, `{"msg":"hi","pod_name":"order-0"}`))
	if js.Raw["pod_name"] != "order-0" {
		t.Fatalf("JSON Raw 应保留原始键: %+v", js.Raw)
	}
	if _, ok := js.Raw["content"]; ok {
		t.Fatalf("JSON Raw 不应注入 content: %+v", js.Raw)
	}

	line := "plain line without metadata"
	txt := appEntry(t, mapAppContent(meta, line))
	if txt.Raw["content"] != line {
		t.Fatalf("纯文本 Raw = %+v, want content=整行", txt.Raw)
	}
}

// TestMapAppContentText 纯文本行:级别识别(方括号/前缀/ISO 时间/level=)、
// trace key=value 提取、识别不到留空(不编造)。
func TestMapAppContentText(t *testing.T) {
	meta := logquery.LogMeta{ResourceID: "app-order-stream"}
	const trace32 = "0123456789abcdef0123456789abcdef"
	tests := []struct {
		name      string
		content   string
		wantLevel string
		wantTrace string
	}{
		{
			name:      "bracket-error",
			content:   "[ERROR] java.lang.RuntimeException: boom",
			wantLevel: "error",
		},
		{
			name:      "bracket-lowercase-warn",
			content:   "[warn] slow query",
			wantLevel: "warn",
		},
		{
			name:      "iso-time-warn",
			content:   "2026-08-27 12:00:00.123 WARN something happened",
			wantLevel: "warn",
		},
		{
			name:      "prefix-error",
			content:   "ERROR something failed",
			wantLevel: "error",
		},
		{
			name:      "level-colon-debug",
			content:   "level: DEBUG boot done",
			wantLevel: "debug",
		},
		{
			name:      "kv-level-with-trace",
			content:   "level=INFO traceId=" + trace32 + " - order created",
			wantLevel: "info",
			wantTrace: trace32,
		},
		{
			name:      "kv-trace-quoted",
			content:   `level=INFO trace_id="abc-123-def" msg`,
			wantLevel: "info",
			wantTrace: "abc-123-def",
		},
		{
			name:      "tid-kv-trace",
			content:   "tid=dead-beef-42 something",
			wantLevel: "",
			wantTrace: "dead-beef-42",
		},
		{
			name:      "plain-line-no-level-no-trace",
			content:   "just a plain log line 42",
			wantLevel: "",
			wantTrace: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := appEntry(t, mapAppContent(meta, tt.content))
			if e.Level != tt.wantLevel {
				t.Errorf("level = %q, want %q", e.Level, tt.wantLevel)
			}
			if e.TraceID != tt.wantTrace {
				t.Errorf("trace_id = %q, want %q", e.TraceID, tt.wantTrace)
			}
			if e.Message != tt.content {
				t.Errorf("message = %q, want 整行 %q", e.Message, tt.content)
			}
			if e.Service != "app-order-stream" {
				t.Errorf("service = %q, want 流名回退", e.Service)
			}
			if e.Timestamp != 0 {
				t.Errorf("timestamp = %d, 纯文本不应编造时间", e.Timestamp)
			}
		})
	}
}

// TestMapAppContentMultiLine 多行文本级别只认首行,Message 保留整段。
func TestMapAppContentMultiLine(t *testing.T) {
	content := "ERROR first line\n\tat x.y.Z(Unknown Source)\n\tat a.b.C"
	e := appEntry(t, mapAppContent(logquery.LogMeta{ResourceID: "s"}, content))
	if e.Level != "error" {
		t.Fatalf("level = %q, want error", e.Level)
	}
	if e.Message != content {
		t.Fatalf("message 应保留多行原文: %q", e.Message)
	}
}

// TestMapAppContentEmpty 空内容(含纯空白)返回 nil,由调用方跳过。
func TestMapAppContentEmpty(t *testing.T) {
	meta := logquery.LogMeta{ResourceID: "app-order-stream"}
	for _, content := range []string{"", "   ", "\n\t ", "\ufeff"} {
		if e := mapAppContent(meta, content); e != nil {
			t.Fatalf("content=%q 应返回 nil,得到 %T", content, e)
		}
	}
}

// TestAppGroups app 组发现规则:catalog 的 WAF/ELB 两组必须排除,其余组
// 全量纳入(不按名字后缀过滤),结果去重且按字典序。
func TestAppGroups(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "excludes-catalog-groups",
			in:   []string{"hwyun-waf-logs", "eda-prod-elb", "app-order", "app-pay"},
			want: []string{"app-order", "app-pay"},
		},
		{
			name: "nil-input",
			in:   nil,
			want: nil,
		},
		{
			name: "only-catalog-groups",
			in:   []string{"eda-prod-elb", "hwyun-waf-logs"},
			want: nil,
		},
		{
			name: "dedupe-empty-and-sort",
			in:   []string{"app-b", "", "app-b", "app-a"},
			want: []string{"app-a", "app-b"},
		},
		{
			// 组名符合 WAF/ELB 形态的同族组(多环境、带序号)同样归 WAF/ELB 页签,
			// 不能当应用日志 —— 2026-11-19 实测 dev-elb/eda-uat-elb 因此丢失。
			name: "same-shape-groups-are-not-app",
			in:   []string{"hwyun-waf-logs-2", "dev-elb", "eda-uat-elb", "app-order"},
			want: []string{"app-order"},
		},
		{
			// 仅含 waf/elb 子串但不符组名形态的应用组仍是应用日志源。
			name: "substring-lookalikes-are-app",
			in:   []string{"eda-prod-elb-app", "waf-service-logs", "my-elb-service"},
			want: []string{"eda-prod-elb-app", "my-elb-service", "waf-service-logs"},
		},
		{
			name: "sort-by-name",
			in:   []string{"zeta-app", "alpha-app"},
			want: []string{"alpha-app", "zeta-app"},
		},
		{
			name: "excludes-platform-group",
			in:   []string{"lts-system", "app-order", "hwyun-waf-logs"},
			want: []string{"app-order"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appGroups(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("appGroups(%v) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("appGroups(%v) = %v, want %v", tt.in, got, tt.want)
				}
			}
		})
	}
}

// TestAppResourceMatch 资源过滤:空 = 不限;流名或 group/stream 命中。
func TestAppResourceMatch(t *testing.T) {
	tests := []struct {
		name      string
		resources []string
		want      bool
	}{
		{name: "empty-means-all", resources: nil, want: true},
		{name: "stream-name", resources: []string{"app-stream"}, want: true},
		{name: "group-stream", resources: []string{"app-group/app-stream"}, want: true},
		{name: "other-resource", resources: []string{"other-group/app-stream"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := appResourceMatch("app-group", "app-stream", tt.resources); got != tt.want {
				t.Fatalf("appResourceMatch = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestAppProviderRegistered 三种日志类型均已注册(WAF/SLB 不回归)。
func TestAppProviderRegistered(t *testing.T) {
	for _, lt := range []logquery.LogType{
		logquery.LogTypeWAF, logquery.LogTypeSLB, logquery.LogTypeApp,
	} {
		if !logquery.IsProviderRegistered(domain.CloudProviderHuawei, lt) {
			t.Fatalf("%s provider 未注册", lt)
		}
	}
}

// TestIsLTSThrottled 限流判定只认明确特征(request id 里含 429 不得误判)。
func TestIsLTSThrottled(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "apigw-0308", err: errors.New(`{"status_code":429,"error_code":"APIGW.0308","error_message":"The throttling threshold has been reached: policy user over ratelimit,limit:10,time:1 second"}`), want: true},
		{name: "status-code-json", err: errors.New(`{"status_code":429}`), want: true},
		{name: "status-code-text", err: errors.New("lts list_logs: status code: 429"), want: true},
		{name: "throttling-word", err: errors.New("Throttling.User"), want: true},
		{name: "request-id-with-429", err: errors.New(`{"request_id":"a429bc","error_code":"APIGW.0301"}`), want: false},
		{name: "network-error", err: errors.New("dial tcp: lookup lts.cn-south-1: no such host"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLTSThrottled(tt.err); got != tt.want {
				t.Fatalf("isLTSThrottled(%v) = %v, 期望 %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestLTSGateSpacing 闸门必须让相邻两次调用至少间隔 ltsCallInterval ——
// 这正是「并发 8 个流被 429 打掉一半源」的修复点。
func TestLTSGateSpacing(t *testing.T) {
	g := &ltsGate{}
	ctx := context.Background()
	start := time.Now()
	if err := g.wait(ctx); err != nil {
		t.Fatalf("首次 wait 出错: %v", err)
	}
	if err := g.wait(ctx); err != nil {
		t.Fatalf("第二次 wait 出错: %v", err)
	}
	if elapsed := time.Since(start); elapsed < ltsCallInterval {
		t.Fatalf("两次调用间隔 %v 小于闸门间隔 %v", elapsed, ltsCallInterval)
	}
}

// TestLTSGateCancel ctx 取消时不得无限等待。
func TestLTSGateCancel(t *testing.T) {
	g := &ltsGate{}
	ctx := context.Background()
	if err := g.wait(ctx); err != nil {
		t.Fatalf("首次 wait 出错: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := g.wait(cancelled); err == nil {
		t.Fatal("ctx 已取消,wait 仍返回 nil")
	}
}

// TestStampAppResource app 条目必须能按「组/流」归属(否则前端看不出日志来自哪个组)。
func TestStampAppResource(t *testing.T) {
	entries := []logquery.LogEntry{
		&logquery.AppLogEntry{Meta: logquery.LogMeta{ResourceID: "stdout-x"}, Message: "a"},
		&logquery.AppLogEntry{Meta: logquery.LogMeta{ResourceID: "event-x"}, Message: "b"},
	}
	got := stampAppResource(entries, "k8s-log-x", "stdout-x")
	if len(got) != 2 {
		t.Fatalf("条目数 = %d, 期望 2", len(got))
	}
	for _, e := range got {
		app := appEntry(t, e)
		if app.Meta.ResourceID != "k8s-log-x/stdout-x" {
			t.Fatalf("resource_id = %q, 期望 k8s-log-x/stdout-x", app.Meta.ResourceID)
		}
	}
	// 非 AppLogEntry 不得被改写(共用骨架给 WAF/ELB 的条目类型)
	other := []logquery.LogEntry{nil}
	if out := stampAppResource(other, "g", "s"); len(out) != 1 || out[0] != nil {
		t.Fatal("非 AppLogEntry 条目被改写")
	}
}
