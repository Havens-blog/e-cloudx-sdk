// 腾讯云 CDN 域名功能配置全景:DescribeDomainsConfig(详情接口返回全部配置)→ 分组归一化。
// 与 GetCacheConfig 同用 DescribeDomainsConfig,缓存规则部分仍由 CDNCacheQuerier 输出,
// 此处只覆盖其余功能组。证书私钥/鉴权密钥脱敏(只体现已配置,不落明文)。
package tencent

import (
	"context"
	"fmt"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	tencentcdn "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cdn/v20180606"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
)

// tencentGroupOrder 分组渲染顺序(与 aliyun/volcano 实现一致)。
var tencentGroupOrder = []struct{ key, label string }{
	{"access_control", "访问控制"},
	{"traffic_limit", "流量限制"},
	{"performance", "性能优化"},
	{"https", "HTTPS"},
	{"redirect", "重定向"},
	{"origin", "回源"},
	{"basic", "基础"},
}

// tencentSettingsBuilder 按分组累积配置项。
type tencentSettingsBuilder struct {
	domain     string
	byCategory map[string][]types.CDNConfigSetting
}

func newTencentSettingsBuilder(domain string) *tencentSettingsBuilder {
	return &tencentSettingsBuilder{domain: domain, byCategory: map[string][]types.CDNConfigSetting{}}
}

// add 追加一项;summary/params 为空时省略对应字段。
func (b *tencentSettingsBuilder) add(category, key, name string, enabled *bool, summary string, params map[string]string) {
	item := types.CDNConfigSetting{Key: key, Name: name, Enabled: enabled, Summary: summary, Params: params}
	b.byCategory[category] = append(b.byCategory[category], item)
}

// build 按 tencentGroupOrder 归组,空组不输出。
func (b *tencentSettingsBuilder) build() *types.CDNDomainSettings {
	settings := &types.CDNDomainSettings{Domain: b.domain}
	for _, g := range tencentGroupOrder {
		if items := b.byCategory[g.key]; len(items) > 0 {
			settings.Groups = append(settings.Groups, types.CDNConfigGroup{
				Category: g.key, Label: g.label, Items: items,
			})
		}
	}
	return settings
}

// GetDomainSettings 查询腾讯云 CDN 域名功能配置全景(DescribeDomainsConfig)。
// 腾讯以域名为检索键(与缓存配置同);domainID 不参与查询。
func (a *CDNAdapter) GetDomainSettings(ctx context.Context, domainName, domainID string) (*types.CDNDomainSettings, error) {
	if domainName == "" {
		return nil, fmt.Errorf("腾讯云CDN功能配置需要域名")
	}

	client, err := a.createClient()
	if err != nil {
		return nil, err
	}

	request := tencentcdn.NewDescribeDomainsConfigRequest()
	request.Filters = []*tencentcdn.DomainFilter{
		{
			Name:  common.StringPtr("domain"),
			Value: common.StringPtrs([]string{domainName}),
		},
	}

	response, err := client.DescribeDomainsConfig(request)
	if err != nil {
		return nil, fmt.Errorf("查询CDN域名配置失败: %w", err)
	}
	if len(response.Response.Domains) == 0 || response.Response.Domains[0] == nil {
		return nil, fmt.Errorf("CDN域名不存在: %s", domainName)
	}

	return buildTencentSettings(domainName, response.Response.Domains[0]), nil
}

// buildTencentSettings DetailDomain → 分组归一化(纯函数,便于单测)。
// d 为 nil 时返回仅含 Domain 的空结构(与 aliyun/volcano 的空数据约定一致,不 panic)。
func buildTencentSettings(domain string, d *tencentcdn.DetailDomain) *types.CDNDomainSettings {
	if d == nil {
		return &types.CDNDomainSettings{Domain: domain}
	}
	b := newTencentSettingsBuilder(domain)

	buildTencentAccessControl(b, d)
	buildTencentTrafficLimit(b, d)
	buildTencentPerformance(b, d)
	buildTencentHTTPS(b, d)
	buildTencentRedirect(b, d)
	buildTencentOrigin(b, d)
	buildTencentBasic(b, d)
	return b.build()
}

