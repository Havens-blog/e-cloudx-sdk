// 火山引擎 CDN 域名功能配置全景:DescribeCdnConfig(CDN)→ DCDN ListDomainConfig
// 回退,与缓存配置同构。证书内容/鉴权密钥不入 params(证书只留名称/ID,密钥脱敏)。
package volcano

import (
	"context"
	"fmt"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/cdn"
	"github.com/volcengine/volcengine-go-sdk/service/dcdn"
)

// groupOrder 分组渲染顺序(与 aliyun 实现一致)。
var volcanoGroupOrder = []struct{ key, label string }{
	{"access_control", "访问控制"},
	{"traffic_limit", "流量限制"},
	{"performance", "性能优化"},
	{"https", "HTTPS"},
	{"redirect", "重定向"},
	{"origin", "回源"},
	{"basic", "基础"},
}

// settingsBuilder 按分组累积配置项。
type settingsBuilder struct {
	domain     string
	byCategory map[string][]types.CDNConfigSetting
}

func newSettingsBuilder(domain string) *settingsBuilder {
	return &settingsBuilder{domain: domain, byCategory: map[string][]types.CDNConfigSetting{}}
}

// add 追加一项;summary/params 为空时省略对应字段。
func (b *settingsBuilder) add(category, key, name string, enabled *bool, summary string, params map[string]string) {
	item := types.CDNConfigSetting{Key: key, Name: name, Enabled: enabled, Summary: summary, Params: params}
	b.byCategory[category] = append(b.byCategory[category], item)
}

// build 按 groupOrder 归组,空组不输出。
func (b *settingsBuilder) build() *types.CDNDomainSettings {
	settings := &types.CDNDomainSettings{Domain: b.domain}
	for _, g := range volcanoGroupOrder {
		if items := b.byCategory[g.key]; len(items) > 0 {
			settings.Groups = append(settings.Groups, types.CDNConfigGroup{
				Category: g.key, Label: g.label, Items: items,
			})
		}
	}
	return settings
}

// sw Switch 指针直通(nil=不可判定)。
func sw(p *bool) *bool { return p }

// GetDomainSettings 查询域名功能配置全景:CDN DescribeCdnConfig 优先,DCDN 回退。
func (a *CDNAdapter) GetDomainSettings(ctx context.Context, domainName, domainID string) (*types.CDNDomainSettings, error) {
	if domainName == "" {
		return nil, fmt.Errorf("火山引擎CDN功能配置需要域名")
	}
	settings, err := a.getCDNDomainSettings(domainName)
	if err != nil {
		a.logger.Debug("CDN 功能配置查询失败,尝试 DCDN", elog.String("domain", domainName), elog.FieldErr(err))
		settings, err = a.getDCDNDomainSettings(domainName)
		if err != nil {
			return nil, err
		}
	}
	return settings, nil
}

// getCDNDomainSettings CDN 域名:DescribeCdnConfig → 分组归一。
func (a *CDNAdapter) getCDNDomainSettings(domainName string) (*types.CDNDomainSettings, error) {
	client, err := a.createClient()
	if err != nil {
		return nil, err
	}
	input := &cdn.DescribeCdnConfigInput{}
	input.SetDomain(domainName)
	output, err := client.DescribeCdnConfig(input)
	if err != nil {
		return nil, fmt.Errorf("查询CDN域名配置失败: %w", err)
	}
	if output.DomainConfig == nil {
		return &types.CDNDomainSettings{Domain: domainName}, nil
	}
	b := newSettingsBuilder(domainName)
	buildCDNAccessControl(b, output.DomainConfig)
	buildCDNTrafficLimit(b, output.DomainConfig)
	buildCDNPerformance(b, output.DomainConfig)
	buildCDNHTTPS(b, output.DomainConfig)
	buildCDNRedirectOrigin(b, output.DomainConfig)
	buildCDNBasic(b, output.DomainConfig)
	return b.build(), nil
}

