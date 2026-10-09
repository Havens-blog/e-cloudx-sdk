// 华为云 CDN 域名功能配置全景:ShowDomainFullConfig 全量配置 → 分组归一化。
// 华为 CDN 无 CDN/DCDN 双产品之分,单接口返回全部配置项;与 GetCacheConfig
// 同走 ShowDomainFullConfig,手工复用缓存规则(交由 CDNCacheQuerier 出)。
// 证书内容/鉴权密钥不入 params(脱敏为 ****** 或仅体现已配置)。
package huawei

import (
	"context"
	"fmt"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	cdnmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cdn/v2/model"
)

// huaweiGroupOrder 分组渲染顺序(与 aliyun/volcano 实现一致)。
var huaweiGroupOrder = []struct{ key, label string }{
	{"access_control", "访问控制"},
	{"traffic_limit", "流量限制"},
	{"performance", "性能优化"},
	{"https", "HTTPS"},
	{"redirect", "重定向"},
	{"origin", "回源"},
	{"basic", "基础"},
}

// huaweiSettingsBuilder 按分组累积配置项。
type huaweiSettingsBuilder struct {
	domain     string
	byCategory map[string][]types.CDNConfigSetting
}

func newHuaweiSettingsBuilder(domain string) *huaweiSettingsBuilder {
	return &huaweiSettingsBuilder{domain: domain, byCategory: map[string][]types.CDNConfigSetting{}}
}

// add 追加一项;summary/params 为空时省略对应字段。
func (b *huaweiSettingsBuilder) add(category, key, name string, enabled *bool, summary string, params map[string]string) {
	item := types.CDNConfigSetting{Key: key, Name: name, Enabled: enabled, Summary: summary, Params: params}
	b.byCategory[category] = append(b.byCategory[category], item)
}

// build 按 huaweiGroupOrder 归组,空组不输出。
func (b *huaweiSettingsBuilder) build() *types.CDNDomainSettings {
	settings := &types.CDNDomainSettings{Domain: b.domain}
	for _, g := range huaweiGroupOrder {
		if items := b.byCategory[g.key]; len(items) > 0 {
			settings.Groups = append(settings.Groups, types.CDNConfigGroup{
				Category: g.key, Label: g.label, Items: items,
			})
		}
	}
	return settings
}

// GetDomainSettings 查询华为云 CDN 域名功能配置全景(ShowDomainFullConfig)。
// enterprise_project_id 设为 "all" 覆盖所有企业项目(与缓存配置口径一致)。
func (a *CDNAdapter) GetDomainSettings(ctx context.Context, domainName, domainID string) (*types.CDNDomainSettings, error) {
	if domainName == "" {
		return nil, fmt.Errorf("华为云CDN功能配置需要域名")
	}

	client, err := a.createClient()
	if err != nil {
		return nil, err
	}

	allProjects := "all"
	request := &cdnmodel.ShowDomainFullConfigRequest{
		DomainName:          domainName,
		EnterpriseProjectId: &allProjects,
	}

	response, err := client.ShowDomainFullConfig(request)
	if err != nil {
		return nil, fmt.Errorf("查询CDN域名全量配置失败: %w", err)
	}
	if response.Configs == nil {
		// 无数据时返回统一空形状(仅 Domain),不 panic
		return &types.CDNDomainSettings{Domain: domainName}, nil
	}

	return buildHuaweiSettings(domainName, response.Configs), nil
}

// buildHuaweiSettings 原始全量配置 → 分组归一化(纯函数,便于单测)。
func buildHuaweiSettings(domain string, c *cdnmodel.ConfigsGetBody) *types.CDNDomainSettings {
	if c == nil {
		return &types.CDNDomainSettings{Domain: domain}
	}
	b := newHuaweiSettingsBuilder(domain)

	buildHuaweiAccessControl(b, c)
	buildHuaweiTrafficLimit(b, c)
	buildHuaweiPerformance(b, c)
	buildHuaweiHTTPS(b, c)
	buildHuaweiRedirect(b, c)
	buildHuaweiOrigin(b, c)
	buildHuaweiBasic(b, c)
	return b.build()
}

