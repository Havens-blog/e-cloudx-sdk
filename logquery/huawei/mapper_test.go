package huawei

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// fixture Phase 0 拉取的真实样本(collect_fixtures.py 落盘;
// content 为 LTS LogContents.Content 原文)。
type fixture struct {
	Meta struct {
		Desc   string `json:"desc"`
		Group  string `json:"group"`
		Stream string `json:"stream"`
	} `json:"meta"`
	Samples []struct {
		Content string `json:"content"`
	} `json:"samples"`
}

func loadFixture(t *testing.T, name string) fixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var f fixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	if len(f.Samples) == 0 {
		t.Fatalf("fixture %s has no samples", name)
	}
	return f
}

func testMeta(group, stream string) logquery.LogMeta {
	return logquery.LogMeta{
		Cloud:      "huawei",
		AccountID:  "2",
		Region:     defaultRegion,
		ResourceID: stream,
		Source:     group + "/" + stream,
	}
}

// TestClassify 流名后缀分类规则。
func TestClassify(t *testing.T) {
	cases := []struct {
		group, stream string
		want          mapperKind
		ok            bool
	}{
		{"hwyun-waf-logs", "hwyun-waf-logs-attack", kindWAFAttack, true},
		{"hwyun-waf-logs", "hwyun-waf-logs-access", kindWAFAccess, true},
		{"eda-prod-elb", "eda-prod-gw-std-elb", kindELB, true},
		{"hwyun-waf-logs", "hwyun-waf-logs-other", "", false},
		{"eda-prod-elb", "something-else", "", false},
	}
	for _, c := range cases {
		got, ok := classify(c.group, c.stream)
		if got != c.want || ok != c.ok {
			t.Errorf("classify(%q, %q) = (%q, %v), want (%q, %v)",
				c.group, c.stream, got, ok, c.want, c.ok)
		}
	}
}

// TestMapWAFAttackGolden 攻击流 golden(fixture: huawei-waf-attack,2026-08-27)。
func TestMapWAFAttackGolden(t *testing.T) {
	f := loadFixture(t, "huawei-waf-attack")
	for i, s := range f.Samples {
		e, ok := mapContent(kindWAFAttack, testMeta("hwyun-waf-logs", "hwyun-waf-logs-attack"), s.Content).(*logquery.WAFLogEntry)
		if !ok {
			t.Fatalf("sample %d: not WAFLogEntry", i)
		}
		if e.Timestamp <= 0 {
			t.Errorf("sample %d: timestamp not normalized(attack-time)", i)
		}
		if e.ClientIP == "" || e.Host == "" || e.Method == "" {
			t.Errorf("sample %d: core fields empty", i)
		}
		if e.Action != "block" {
			t.Errorf("sample %d: action = %q, want block", i, e.Action)
		}
		if e.RuleName == "" {
			t.Errorf("sample %d: rule name lost", i)
		}
		if e.UserAgent == "" {
			t.Errorf("sample %d: UA not extracted from header JSON", i)
		}
	}
	e, _ := mapContent(kindWAFAttack, testMeta("hwyun-waf-logs", "hwyun-waf-logs-attack"), f.Samples[0].Content).(*logquery.WAFLogEntry)
	if e.Timestamp != 1787824562000 {
		t.Errorf("timestamp = %d, want 1787824562000(attack-time 2026-08-27T09:56:02.000Z)", e.Timestamp)
	}
	if e.ClientIP != "118.112.231.27" || e.Host != "lceda.cn" {
		t.Errorf("client/host = %s/%s", e.ClientIP, e.Host)
	}
	if e.Severity != "low" { // level 2
		t.Errorf("severity = %q, want low(level=2)", e.Severity)
	}
}

// TestMapWAFAccessGolden 访问流 golden(fixture: huawei-waf-access)。
func TestMapWAFAccessGolden(t *testing.T) {
	f := loadFixture(t, "huawei-waf-access")
	for i, s := range f.Samples {
		e, ok := mapContent(kindWAFAccess, testMeta("hwyun-waf-logs", "hwyun-waf-logs-access"), s.Content).(*logquery.WAFLogEntry)
		if !ok {
			t.Fatalf("sample %d: not WAFLogEntry", i)
		}
		if e.Timestamp <= 0 {
			t.Errorf("sample %d: timestamp not normalized(waf-time)", i)
		}
		if e.Action != "pass" {
			t.Errorf("sample %d: access action = %q, want pass", i, e.Action)
		}
		if e.UserAgent == "" {
			t.Errorf("sample %d: UA lost(独立 user_agent 字段)", i)
		}
		if !strings.Contains(e.URI, "?") {
			t.Errorf("sample %d: args not appended to url: %q", i, e.URI)
		}
	}
	e, _ := mapContent(kindWAFAccess, testMeta("hwyun-waf-logs", "hwyun-waf-logs-access"), f.Samples[0].Content).(*logquery.WAFLogEntry)
	if e.Timestamp != 1787824562000 {
		t.Errorf("timestamp = %d, want 1787824562000", e.Timestamp)
	}
	if e.Status != 200 {
		t.Errorf("status = %d, want 200(response_code)", e.Status)
	}
	if e.URI != "/api/v4/snapshots/apply?project=4c0642828c8552ee6b06c9d0725efca6d043243c0a7c7adb582b4c7522df51e8&branch=b47f0ee15eb14d3d8e6ba39bf72ebeca&path=4c0642828c8552ee6b06c9d0725efca6d043243c0a7c7adb582b4c7522df51e8" {
		t.Errorf("uri = %q", e.URI)
	}
}