// buildCDNAccessControl 访问控制:IP/UA/Referer 黑白名单、URL 鉴权、请求拦截、远程鉴权、方法禁用。
func buildCDNAccessControl(b *settingsBuilder, cfg *cdn.DomainConfigForDescribeCdnConfigOutput) {
	if r := cfg.IpAccessRule; r != nil {
		ips := derefList(r.Ip)
		b.add("access_control", "ip_access_rule", "IP 黑白名单", sw(r.Switch),
			aclSummary(stringValue(r.RuleType), ips), map[string]string{"type": stringValue(r.RuleType), "ip_list": joinLimit(ips, 10)})
	}
	if r := cfg.UaAccessRule; r != nil {
		ua := derefList(r.UserAgent)
		b.add("access_control", "ua_access_rule", "UA 黑白名单", sw(r.Switch),
			aclSummary(stringValue(r.RuleType), ua), map[string]string{"type": stringValue(r.RuleType), "ua_list": joinLimit(ua, 10)})
	}
	if r := cfg.RefererAccessRule; r != nil {
		refs := derefList(r.Referers)
		summary := accessKind(stringValue(r.RuleType)) + " " + joinLimit(refs, 3)
		if r.AllowEmpty != nil && *r.AllowEmpty {
			summary += "(允许空 Referer)"
		}
		b.add("access_control", "referer_access_rule", "Referer 防盗链", sw(r.Switch), summary,
			map[string]string{"type": stringValue(r.RuleType), "referer_list": joinLimit(refs, 10)})
	}
	if r := cfg.SignedUrlAuth; r != nil {
		params := map[string]string{}
		summary := "URL 鉴权"
		if len(r.SignedUrlAuthRules) > 0 && r.SignedUrlAuthRules[0] != nil {
			act := r.SignedUrlAuthRules[0].SignedUrlAuthAction
			if act != nil {
				summary = fmt.Sprintf("URL 鉴权(类型 %s)", stringValue(act.URLAuthType))
				if act.Duration != nil {
					summary += fmt.Sprintf(" 有效期 %ds", *act.Duration)
				}
				params["auth_type"] = stringValue(act.URLAuthType)
				// 密钥脱敏,只体现是否配置
				params["master_secret_key"] = maskIfSet(stringValue(act.MasterSecretKey))
				params["backup_secret_key"] = maskIfSet(stringValue(act.BackupSecretKey))
			}
		}
		b.add("access_control", "signed_url_auth", "URL 鉴权", sw(r.Switch), summary, params)
	}
	if r := cfg.RequestBlockRule; r != nil {
		names := make([]string, 0, len(r.BlockRule))
		for _, br := range r.BlockRule {
			if br != nil && br.RuleName != nil {
				names = append(names, *br.RuleName)
			}
		}
		b.add("access_control", "request_block_rule", "请求拦截", sw(r.Switch),
			fmt.Sprintf("%d 条规则", len(names)), map[string]string{"rules": strings.Join(names, ",")})
	}
	if r := cfg.RemoteAuth; r != nil {
		b.add("access_control", "remote_auth", "远程鉴权", sw(r.Switch), "鉴权请求转发远端服务校验", nil)
	}
	if r := cfg.MethodDeniedRule; r != nil {
		b.add("access_control", "method_denied_rule", "请求方法限制", sw(r.Switch),
			"禁用: "+stringValue(r.Methods), map[string]string{"methods": stringValue(r.Methods)})
	}
}

