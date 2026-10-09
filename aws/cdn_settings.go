// AWS CloudFront 域名功能配置全景(DistributionConfig → 分组归一化)。
// CloudFront 只有"分配"一个产品、无 CDN/DCDN 双产品回退;以分配 ID 为检索键
// (域名无法定位分配,与 GetCacheConfig 同)。DistributionConfig 能真实提供的
// 配置:ViewerProtocolPolicy(访问协议)、TLS 最低版本、HTTP 版本、智能压缩、
// 地理限制(geo restriction)、源站协议/超时、IPv6、价格等级、访问日志等;
// CloudFront 没有可查询的 回源URL重写/UA黑白名单/带宽封顶 等配置,故不映射。
package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
)

// awsGroupOrder 分组渲染顺序(与 aliyun/volcano 实现一致)。
var awsGroupOrder = []struct{ key, label string }{
	{"access_control", "访问控制"},
	{"traffic_limit", "流量限制"},
	{"performance", "性能优化"},
	{"https", "HTTPS"},
	{"redirect", "重定向"},
	{"origin", "回源"},
	{"basic", "基础"},
}

// awsSettingsBuilder 按分组累积配置项。
type awsSettingsBuilder struct {
	domain     string
	byCategory map[string][]types.CDNConfigSetting
}

func newAWSSettingsBuilder(domain string) *awsSettingsBuilder {
	return &awsSettingsBuilder{domain: domain, byCategory: map[string][]types.CDNConfigSetting{}}
}

// add 追加一项;summary/params 为空时省略对应字段。
func (b *awsSettingsBuilder) add(category, key, name string, enabled *bool, summary string, params map[string]string) {
	item := types.CDNConfigSetting{Key: key, Name: name, Enabled: enabled, Summary: summary, Params: params}
	b.byCategory[category] = append(b.byCategory[category], item)
}

// build 按 awsGroupOrder 归组,空组不输出。
func (b *awsSettingsBuilder) build() *types.CDNDomainSettings {
	settings := &types.CDNDomainSettings{Domain: b.domain}
	for _, g := range awsGroupOrder {
		if items := b.byCategory[g.key]; len(items) > 0 {
			settings.Groups = append(settings.Groups, types.CDNConfigGroup{
				Category: g.key, Label: g.label, Items: items,
			})
		}
	}
	return settings
}

// GetDomainSettings 查询 CloudFront 分配的功能配置全景。
// CloudFront 以分配 ID 为键(域名无法定位分配),domainID 必传,与 GetCacheConfig 同约。
func (a *CDNAdapter) GetDomainSettings(ctx context.Context, domainName, domainID string) (*types.CDNDomainSettings, error) {
	if domainID == "" {
		return nil, fmt.Errorf("AWS CloudFront 功能配置需要分配 ID")
	}

	client, err := a.createClient(ctx)
	if err != nil {
		return nil, err
	}

	output, err := client.GetDistribution(ctx, &cloudfront.GetDistributionInput{
		Id: awssdk.String(domainID),
	})
	if err != nil {
		return nil, fmt.Errorf("获取CloudFront分配失败: %w", err)
	}
	if output.Distribution == nil {
		return nil, fmt.Errorf("CloudFront分配不存在: %s", domainID)
	}

	dist := output.Distribution
	domain := domainName
	if domain == "" {
		domain = awssdk.ToString(dist.DomainName)
	}
	if domain == "" {
		domain = domainID // 极端兜底:以 ID 作为展示名
	}
	return buildCloudFrontSettings(domain, dist.DistributionConfig), nil
}

// buildCloudFrontSettings DistributionConfig → 分组归一化(纯函数,便于单测)。
// cfg 为 nil 时返回仅含 Domain 的空结构(与 aliyun/volcano 的空数据约定一致,不 panic)。
func buildCloudFrontSettings(domain string, cfg *cftypes.DistributionConfig) *types.CDNDomainSettings {
	if cfg == nil {
		return &types.CDNDomainSettings{Domain: domain}
	}
	b := newAWSSettingsBuilder(domain)

	buildCFAccessControl(b, cfg)
	buildCFPerformance(b, cfg)
	buildCFHTTPS(b, cfg)
	buildCFOrigin(b, cfg)
	buildCFBasic(b, cfg)
	return b.build()
}

