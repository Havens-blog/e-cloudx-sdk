package aws

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// awsFixture Phase 0 拉取的真实样本(collect_fixtures.py / fix_aws_fixtures.py 落盘)。
type awsFixture struct {
	Meta struct {
		Bucket      string   `json:"bucket"`
		Key         string   `json:"key"`
		Format      string   `json:"format"`
		HeaderLines []string `json:"header_lines"`
	} `json:"meta"`
	Samples []json.RawMessage `json:"samples"`
}

func loadAWSFixture(t *testing.T, name string) awsFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var f awsFixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	if len(f.Samples) == 0 {
		t.Fatalf("fixture %s has no samples", name)
	}
	return f
}

func testMeta() logquery.LogMeta {
	return logquery.LogMeta{
		Cloud: "aws", AccountID: "3", Region: "us-east-1",
		ResourceID: "fixture", Source: "bucket/key",
	}
}

func decodeWAF(t *testing.T, s json.RawMessage) map[string]any {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(s, &raw); err != nil {
		t.Fatalf("decode waf sample: %v", err)
	}
	return raw
}

func decodeLine(t *testing.T, s json.RawMessage) string {
	t.Helper()
	var obj struct {
		RawLine string `json:"_raw_line"`
	}
	if err := json.Unmarshal(s, &obj); err != nil {
		t.Fatalf("decode line sample: %v", err)
	}
	return obj.RawLine
}

func cfFields(t *testing.T, f awsFixture) []string {
	t.Helper()
	for _, h := range f.Meta.HeaderLines {
		if strings.HasPrefix(h, "#Fields:") {
			return strings.Fields(strings.TrimPrefix(h, "#Fields:"))
		}
	}
	t.Fatal("no #Fields header in fixture")
	return nil
}

// TestMapWAFJSONGolden AWS WAFv2 JSON golden(fixture: aws-waf-cloudfront)。
func TestMapWAFJSONGolden(t *testing.T) {
	f := loadAWSFixture(t, "aws-waf-cloudfront")
	for i, s := range f.Samples {
		e := mapWAFJSON(testMeta(), decodeWAF(t, s))
		if e.Timestamp <= 0 {
			t.Errorf("sample %d: timestamp bad(ms 直取)", i)
		}
		if e.ClientIP == "" || e.Method == "" {
			t.Errorf("sample %d: client/method empty", i)
		}
		if e.Host == "" {
			t.Errorf("sample %d: host not found in headers array", i)
		}
		if e.UserAgent == "" {
			t.Errorf("sample %d: UA not found in headers array", i)
		}
		if e.Action == "" {
			t.Errorf("sample %d: action empty", i)
		}
		if e.Meta.ResourceID == "" {
			t.Errorf("sample %d: httpSourceId not mapped", i)
		}
	}
	// sample0 精确值:timestamp=1756005799639, ALLOW + Default_Action -> allow
	e0 := mapWAFJSON(testMeta(), decodeWAF(t, f.Samples[0]))
	if e0.Timestamp != 1756005799639 {
		t.Errorf("timestamp = %d, want 1756005799639", e0.Timestamp)
	}
	if e0.Action != "allow" {
		t.Errorf("action = %q, want allow(Default_Action 归一)", e0.Action)
	}
	if e0.RuleID != "" {
		t.Errorf("rule id = %q, want 空(Default_Action 不保留)", e0.RuleID)
	}
	if e0.Host != "attachment.forface3d.net" {
		t.Errorf("host = %q", e0.Host)
	}
	if e0.Meta.ResourceID != "E2U51XYZHX0AP" {
		t.Errorf("resource = %q, want E2U51XYZHX0AP", e0.Meta.ResourceID)
	}
}

