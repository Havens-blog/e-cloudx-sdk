package tencent

import (
	"context"
	"strings"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	tencentcdn "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cdn/v20180606"
)

// 测试用指针构造器(仅本文件使用)。
func tcStr(v string) *string    { return &v }
func tcInt64(v int64) *int64    { return &v }
func tcUint64(v uint64) *uint64 { return &v }
func tcBool(v bool) *bool       { return &v }

// tcFindItem 按分组/键查找配置项。
func tcFindItem(s *types.CDNDomainSettings, category, key string) (types.CDNConfigSetting, bool) {
	for _, g := range s.Groups {
		if g.Category != category {
			continue
		}
		for _, it := range g.Items {
			if it.Key == key {
				return it, true
			}
		}
	}
	return types.CDNConfigSetting{}, false
}

// tcCheck 断言配置项的摘要子串与启用态。
func tcCheck(t *testing.T, s *types.CDNDomainSettings, category, key string, subs []string, enabled *bool) {
	t.Helper()
	it, ok := tcFindItem(s, category, key)
	if !ok {
		t.Fatalf("缺少配置项 %s/%s", category, key)
	}
	for _, sub := range subs {
		if !strings.Contains(it.Summary, sub) {
			t.Errorf("%s/%s summary=%q 应包含 %q", category, key, it.Summary, sub)
		}
	}
	if (it.Enabled == nil) != (enabled == nil) {
		t.Errorf("%s/%s enabled=%v 应为 %v", category, key, it.Enabled, enabled)
	} else if enabled != nil && *it.Enabled != *enabled {
		t.Errorf("%s/%s enabled=%v 应为 %v", category, key, *it.Enabled, *enabled)
	}
}