// buildCDNTrafficLimit 流量限制:带宽封顶、IP 频次/限速、下载限速。
func buildCDNTrafficLimit(b *settingsBuilder, cfg *cdn.DomainConfigForDescribeCdnConfigOutput) {
	if r := cfg.BandwidthLimit; r != nil && r.BandwidthLimitRule != nil && r.BandwidthLimitRule.BandwidthLimitAction != nil {
		act := r.BandwidthLimitRule.BandwidthLimitAction
		summary := ""
		if act.BandwidthThreshold != nil {
			summary = fmt.Sprintf("带宽封顶 %dbps", *act.BandwidthThreshold)
			if act.SpeedLimitRate != nil {
				summary += fmt.Sprintf(",超限降速至 %dbps", *act.SpeedLimitRate)
			}
		}
		b.add("traffic_limit", "bandwidth_limit", "带宽封顶", sw(r.Switch), summary, nil)
	}
	if r := cfg.IpFreqLimit; r != nil {
		summary := "IP 访问频次限制"
		if len(r.IpFreqLimitRules) > 0 && r.IpFreqLimitRules[0] != nil && r.IpFreqLimitRules[0].IpFreqLimitAction != nil {
			if rate := r.IpFreqLimitRules[0].IpFreqLimitAction.FreqLimitRate; rate != nil {
				summary = fmt.Sprintf("单 IP 限频 %d", *rate)
			}
		}
		b.add("traffic_limit", "ip_freq_limit", "IP 频次限制", sw(r.Switch), summary, nil)
	}
	if r := cfg.IpSpeedLimit; r != nil {
		summary := "单 IP 限速"
		if len(r.IpSpeedLimitRules) > 0 && r.IpSpeedLimitRules[0] != nil && r.IpSpeedLimitRules[0].IpSpeedLimitAction != nil {
			if rate := r.IpSpeedLimitRules[0].IpSpeedLimitAction.SpeedLimitRate; rate != nil {
				summary = fmt.Sprintf("单 IP 限速 %d", *rate)
			}
		}
		b.add("traffic_limit", "ip_speed_limit", "单 IP 限速", sw(r.Switch), summary, nil)
	}
	if r := cfg.DownloadSpeedLimit; r != nil {
		summary := "拖拽/下载限速"
		if len(r.DownloadSpeedLimitRules) > 0 && r.DownloadSpeedLimitRules[0] != nil && r.DownloadSpeedLimitRules[0].DownloadSpeedLimitAction != nil {
			act := r.DownloadSpeedLimitRules[0].DownloadSpeedLimitAction
			if act.SpeedLimitRate != nil {
				summary = fmt.Sprintf("限速 %d(首 %d 后)", *act.SpeedLimitRate, pointerOrZero64(act.SpeedLimitRateAfter))
			}
		}
		b.add("traffic_limit", "download_speed_limit", "下载限速", sw(r.Switch), summary, nil)
	}
}

// buildCDNPerformance 性能优化:智能压缩、页面优化、Range 分片、视频拖拽、QUIC、URL 规范化。
func buildCDNPerformance(b *settingsBuilder, cfg *cdn.DomainConfigForDescribeCdnConfigOutput) {
	if r := cfg.Compression; r != nil {
		formats := make([]string, 0, len(r.CompressionRules))
		for _, cr := range r.CompressionRules {
			if cr != nil && cr.CompressionAction != nil && cr.CompressionAction.CompressionFormat != nil {
				formats = append(formats, strings.ToUpper(*cr.CompressionAction.CompressionFormat))
			}
		}
		b.add("performance", "compression", "智能压缩", sw(r.Switch), detailOnOff(formats), nil)
	}
	if r := cfg.PageOptimization; r != nil {
		kinds := derefList(r.OptimizationType)
		b.add("performance", "page_optimization", "页面优化", sw(r.Switch), detailOnOff(kinds), nil)
	}
	if r := cfg.MultiRange; r != nil {
		b.add("performance", "multi_range", "Range 分片回源", sw(r.Switch), "已开启(大文件分片回源,省回源带宽)", nil)
	}
	if r := cfg.VideoDrag; r != nil {
		b.add("performance", "video_drag", "视频拖拽播放", sw(r.Switch), "已开启", nil)
	}
	if r := cfg.Quic; r != nil {
		b.add("performance", "quic", "QUIC", sw(r.Switch), "已开启", nil)
	}
	if r := cfg.UrlNormalize; r != nil {
		objs := derefList(r.NormalizeObject)
		b.add("performance", "url_normalize", "URL 规范化", sw(r.Switch), "归一化: "+strings.Join(objs, "/"), nil)
	}
}