// TestMapELBLineGolden ELB 文本行 golden(fixture: huawei-elb-access;列位解析)。
func TestMapELBLineGolden(t *testing.T) {
	f := loadFixture(t, "huawei-elb-access")
	for i, s := range f.Samples {
		e, ok := mapContent(kindELB, testMeta("eda-prod-elb", "eda-prod-gw-std-elb"), s.Content).(*logquery.SLBLogEntry)
		if !ok {
			t.Fatalf("sample %d: not SLBLogEntry", i)
		}
		if e.Timestamp <= 0 {
			t.Errorf("sample %d: timestamp bad", i)
		}
		if e.ClientIP == "" || e.Status == 0 {
			t.Errorf("sample %d: client/status empty", i)
		}
		if e.TargetIP == "" {
			t.Errorf("sample %d: upstream addr not split", i)
		}
		if e.URL == "" || e.Method == "" {
			t.Errorf("sample %d: request line not parsed", i)
		}
		if e.LatencyMs <= 0 {
			t.Errorf("sample %d: latency bad: %d", i, e.LatencyMs)
		}
	}
	e, _ := mapContent(kindELB, testMeta("eda-prod-elb", "eda-prod-gw-std-elb"), f.Samples[0].Content).(*logquery.SLBLogEntry)
	if e.Timestamp != 1787824562000 { // [2026-08-27T17:56:02+08:00]
		t.Errorf("timestamp = %d, want 1787824562000", e.Timestamp)
	}
	if e.ClientIP != "10.41.185.47" || e.ClientPort != 49798 {
		t.Errorf("client = %s:%d", e.ClientIP, e.ClientPort)
	}
	if e.Status != 200 {
		t.Errorf("status = %d, want 200", e.Status)
	}
	if e.Method != "GET" || e.Protocol != "HTTP/1.1" {
		t.Errorf("method/proto = %s/%s", e.Method, e.Protocol)
	}
	if e.Host != "inner.oshwhub.com" {
		t.Errorf("host = %q, want inner.oshwhub.com(absolute URL 解析)", e.Host)
	}
	if e.LatencyMs != 32 { // request_time 0.032s
		t.Errorf("latency = %d, want 32", e.LatencyMs)
	}
	if e.TargetIP != "10.41.185.220" || e.TargetPort != 8080 {
		t.Errorf("target = %s:%d", e.TargetIP, e.TargetPort)
	}
	if e.UpstreamStatus != 200 {
		t.Errorf("upstream status = %d, want 200", e.UpstreamStatus)
	}
	if e.Meta.ResourceID == "" || e.Meta.ResourceID != "56d25663-2920-41e4-8595-97f885be716c" {
		t.Errorf("resource id = %q, want loadbalancer uuid", e.Meta.ResourceID)
	}
	if e.UserAgent != "node" {
		t.Errorf("ua = %q, want node", e.UserAgent)
	}
}

// TestMapELBLineDegraded 列数不足/乱格式:降级整行进 Raw,统一字段留空,不 panic。
func TestMapELBLineDegraded(t *testing.T) {
	for _, line := range []string{"", "short line", "a b c d e"} {
		e := mapContent(kindELB, testMeta("eda-prod-elb", "x"), line)
		slb, ok := e.(*logquery.SLBLogEntry)
		if !ok {
			t.Fatalf("not SLBLogEntry")
		}
		if slb.Raw == nil {
			t.Errorf("raw must be preserved for degraded line %q", line)
		}
	}
}

// TestMapContentBadJSON WAF content 非 JSON:降级不 panic。
func TestMapContentBadJSON(t *testing.T) {
	e := mapContent(kindWAFAttack, testMeta("g", "s"), "not-json{")
	if e == nil {
		t.Fatal("nil entry")
	}
	if e.GetTimestamp() != 0 {
		t.Errorf("bad json should zero timestamp")
	}
}

// TestTokenizeELBLine 引号/方括号段为单 token。
func TestTokenizeELBLine(t *testing.T) {
	toks := tokenizeELBLine(`1.5 uuid [2026-08-27T17:56:02+08:00] elb_01 "GET https://a.b/c?x=1 HTTP/1.1" 200 "10.0.0.1:80"`)
	want := []string{"1.5", "uuid", "2026-08-27T17:56:02+08:00", "elb_01",
		"GET https://a.b/c?x=1 HTTP/1.1", "200", "10.0.0.1:80"}
	if len(toks) != len(want) {
		t.Fatalf("tokens = %#v, want %#v", toks, want)
	}
	for i := range want {
		if toks[i] != want[i] {
			t.Errorf("tok[%d] = %q, want %q", i, toks[i], want[i])
		}
	}
}