// buildTencentAccessControl 访问控制:IP 黑白名单、Referer 防盗链、UA 黑白名单、
// 时间戳防盗链、请求头+URL 访问控制、区域访问控制。
func buildTencentAccessControl(b *tencentSettingsBuilder, d *tencentcdn.DetailDomain) {
	// IP 黑白名单
	if d.IpFilter != nil {
		filters := tcDeref(d.IpFilter.Filters)
		enabled := tcOnOffPtr(tcVal(d.IpFilter.Switch))
		summary := tcACLSummary(tcValLower(d.IpFilter.FilterType), filters)
		params := map[string]string{"type": tcValLower(d.IpFilter.FilterType)}
		if len(filters) > 0 {
			params["ip_list"] = tcJoinLimit(filters, 10)
		}
		b.add("access_control", "ip_filter", "IP 黑白名单", enabled, summary, params)
	}
	// IP 访问限频(按次/秒)
	if d.IpFreqLimit != nil {
		summary := "IP 访问频次限制"
		if q := d.IpFreqLimit.Qps; q != nil {
			summary = fmt.Sprintf("单 IP 限频 %d 次/秒", *q)
		}
		b.add("traffic_limit", "ip_freq_limit", "IP 访问限频",
			tcOnOffPtr(tcVal(d.IpFreqLimit.Switch)), summary, nil)
	}
	// Referer 防盗链(取首条路径规则代表展示,总数另计)
	if d.Referer != nil {
		enabled := tcOnOffPtr(tcVal(d.Referer.Switch))
		summary := "Referer 防盗链"
		params := map[string]string{}
		if len(d.Referer.RefererRules) > 0 {
			r := d.Referer.RefererRules[0]
			refs := tcDeref(r.Referers)
			kind := tcAccessKind(tcValLower(r.RefererType))
			summary = fmt.Sprintf("%s %s", kind, tcJoinLimit(refs, 3))
			if r.AllowEmpty != nil && *r.AllowEmpty {
				summary += "(允许空 Referer)"
			}
			params["type"] = tcValLower(r.RefererType)
			params["referer_list"] = tcJoinLimit(refs, 10)
		}
		if len(d.Referer.RefererRules) > 1 {
			summary += fmt.Sprintf(",共 %d 条路径规则", len(d.Referer.RefererRules))
		}
		b.add("access_control", "referer_filter", "Referer 防盗链", enabled, summary, params)
	}
	// UA 黑白名单
	if d.UserAgentFilter != nil {
		enabled := tcOnOffPtr(tcVal(d.UserAgentFilter.Switch))
		summary := "UA 黑白名单"
		params := map[string]string{}
		if len(d.UserAgentFilter.FilterRules) > 0 {
			r := d.UserAgentFilter.FilterRules[0]
			uas := tcDeref(r.UserAgents)
			kind := tcAccessKind(tcValLower(r.FilterType))
			summary = kind + " " + tcJoinLimit(uas, 3)
			params["type"] = tcValLower(r.FilterType)
			params["ua_list"] = tcJoinLimit(uas, 10)
		}
		b.add("access_control", "ua_filter", "UA 黑白名单", enabled, summary, params)
	}
	// 时间戳防盗链(密钥脱敏)
	if d.Authentication != nil {
		summary := "时间戳防盗链"
		params := map[string]string{}
		if d.Authentication.AuthAlgorithm != nil && *d.Authentication.AuthAlgorithm != "" {
			params["algorithm"] = *d.Authentication.AuthAlgorithm
		}
		mode, key := tcAuthMode(d.Authentication)
		if mode != "" {
			summary = fmt.Sprintf("时间戳防盗链(%s · 算法 %s)", mode, tcVal(d.Authentication.AuthAlgorithm))
			params["mode"] = mode
		}
		if key != "" {
			params["secret_key"] = "******"
		}
		b.add("access_control", "authentication", "URL 鉴权",
			tcOnOffPtr(tcVal(d.Authentication.Switch)), summary, params)
	}
	// 请求头部及请求 URL 访问控制
	if d.AccessControl != nil {
		n := 0
		if d.AccessControl.AccessControlRules != nil {
			n = len(d.AccessControl.AccessControlRules)
		}
		enabled := false
		if d.AccessControl.Switch != nil && *d.AccessControl.Switch == "on" {
			enabled = true
		}
		b.add("access_control", "access_control", "请求头/URL 访问控制", &enabled,
			fmt.Sprintf("%d 条访问规则", n), nil)
	}
	// 区域访问控制(灰度功能)
	if d.GeoBlocker != nil {
		b.add("access_control", "geo_blocker", "区域访问控制",
			tcOnOffPtr(tcVal(d.GeoBlocker.Switch)),
			fmt.Sprintf("%d 条地区规则", len(d.GeoBlocker.BlockRules)), nil)
	}
}

