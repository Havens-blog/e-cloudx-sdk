package aws

import (
	"context"
	"strings"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
)

// 测试用指针构造器(仅本文件使用)。
func cfStr(v string) *string { return &v }
func cfBool(v bool) *bool    { return &v }
func cfInt32(v int32) *int32 { return &v }

// cfFindItem 按分组/键查找配置项。
func cfFindItem(s *types.CDNDomainSettings, category, key string) (types.CDNConfigSetting, bool) {
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

// cfCheck 断言配置项的摘要子串与启用态。
func cfCheck(t *testing.T, s *types.CDNDomainSettings, category, key string, subs []string, enabled *bool) {
	t.Helper()
	it, ok := cfFindItem(s, category, key)
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

// TestCloudFrontDomainSettingsFull 构造 DistributionConfig 全景,验证典型字段映射。
func TestCloudFrontDomainSettingsFull(t *testing.T) {
	cfg := &cftypes.DistributionConfig{
		DefaultCacheBehavior: &cftypes.DefaultCacheBehavior{
			ViewerProtocolPolicy: cftypes.ViewerProtocolPolicyRedirectToHttps,
			Compress:             cfBool(true),
			SmoothStreaming:      cfBool(true),
		},
		ViewerCertificate: &cftypes.ViewerCertificate{
			ACMCertificateArn:      cfStr("arn:aws:acm:us-east-1:123456789012:certificate/abc-123"),
			MinimumProtocolVersion: cftypes.MinimumProtocolVersionTLSv122021,
			SSLSupportMethod:       cftypes.SSLSupportMethodSniOnly,
		},
		HttpVersion:   cftypes.HttpVersionHttp2and3,
		IsIPV6Enabled: cfBool(true),
		Restrictions: &cftypes.Restrictions{
			GeoRestriction: &cftypes.GeoRestriction{
				RestrictionType: cftypes.GeoRestrictionTypeBlacklist,
				Items:           []string{"CN", "RU", "IN"},
			},
		},
		PriceClass:        cftypes.PriceClassPriceClass200,
		Aliases:           &cftypes.Aliases{Items: []string{"www.example.com", "img.example.com"}},
		DefaultRootObject: cfStr("index.html"),
		Logging: &cftypes.LoggingConfig{
			Enabled: cfBool(true), Bucket: cfStr("logs.example.com.s3.amazonaws.com"), Prefix: cfStr("cdn/"),
		},
		Origins: &cftypes.Origins{Items: []cftypes.Origin{
			{
				Id:         cfStr("origin-1"),
				DomainName: cfStr("origin.example.com"),
				CustomOriginConfig: &cftypes.CustomOriginConfig{
					OriginProtocolPolicy: cftypes.OriginProtocolPolicyMatchViewer,
					OriginReadTimeout:    cfInt32(30),
				},
			},
		}},
	}

	s := buildCloudFrontSettings("www.example.com", cfg)
	if s.Domain != "www.example.com" {
		t.Fatalf("domain = %q", s.Domain)
	}
	// 分组按统一顺序输出
	wantCats := []string{"access_control", "performance", "https", "origin", "basic"}
	if len(s.Groups) != len(wantCats) {
		t.Fatalf("groups = %d, want %d", len(s.Groups), len(wantCats))
	}
	for i, cat := range wantCats {
		if s.Groups[i].Category != cat {
			t.Errorf("groups[%d].Category = %q, want %q", i, s.Groups[i].Category, cat)
		}
	}

	cfCheck(t, s, "access_control", "geo_restriction", []string{"黑名单", "3 个国家/地区", "CN"}, cfBool(true))
	cfCheck(t, s, "performance", "compress", []string{"已开启"}, cfBool(true))
	cfCheck(t, s, "performance", "smooth_streaming", []string{"已开启"}, cfBool(true))
	cfCheck(t, s, "https", "viewer_protocol_policy", []string{"301", "HTTPS"}, cfBool(true))
	cfCheck(t, s, "https", "https_certificate", []string{"ACM 证书", "abc-123"}, cfBool(true))
	cfCheck(t, s, "https", "tls_min_version", []string{"TLSv1.2"}, nil)
	cfCheck(t, s, "https", "http_version", []string{"HTTP/2 + HTTP/3"}, nil)
	cfCheck(t, s, "origin", "origins", []string{"1 个源站", "origin.example.com"}, nil)
	cfCheck(t, s, "origin", "origin_protocol", []string{"协议跟随"}, nil)
	cfCheck(t, s, "origin", "origin_timeout", []string{"30 秒"}, nil)
	cfCheck(t, s, "basic", "ipv6", []string{"已开启"}, cfBool(true))
	cfCheck(t, s, "basic", "aliases", []string{"2 个", "www.example.com"}, nil)
	cfCheck(t, s, "basic", "price_class", []string{"200 级"}, nil)
	cfCheck(t, s, "basic", "access_logging", []string{"logs.example.com", "cdn/"}, cfBool(true))
	cfCheck(t, s, "basic", "default_root_object", []string{"index.html"}, nil)
}

// TestCloudFrontDomainSettingsGeoNone 地理限制 none → 未开启且摘要"未限制"。
func TestCloudFrontDomainSettingsGeoNone(t *testing.T) {
	cfg := &cftypes.DistributionConfig{
		Restrictions: &cftypes.Restrictions{
			GeoRestriction: &cftypes.GeoRestriction{RestrictionType: cftypes.GeoRestrictionTypeNone},
		},
	}
	s := buildCloudFrontSettings("x.example.com", cfg)
	cfCheck(t, s, "access_control", "geo_restriction", []string{"未限制"}, cfBool(false))
}

// TestCloudFrontDomainSettingsEmpty nil/空配置返回仅 Domain 的空结构,不 panic。
func TestCloudFrontDomainSettingsEmpty(t *testing.T) {
	s := buildCloudFrontSettings("d.example.com", nil)
	if s.Domain != "d.example.com" || len(s.Groups) != 0 {
		t.Fatalf("nil config: %+v", s)
	}
	s2 := buildCloudFrontSettings("d.example.com", &cftypes.DistributionConfig{})
	if s2.Domain != "d.example.com" || len(s2.Groups) != 0 {
		t.Fatalf("empty config: %+v", s2)
	}
}

// TestGetDomainSettingsRequiresID CloudFront 仅认分配 ID,缺失时直接报错(不触网)。
func TestGetDomainSettingsRequiresID(t *testing.T) {
	a := &CDNAdapter{}
	_, err := a.GetDomainSettings(context.Background(), "www.example.com", "")
	if err == nil || !strings.Contains(err.Error(), "分配 ID") {
		t.Fatalf("应报缺少分配 ID 错误, got %v", err)
	}
}