// TestMapCloudFrontLineGolden CloudFront TSV golden(fixture: aws-cloudfront)。
func TestMapCloudFrontLineGolden(t *testing.T) {
	f := loadAWSFixture(t, "aws-cloudfront")
	fields := cfFields(t, f)
	for i, s := range f.Samples {
		line := decodeLine(t, s)
		if line == "" {
			continue
		}
		e := mapCloudFrontLine(testMeta(), fields, line)
		if e.Timestamp <= 0 {
			t.Errorf("sample %d: timestamp bad(date+time UTC)", i)
		}
		if e.Host == "" || e.Status == 0 || e.ClientIP == "" {
			t.Errorf("sample %d: core fields empty", i)
		}
		if e.LatencyMs <= 0 {
			t.Errorf("sample %d: latency bad(time-taken float)", i)
		}
		if e.RequestID == "" || e.EdgeNode == "" {
			t.Errorf("sample %d: edge fields lost", i)
		}
	}
	// sample0 精确值:date=2026-04-29 time=22:57:56 status=502 time-taken=0.885
	e0 := mapCloudFrontLine(testMeta(), fields, decodeLine(t, f.Samples[0]))
	want, _ := time.Parse(time.RFC3339, "2026-04-29T22:57:56Z")
	if e0.Timestamp != want.UnixMilli() {
		t.Errorf("timestamp = %d, want %d(UTC 拼接)", e0.Timestamp, want.UnixMilli())
	}
	if e0.Status != 502 {
		t.Errorf("status = %d, want 502", e0.Status)
	}
	if e0.LatencyMs != 885 {
		t.Errorf("latency = %d, want 885(0.885s)", e0.LatencyMs)
	}
	if e0.Host != "d3cq5m6xeuedwf.cloudfront.net" {
		t.Errorf("host = %q", e0.Host)
	}
	if e0.EdgeNode != "CDG54-P2" {
		t.Errorf("edge = %q, want CDG54-P2", e0.EdgeNode)
	}
}

// TestMapCloudFrontLineDirty 脏行(列数不符):降级整行进 Raw,统一字段留空。
func TestMapCloudFrontLineDirty(t *testing.T) {
	fields := []string{"date", "time", "c-ip"}
	e := mapCloudFrontLine(testMeta(), fields, "2026-04-29\t22:57:56\t1.2.3.4\textra")
	if e.Timestamp != 0 || e.ClientIP != "" {
		t.Errorf("dirty line should leave unified fields empty, got %+v", e)
	}
	if e.Raw == nil {
		t.Error("raw must be preserved")
	}
}

// TestHeaderLookup 大小写不敏感与缺失容忍。
func TestHeaderLookup(t *testing.T) {
	headers := []any{
		map[string]any{"name": "Host", "value": "a.example.com"},
		map[string]any{"name": "User-Agent", "value": "curl/8"},
		map[string]any{"name": "other", "value": "x"},
	}
	host, ua := headerLookup(headers)
	if host != "a.example.com" || ua != "curl/8" {
		t.Errorf("got %q/%q", host, ua)
	}
	if h, u := headerLookup(nil); h != "" || u != "" {
		t.Error("nil headers should return empty")
	}
}

// TestParseCloudFrontBodyFieldsRequired 无 #Fields 头的正文不可解析(防御)。
func TestParseCloudFrontBodyFieldsRequired(t *testing.T) {
	params := logquery.SearchParams{StartTime: 0, EndTime: 9e15}
	got := parseCloudFrontBody(testMeta(), []byte("2026-04-29\t22:57:56"), params, 10)
	if len(got) != 0 {
		t.Errorf("body without header should yield 0 entries, got %d", len(got))
	}
}

// TestPathBase 取路径末段。
func TestPathBase(t *testing.T) {
	cases := map[string]string{
		"AWSLogs/123/WAFLogs/cloudfront/MyAcl/": "MyAcl",
		"AWSLogs/123/":                          "123",
		"plain":                                 "plain",
	}
	for in, want := range cases {
		if got := pathBase(in); got != want {
			t.Errorf("pathBase(%q) = %q, want %q", in, got, want)
		}
	}
}