// buildTencentTrafficLimit 流量限制:带宽封顶、单链接下行限速。
func buildTencentTrafficLimit(b *tencentSettingsBuilder, d *tencentcdn.DetailDomain) {
	// 用量封顶(带宽/流量)
	if d.BandwidthAlert != nil {
		summary := "用量封顶"
		params := map[string]string{}
		if m := d.BandwidthAlert.Metric; m != nil && *m != "" {
			params["metric"] = *m
		}
		if th := d.BandwidthAlert.BpsThreshold; th != nil {
			summary = fmt.Sprintf("带宽封顶 %d bps", *th)
			params["bps_threshold"] = fmt.Sprintf("%d", *th)
		}
		if cm := d.BandwidthAlert.CounterMeasure; cm != nil && *cm != "" {
			summary += "(" + *cm + ")"
			params["counter_measure"] = *cm
		}
		b.add("traffic_limit", "bandwidth_alert", "用量封顶",
			tcOnOffPtr(tcVal(d.BandwidthAlert.Switch)), summary, params)
	}
	// 单链接下行限速
	if d.DownstreamCapping != nil {
		summary := "单链接下行限速"
		params := map[string]string{}
		if len(d.DownstreamCapping.CappingRules) > 0 {
			r := d.DownstreamCapping.CappingRules[0]
			if r != nil && r.KBpsThreshold != nil {
				summary = fmt.Sprintf("单链接限速 %d KB/s", *r.KBpsThreshold)
				params["kbps_threshold"] = fmt.Sprintf("%d", *r.KBpsThreshold)
			}
		}
		if len(d.DownstreamCapping.CappingRules) > 1 {
			summary += fmt.Sprintf(",共 %d 条规则", len(d.DownstreamCapping.CappingRules))
		}
		b.add("traffic_limit", "downstream_capping", "下载限速",
			tcOnOffPtr(tcVal(d.DownstreamCapping.Switch)), summary, params)
	}
}