// buildHuaweiAccessControl 访问控制:IP/UA/Referer 黑白名单、URL 鉴权、远程鉴权。
func buildHuaweiAccessControl(b *huaweiSettingsBuilder, c *cdnmodel.ConfigsGetBody) {
	// IP 黑白名单
	if c.IpFilter != nil {
		enabled := hwACLEnabled(c.IpFilter.Type)
		params := map[string]string{"type": c.IpFilter.Type}
		if c.IpFilter.Value != nil && c.IpFilter.Type != "off" {
			params["ip_list"] = hwJoinLimit(hwSplitHosts(*c.IpFilter.Value), 10)
		}
		b.add("access_control", "ip_filter", "IP 黑白名单", enabled,
			hwACLSummary(c.IpFilter.Type, hwPtrList(c.IpFilter.Value)), params)
	}
	// Referer 防盗链
	if c.Referer != nil {
		enabled := hwACLEnabled(c.Referer.Type)
		summary := hwACLSummary(c.Referer.Type, hwPtrList(c.Referer.Value))
		params := map[string]string{"type": c.Referer.Type}
		if c.Referer.Value != nil && c.Referer.Type != "off" {
			params["referer_list"] = hwJoinLimit(hwSplitHosts(*c.Referer.Value), 10)
		}
		if c.Referer.IncludeEmpty != nil && *c.Referer.IncludeEmpty {
			summary += "(含空 Referer)"
			params["include_empty"] = "true"
		}
		b.add("access_control", "referer_filter", "Referer 防盗链", enabled, summary, params)
	}
	// UA 黑白名单
	if c.UserAgentFilter != nil {
		enabled := hwACLEnabled(c.UserAgentFilter.Type)
		uas := []string{}
		if c.UserAgentFilter.UaList != nil {
			uas = *c.UserAgentFilter.UaList
		} else if c.UserAgentFilter.Value != nil {
			uas = hwSplitHosts(*c.UserAgentFilter.Value)
		}
		params := map[string]string{"type": c.UserAgentFilter.Type}
		if c.UserAgentFilter.Type != "off" && len(uas) > 0 {
			params["ua_list"] = hwJoinLimit(uas, 10)
		}
		b.add("access_control", "ua_filter", "UA 黑白名单", enabled,
			hwACLSummary(c.UserAgentFilter.Type, uas), params)
	}
	// URL 鉴权(密钥脱敏)
	if c.UrlAuth != nil {
		summary := "URL 鉴权"
		params := map[string]string{}
		if t := c.UrlAuth.Type; t != nil && *t != "" {
			summary = "URL 鉴权(" + hwAuthTypeLabel(*t) + ")"
			params["type"] = *t
		}
		if e := c.UrlAuth.ExpireTime; e != nil {
			summary += fmt.Sprintf(" · 有效期 %ds", *e)
		}
		if c.UrlAuth.Key != nil && *c.UrlAuth.Key != "" {
			params["key"] = "******"
		}
		if c.UrlAuth.BackupKey != nil && *c.UrlAuth.BackupKey != "" {
			params["backup_key"] = "******"
		}
		b.add("access_control", "url_auth", "URL 鉴权", hwOnOffPtr(c.UrlAuth.Status), summary, params)
	}
	// 远程鉴权
	if c.RemoteAuth != nil {
		b.add("access_control", "remote_auth", "远程鉴权",
			hwOnOffPtr(c.RemoteAuth.RemoteAuthentication),
			"鉴权请求转发远端服务校验", nil)
	}
}

// buildHuaweiTrafficLimit 流量限制:IP 限频、请求限速、用量封顶。
func buildHuaweiTrafficLimit(b *huaweiSettingsBuilder, c *cdnmodel.ConfigsGetBody) {
	// IP 访问限频
	if c.IpFrequencyLimit != nil {
		summary := "单 IP 限频"
		if c.IpFrequencyLimit.Qps != nil {
			summary = fmt.Sprintf("单 IP 限频 %d 次/秒", *c.IpFrequencyLimit.Qps)
		}
		b.add("traffic_limit", "ip_frequency_limit", "IP 访问限频",
			hwOnOffPtr(c.IpFrequencyLimit.Status), summary, nil)
	}
	// 请求限速(拖拽/下载限速)
	if c.RequestLimitRules != nil && len(*c.RequestLimitRules) > 0 {
		n := len(*c.RequestLimitRules)
		enabled := true
		summary := fmt.Sprintf("%d 条规则", n)
		if r := (*c.RequestLimitRules)[0]; r.Status != nil && *r.Status == "on" {
			summary = fmt.Sprintf("限速 %d Bps(传输 %d 字节后生效)", r.LimitRateValue, r.LimitRateAfter)
		}
		b.add("traffic_limit", "request_limit_rules", "请求限速", &enabled, summary, nil)
	}
	// 用量封顶(带宽/流量,达阈值停用域名)
	if c.FlowLimitStrategy != nil && len(*c.FlowLimitStrategy) > 0 {
		enabled := true
		summary := fmt.Sprintf("%d 条策略", len(*c.FlowLimitStrategy))
		if s := (*c.FlowLimitStrategy)[0]; s.ItemType == "bandwidth" {
			summary = fmt.Sprintf("带宽封顶 %d bps(达阈值停用域名)", s.LimitValue)
		} else if s.ItemType == "traffic" {
			summary = fmt.Sprintf("流量封顶 %d byte(达阈值停用域名)", s.LimitValue)
		}
		b.add("traffic_limit", "flow_limit_strategy", "用量封顶", &enabled, summary, nil)
	}
}