// buildCFAccessControl 访问控制:地理限制、WAF 绑定、签名 URL 校验。
func buildCFAccessControl(b *awsSettingsBuilder, cfg *cftypes.DistributionConfig) {
	if cfg.Restrictions != nil && cfg.Restrictions.GeoRestriction != nil {
		g := cfg.Restrictions.GeoRestriction
		enabled := g.RestrictionType != cftypes.GeoRestrictionTypeNone && g.RestrictionType != ""
		enabledPtr := &enabled
		summary := "未限制"
		if enabled {
			kind := "黑名单"
			if g.RestrictionType == cftypes.GeoRestrictionTypeWhitelist {
				kind = "白名单"
			}
			summary = fmt.Sprintf("%s %d 个国家/地区", kind, len(g.Items))
			if len(g.Items) > 0 {
				summary += ": " + awsJoinLimit(g.Items, 5)
			}
		}
		b.add("access_control", "geo_restriction", "地理访问限制", enabledPtr, summary,
			map[string]string{"type": string(g.RestrictionType), "locations": awsJoinLimit(g.Items, 10)})
	}
	if cfg.WebACLId != nil && *cfg.WebACLId != "" {
		enabled := true
		b.add("access_control", "waf_acl", "WAF 防护", &enabled,
			"WAF WebACL: "+truncateCF(*cfg.WebACLId, 60), map[string]string{"web_acl_id": *cfg.WebACLId})
	}
	if def := cfg.DefaultCacheBehavior; def != nil {
		var kgNames []string
		if def.TrustedKeyGroups != nil {
			for _, kg := range def.TrustedKeyGroups.Items {
				kgNames = append(kgNames, kg)
			}
		}
		if len(kgNames) > 0 {
			enabled := true
			b.add("access_control", "signed_url_key_groups", "签名 URL 鉴权", &enabled,
				fmt.Sprintf("%d 个 KeyGroup", len(kgNames)), map[string]string{"key_groups": awsJoinLimit(kgNames, 5)})
		}
	}
}

// buildCFPerformance 性能优化:智能压缩、Smooth Streaming。
func buildCFPerformance(b *awsSettingsBuilder, cfg *cftypes.DistributionConfig) {
	if def := cfg.DefaultCacheBehavior; def != nil {
		if def.Compress != nil {
			enabled := *def.Compress
			b.add("performance", "compress", "智能压缩", &enabled, awsOnOffText(def.Compress),
				map[string]string{"default_behavior": "true"})
		}
		if def.SmoothStreaming != nil {
			enabled := *def.SmoothStreaming
			b.add("performance", "smooth_streaming", "Smooth Streaming 视频平滑流", &enabled,
				awsOnOffText(def.SmoothStreaming), nil)
		}
	}
}

// buildCFHTTPS HTTPS:访问协议策略、证书、TLS 最低版本、HTTP 版本。
func buildCFHTTPS(b *awsSettingsBuilder, cfg *cftypes.DistributionConfig) {
	// 访问协议策略(默认缓存行为 + 路径级行为差异统计)
	if def := cfg.DefaultCacheBehavior; def != nil {
		policy := string(def.ViewerProtocolPolicy)
		if policy == "" {
			policy = string(cftypes.ViewerProtocolPolicyAllowAll)
		}
		enabled := false
		summary := "HTTP/HTTPS 均可访问"
		switch def.ViewerProtocolPolicy {
		case cftypes.ViewerProtocolPolicyRedirectToHttps:
			enabled, summary = true, "HTTP 301 跳转 HTTPS"
		case cftypes.ViewerProtocolPolicyHttpsOnly:
			enabled, summary = true, "仅 HTTPS(HTTP 返回 403)"
		}
		if cfg.CacheBehaviors != nil {
			diff := 0
			for _, cb := range cfg.CacheBehaviors.Items {
				if p := string(cb.ViewerProtocolPolicy); p != "" && p != policy {
					diff++
				}
			}
			if diff > 0 {
				summary += fmt.Sprintf("(%d 条路径规则策略不同)", diff)
			}
		}
		b.add("https", "viewer_protocol_policy", "访问协议策略", &enabled, summary,
			map[string]string{"default": policy})
	}

	// HTTPS 证书 + TLS 最低版本/SNI 能力
	if vc := cfg.ViewerCertificate; vc != nil {
		enabled := true
		summary := "HTTPS"
		params := map[string]string{}
		if vc.CloudFrontDefaultCertificate != nil && *vc.CloudFrontDefaultCertificate {
			summary += " · 默认 CloudFront 证书(*.cloudfront.net)"
			params["cert_source"] = "cloudfront_default"
		} else if vc.ACMCertificateArn != nil && *vc.ACMCertificateArn != "" {
			summary += " · ACM 证书: " + shortCFArn(*vc.ACMCertificateArn)
			params["cert_source"] = "acm"
			params["cert_arn"] = *vc.ACMCertificateArn
		} else if vc.IAMCertificateId != nil && *vc.IAMCertificateId != "" {
			summary += " · IAM 证书: " + *vc.IAMCertificateId
			params["cert_source"] = "iam"
			params["cert_id"] = *vc.IAMCertificateId
		}
		if vc.SSLSupportMethod != "" && vc.SSLSupportMethod != cftypes.SSLSupportMethodSniOnly {
			summary += "(" + snsLabelCF(string(vc.SSLSupportMethod)) + ")"
		}
		b.add("https", "https_certificate", "HTTPS 证书", &enabled, summary, params)

		// TLS 最低版本(安全策略)
		if vc.MinimumProtocolVersion != "" {
			b.add("https", "tls_min_version", "TLS 最低版本", nil,
				"TLS 最低版本: "+tlsMinLabelCF(string(vc.MinimumProtocolVersion)),
				map[string]string{"min_version": string(vc.MinimumProtocolVersion)})
		}
	}

	// HTTP 版本(默认 http2;http3 需 TLSv1.3+SNI)
	if cfg.HttpVersion != "" {
		b.add("https", "http_version", "HTTP 版本", nil,
			httpVersionLabelCF(string(cfg.HttpVersion)),
			map[string]string{"http_version": string(cfg.HttpVersion)})
	}
}