// buildTencentPerformance 性能优化:智能压缩、分片回源、视频拖拽、QUIC、SEO、WebSocket、离线缓存。
func buildTencentPerformance(b *tencentSettingsBuilder, d *tencentcdn.DetailDomain) {
	// 智能压缩(gzip/brotli)
	if d.Compression != nil {
		algorithms := []string{}
		enabled := switchOn(d.Compression.Switch)
		if d.Compression.CompressionRules != nil {
			for _, r := range d.Compression.CompressionRules {
				if r == nil {
					continue
				}
				for _, a := range r.Algorithms {
					if a != nil && *a != "" {
						algorithms = append(algorithms, strings.ToUpper(*a))
					}
				}
			}
		}
		summary := "未开启"
		params := map[string]string{}
		if enabled {
			summary = "已开启"
			if len(algorithms) > 0 {
				summary += "(" + strings.Join(tcUnique(algorithms), "/") + ")"
				params["algorithms"] = strings.Join(tcUnique(algorithms), ",")
			}
		}
		b.add("performance", "compression", "智能压缩", &enabled, summary, params)
	}
	// 分片回源
	if d.RangeOriginPull != nil {
		summary := "已开启(大文件分片回源,省回源带宽)"
		if tcVal(d.RangeOriginPull.Switch) == "off" {
			summary = "未开启"
		}
		b.add("performance", "range_origin_pull", "分片回源",
			tcOnOffPtr(tcVal(d.RangeOriginPull.Switch)), summary, nil)
	}
	// 视频拖拽
	if d.VideoSeek != nil {
		b.add("performance", "video_seek", "视频拖拽播放",
			tcOnOffPtr(tcVal(d.VideoSeek.Switch)), tcOnOffText(tcVal(d.VideoSeek.Switch)), nil)
	}
	// QUIC
	if d.Quic != nil {
		b.add("performance", "quic", "QUIC", tcOnOffPtr(tcVal(d.Quic.Switch)),
			tcOnOffText(tcVal(d.Quic.Switch)), nil)
	}
	// SEO 优化
	if d.Seo != nil {
		b.add("performance", "seo", "SEO 优化", tcOnOffPtr(tcVal(d.Seo.Switch)),
			tcOnOffText(tcVal(d.Seo.Switch)), nil)
	}
	// WebSocket(腾讯开关只控制超时,关闭时默认 15 秒)
	if d.WebSocket != nil {
		summary := "WebSocket"
		if t := d.WebSocket.Timeout; t != nil {
			summary = fmt.Sprintf("WebSocket(超时 %ds)", *t)
		}
		b.add("performance", "websocket", "WebSocket",
			tcOnOffPtr(tcVal(d.WebSocket.Switch)), summary, nil)
	}
	// 离线缓存
	if d.OfflineCache != nil {
		b.add("performance", "offline_cache", "离线缓存(源站异常兜底)",
			tcOnOffPtr(tcVal(d.OfflineCache.Switch)),
			"源站异常时返回过期缓存", nil)
	}
}

// buildTencentHTTPS HTTPS:证书/HTTP2/TLS/HSTS、强制跳转。
func buildTencentHTTPS(b *tencentSettingsBuilder, d *tencentcdn.DetailDomain) {
	if d.Https != nil {
		summary := "HTTPS"
		params := map[string]string{}
		if switchOn(d.Https.Http2) {
			summary += " + HTTP/2"
		}
		if d.Https.CertInfo != nil && d.Https.CertInfo.CertName != nil && *d.Https.CertInfo.CertName != "" {
			summary += " · 证书: " + *d.Https.CertInfo.CertName
			params["cert_name"] = *d.Https.CertInfo.CertName
		}
		if d.Https.OcspStapling != nil && *d.Https.OcspStapling == "on" {
			params["ocsp_stapling"] = "on"
		}
		if len(d.Https.TlsVersion) > 0 {
			params["tls_version"] = strings.Join(tcDeref(d.Https.TlsVersion), ",")
		}
		if len(params) == 0 {
			params = nil
		}
		b.add("https", "https", "HTTPS 证书", tcOnOffPtr(tcVal(d.Https.Switch)), summary, params)

		// TLS 版本(列表独立展示)
		if len(d.Https.TlsVersion) > 0 {
			b.add("https", "tls_version", "TLS 版本", nil,
				"启用: "+niceTLSVersionsTC(tcDeref(d.Https.TlsVersion)), nil)
		}
		// HSTS
		if hs := d.Https.Hsts; hs != nil {
			summary := "HSTS"
			params := map[string]string{}
			if hs.MaxAge != nil {
				summary = fmt.Sprintf("max-age=%d", *hs.MaxAge)
			}
			if hs.IncludeSubDomains != nil && *hs.IncludeSubDomains == "on" {
				summary += " includeSubDomains=on"
				params["include_subdomains"] = "on"
			}
			b.add("https", "hsts", "HSTS", tcOnOffPtr(tcVal(hs.Switch)), summary, params)
		}
	}
	// 访问协议强制跳转
	if d.ForceRedirect != nil {
		summary := "强制跳转"
		if t := d.ForceRedirect.RedirectType; t != nil && *t != "" {
			summary = "强制 " + strings.ToUpper(*t) + " 跳转"
		}
		if code := d.ForceRedirect.RedirectStatusCode; code != nil && *code > 0 {
			summary += fmt.Sprintf("(%d)", *code)
		}
		b.add("https", "force_redirect", "强制跳转",
			tcOnOffPtr(tcVal(d.ForceRedirect.Switch)), summary, nil)
	}
}

