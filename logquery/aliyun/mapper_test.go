package aliyun

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// fixture Phase 0 拉取的真实样本结构(collect_fixtures.py 落盘)。
type fixture struct {
	Meta struct {
		Cloud    string `json:"cloud"`
		LogType  string `json:"log_type"`
		Desc     string `json:"desc"`
		Region   string `json:"region"`
		Project  string `json:"project"`
		Logstore string `json:"logstore"`
	} `json:"meta"`
	Samples []map[string]string `json:"samples"`
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

func testMeta(f fixture) logquery.LogMeta {
	return logquery.LogMeta{
		Cloud:       "aliyun",
		AccountID:   "1",
		AccountName: "fixture",
		Region:      f.Meta.Region,
		ResourceID:  f.Meta.Logstore,
		Source:      f.Meta.Project + "/" + f.Meta.Logstore,
	}
}

// TestMapALBGolden 阿里 ALB 访问日志 golden(field-mapping.md §一.1;
// fixture: aliyun-alb-access.json,2026-08-27 实时拉取)。
func TestMapALBGolden(t *testing.T) {
	f := loadFixture(t, "aliyun-alb-access")
	for i, raw := range f.Samples {
		e, ok := mapEntry(kindALB, testMeta(f), raw).(*logquery.SLBLogEntry)
		if !ok {
			t.Fatalf("sample %d: not SLBLogEntry", i)
		}
		if e.Timestamp <= 0 {
			t.Errorf("sample %d: timestamp not normalized: %d", i, e.Timestamp)
		}
		if e.ClientIP == "" || e.Status == 0 || e.Method == "" {
			t.Errorf("sample %d: core fields empty: %+v", i, e)
		}
		if e.TargetIP == "" {
			t.Errorf("sample %d: upstream_addr not split: %q", i, raw["upstream_addr"])
		}
		if e.URL == "" || e.Host == "" {
			t.Errorf("sample %d: url/host not built", i)
		}
		if len(e.Raw) != len(raw) {
			t.Errorf("sample %d: raw not fully preserved: %d != %d", i, len(e.Raw), len(raw))
		}
		if e.Meta.ResourceID == "" {
			t.Errorf("sample %d: resource id empty", i)
		}
	}
	// 具体值断言(样本 0,Phase 0 实测字段值)
	e, _ := mapEntry(kindALB, testMeta(f), f.Samples[0]).(*logquery.SLBLogEntry)
	if e.Timestamp != 1787230557000 {
		t.Errorf("timestamp = %d, want 1787230557000(2026-08-20T20:55:57+08:00)", e.Timestamp)
	}
	if e.Status != 403 {
		t.Errorf("status = %d, want 403", e.Status)
	}
	if e.TargetIP != "172.19.152.31" || e.TargetPort != 6090 {
		t.Errorf("target = %s:%d, want 172.19.152.31:6090", e.TargetIP, e.TargetPort)
	}
	if e.LatencyMs != 9 { // request_time 0.009s
		t.Errorf("latency = %d, want 9", e.LatencyMs)
	}
}

// TestMapALBOverseasGolden 海外 ALB(schema 与国内一致,fixture 跨 region 校验)。
func TestMapALBOverseasGolden(t *testing.T) {
	f := loadFixture(t, "aliyun-alb-access-overseas")
	for i, raw := range f.Samples {
		e, ok := mapEntry(kindALB, testMeta(f), raw).(*logquery.SLBLogEntry)
		if !ok {
			t.Fatalf("sample %d: not SLBLogEntry", i)
		}
		if e.Timestamp <= 0 || e.Status == 0 {
			t.Errorf("sample %d: core fields bad: %+v", i, e)
		}
	}
}

// TestMapWAF3Golden 阿里 WAF3.0 访问日志 golden(field-mapping.md §二.3)。
func TestMapWAF3Golden(t *testing.T) {
	f := loadFixture(t, "aliyun-waf3-access")
	for i, raw := range f.Samples {
		e, ok := mapEntry(kindWAF3, testMeta(f), raw).(*logquery.WAFLogEntry)
		if !ok {
			t.Fatalf("sample %d: not WAFLogEntry", i)
		}
		if e.Timestamp <= 0 {
			t.Errorf("sample %d: timestamp not normalized(start_time=%q): %d",
				i, raw["start_time"], e.Timestamp)
		}
		if e.ClientIP == "" {
			t.Errorf("sample %d: client ip empty(real/src/remote 回退全失败)", i)
		}
		if e.Action != "pass" {
			t.Errorf("sample %d: access-log action = %q, want pass", i, e.Action)
		}
		// Raw 保留除头/cookie 转储外全部字段(裁头字段只服务明细抽屉,见 raw_trim.go)
		for k := range raw {
			if logquery.IsRawHeaderDumpKey(k) {
				continue
			}
			if _, ok := e.Raw[k]; !ok {
				t.Errorf("sample %d: raw key %q not preserved", i, k)
			}
		}
	}
	e, _ := mapEntry(kindWAF3, testMeta(f), f.Samples[0]).(*logquery.WAFLogEntry)
	if e.Timestamp != 1787824584000 {
		t.Errorf("timestamp = %d, want 1787824584000", e.Timestamp)
	}
	if e.ClientIP != "120.241.127.158" {
		t.Errorf("client ip = %q, want 120.241.127.158(real_client_ip 优先)", e.ClientIP)
	}
}

// TestMapDCDNGolden DCDN 边缘实时日志 golden(field-mapping.md §三.7)。
func TestMapDCDNGolden(t *testing.T) {
	f := loadFixture(t, "aliyun-dcdn-edge-rtlog")
	for i, raw := range f.Samples {
		e, ok := mapEntry(kindDCDN, testMeta(f), raw).(*logquery.CDNLogEntry)
		if !ok {
			t.Fatalf("sample %d: not CDNLogEntry", i)
		}
		if e.Timestamp <= 0 {
			t.Errorf("sample %d: timestamp bad(unixtime=%q)", i, raw["unixtime"])
		}
		if e.Host == "" || e.Status == 0 {
			t.Errorf("sample %d: domain/status empty", i)
		}
		if e.EdgeNode == "" && raw["via_info"] != "" {
			t.Errorf("sample %d: edge node not extracted: %q", i, raw["via_info"])
		}
	}
	e, _ := mapEntry(kindDCDN, testMeta(f), f.Samples[0]).(*logquery.CDNLogEntry)
	if e.Timestamp != 1787824545000 {
		t.Errorf("timestamp = %d, want 1787824545000", e.Timestamp)
	}
	if e.EdgeNode != "cache15.l2eu95-4[161,0]" {
		t.Errorf("edge node = %q", e.EdgeNode)
	}
}

// TestMapAkamaiCDNGolden Akamai CDN golden(field-mapping.md §三.8;坑位:reqTimeSec
// 为时刻戳、UA 为 URL 编码、cacheStatus 码表)。
func TestMapAkamaiCDNGolden(t *testing.T) {
	f := loadFixture(t, "aliyun-akamai-cdn")
	for i, raw := range f.Samples {
		e, ok := mapEntry(kindAkamaiCDN, testMeta(f), raw).(*logquery.CDNLogEntry)
		if !ok {
			t.Fatalf("sample %d: not CDNLogEntry", i)
		}
		if e.Timestamp <= 0 {
			t.Errorf("sample %d: timestamp bad(reqTimeSec=%q)", i, raw["reqTimeSec"])
		}
		if e.UserAgent != "" && e.UserAgent == raw["UA"] && strings.Contains(raw["UA"], "%") {
			t.Errorf("sample %d: UA not url-decoded: %q", i, e.UserAgent)
		}
		if e.CacheHit != "hit" && raw["cacheStatus"] == "0" {
			t.Errorf("sample %d: akamai cacheStatus 0 -> %q, want hit", i, e.CacheHit)
		}
	}
	e, _ := mapEntry(kindAkamaiCDN, testMeta(f), f.Samples[0]).(*logquery.CDNLogEntry)
	if e.Timestamp != 1787225893800 {
		t.Errorf("timestamp = %d, want 1787225893800(reqTimeSec 1787225893.800)", e.Timestamp)
	}
}

// TestMapAkamaiWAFGolden Akamai WAF golden(field-mapping.md §二.5;坑位:start 为秒,
// _unixtimestamp_ 为纳秒,severity 为 1~10)。
func TestMapAkamaiWAFGolden(t *testing.T) {
	f := loadFixture(t, "aliyun-akamai-waf")
	for i, raw := range f.Samples {
		e, ok := mapEntry(kindAkamaiWAF, testMeta(f), raw).(*logquery.WAFLogEntry)
		if !ok {
			t.Fatalf("sample %d: not WAFLogEntry", i)
		}
		if e.Timestamp <= 0 {
			t.Errorf("sample %d: timestamp bad(start=%q)", i, raw["start"])
		}
		if e.Action == "" {
			t.Errorf("sample %d: action empty(act=%q)", i, raw["act"])
		}
		if e.RuleName == "" && raw["name"] != "" {
			t.Errorf("sample %d: rule name lost", i)
		}
	}
	e, _ := mapEntry(kindAkamaiWAF, testMeta(f), f.Samples[0]).(*logquery.WAFLogEntry)
	if e.Timestamp != 1787824470000 {
		t.Errorf("timestamp = %d, want 1787824470000(start=1787824470 秒)", e.Timestamp)
	}
	if e.Action != "alert" {
		t.Errorf("action = %q, want alert(act=alert)", e.Action)
	}
	if e.Host != "wmsc.lcsc.com" {
		t.Errorf("host = %q, want wmsc.lcsc.com", e.Host)
	}
}

// TestMapCDNMonitorGolden CDN 实时投递(监控)golden:与 DCDN 边缘实时同构,
// 复用 kindDCDN mapper(fixture: aliyun-cdn-monitor,2026-09-04 实时拉取)。
func TestMapCDNMonitorGolden(t *testing.T) {
	f := loadFixture(t, "aliyun-cdn-monitor")
	for i, raw := range f.Samples {
		e, ok := mapEntry(kindDCDN, testMeta(f), raw).(*logquery.CDNLogEntry)
		if !ok {
			t.Fatalf("sample %d: not CDNLogEntry", i)
		}
		if e.Timestamp <= 0 || e.Host == "" || e.Status == 0 {
			t.Errorf("sample %d: core fields bad: %+v", i, e)
		}
		if e.Host != raw["domain"] {
			t.Errorf("sample %d: host = %q, want domain %q", i, e.Host, raw["domain"])
		}
	}
	e, _ := mapEntry(kindDCDN, testMeta(f), f.Samples[0]).(*logquery.CDNLogEntry)
	if e.Timestamp != 1788488570000 {
		t.Errorf("timestamp = %d, want 1788488570000", e.Timestamp)
	}
	if e.Host != "static-passport.jlc.com" {
		t.Errorf("host = %q, want static-passport.jlc.com", e.Host)
	}
	// request_time 已是 ms 语义(不能套 ALB 的秒->ms 换算,曾放大 1000 倍)
	if want := int64(2); e.LatencyMs != want {
		t.Errorf("latency = %d, want %d(request_time 原值即 ms)", e.LatencyMs, want)
	}
}

// TestMapCDNOfflineGolden CDN 离线转存(独立 PascalCase schema)golden
// (field-mapping.md §三.10;fixture: aliyun-cdn-offline,2026-09-04 实时拉取)。
// 坑位:Time 为 nginx 格式 '3/Sep/2026:11:23:01 +0800';域名藏在 RequestURL 里;
// RequestTime 为 ms 语义(实测 78~194 与 API 耗时吻合)。
func TestMapCDNOfflineGolden(t *testing.T) {
	f := loadFixture(t, "aliyun-cdn-offline")
	for i, raw := range f.Samples {
		e, ok := mapEntry(kindCDNOffline, testMeta(f), raw).(*logquery.CDNLogEntry)
		if !ok {
			t.Fatalf("sample %d: not CDNLogEntry", i)
		}
		if e.Timestamp <= 0 {
			t.Errorf("sample %d: timestamp bad(Time=%q)", i, raw["Time"])
		}
		if e.Host == "" || e.Host != hostOf(raw["RequestURL"]) {
			t.Errorf("sample %d: host not parsed from RequestURL: %q vs %q", i, e.Host, raw["RequestURL"])
		}
		if e.URL != raw["RequestURL"] || e.ClientIP != raw["RemoteIP"] {
			t.Errorf("sample %d: url/clientip not mapped: %q / %q", i, e.URL, e.ClientIP)
		}
		if e.Status == 0 || e.BytesSent == 0 {
			t.Errorf("sample %d: status/bytes empty", i)
		}
		if len(e.Raw) != len(raw) {
			t.Errorf("sample %d: raw not preserved: %d != %d", i, len(e.Raw), len(raw))
		}
	}
	e, _ := mapEntry(kindCDNOffline, testMeta(f), f.Samples[0]).(*logquery.CDNLogEntry)
	if e.Timestamp != 1788405781000 {
		t.Errorf("timestamp = %d, want 1788405781000('3/Sep/2026:11:23:01 +0800' -> UTC)", e.Timestamp)
	}
	if e.Status != 200 || e.BytesSent != 507 || e.LatencyMs != 194 {
		t.Errorf("status/bytes/latency = %d/%d/%d, want 200/507/194", e.Status, e.BytesSent, e.LatencyMs)
	}
	if e.CacheHit != "miss" {
		t.Errorf("cache hit = %q, want miss(HitInfo=MISS)", e.CacheHit)
	}
	if e.Meta.ResourceID != "www.jlcfa.com" {
		t.Errorf("resource id = %q, want www.jlcfa.com(域名取自 RequestURL)", e.Meta.ResourceID)
	}
}

// hostOf 从 URL 提取 host(测试辅助,与 mapper 内 domainFromURL 同逻辑)。
func hostOf(u string) string {
	if u2, err := url.Parse(u); err == nil {
		return u2.Hostname()
	}
	return ""
}

// TestMapEntryUnknownKind 未知 kind 返回 nil(新增源未配 mapper 时不静默产出错误数据)。
func TestMapEntryUnknownKind(t *testing.T) {
	if got := mapEntry(mapperKind("nope"), testMeta(fixture{}), map[string]string{"a": "b"}); got != nil {
		t.Errorf("unknown kind should return nil, got %v", got)
	}
}

// TestDomainField 混装源的域名/host 原始字段名(按域名扇出的分组键;
// CDN 与 WAF 源均为域名粒度选择,ALB/转存整流查询无此维度)。
func TestDomainField(t *testing.T) {
	cases := map[mapperKind]string{
		kindDCDN:      "domain",
		kindAkamaiCDN: "reqHost",
		kindWAF3:      "host",
		kindAkamaiWAF: "dhost",
		kindALB:       "",
	}
	for kind, want := range cases {
		if got := domainField(kind); got != want {
			t.Errorf("domainField(%q) = %q, want %q", kind, got, want)
		}
	}
}

// TestSplitByDomain 探查样本按域名分组(空域名行丢弃,分组键正确)。
func TestSplitByDomain(t *testing.T) {
	logs := []map[string]string{
		{"domain": "a.com"},
		{"domain": "b.com"},
		{"domain": "a.com"},
		{"domain": ""},     // 空域名丢弃
		{"reqHost": "c.com"}, // 非 domain 键,DCDN 分组下丢弃
	}
	got := splitByDomain(kindDCDN, logs)
	if len(got) != 2 {
		t.Fatalf("groups = %d, want 2: %v", len(got), got)
	}
	if len(got["a.com"]) != 2 || len(got["b.com"]) != 1 {
		t.Errorf("group counts wrong: %v", got)
	}
	if splitByDomain(kindALB, logs) != nil {
		t.Error("non-CDN kind should return nil")
	}
}

// TestDomainCacheTTL 域名枚举缓存:命中返回缓存副本+命中标记、过期重取、缓存 nil 不存。
func TestDomainCacheTTL(t *testing.T) {
	c := newDomainCache()
	calls := 0
	fetch := func() []string {
		calls++
		return []string{"a.com", "b.com"}
	}
	got, hit := c.get("k1", fetch)
	if hit || len(got) != 2 || calls != 1 {
		t.Fatalf("first get = %v hit=%v calls=%d", got, hit, calls)
	}
	got, hit = c.get("k1", fetch) // TTL 内命中
	if !hit || calls != 1 {
		t.Errorf("second get should hit cache, hit=%v calls=%d", hit, calls)
	}
	// 过期
	c.expire("k1")
	got, hit = c.get("k1", fetch)
	if hit || calls != 2 {
		t.Errorf("expired entry should refetch, hit=%v calls=%d", hit, calls)
	}
	// 空 result 不缓存(源数据未就绪时不长期锁死)
	c.set("k2", nil)
	if calls2 := 0; calls2 != 0 {
		t.Error("unreachable")
	}
	got, hit = c.get("k2", func() []string { return []string{"x.com"} })
	if hit || len(got) != 1 {
		t.Errorf("nil cached should fall back to fetch, hit=%v got=%v", hit, got)
	}
}
// 字段集完全相同 = 同一条日志)。曾用 CDN 字段名做键,WAF 行字段名不同
// 全落空键被去重到 1 条。
func TestDedupProbeOverlap(t *testing.T) {
	logs := []map[string]string{
		{"uuid": "r1", "domain": "a.com", "unixtime": "100"},
		{"uuid": "r1", "domain": "a.com", "unixtime": "100"}, // 探查+查询重叠(全同)
		{"uuid": "r2", "domain": "a.com", "unixtime": "101"},
		{"host": "b.com", "start_time": "100", "real_client_ip": "1.1.1.1"}, // WAF 形态
		{"host": "b.com", "start_time": "100", "real_client_ip": "1.1.1.1"}, // 同上,重复
		{"host": "b.com", "start_time": "102", "real_client_ip": "1.1.1.1"},
	}
	got := dedupLogs(logs)
	if len(got) != 4 {
		t.Fatalf("dedup = %d, want 4: %v", len(got), got)
	}
	if dedupLogs(nil) != nil {
		t.Error("nil input should return nil")
	}
}

// TestDomainNamesFromSample 域名清单热度序(样本数降序;ListLogSources
// 域名枚举与 Search 扇出共用此排序)。
func TestDomainNamesFromSample(t *testing.T) {
	probe := []map[string]string{
		{"domain": "hot.com"},
		{"domain": "warm.com"},
		{"domain": "hot.com"},
		{"domain": "hot.com"},
		{"domain": ""}, // 空域名丢弃
	}
	got := domainNamesFromSample(kindDCDN, probe)
	want := []string{"hot.com", "warm.com"}
	if len(got) != len(want) {
		t.Fatalf("domains = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("domains[%d] = %q, want %q(热度序:样本数降序)", i, got[i], want[i])
		}
	}
	if got := domainNamesFromSample(kindDCDN, nil); len(got) != 0 {
		t.Errorf("empty probe should yield no domains, got %v", got)
	}
	if got := domainNamesFromSample(kindALB, probe); len(got) != 0 {
		t.Errorf("non-CDN kind should yield no domains, got %v", got)
	}
}