// buildCFOrigin 回源:源站列表、回源协议、回源响应超时。
func buildCFOrigin(b *awsSettingsBuilder, cfg *cftypes.DistributionConfig) {
	if cfg.Origins == nil || len(cfg.Origins.Items) == 0 {
		return
	}
	names := make([]string, 0, len(cfg.Origins.Items))
	for _, o := range cfg.Origins.Items {
		names = append(names, awssdk.ToString(o.DomainName))
	}
	b.add("origin", "origins", "源站列表", nil,
		fmt.Sprintf("%d 个源站: %s", len(names), awsJoinLimit(names, 5)),
		map[string]string{"origins": awsJoinLimit(names, 10)})

	// 首个自定义源站的回源协议/超时(S3 源站无协议可映射,给出桶访问形态)
	for _, o := range cfg.Origins.Items {
		if o.CustomOriginConfig != nil {
			proto := string(o.CustomOriginConfig.OriginProtocolPolicy)
			if proto != "" {
				b.add("origin", "origin_protocol", "回源协议", nil,
					"回源协议: "+originProtocolLabelCF(proto),
					map[string]string{"protocol": proto})
			}
			if t := o.CustomOriginConfig.OriginReadTimeout; t != nil {
				b.add("origin", "origin_timeout", "回源超时", nil,
					fmt.Sprintf("%d 秒", *t), map[string]string{"seconds": fmt.Sprintf("%d", *t)})
			}
			break
		}
		if o.S3OriginConfig != nil {
			summary := "S3 源站: " + awssdk.ToString(o.DomainName)
			if o.S3OriginConfig.OriginAccessIdentity != nil && *o.S3OriginConfig.OriginAccessIdentity != "" {
				summary += "(OAI 私有访问)"
			}
			params := map[string]string{}
			if t := o.S3OriginConfig.OriginReadTimeout; t != nil {
				params["seconds"] = fmt.Sprintf("%d", *t)
				summary += fmt.Sprintf(" · 超时 %ds", *t)
			}
			b.add("origin", "s3_origin", "S3 源站", nil, summary, params)
			break
		}
	}
}

