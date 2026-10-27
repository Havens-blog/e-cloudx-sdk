package logquery

import "testing"

func appTestEntry(service, level, message, traceID string) *AppLogEntry {
	return &AppLogEntry{
		Service:   service,
		Level:     level,
		Message:   message,
		TraceID:   traceID,
		Exception: "goroutine 1: NullPointerException",
		Timestamp: 1800000000000,
	}
}

// app 业务日志的字段过滤语义(/types 字典 app Fields=service/level/message/exception/trace_id,
// 五字段逐一锁定 —— 此前 exception 漏 case,前端按「异常堆栈」筛选静默恒空)。
// 前端 TopN 下钻依赖 service eq <图上服务名> 命中明细,漏 case 会静默恒空。
func TestEntryMatchesAppFields(t *testing.T) {
	e := appTestEntry("order-business", "error", "boom: NullPointerException", "trace-1")

	cases := []struct {
		name    string
		filters []FieldFilter
		want    bool
	}{
		{"service eq 命中", []FieldFilter{{Field: "service", Op: "eq", Value: "order-business"}}, true},
		{"service eq 不命中", []FieldFilter{{Field: "service", Op: "eq", Value: "user-business"}}, false},
		{"service contains 命中", []FieldFilter{{Field: "service", Op: "contains", Value: "order"}}, true},
		{"service prefix 命中", []FieldFilter{{Field: "service", Op: "prefix", Value: "order-"}}, true},
		{"service neq 命中(值不同)", []FieldFilter{{Field: "service", Op: "neq", Value: "user-business"}}, true},
		{"level eq 命中", []FieldFilter{{Field: "level", Op: "eq", Value: "error"}}, true},
		{"message contains 命中", []FieldFilter{{Field: "message", Op: "contains", Value: "NullPointer"}}, true},
		{"trace_id eq 命中", []FieldFilter{{Field: "trace_id", Op: "eq", Value: "trace-1"}}, true},
		{"exception contains 命中(此前漏 case 恒空)", []FieldFilter{{Field: "exception", Op: "contains", Value: "NullPointerException"}}, true},
		{"exception eq 命中", []FieldFilter{{Field: "exception", Op: "eq", Value: "goroutine 1: NullPointerException"}}, true},
		{"exception 不命中", []FieldFilter{{Field: "exception", Op: "contains", Value: "OutOfMemory"}}, false},
		{"字典外字段(host)不适用即剔除", []FieldFilter{{Field: "host", Op: "eq", Value: "x"}}, false},
		{"多条件 AND", []FieldFilter{{Field: "service", Op: "eq", Value: "order-business"}, {Field: "level", Op: "eq", Value: "warn"}}, false},
		{"空条件恒真", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EntryMatches(e, c.filters); got != c.want {
				t.Fatalf("EntryMatches(%v) = %v, want %v", c.filters, got, c.want)
			}
		})
	}
}

// 对照:cdn 条目的 service 字段不适用(只有 app 条目承载 service)。
func TestEntryMatchesAppServiceNotApplicableToCDN(t *testing.T) {
	e := &CDNLogEntry{Host: "a.com", Status: 200, Timestamp: 1800000000000}
	if EntryMatches(e, []FieldFilter{{Field: "service", Op: "eq", Value: "order-business"}}) {
		t.Fatal("cdn 条目不应命中 service 筛选")
	}
	if !EntryMatches(e, []FieldFilter{{Field: "host", Op: "eq", Value: "a.com"}}) {
		t.Fatal("cdn host eq 应命中")
	}
}