// buildCDNHTTPS HTTPS:证书/HTTP2/HSTS/TLS 版本/强制跳转。
func buildCDNHTTPS(b *settingsBuilder, cfg *cdn.DomainConfigForDescribeCdnConfigOutput) {
	if r := cfg.HTTPS; r != nil {
		summary := "HTTPS"
		if r.HTTP2 != nil && *r.HTTP2 {
			summary += " + HTTP/2"
		}
		if r.CertInfo != nil && r.CertInfo.CertName != nil {
			summary += " · 证书: " + *r.CertInfo.CertName
		}
		params := map[string]string{}
		if len(r.TlsVersion) > 0 {
			params["tls_version"] = strings.Join(derefList(r.TlsVersion), ",")
		}
		if r.Hsts != nil && r.Hsts.Switch != nil && *r.Hsts.Switch {
			summary += fmt.Sprintf(" · HSTS max-age=%d", pointerOrZero64(r.Hsts.Ttl))
			params["hsts_max_age"] = fmt.Sprintf("%d", pointerOrZero64(r.Hsts.Ttl))
			params["hsts_subdomain"] = stringValue(r.Hsts.Subdomain)
		}
		if r.OCSP != nil && *r.OCSP {
			params["ocsp"] = "on"
		}
		if len(params) == 0 {
			params = nil
		}
		b.add("https", "https", "HTTPS 证书", sw(r.Switch), summary, params)
	}
	if r := cfg.HttpForcedRedirect; r != nil {
		summary := "强制跳转"
		if r.StatusCode != nil {
			summary += "(" + *r.StatusCode + ")"
		}
		b.add("https", "forced_redirect", "强制 HTTPS 跳转", r.EnableForcedRedirect, summary, nil)
	}
}

// buildCDNRedirectOrigin 重定向与回源:URL 重写、回源 Host/协议/302 跟随/超时/头改写。
func buildCDNRedirectOrigin(b *settingsBuilder, cfg *cdn.DomainConfigForDescribeCdnConfigOutput) {
	if r := cfg.RedirectionRewrite; r != nil {
		b.add("redirect", "redirection_rewrite", "URL 重写/跳转", sw(r.Switch),
			rewriteSummary(len(r.RedirectionRule)), nil)
	}
	if cfg.OriginHost != nil {
		b.add("origin", "origin_host", "回源 Host", nil, "回源 Host: "+*cfg.OriginHost, nil)
	}
	if cfg.OriginProtocol != nil {
		b.add("origin", "origin_protocol", "回源协议", nil, "回源协议: "+protocolLabel(*cfg.OriginProtocol), nil)
	}
	if cfg.FollowRedirect != nil {
		b.add("origin", "follow_redirect", "回源 302 跟随", cfg.FollowRedirect, "源站 302/301 时跟随跳转回源", nil)
	}
	if r := cfg.Timeout; r != nil && len(r.TimeoutRules) > 0 && r.TimeoutRules[0] != nil && r.TimeoutRules[0].TimeoutAction != nil {
		act := r.TimeoutRules[0].TimeoutAction
		b.add("origin", "timeout", "回源超时", sw(r.Switch),
			fmt.Sprintf("HTTP %ds / TCP %ds", pointerOrZero64(act.HttpTimeout), pointerOrZero64(act.TcpTimeout)), nil)
	}
	if len(cfg.RequestHeader) > 0 {
		b.add("origin", "request_header", "回源请求头改写", nil,
			headerSummary(len(cfg.RequestHeader), func(i int) (string, string) {
				inst := cfg.RequestHeader[i].RequestHeaderAction
				if inst == nil || len(inst.RequestHeaderInstances) == 0 || inst.RequestHeaderInstances[0] == nil {
					return "", ""
				}
				h := inst.RequestHeaderInstances[0]
				return headerOpLabel(stringValue(h.Action)), stringValue(h.Key)
			}), nil)
	}
	if len(cfg.ResponseHeader) > 0 {
		b.add("origin", "response_header", "节点响应头改写", nil,
			headerSummary(len(cfg.ResponseHeader), func(i int) (string, string) {
				inst := cfg.ResponseHeader[i].ResponseHeaderAction
				if inst == nil || len(inst.ResponseHeaderInstances) == 0 || inst.ResponseHeaderInstances[0] == nil {
					return "", ""
				}
				h := inst.ResponseHeaderInstances[0]
				return headerOpLabel(stringValue(h.Action)), stringValue(h.Key)
			}), nil)
	}
}