// buildHuaweiPerformance 性能优化:智能压缩、分片回源、WebSocket、视频拖拽、QUIC。
func buildHuaweiPerformance(b *huaweiSettingsBuilder, c *cdnmodel.ConfigsGetBody) {
	// 智能压缩(gzip/brotli)
	if c.Compress != nil {
		summary := "未开启"
		if c.Compress.Status == "on" {
			summary = "已开启"
			if t := c.Compress.Type; t != nil && *t != "" {
				summary += "(" + strings.ToLower(*t) + ")"
			}
			if f := c.Compress.FileType; f != nil && *f != "" {
				summary += " · 文件类型: " + *f
			}
		}
		b.add("performance", "compress", "智能压缩", hwOnOffPtr(c.Compress.Status), summary, nil)
	}
	// 分片(Range)回源
	if c.OriginRangeStatus != nil {
		enabled := hwOnOffPtr(*c.OriginRangeStatus)
		summary := "已开启(大文件分片回源,省回源带宽)"
		if enabled != nil && !*enabled {
			summary = "未开启"
		}
		b.add("performance", "range_origin", "Range 分片回源", enabled, summary, nil)
	}
	// WebSocket(仅全站加速域名支持)
	if c.Websocket != nil {
		summary := "WebSocket"
		if c.Websocket.Timeout != nil {
			summary = fmt.Sprintf("WebSocket(超时 %ds)", *c.Websocket.Timeout)
		}
		b.add("performance", "websocket", "WebSocket", hwOnOffPtr(c.Websocket.Status), summary, nil)
	}
	// 视频拖拽(纯 bool 开关)
	if c.VideoSeek != nil {
		enabled := c.VideoSeek.EnableVideoSeek
		b.add("performance", "video_seek", "视频拖拽播放", &enabled,
			hwBoolText(enabled), nil)
	}
	// QUIC
	if c.Quic != nil {
		b.add("performance", "quic", "QUIC", hwOnOffPtr(c.Quic.Status), "已开启(QUIC 传输协议)", nil)
	}
}

// buildHuaweiHTTPS HTTPS:证书/HTTP2、TLS 版本、强制跳转、HSTS。
func buildHuaweiHTTPS(b *huaweiSettingsBuilder, c *cdnmodel.ConfigsGetBody) {
	// HTTPS 证书 + HTTP/2 + OCSP
	if c.Https != nil {
		summary := "HTTPS"
		params := map[string]string{}
		if c.Https.Http2Status != nil && *c.Https.Http2Status == "on" {
			summary += " + HTTP/2"
		}
		if c.Https.CertificateName != nil && *c.Https.CertificateName != "" {
			summary += " · 证书: " + *c.Https.CertificateName
		}
		if t := c.Https.TlsVersion; t != nil && *t != "" {
			params["tls_version"] = *t
		}
		if o := c.Https.OcspStaplingStatus; o != nil && *o == "on" {
			params["ocsp_stapling"] = "on"
		}
		if len(params) == 0 {
			params = nil
		}
		// HttpsStatus 与 Https 结构并存时证书才生效
		enabled := hwOnOffPtr("")
		if c.Https.HttpsStatus != nil {
			enabled = hwOnOffPtr(*c.Https.HttpsStatus)
		}
		b.add("https", "https", "HTTPS 证书", enabled, summary, params)
	}
	// TLS 版本与加密套件(独立结构,可能不与 Https 并存)
	if c.HttpsTlsVersion != nil && c.HttpsTlsVersion.TlsVersion != "" {
		summary := "启用: " + niceTLSVersions(c.HttpsTlsVersion.TlsVersion)
		if c.HttpsTlsVersion.CipherSuiteGroup != "" {
			summary += " · 加密套件: " + c.HttpsTlsVersion.CipherSuiteGroup
		}
		b.add("https", "tls_version", "TLS 版本", nil, summary,
			map[string]string{"tls_version": c.HttpsTlsVersion.TlsVersion, "cipher_suite_group": c.HttpsTlsVersion.CipherSuiteGroup})
	}
	// 强制 HTTPS/HTTP 跳转
	if c.ForceRedirect != nil {
		summary := "强制跳转"
		if t := c.ForceRedirect.Type; t != nil && *t != "" {
			summary += " " + strings.ToUpper(*t)
		}
		if code := c.ForceRedirect.RedirectCode; code != nil && *code > 0 {
			summary += fmt.Sprintf("(%d)", *code)
		}
		b.add("https", "force_redirect", "强制跳转", hwOnOffPtr(c.ForceRedirect.Status), summary, nil)
	}
	// HSTS
	if c.Hsts != nil {
		summary := "HSTS"
		if c.Hsts.MaxAge != nil {
			summary = fmt.Sprintf("max-age=%d", *c.Hsts.MaxAge)
		}
		params := map[string]string{}
		if c.Hsts.IncludeSubdomains != nil {
			summary += fmt.Sprintf(" includeSubDomains=%s", *c.Hsts.IncludeSubdomains)
			params["include_subdomains"] = *c.Hsts.IncludeSubdomains
		}
		b.add("https", "hsts", "HSTS", hwOnOffPtr(c.Hsts.Status), summary, params)
	}
}