// buildCFBasic 基础:别名域名、IPv6、价格等级、访问日志、默认首页文件。
func buildCFBasic(b *awsSettingsBuilder, cfg *cftypes.DistributionConfig) {
	if cfg.Aliases != nil && len(cfg.Aliases.Items) > 0 {
		b.add("basic", "aliases", "别名域名(CNAME)", nil,
			fmt.Sprintf("%d 个: %s", len(cfg.Aliases.Items), awsJoinLimit(cfg.Aliases.Items, 5)),
			map[string]string{"aliases": awsJoinLimit(cfg.Aliases.Items, 10)})
	}
	if cfg.IsIPV6Enabled != nil {
		enabled := *cfg.IsIPV6Enabled
		b.add("basic", "ipv6", "IPv6", &enabled, awsOnOffText(cfg.IsIPV6Enabled), nil)
	}
	if cfg.PriceClass != "" {
		b.add("basic", "price_class", "节点覆盖(价格等级)", nil,
			priceClassLabelCF(string(cfg.PriceClass)), map[string]string{"price_class": string(cfg.PriceClass)})
	}
	if cfg.Logging != nil && cfg.Logging.Enabled != nil {
		enabled := *cfg.Logging.Enabled
		summary := "S3: " + awssdk.ToString(cfg.Logging.Bucket)
		if cfg.Logging.Prefix != nil && *cfg.Logging.Prefix != "" {
			summary += "/" + *cfg.Logging.Prefix
		}
		b.add("basic", "access_logging", "访问日志", &enabled, summary, nil)
	}
	if cfg.DefaultRootObject != nil && *cfg.DefaultRootObject != "" {
		b.add("basic", "default_root_object", "默认首页文件", nil,
			"默认首页: "+*cfg.DefaultRootObject, map[string]string{"root_object": *cfg.DefaultRootObject})
	}
}

// ===== 小工具 =====

// awsJoinLimit 串联列表,超出 max 折叠为 "n 条"。
func awsJoinLimit(list []string, max int) string {
	if len(list) <= max {
		return strings.Join(list, ",")
	}
	return fmt.Sprintf("%d 条", len(list))
}

// awsOnOffText 布尔指针启停文案。
func awsOnOffText(p *bool) string {
	if p != nil && *p {
		return "已开启"
	}
	return "未开启"
}

// originProtocolLabelCF CloudFront 回源协议标签。
func originProtocolLabelCF(p string) string {
	switch p {
	case string(cftypes.OriginProtocolPolicyHttpOnly):
		return "HTTP"
	case string(cftypes.OriginProtocolPolicyHttpsOnly):
		return "HTTPS"
	case string(cftypes.OriginProtocolPolicyMatchViewer):
		return "协议跟随"
	}
	return p
}

// tlsMinLabelCF CloudFront 安全策略(TLS 最低版本)标签。
func tlsMinLabelCF(v string) string {
	switch v {
	case "TLSv1_2016":
		return "TLSv1(2016 策略)"
	case "TLSv1.1_2016":
		return "TLSv1.1(2016 策略)"
	case "TLSv1.2_2018":
		return "TLSv1.2(2018 策略)"
	case "TLSv1.2_2019":
		return "TLSv1.2(2019 策略)"
	case "TLSv1.2_2021":
		return "TLSv1.2(2021 策略)"
	case "TLSv1.2_2025":
		return "TLSv1.2(2025 策略)"
	case "TLSv1.3_2025":
		return "TLSv1.3(2025 策略)"
	}
	return v
}

// httpVersionLabelCF CloudFront HttpVersion 标签。
func httpVersionLabelCF(v string) string {
	switch v {
	case string(cftypes.HttpVersionHttp11):
		return "HTTP/1.1"
	case string(cftypes.HttpVersionHttp2):
		return "HTTP/2"
	case string(cftypes.HttpVersionHttp2and3):
		return "HTTP/2 + HTTP/3"
	case string(cftypes.HttpVersionHttp3):
		return "HTTP/3"
	}
	return v
}

// priceClassLabelCF CloudFront 价格等级标签(节点覆盖范围)。
func priceClassLabelCF(v string) string {
	switch v {
	case string(cftypes.PriceClassPriceClass100):
		return "北美+欧洲(100 级)"
	case string(cftypes.PriceClassPriceClass200):
		return "北美+欧洲+亚洲+中东+非洲(200 级)"
	case string(cftypes.PriceClassPriceClassAll):
		return "全球全部边缘节点"
	}
	return v
}

// snsLabelCF SSLSupportMethod 标签。
func snsLabelCF(v string) string {
	switch v {
	case "vip":
		return "专用 VIP(不推荐)"
	case "static-ip":
		return "静态 IP"
	}
	return v
}

// shortCFArn ARN 取末段(证书 ID)展示,过长截断。
func shortCFArn(arn string) string {
	if i := strings.LastIndex(arn, "/"); i >= 0 {
		return arn[i+1:]
	}
	return truncateCF(arn, 40)
}

// truncateCF 超长截断。
func truncateCF(s string, max int) string {
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