// buildTencentRedirect 重定向:访问 URL 重写。
func buildTencentRedirect(b *tencentSettingsBuilder, d *tencentcdn.DetailDomain) {
	if d.UrlRedirect != nil {
		enabled := tcOnOffPtr(tcVal(d.UrlRedirect.Switch))
		summary := "URL 重写"
		if len(d.UrlRedirect.PathRules) > 0 {
			r := d.UrlRedirect.PathRules[0]
			summary = fmt.Sprintf("%d 条重写规则", len(d.UrlRedirect.PathRules))
			if r != nil && r.Pattern != nil && r.RedirectUrl != nil {
				code := int64(0)
				if r.RedirectStatusCode != nil {
					code = *r.RedirectStatusCode
				}
				summary += fmt.Sprintf("(首条 %s →%d→ %s)", *r.Pattern, code, *r.RedirectUrl)
			}
		}
		b.add("redirect", "url_redirect", "URL 重写/跳转", enabled, summary, nil)
	}
}

// buildTencentOrigin 回源:源站、回源 Host、协议、302 跟随、头改写、超时、COS 私有访问。
func buildTencentOrigin(b *tencentSettingsBuilder, d *tencentcdn.DetailDomain) {
	if d.Origin != nil {
		o := d.Origin
		origins := tcDeref(o.Origins)
		if len(origins) > 0 {
			b.add("origin", "origins", "源站列表", nil,
				fmt.Sprintf("%d 个源站: %s", len(origins), tcJoinLimit(origins, 5)),
				map[string]string{"origins": tcJoinLimit(origins, 10), "origin_type": tcVal(o.OriginType)})
		}
		if len(tcDeref(o.BackupOrigins)) > 0 {
			b.add("origin", "backup_origins", "备源站", nil,
				fmt.Sprintf("%d 个备源站: %s", len(tcDeref(o.BackupOrigins)), tcJoinLimit(tcDeref(o.BackupOrigins), 3)), nil)
		}
		if o.ServerName != nil && *o.ServerName != "" {
			b.add("origin", "origin_host", "回源 Host", nil, "回源 Host: "+*o.ServerName, nil)
		}
		if o.OriginPullProtocol != nil && *o.OriginPullProtocol != "" {
			b.add("origin", "origin_protocol", "回源协议", nil,
				"回源协议: "+tcProtocolLabel(*o.OriginPullProtocol),
				map[string]string{"protocol": *o.OriginPullProtocol})
		}
		if o.CosPrivateAccess != nil && *o.CosPrivateAccess == "on" {
			enabled := true
			b.add("origin", "cos_private_access", "COS 私有桶回源", &enabled,
				"COS 私有访问已开启", nil)
		}
		if o.BasePath != nil && *o.BasePath != "" {
			b.add("origin", "base_path", "回源路径", nil, "回源路径: "+*o.BasePath, nil)
		}
	}
	// 回源 302 跟随
	if d.FollowRedirect != nil {
		b.add("origin", "follow_redirect", "回源 302 跟随",
			tcOnOffPtr(tcVal(d.FollowRedirect.Switch)),
			"源站 302/301 时跟随跳转回源", nil)
	}
	// 回源请求头改写
	if d.RequestHeader != nil {
		b.add("origin", "request_header", "回源请求头改写",
			tcOnOffPtr(tcVal(d.RequestHeader.Switch)),
			tcHeaderSummary(len(d.RequestHeader.HeaderRules), func(i int) (string, string) {
				r := d.RequestHeader.HeaderRules[i]
				if r == nil {
					return "", ""
				}
				return tcValLower(r.HeaderMode), tcVal(r.HeaderName)
			}), nil)
	}
	// 节点响应头改写
	if d.ResponseHeader != nil {
		b.add("origin", "response_header", "节点响应头改写",
			tcOnOffPtr(tcVal(d.ResponseHeader.Switch)),
			tcHeaderSummary(len(d.ResponseHeader.HeaderRules), func(i int) (string, string) {
				r := d.ResponseHeader.HeaderRules[i]
				if r == nil {
					return "", ""
				}
				return tcValLower(r.HeaderMode), tcVal(r.HeaderName)
			}), nil)
	}
	// 回源超时(建连/接收)
	if d.OriginPullTimeout != nil {
		summary := "回源超时"
		parts := []string{}
		if t := d.OriginPullTimeout.ConnectTimeout; t != nil {
			parts = append(parts, fmt.Sprintf("建连 %ds", *t))
		}
		if t := d.OriginPullTimeout.ReceiveTimeout; t != nil {
			parts = append(parts, fmt.Sprintf("接收 %ds", *t))
		}
		if len(parts) > 0 {
			summary = strings.Join(parts, " · ")
		}
		b.add("origin", "origin_timeout", "回源超时", nil, summary, nil)
	}
}