// buildHuaweiRedirect 重定向:访问 URL 重写、回源 URL 改写。
func buildHuaweiRedirect(b *huaweiSettingsBuilder, c *cdnmodel.ConfigsGetBody) {
	// 访问 URL 重写
	if c.RequestUrlRewrite != nil && len(*c.RequestUrlRewrite) > 0 {
		n := len(*c.RequestUrlRewrite)
		summary := fmt.Sprintf("%d 条重写规则", n)
		if r := (*c.RequestUrlRewrite)[0]; r.RedirectUrl != "" {
			code := int32(0)
			if r.RedirectStatusCode != nil {
				code = *r.RedirectStatusCode
			}
			summary += fmt.Sprintf("(首条 %d→%s)", code, r.RedirectUrl)
		}
		b.add("redirect", "request_url_rewrite", "URL 重写/跳转", nil, summary, nil)
	}
	// 回源 URL 改写
	if c.OriginRequestUrlRewrite != nil && len(*c.OriginRequestUrlRewrite) > 0 {
		first := (*c.OriginRequestUrlRewrite)[0]
		b.add("redirect", "origin_url_rewrite", "回源 URL 改写", nil,
			fmt.Sprintf("%d 条规则(首条源 %s → %s)", len(*c.OriginRequestUrlRewrite),
				hwOriginRewriteSource(first), first.TargetUrl), nil)
	}
}

// buildHuaweiOrigin 回源:源站、回源 Host、协议、302 跟随、SNI、超时。
func buildHuaweiOrigin(b *huaweiSettingsBuilder, c *cdnmodel.ConfigsGetBody) {
	if c.Sources != nil && len(*c.Sources) > 0 {
		sources := *c.Sources
		addrs := make([]string, 0, len(sources))
		for _, s := range sources {
			addrs = append(addrs, s.OriginAddr)
		}
		b.add("origin", "sources", "源站列表", nil,
			fmt.Sprintf("%d 个源站: %s", len(addrs), hwJoinLimit(addrs, 5)),
			map[string]string{"origins": hwJoinLimit(addrs, 10), "origin_type": sources[0].OriginType})
		// 首个源站的回源 Host
		if h := sources[0].HostName; h != nil && *h != "" {
			b.add("origin", "origin_host", "回源 Host", nil, "回源 Host: "+*h, nil)
		}
	}
	if c.OriginProtocol != nil && *c.OriginProtocol != "" {
		b.add("origin", "origin_protocol", "回源协议", nil,
			"回源协议: "+hwProtocolLabel(*c.OriginProtocol),
			map[string]string{"protocol": *c.OriginProtocol})
	}
	if c.OriginFollow302Status != nil {
		b.add("origin", "follow_302", "回源 302 跟随", hwOnOffPtr(*c.OriginFollow302Status),
			"源站 302/301 时跟随跳转回源", nil)
	}
	if c.Sni != nil {
		summary := "回源 SNI"
		if c.Sni.ServerName != nil && *c.Sni.ServerName != "" {
			summary += ": " + *c.Sni.ServerName
		}
		b.add("origin", "sni", "回源 SNI", hwOnOffPtr(c.Sni.Status), summary, nil)
	}
	if c.OriginReceiveTimeout != nil && *c.OriginReceiveTimeout > 0 {
		b.add("origin", "origin_timeout", "回源超时", nil,
			fmt.Sprintf("%d 秒", *c.OriginReceiveTimeout), nil)
	}
}