// buildCDNBasic 基础:IPv6、加速区域、离线缓存。
func buildCDNBasic(b *settingsBuilder, cfg *cdn.DomainConfigForDescribeCdnConfigOutput) {
	if cfg.IPv6 != nil {
		b.add("basic", "ipv6", "IPv6", sw(cfg.IPv6.Switch), "IPv6 访问", nil)
	}
	if cfg.ServiceRegion != nil {
		b.add("basic", "service_region", "加速区域", nil, regionLabel(*cfg.ServiceRegion), nil)
	}
	if r := cfg.OfflineCache; r != nil {
		b.add("basic", "offline_cache", "离线缓存(源站异常兜底)", sw(r.Switch),
			"源站 "+stringValue(r.StatusCode)+" 时返回过期缓存", nil)
	}
}

// getDCDNDomainSettings DCDN 域名:ListDomainConfig → 分组归一(字段较 CDN 少)。
func (a *CDNAdapter) getDCDNDomainSettings(domainName string) (*types.CDNDomainSettings, error) {
	client, err := a.createDCDNClient()
	if err != nil {
		return nil, err
	}
	input := &dcdn.ListDomainConfigInput{}
	input.SetKeyword(domainName)
	input.SetPageNumber(1)
	input.SetPageSize(10)
	output, err := client.ListDomainConfig(input)
	if err != nil {
		return nil, fmt.Errorf("查询DCDN域名配置失败: %w", err)
	}
	for _, d := range output.DomainList {
		if d == nil || d.Domain == nil || *d.Domain != domainName {
			continue
		}
		b := newSettingsBuilder(domainName)
		if d.IpAccess != nil {
			b.add("access_control", "ip_access", "IP 黑白名单", boolPtr(d.IpAccess.Enable),
				aclSummary(stringValue(d.IpAccess.FilterType), derefList(d.IpAccess.FilterList)), nil)
		}
		if d.UserAgentAccess != nil {
			b.add("access_control", "ua_access", "UA 黑白名单", boolPtr(d.UserAgentAccess.Enable),
				aclSummary(stringValue(d.UserAgentAccess.FilterType), derefList(d.UserAgentAccess.FilterList)), nil)
		}
		if d.RefererAccess != nil {
			b.add("access_control", "referer_access", "Referer 防盗链", boolPtr(d.RefererAccess.Enable),
				aclSummary(stringValue(d.RefererAccess.FilterType), derefList(d.RefererAccess.FilterList)), nil)
		}
		if d.UrlAccess != nil {
			b.add("access_control", "url_auth", "URL 鉴权", boolPtr(d.UrlAccess.Enable),
				onOffText(d.UrlAccess.Enable), nil)
		}
		if d.GzipCompress != nil {
			b.add("performance", "gzip", "智能压缩(Gzip)", boolPtr(d.GzipCompress.Enable), onOffText(d.GzipCompress.Enable), nil)
		}
		if d.BrCompress != nil {
			b.add("performance", "brotli", "Brotli 压缩", boolPtr(d.BrCompress.Enable), onOffText(d.BrCompress.Enable), nil)
		}
		if d.WebSocket != nil {
			b.add("performance", "websocket", "WebSocket", boolPtr(d.WebSocket.Enable),
				fmt.Sprintf("%s(超时 %ds)", onOffText(d.WebSocket.Enable), pointerOrZero32(d.WebSocket.Timeout)), nil)
		}
		if d.Https != nil {
			summary := "HTTPS"
			if d.Https.Http2 != nil && *d.Https.Http2 {
				summary += " + HTTP/2"
			}
			if d.Https.CertBind != nil && d.Https.CertBind.CertName != nil {
				summary += " · 证书: " + *d.Https.CertBind.CertName
			}
			b.add("https", "https", "HTTPS 证书", boolPtr(d.Https.EnableHttps), summary, nil)
			if d.Https.ForceRedirect != nil {
				summary := "强制跳转"
				if code := pointerOrZero32(d.Https.ForceRedirect.RedirectCode); code > 0 {
					summary = fmt.Sprintf("强制跳转(%d)", code)
				}
				b.add("https", "force_redirect", "强制 HTTPS 跳转", boolPtr(d.Https.ForceRedirect.Enable), summary, nil)
			}
		}
		if d.UrlRedirect != nil {
			b.add("redirect", "url_redirect", "URL 重写/跳转", boolPtr(d.UrlRedirect.Enable),
				rewriteSummary(len(d.UrlRedirect.Rules)), nil)
		}
		if d.Origin != nil {
			if d.Origin.OriginHost != nil && d.Origin.OriginHost.HostInfo != nil {
				b.add("origin", "origin_host", "回源 Host", nil, "回源 Host: "+*d.Origin.OriginHost.HostInfo, nil)
			}
			if d.Origin.OriginProtocolType != nil {
				b.add("origin", "origin_protocol", "回源协议", nil, "回源协议: "+protocolLabel(*d.Origin.OriginProtocolType), nil)
			}
		}
		if d.IPv6Switch != nil {
			b.add("basic", "ipv6", "IPv6", d.IPv6Switch, "IPv6 访问", nil)
		}
		if d.Scope != nil {
			b.add("basic", "service_region", "加速区域", nil, regionLabel(*d.Scope), nil)
		}
		return b.build(), nil
	}
	return nil, fmt.Errorf("DCDN域名不存在: %s", domainName)
}