// buildTencentBasic 基础:IPv6 访问/回源、加速区域、业务类型。
func buildTencentBasic(b *tencentSettingsBuilder, d *tencentcdn.DetailDomain) {
	// 节点 IPv6 访问
	if d.Ipv6Access != nil {
		b.add("basic", "ipv6_access", "IPv6 访问", tcOnOffPtr(tcVal(d.Ipv6Access.Switch)),
			tcOnOffText(tcVal(d.Ipv6Access.Switch)), nil)
	}
	// 源站 IPv6
	if d.Ipv6 != nil {
		b.add("basic", "ipv6_origin", "源站 IPv6", tcOnOffPtr(tcVal(d.Ipv6.Switch)),
			tcOnOffText(tcVal(d.Ipv6.Switch)), nil)
	}
	// 加速区域
	if d.Area != nil && *d.Area != "" {
		b.add("basic", "service_region", "加速区域", nil,
			tcRegionLabel(*d.Area), map[string]string{"area": *d.Area})
	}
	// 业务类型
	if d.ServiceType != nil && *d.ServiceType != "" {
		b.add("basic", "business_type", "业务类型", nil,
			tcBizTypeLabel(*d.ServiceType), map[string]string{"type": *d.ServiceType})
	}
}

// ===== 小工具 =====

// tcOnOffPtr "on"/"off" → *bool;其他值或 nil 字符串 → nil(不可判定)。
func tcOnOffPtr(s string) *bool {
	if s == "on" || s == "off" {
		v := s == "on"
		return &v
	}
	return nil
}

// tcOnOffText 字符串开关启停文案。
func tcOnOffText(s string) string {
	switch s {
	case "on":
		return "已开启"
	case "off":
		return "未开启"
	}
	return "已配置"
}

// tcVal 字符串指针安全取值。
func tcVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// tcValLower 字符串指针安全取值并小写(腾讯类型值多为小写)。
func tcValLower(p *string) string {
	return strings.ToLower(tcVal(p))
}

// tcDeref 字符串指针切片 → 值切片(nil 元素跳过)。
func tcDeref(ps []*string) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		if p != nil && *p != "" {
			out = append(out, *p)
		}
	}
	return out
}

// tcAccessKind whitelist/blacklist → 白名单/黑名单。
func tcAccessKind(t string) string {
	switch t {
	case "whitelist":
		return "白名单"
	case "blacklist":
		return "黑名单"
	}
	return t
}