// buildHuaweiBasic 基础:IPv6 加速、加速区域、业务类型。
func buildHuaweiBasic(b *huaweiSettingsBuilder, c *cdnmodel.ConfigsGetBody) {
	// Ipv6Accelerate:1=打开 0=关闭
	if c.Ipv6Accelerate != nil {
		enabled := *c.Ipv6Accelerate == 1
		b.add("basic", "ipv6", "IPv6", &enabled, hwBoolText(enabled), nil)
	}
	if c.ServiceArea != nil && *c.ServiceArea != "" {
		b.add("basic", "service_region", "加速区域", nil,
			hwRegionLabel(*c.ServiceArea), map[string]string{"scope": *c.ServiceArea})
	}
	if c.BusinessType != nil && *c.BusinessType != "" {
		b.add("basic", "business_type", "业务类型", nil,
			hwBizTypeLabel(*c.BusinessType), map[string]string{"type": *c.BusinessType})
	}
}

// ===== 小工具 =====

// hwOnOffPtr "on"/"off" → *bool;其他值 nil(不可判定)。
func hwOnOffPtr(s string) *bool {
	if s == "on" || s == "off" {
		v := s == "on"
		return &v
	}
	return nil
}

// hwACLEnabled Type 为 black/white 视为已开启,off 明确关闭,空值 nil(不可判定)。
func hwACLEnabled(t string) *bool {
	switch t {
	case "black", "white":
		v := true
		return &v
	case "off":
		v := false
		return &v
	}
	return nil
}

// hwACLSummary 黑白名单摘要:类型 + 列表折叠(最大 3 条)。
func hwACLSummary(t string, list []string) string {
	kind := hwAccessKind(t)
	if len(list) == 0 {
		return kind + "(未配置)"
	}
	return kind + " " + hwJoinLimit(list, 3)
}

// hwAccessKind black/white → 黑名单/白名单。
func hwAccessKind(t string) string {
	switch t {
	case "black":
		return "黑名单"
	case "white":
		return "白名单"
	case "off":
		return "未开启"
	}
	return t
}

// hwJoinLimit 串联列表,超出 max 折叠为 "n 条"。
func hwJoinLimit(list []string, max int) string {
	if len(list) <= max {
		return strings.Join(list, ",")
	}
	return fmt.Sprintf("%d 条", len(list))
}

// hwSplitHosts 华为以 "," 分隔 IP/Referer/UA 列表。
func hwSplitHosts(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// hwPtrList 指针字符串 → 一律转列表形态。
func hwPtrList(p *string) []string {
	if p == nil || *p == "" {
		return nil
	}
	return hwSplitHosts(*p)
}

// hwAuthTypeLabel 华为 URL 鉴权方式标签(type_a/type_b/type_c1/type_c2)。
func hwAuthTypeLabel(t string) string {
	switch t {
	case "type_a":
		return "鉴权方式 A"
	case "type_b":
		return "鉴权方式 B"
	case "type_c1":
		return "鉴权方式 C1"
	case "type_c2":
		return "鉴权方式 C2"
	}
	return t
}

// hwBoolText 布尔启停文案。
func hwBoolText(v bool) string {
	if v {
		return "已开启"
	}
	return "未开启"
}

// hwProtocolLabel http/https/follow 回源协议标签。
func hwProtocolLabel(p string) string {
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

// hwRegionLabel 华为加速区域标签(mainland_china/global/outside_mainland_china)。
func hwRegionLabel(r string) string {
	switch r {
	case "mainland_china":
		return "中国内地"
	case "outside_mainland_china":
		return "全球(不含中国内地)"
	case "global":
		return "全球"
	}
	return r
}

// hwBizTypeLabel 华为业务类型标签(web/download/video/wholesite)。
func hwBizTypeLabel(t string) string {
	switch t {
	case "web":
		return "网站加速"
	case "download":
		return "文件下载加速"
	case "video":
		return "点播加速"
	case "wholesite":
		return "全站加速"
	}
	return t
}

// niceTLSVersions TLSv1.1,TLSv1.2 → TLS1.1 / TLS1.2 形式展示。
func niceTLSVersions(v string) string {
	parts := strings.Split(v, ",")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(strings.TrimSpace(p), "TLSv", "TLS ")
	}
	return strings.Join(parts, " / ")
}

// hwOriginRewriteSource 首条回源 URL 改写规则的匹配源。
func hwOriginRewriteSource(r cdnmodel.OriginRequestUrlRewrite) string {
	if r.SourceUrl != nil && *r.SourceUrl != "" {
		return *r.SourceUrl
	}
	return "*(全部文件)"
}