// TestTencentDomainSettingsFull 构造 DetailDomain 全景,验证典型字段映射。
func TestTencentDomainSettingsFull(t *testing.T) {
	d := &tencentcdn.DetailDomain{
		Area:        tcStr("global"),
		ServiceType: tcStr("web"),
		IpFilter: &tencentcdn.IpFilter{
			Switch: tcStr("on"), FilterType: tcStr("blacklist"),
			Filters: []*string{tcStr("1.2.3.4"), tcStr("5.6.7.0/24")},
		},
		IpFreqLimit: &tencentcdn.IpFreqLimit{Switch: tcStr("on"), Qps: tcInt64(50)},
		Referer: &tencentcdn.Referer{
			Switch: tcStr("on"),
			RefererRules: []*tencentcdn.RefererRule{{
				RefererType: tcStr("whitelist"), Referers: []*string{tcStr("a.com"), tcStr("b.com")}, AllowEmpty: tcBool(true),
			}},
		},
		UserAgentFilter: &tencentcdn.UserAgentFilter{
			Switch: tcStr("on"),
			FilterRules: []*tencentcdn.UserAgentFilterRule{{
				FilterType: tcStr("blacklist"), UserAgents: []*string{tcStr("BadBot")},
			}},
		},
		Authentication: &tencentcdn.Authentication{
			Switch: tcStr("on"), AuthAlgorithm: tcStr("md5"),
			TypeA: &tencentcdn.AuthenticationTypeA{SecretKey: tcStr("sk-123456"), ExpireTime: tcInt64(7200)},
		},
		AccessControl: &tencentcdn.AccessControl{
			Switch: tcStr("on"), AccessControlRules: []*tencentcdn.AccessControlRule{{}},
		},
		GeoBlocker: &tencentcdn.GeoBlocker{Switch: tcStr("off"), BlockRules: []*tencentcdn.GeoBlockStrategy{{}}},
		BandwidthAlert: &tencentcdn.BandwidthAlert{
			Switch: tcStr("on"), Metric: tcStr("bandwidth"), BpsThreshold: tcInt64(1000000000), CounterMeasure: tcStr("RETURN_404"),
		},
		DownstreamCapping: &tencentcdn.DownstreamCapping{
			Switch: tcStr("on"),
			CappingRules: []*tencentcdn.CappingRule{{
				RuleType: tcStr("all"), RulePaths: []*string{tcStr("*")}, KBpsThreshold: tcInt64(1024),
			}},
		},
		Compression: &tencentcdn.Compression{
			Switch: tcStr("on"),
			CompressionRules: []*tencentcdn.CompressionRule{{
				Compress: tcBool(true), Algorithms: []*string{tcStr("gzip"), tcStr("brotli")},
			}},
		},
		RangeOriginPull: &tencentcdn.RangeOriginPull{Switch: tcStr("on")},
		VideoSeek:       &tencentcdn.VideoSeek{Switch: tcStr("on")},
		Quic:            &tencentcdn.Quic{Switch: tcStr("on")},
		Seo:             &tencentcdn.Seo{Switch: tcStr("on")},
		WebSocket:       &tencentcdn.WebSocket{Switch: tcStr("on"), Timeout: tcInt64(120)},
		OfflineCache:    &tencentcdn.OfflineCache{Switch: tcStr("on")},
		Https: &tencentcdn.Https{
			Switch:       tcStr("on"),
			Http2:        tcStr("on"),
			OcspStapling: tcStr("on"),
			TlsVersion:   []*string{tcStr("TLSv1.2"), tcStr("TLSv1.3")},
			CertInfo:     &tencentcdn.ServerCert{CertName: tcStr("tencent-cert")},
			Hsts:         &tencentcdn.Hsts{Switch: tcStr("on"), MaxAge: tcInt64(7200), IncludeSubDomains: tcStr("on")},
		},
		ForceRedirect: &tencentcdn.ForceRedirect{
			Switch: tcStr("on"), RedirectType: tcStr("https"), RedirectStatusCode: tcInt64(302),
		},
		UrlRedirect: &tencentcdn.UrlRedirect{
			Switch: tcStr("on"),
			PathRules: []*tencentcdn.UrlRedirectRule{{
				RedirectStatusCode: tcInt64(301), Pattern: tcStr("/old"), RedirectUrl: tcStr("/new"),
			}},
		},
		Origin: &tencentcdn.Origin{
			Origins:            []*string{tcStr("1.1.1.1"), tcStr("2.2.2.2")},
			OriginType:         tcStr("ip"),
			ServerName:         tcStr("src.example.com"),
			OriginPullProtocol: tcStr("follow"),
			BackupOrigins:      []*string{tcStr("9.9.9.9")},
			BackupOriginType:   tcStr("ip"),
		},
		FollowRedirect: &tencentcdn.FollowRedirect{Switch: tcStr("on")},
		RequestHeader: &tencentcdn.RequestHeader{
			Switch: tcStr("on"),
			HeaderRules: []*tencentcdn.HttpHeaderPathRule{{
				HeaderMode: tcStr("add"), HeaderName: tcStr("X-From-CDN"), HeaderValue: tcStr("1"),
				RuleType: tcStr("all"), RulePaths: []*string{tcStr("*")},
			}},
		},
		ResponseHeader:    &tencentcdn.ResponseHeader{Switch: tcStr("off")},
		OriginPullTimeout: &tencentcdn.OriginPullTimeout{ConnectTimeout: tcUint64(5), ReceiveTimeout: tcUint64(60)},
		Ipv6Access:        &tencentcdn.Ipv6Access{Switch: tcStr("on")},
		Ipv6:              &tencentcdn.Ipv6{Switch: tcStr("off")},
	}

	s := buildTencentSettings("www.example.com", d)
	if s.Domain != "www.example.com" {
		t.Fatalf("domain = %q", s.Domain)
	}
	wantCats := []string{"access_control", "traffic_limit", "performance", "https", "redirect", "origin", "basic"}
	if len(s.Groups) != len(wantCats) {
		t.Fatalf("groups = %d, want %d", len(s.Groups), len(wantCats))
	}
	for i, cat := range wantCats {
		if s.Groups[i].Category != cat {
			t.Errorf("groups[%d].Category = %q, want %q", i, s.Groups[i].Category, cat)
		}
	}

	chk := func(cat, key string, subs []string, on bool) {
		tcCheck(t, s, cat, key, subs, tcBool(on))
	}
	chk("access_control", "ip_filter", []string{"黑名单", "1.2.3.4"}, true)
	chk("access_control", "referer_filter", []string{"白名单", "a.com", "空 Referer"}, true)
	chk("access_control", "ua_filter", []string{"黑名单", "BadBot"}, true)
	chk("traffic_limit", "ip_freq_limit", []string{"50 次/秒"}, true)
	chk("access_control", "access_control", []string{"1 条访问规则"}, true)
	chk("access_control", "geo_blocker", []string{"1 条地区规则"}, false)
	chk("traffic_limit", "bandwidth_alert", []string{"带宽封顶", "RETURN_404"}, true)
	chk("traffic_limit", "downstream_capping", []string{"1024 KB/s"}, true)
	chk("performance", "compression", []string{"已开启", "GZIP/BROTLI"}, true)
	chk("performance", "range_origin_pull", []string{"已开启"}, true)
	chk("performance", "video_seek", []string{"已开启"}, true)
	chk("performance", "quic", []string{"已开启"}, true)
	chk("performance", "seo", []string{"已开启"}, true)
	chk("performance", "websocket", []string{"120s"}, true)
	chk("performance", "offline_cache", []string{"过期缓存"}, true)
	chk("https", "https", []string{"HTTP/2", "tencent-cert"}, true)
	tcCheck(t, s, "https", "tls_version", []string{"TLS 1.2", "TLS 1.3"}, nil)
	chk("https", "hsts", []string{"max-age=7200", "includeSubDomains=on"}, true)
	chk("https", "force_redirect", []string{"HTTPS", "302"}, true)
	chk("redirect", "url_redirect", []string{"301", "/old", "/new"}, true)
	tcCheck(t, s, "origin", "origins", []string{"2 个源站", "1.1.1.1"}, nil)
	tcCheck(t, s, "origin", "backup_origins", []string{"9.9.9.9"}, nil)
	tcCheck(t, s, "origin", "origin_host", []string{"src.example.com"}, nil)
	tcCheck(t, s, "origin", "origin_protocol", []string{"协议跟随"}, nil)
	chk("origin", "follow_redirect", []string{"跟随跳转"}, true)
	chk("origin", "request_header", []string{"添加 X-From-CDN"}, true)
	chk("origin", "response_header", []string{"0 条"}, false)
	tcCheck(t, s, "origin", "origin_timeout", []string{"建连 5s", "接收 60s"}, nil)
	chk("basic", "ipv6_access", []string{"已开启"}, true)
	chk("basic", "ipv6_origin", []string{"未开启"}, false)
	tcCheck(t, s, "basic", "service_region", []string{"全球"}, nil)
	tcCheck(t, s, "basic", "business_type", []string{"网页小文件"}, nil)

	// 时间戳防盗链:模式 A、密钥脱敏
	auth, ok := tcFindItem(s, "access_control", "authentication")
	if !ok {
		t.Fatal("缺少 authentication 配置项")
	}
	if !strings.Contains(auth.Summary, "模式 A") || !strings.Contains(auth.Summary, "md5") {
		t.Errorf("authentication summary=%q", auth.Summary)
	}
	if v, ok := auth.Params["secret_key"]; !ok || v != "******" {
		t.Errorf("authentication 密钥应脱敏, got %q", auth.Params["secret_key"])
	}
}

// TestTencentDomainSettingsEmpty nil/空配置返回仅 Domain 的空结构,不 panic。
func TestTencentDomainSettingsEmpty(t *testing.T) {
	s := buildTencentSettings("d.example.com", nil)
	if s.Domain != "d.example.com" || len(s.Groups) != 0 {
		t.Fatalf("nil config: %+v", s)
	}
	s2 := buildTencentSettings("d.example.com", &tencentcdn.DetailDomain{})
	if s2.Domain != "d.example.com" || len(s2.Groups) != 0 {
		t.Fatalf("empty config: %+v", s2)
	}
}

// TestGetDomainSettingsRequiresDomain 腾讯以域名为检索键,缺失时报错(不触网)。
func TestGetDomainSettingsRequiresDomain(t *testing.T) {
	a := &CDNAdapter{}
	_, err := a.GetDomainSettings(context.Background(), "", "")
	if err == nil || !strings.Contains(err.Error(), "需要域名") {
		t.Fatalf("应报缺少域名错误, got %v", err)
	}
}