// ===== 小工具 =====

func boolPtr(v *bool) *bool { return v }

func derefList(ps []*string) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		if p != nil {
			out = append(out, *p)
		}
	}
	return out
}

// joinLimit 串联列表,超出 max 折叠为 "n 条"。
func joinLimit(list []string, max int) string {
	if len(list) <= max {
		return strings.Join(list, ",")
	}
	return fmt.Sprintf("%d 条", len(list))
}

// accessKind whitelist/blacklist → 白名单/黑名单。
func accessKind(t string) string {
	if t == "whitelist" {
		return "白名单"
	}
	if t == "blacklist" {
		return "黑名单"
	}
	return t
}

// aclSummary 黑白名单摘要:类型 + 列表折叠,空列表只留类型。
func aclSummary(filterType string, list []string) string {
	kind := accessKind(filterType)
	if len(list) == 0 {
		return kind + "(未配置)"
	}
	return kind + " " + joinLimit(list, 3)
}

// onOffText 启停文案(nil 不可判定时给通用文案)。
func onOffText(p *bool) string {
	if p == nil {
		return "已配置"
	}
	if *p {
		return "已开启"
	}
	return "未开启"
}

// detailOnOff 列表型功能摘要:有明细拼括号,无明细退回已开启。
func detailOnOff(details []string) string {
	if len(details) == 0 {
		return "已开启"
	}
	return "已开启(" + strings.Join(details, "/") + ")"
}

// rewriteSummary 重写规则数摘要,0 条降级为未配置。
func rewriteSummary(n int) string {
	if n == 0 {
		return "未配置"
	}
	return fmt.Sprintf("%d 条重写规则", n)
}

// protocolLabel http/https/follow 等回源协议标签。
func protocolLabel(p string) string {
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

// regionLabel 加速区域(cn/global/...)。
func regionLabel(r string) string {
	switch r {
	case "cn", "domestic":
		return "中国内地"
	case "global":
		return "全球"
	case "oversea", "abroad":
		return "全球(不含中国内地)"
	}
	return r
}

// headerOpLabel add/set/delete → 添加/设置/删除。
func headerOpLabel(op string) string {
	switch op {
	case "add":
		return "添加"
	case "set":
		return "设置"
	case "delete":
		return "删除"
	}
	return op
}

// headerSummary 通用头改写摘要:遍历取 op+key 串联。
func headerSummary(n int, pick func(i int) (string, string)) string {
	parts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		op, key := pick(i)
		if key == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s", op, key))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%d 条", n)
	}
	return strings.Join(parts, " · ")
}

// maskIfSet 非空值脱敏(密钥只体现已配置)。
func maskIfSet(s string) string {
	if s == "" {
		return ""
	}
	return "******"
}
