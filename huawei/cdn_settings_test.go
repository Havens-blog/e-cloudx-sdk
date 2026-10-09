package huawei

import (
	"context"
	"strings"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	cdnmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cdn/v2/model"
)

// 测试用指针构造器(仅本文件使用)。
func hwStr(v string) *string { return &v }
func hwI32(v int32) *int32   { return &v }
func hwBool(v bool) *bool    { return &v }

// hwFindItem 按分组/键查找配置项。
func hwFindItem(s *types.CDNDomainSettings, category, key string) (types.CDNConfigSetting, bool) {
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

// hwCheck 断言配置项的摘要子串与启用态。
func hwCheck(t *testing.T, s *types.CDNDomainSettings, category, key string, subs []string, enabled *bool) {
	t.Helper()
	it, ok := hwFindItem(s, category, key)
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

// TestHuaweiDomainSettingsFull 构造 ConfigsGetBody 全景,验证典型字段映射。
func TestHuaweiDomainSettingsFull(t *testing.T) {
	cfg := &cdnmodel.ConfigsGetBody{
		IpFilter:        &cdnmodel.IpFilter{Type: "white", Value: hwStr("1.2.3.4,5.6.7.0/24")},
		Referer:         &cdnmodel.RefererConfig{Type: "black", Value: hwStr("a.com,b.com"), IncludeEmpty: hwBool(true)},
		UserAgentFilter: &cdnmodel.UserAgentFilter{Type: "white", UaList: &[]string{"GoodBot"}},
		UrlAuth: &cdnmodel.UrlAuthGetBody{
			Status: "on", Type: hwStr("type_a"), ExpireTime: hwI32(3600), Key: hwStr("secret-key-material"),
		},
		RemoteAuth:            &cdnmodel.CommonRemoteAuth{RemoteAuthentication: "on"},
		IpFrequencyLimit:      &cdnmodel.IpFrequencyLimitQuery{Status: "on", Qps: hwI32(100)},
		FlowLimitStrategy:     &[]cdnmodel.FlowLimitStrategy{{StrategyType: "instant", ItemType: "bandwidth", LimitValue: 1000000000}},
		Compress:              &cdnmodel.Compress{Status: "on", Type: hwStr("gzip"), FileType: hwStr(".js,.css")},
		OriginRangeStatus:     hwStr("on"),
		Websocket:             &cdnmodel.WebSocketSeek{Status: "on", Timeout: hwI32(60)},
		VideoSeek:             &cdnmodel.VideoSeek{EnableVideoSeek: true},
		Quic:                  &cdnmodel.Quic{Status: "on"},
		Https:                 &cdnmodel.HttpGetBody{HttpsStatus: hwStr("on"), CertificateName: hwStr("cert-a"), Http2Status: hwStr("on"), TlsVersion: hwStr("TLSv1.2")},
		HttpsTlsVersion:       &cdnmodel.HttpsTlsVersion{TlsVersion: "TLSv1.2,TLSv1.3", CipherSuiteGroup: "strict"},
		ForceRedirect:         &cdnmodel.ForceRedirectConfig{Status: "on", Type: hwStr("https"), RedirectCode: hwI32(301)},
		Hsts:                  &cdnmodel.HstsQuery{Status: "on", MaxAge: hwI32(86400), IncludeSubdomains: hwStr("on")},
		Sources:               &[]cdnmodel.SourcesConfigResponseBody{{OriginType: "domain", OriginAddr: "origin.example.com", HostName: hwStr("src.example.com")}},
		OriginProtocol:        hwStr("https"),
		OriginFollow302Status: hwStr("on"),
		Sni:                   &cdnmodel.Sni{Status: "on", ServerName: hwStr("src.example.com")},
		OriginReceiveTimeout:  hwI32(30),
		Ipv6Accelerate:        hwI32(1),
		ServiceArea:           hwStr("global"),
		BusinessType:          hwStr("web"),
	}

	s := buildHuaweiSettings("www.example.com", cfg)
	if s.Domain != "www.example.com" {
		t.Fatalf("domain = %q", s.Domain)
	}
	wantCats := []string{"access_control", "traffic_limit", "performance", "https", "origin", "basic"}
	if len(s.Groups) != len(wantCats) {
		t.Fatalf("groups = %d, want %d", len(s.Groups), len(wantCats))
	}
	for i, cat := range wantCats {
		if s.Groups[i].Category != cat {
			t.Errorf("groups[%d].Category = %q, want %q", i, s.Groups[i].Category, cat)
		}
	}

	hwCheck(t, s, "access_control", "ip_filter", []string{"白名单", "1.2.3.4"}, hwBool(true))
	hwCheck(t, s, "access_control", "referer_filter", []string{"黑名单", "a.com", "空 Referer"}, hwBool(true))
	hwCheck(t, s, "access_control", "ua_filter", []string{"白名单", "GoodBot"}, hwBool(true))
	// URL 鉴权:密钥脱敏,不得出现明文
	auth, _ := hwFindItem(s, "access_control", "url_auth")
	if !strings.Contains(auth.Summary, "鉴权方式 A") || !strings.Contains(auth.Summary, "3600") {
		t.Errorf("url_auth summary=%q", auth.Summary)
	}
	if auth.Enabled == nil || !*auth.Enabled {
		t.Errorf("url_auth enabled=%v", auth.Enabled)
	}
	if v, ok := auth.Params["key"]; !ok || v != "******" {
		t.Errorf("url_auth key 应脱敏, got %q", auth.Params["key"])
	}
	hwCheck(t, s, "access_control", "remote_auth", []string{"远端服务"}, hwBool(true))

	hwCheck(t, s, "traffic_limit", "ip_frequency_limit", []string{"100 次/秒"}, hwBool(true))
	hwCheck(t, s, "traffic_limit", "flow_limit_strategy", []string{"带宽封顶", "停用"}, hwBool(true))

	hwCheck(t, s, "performance", "compress", []string{"已开启", "gzip", ".js,.css"}, hwBool(true))
	hwCheck(t, s, "performance", "range_origin", []string{"已开启"}, hwBool(true))
	hwCheck(t, s, "performance", "websocket", []string{"60s"}, hwBool(true))
	hwCheck(t, s, "performance", "video_seek", []string{"已开启"}, hwBool(true))
	hwCheck(t, s, "performance", "quic", []string{"QUIC"}, hwBool(true))

	hwCheck(t, s, "https", "https", []string{"HTTP/2", "cert-a"}, hwBool(true))
	hwCheck(t, s, "https", "tls_version", []string{"TLS 1.2", "strict"}, nil)
	hwCheck(t, s, "https", "force_redirect", []string{"HTTPS", "301"}, hwBool(true))
	hwCheck(t, s, "https", "hsts", []string{"max-age=86400", "includeSubDomains=on"}, hwBool(true))

	hwCheck(t, s, "origin", "sources", []string{"origin.example.com"}, nil)
	hwCheck(t, s, "origin", "origin_host", []string{"src.example.com"}, nil)
	hwCheck(t, s, "origin", "origin_protocol", []string{"HTTPS"}, nil)
	hwCheck(t, s, "origin", "follow_302", []string{}, hwBool(true))
	hwCheck(t, s, "origin", "sni", []string{"src.example.com"}, hwBool(true))
	hwCheck(t, s, "origin", "origin_timeout", []string{"30 秒"}, nil)

	hwCheck(t, s, "basic", "ipv6", []string{"已开启"}, hwBool(true))
	hwCheck(t, s, "basic", "service_region", []string{"全球"}, nil)
	hwCheck(t, s, "basic", "business_type", []string{"网站加速"}, nil)
}

// TestHuaweiDomainSettingsEmpty nil/空配置返回仅 Domain 的空结构,不 panic。
func TestHuaweiDomainSettingsEmpty(t *testing.T) {
	s := buildHuaweiSettings("d.example.com", nil)
	if s.Domain != "d.example.com" || len(s.Groups) != 0 {
		t.Fatalf("nil config: %+v", s)
	}
	s2 := buildHuaweiSettings("d.example.com", &cdnmodel.ConfigsGetBody{})
	if s2.Domain != "d.example.com" || len(s2.Groups) != 0 {
		t.Fatalf("empty config: %+v", s2)
	}
}

// TestHuaweiDomainSettingsACLOff 白名单关闭(type=off)时 enabled=false。
func TestHuaweiDomainSettingsACLOff(t *testing.T) {
	cfg := &cdnmodel.ConfigsGetBody{
		IpFilter: &cdnmodel.IpFilter{Type: "off", Value: hwStr("1.2.3.4")},
		Https:    &cdnmodel.HttpGetBody{},
	}
	s := buildHuaweiSettings("d.example.com", cfg)
	hwCheck(t, s, "access_control", "ip_filter", []string{"未开启"}, hwBool(false))
	// Https 结构存在但 HttpsStatus 为空 → 不可判定(nil)
	if it, ok := hwFindItem(s, "https", "https"); !ok || it.Enabled != nil {
		t.Errorf("https enabled 应为 nil(不可判定), got %+v", it)
	}
}

// TestGetDomainSettingsRequiresDomain 华为以域名为检索键,缺失时报错(不触网)。
func TestGetDomainSettingsRequiresDomain(t *testing.T) {
	a := &CDNAdapter{}
	_, err := a.GetDomainSettings(context.Background(), "", "")
	if err == nil || !strings.Contains(err.Error(), "需要域名") {
		t.Fatalf("应报缺少域名错误, got %v", err)
	}
}