// tcACLSummary 黑白名单摘要:类型 + 列表折叠(最大 3 条),空列表只留类型。
func tcACLSummary(t string, list []string) string {
	kind := tcAccessKind(t)
	if len(list) == 0 {
		return kind + "(未配置)"
	}
	return kind + " " + tcJoinLimit(list, 3)
}

// tcJoinLimit 串联列表,超出 max 折叠为 "n 条"。
func tcJoinLimit(list []string, max int) string {
	if len(list) <= max {
		return strings.Join(list, ",")
	}
	return fmt.Sprintf("%d 条", len(list))
}

// tcAuthMode 时间戳防盗链模式(A/B/C/D),返回模式名与是否配置密钥。
func tcAuthMode(auth *tencentcdn.Authentication) (mode, key string) {
	switch {
	case auth.TypeA != nil:
		mode = "模式 A"
		if auth.TypeA.SecretKey != nil && *auth.TypeA.SecretKey != "" {
			key = *auth.TypeA.SecretKey
		}
	case auth.TypeB != nil:
		mode = "模式 B"
		if auth.TypeB.SecretKey != nil && *auth.TypeB.SecretKey != "" {
			key = *auth.TypeB.SecretKey
		}
	case auth.TypeC != nil:
		mode = "模式 C"
		if auth.TypeC.SecretKey != nil && *auth.TypeC.SecretKey != "" {
			key = *auth.TypeC.SecretKey
		}
	case auth.TypeD != nil:
		mode = "模式 D"
		if auth.TypeD.SecretKey != nil && *auth.TypeD.SecretKey != "" {
			key = *auth.TypeD.SecretKey
		}
	}
	return mode, key
}

// tcProtocolLabel http/https/follow 回源协议标签。
func tcProtocolLabel(p string) string {
	switch p {
	case "http":
		return "HTTP"
	case "https":
		return "HTTPS"
	case "follow":
		return "协议跟随"
	}
	return p
}

// tcRegionLabel 腾讯加速区域标签(mainland/overseas/global)。
func tcRegionLabel(r string) string {
	switch r {
	case "mainland":
		return "中国内地"
	case "overseas":
		return "全球(不含中国内地)"
	case "global":
		return "全球"
	}
	return r
}

// tcBizTypeLabel 腾讯业务类型标签(web/download/media/hybrid/dynamic)。
func tcBizTypeLabel(t string) string {
	switch t {
	case "web":
		return "网页小文件加速"
	case "download":
		return "下载大文件加速"
	case "media":
		return "音视频点播加速"
	case "hybrid":
		return "动静混合加速"
	case "dynamic":
		return "动态加速"
	}
	return t
}

// niceTLSVersionsTC TLSv1.2,TLSv1.3 → TLS 1.2 / TLS 1.3 形式展示。
func niceTLSVersionsTC(vers []string) string {
	parts := make([]string, 0, len(vers))
	for _, v := range vers {
		parts = append(parts, strings.ReplaceAll(v, "TLSv", "TLS "))
	}
	return strings.Join(parts, " / ")
}

// tcHeaderSummary 通用头改写摘要:遍历取 op+key 串联(最多拼 3 条,其余折叠计数)。
func tcHeaderSummary(n int, pick func(i int) (string, string)) string {
	parts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		op, key := pick(i)
		if key == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s", tcHeaderOpLabel(op), key))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%d 条", n)
	}
	if len(parts) > 3 {
		return strings.Join(parts[:3], " · ") + fmt.Sprintf(",共 %d 条", n)
	}
	return strings.Join(parts, " · ")
}

// tcHeaderOpLabel add/set/del → 添加/设置/删除。
func tcHeaderOpLabel(op string) string {
	switch op {
	case "add":
		return "添加"
	case "set":
		return "设置"
	case "del", "delete":
		return "删除"
	}
	return op
}

// tcUnique 去重(压缩算法可能重复出现)。
func tcUnique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
