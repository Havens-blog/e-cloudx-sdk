package logquery

import (
	"fmt"
	"strings"
	"testing"
)

// TestTopNFromEntries 采样分组纯函数:按维度字段计数降序;空/缺失/"null"
// 值不编组(不产出空名组——SQL NULL 组曾以字面 "null" 穿透空键守卫);
// limit 截断。
func TestTopNFromEntries(t *testing.T) {
	entries := []LogEntry{
		&AppLogEntry{Service: "svc-a", Level: "error"},
		&AppLogEntry{Service: "svc-a", Level: "error"},
		&AppLogEntry{Service: "svc-b", Level: "error"},
		&AppLogEntry{Service: "", Level: "error"},     // 空服务不编组
		&AppLogEntry{Service: "null", Level: "error"}, // 字面 null 不编组
	}
	got := TopNFromEntries(entries, "service", 10)
	if len(got) != 2 || got[0].Name != "svc-a" || got[0].Count != 2 || got[1].Name != "svc-b" {
		t.Fatalf("got %+v", got)
	}
	// 降序 + limit 截断
	many := []LogEntry{}
	for i := 0; i < 15; i++ {
		many = append(many, &AppLogEntry{Service: fmt.Sprintf("svc-%02d", i)})
	}
	if got := TopNFromEntries(many, "service", 5); len(got) != 5 {
		t.Fatalf("limit 截断失效: %d", len(got))
	}
	// trace_id 维度
	tr := []LogEntry{&AppLogEntry{TraceID: "t1"}, &AppLogEntry{TraceID: "t1"}, &AppLogEntry{}}
	if got := TopNFromEntries(tr, "trace_id", 10); len(got) != 1 || got[0].Name != "t1" || got[0].Count != 2 {
		t.Fatalf("trace_id 分组: %+v", got)
	}
	// 未知维度返回空
	if got := TopNFromEntries(entries, "nope", 10); len(got) != 0 {
		t.Fatalf("未知维度应空: %+v", got)
	}
	if got := TopNFromEntries(entries, "", 10); got != nil {
		t.Fatal("空维度应 nil")
	}
}

// TestScanTopNNote 口径说明文案含采样量。
func TestScanTopNNote(t *testing.T) {
	note := ScanTopNNote(123)
	if !strings.Contains(note, "123") || !strings.Contains(note, "非精确") {
		t.Fatalf("note = %q", note)
	}
}
