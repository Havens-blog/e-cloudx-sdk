// CDN 域名功能配置全景(性能优化/访问控制/流量限制/HTTPS/重定向/回源)。
// 2026-09-07 全景探测 11 域名(CDN+DCDN 双产品,46 个函数名)后按语义归类;
// 参数敏感项(cert/pkey/dkey/鉴权密钥)脱敏。未收录函数跳过(随探测扩展)。
package aliyun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/cdn"
	"github.com/gotomicro/ego/core/elog"
)

// funcMeta 函数 → 归一化名/分组(实测出现的 + 文档常见预防性收录)。
var funcMeta = map[string]struct {
	name     string
	category string
}{
	// 访问控制
	"ali_ua":                {"UA 黑白名单", "access_control"},
	"ip_black_list_set":     {"IP 黑名单", "access_control"},
	"ip_white_list_set":     {"IP 白名单", "access_control"},
	"referer_white_list_set": {"Referer 白名单", "access_control"},
	"referer_black_list_set": {"Referer 黑名单", "access_control"},
	"auth_key":              {"URL 鉴权(A/B/C)", "access_control"},
	"cc_defense":            {"CC 防护", "access_control"},
	// 流量限制
	"limit_rate":    {"拖拽限速", "traffic_limit"},
	"traffic_limit": {"流量封顶", "traffic_limit"},
	// 性能优化
	"gzip":          {"智能压缩(Gzip)", "performance"},
	"brotli":        {"Brotli 压缩", "performance"},
	"tesla":         {"页面优化(HTML/JS/CSS 去冗)", "performance"},
	"range":         {"分片回源", "performance"},
	"websocket":     {"WebSocket", "performance"},
	"video_seek":    {"视频拖拽播放", "performance"},
	"quic":          {"QUIC", "performance"},
	"p2pqos":        {"P2P 加速", "performance"},
	"p2pqos_l2":     {"P2P 加速(L2)", "performance"},
	"edge_function": {"边缘函数(EdgeScript)", "performance"},
	"dynamic":       {"动态内容路由(全站加速)", "performance"},
	// HTTPS
	"https_option":      {"HTTPS 证书", "https"},
	"https_force":       {"强制 HTTPS 跳转", "https"},
	"https_tls_version": {"TLS 版本", "https"},
	"HSTS":              {"HSTS", "https"},
	// 重定向
	"host_redirect": {"URL 重写/跳转", "redirect"},
	// 回源
	"set_req_host_header":    {"回源 Host", "origin"},
	"forward_scheme":         {"回源协议跟随", "origin"},
	"oss_auth":               {"OSS 私有桶回源", "origin"},
	"origin_response_header": {"回源响应头改写", "origin"},
	"set_resp_header":        {"节点响应头改写", "origin"},
	"forward_timeout":        {"回源超时", "origin"},
	"domainVpc":              {"源站 VPC", "origin"},
	"ipv6":        {"IPv6", "basic"},
	"dm_coverage": {"加速区域", "basic"},
}

// sensitiveArgs 参数级脱敏(证书/私钥/密钥)。裸 key 不脱敏——set_resp_header
// 等函数的 ArgName key/header_name 是表头名,不是密钥;URL 鉴权密钥形如 key1/key_a。
var sensitiveArgs = map[string]bool{
	"cert": true, "pkey": true, "dkey": true, "private_key": true,
	"key1": true, "key2": true, "key3": true,
	"key_a": true, "key_b": true, "key_c": true,
}

// groupOrder 分组渲染顺序。
var groupOrder = []struct{ key, label string }{
	{"access_control", "访问控制"},
	{"traffic_limit", "流量限制"},
	{"performance", "性能优化"},
	{"https", "HTTPS"},
	{"redirect", "重定向"},
	{"origin", "回源"},
	{"basic", "基础"},
}

// GetDomainSettings 域名功能配置全景:CDN API 优先,DCDN 回退(与缓存配置同构)。
func (a *CDNAdapter) GetDomainSettings(ctx context.Context, domainName, domainID string) (*types.CDNDomainSettings, error) {
	if domainName == "" {
		return nil, fmt.Errorf("阿里云CDN功能配置需要域名")
	}
	raw, product, err := a.describeDomainConfigs(domainName)
	if err != nil {
		return nil, err
	}
	return buildDomainSettings(domainName, product, raw), nil
}

// describeDomainConfigs 拉全量 DomainConfigs(CDN → DCDN 回退),返回原始
// JSON 与产品标识。不传 FunctionNames(传不支持函数名会 400,全量免疫漂移)。
func (a *CDNAdapter) describeDomainConfigs(domainName string) (json.RawMessage, string, error) {
	client, err := a.createClient()
	if err != nil {
		return nil, "", err
	}
	req := cdn.CreateDescribeCdnDomainConfigsRequest()
	req.DomainName = domainName
	if resp, err := client.DescribeCdnDomainConfigs(req); err == nil {
		return json.RawMessage(resp.GetHttpContentBytes()), "cdn", nil
	} else {
		elog.Debug("CDN 功能配置查询失败,尝试 DCDN", elog.String("domain", domainName), elog.FieldErr(err))
	}
	common, err := sdk.NewClientWithAccessKey(a.defaultRegion, a.accessKeyID, a.accessKeySecret)
	if err != nil {
		return nil, "", err
	}
	request := requests.NewCommonRequest()
	request.Method = "POST"
	request.Scheme = "https"
	request.Domain = "dcdn.aliyuncs.com"
	request.Version = "2018-01-15"
	request.ApiName = "DescribeDcdnDomainConfigs"
	request.QueryParams["DomainName"] = domainName
	resp, err := common.ProcessCommonRequest(request)
	if err != nil {
		return nil, "", fmt.Errorf("查询域名功能配置失败: %w", err)
	}
	return json.RawMessage(resp.GetHttpContentBytes()), "dcdn", nil
}

// rawConfig 统一原始响应形态(两产品 FunctionArgs 均为 {"FunctionArg":[...]})。
type rawConfig struct {
	DomainConfigs struct {
		DomainConfig []struct {
			FunctionName string `json:"FunctionName"`
			FunctionArgs struct {
				FunctionArg []struct {
					ArgName  string `json:"ArgName"`
					ArgValue string `json:"ArgValue"`
				} `json:"FunctionArg"`
			} `json:"FunctionArgs"`
		} `json:"DomainConfig"`
	} `json:"DomainConfigs"`
}

// buildDomainSettings 原始配置 → 分组归一化。
func buildDomainSettings(domain, product string, raw json.RawMessage) *types.CDNDomainSettings {
	var parsed rawConfig
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return &types.CDNDomainSettings{Domain: domain}
	}
	settings := &types.CDNDomainSettings{Domain: domain}
	byCategory := map[string][]types.CDNConfigSetting{}
	for _, cfg := range parsed.DomainConfigs.DomainConfig {
		meta, ok := funcMeta[cfg.FunctionName]
		if !ok {
			continue // 未收录函数跳过
		}
		args := map[string]string{}
		for _, arg := range cfg.FunctionArgs.FunctionArg {
			v := arg.ArgValue
			if sensitiveArgs[arg.ArgName] {
				v = "******"
			}
			args[arg.ArgName] = truncateArg(v)
		}
		byCategory[meta.category] = append(byCategory[meta.category], types.CDNConfigSetting{
			Key:     cfg.FunctionName,
			Name:    meta.name,
			Enabled: enabledOf(cfg.FunctionName, args),
			Summary: summarizeFunc(cfg.FunctionName, product, args),
			Params:  args,
		})
	}
	for _, g := range groupOrder {
		if items := byCategory[g.key]; len(items) > 0 {
			settings.Groups = append(settings.Groups, types.CDNConfigGroup{
				Category: g.key, Label: g.label, Items: items,
			})
		}
	}
	return settings
}

// enabledOf 可判定启用状态的函数返回 on/off,其余 nil(不适用)。
func enabledOf(fn string, args map[string]string) *bool {
	var on bool
	switch fn {
	case "gzip", "brotli", "tesla", "range", "websocket", "video_seek", "quic",
		"p2pqos", "p2pqos_l2", "edge_function", "cc_defense", "https_force",
		"ipv6", "HSTS", "dynamic":
		on = args["enable"] == "on" || args["enabled"] == "on" || args["switch"] == "on"
		return &on
	case "ali_ua":
		on = args["ua"] != ""
		return &on
	case "ip_black_list_set", "ip_white_list_set":
		on = args["ip_list"] != ""
		return &on
	case "limit_rate":
		on = args["ali_limit_rate"] != ""
		return &on
	}
	return nil
}

// summarizeFunc 人话摘要(高频函数特化,其余通用 key=value 串联)。
func summarizeFunc(fn, product string, args map[string]string) string {
	switch fn {
	case "https_option":
		s := "HTTPS"
		if args["http2"] == "on" {
			s += " + HTTP/2"
		}
		if args["cert_name"] != "" {
			s += " · 证书: " + args["cert_name"]
		}
		return s
	case "https_tls_version":
		var on []string
		for _, k := range []string{"tls10", "tls11", "tls12", "tls13"} {
			if args[k] == "on" {
				on = append(on, strings.ToUpper(strings.Replace(k, "tls", "TLS ", 1)))
			}
		}
		return "启用: " + strings.Join(on, " / ")
	case "HSTS":
		return fmt.Sprintf("max-age=%s includeSubDomains=%s", args["https_hsts_max_age"], args["https_hsts_include_subdomains"])
	case "host_redirect":
		return fmt.Sprintf("%s → %s(%s)", args["regex"], args["replacement"], redirectFlag(args["flag"]))
	case "ali_ua":
		kind := "黑名单"
		if args["type"] == "white" {
			kind = "白名单"
		}
		return kind + ": " + args["ua"]
	case "ip_black_list_set", "ip_white_list_set":
		return fmt.Sprintf("%d 条网段", len(strings.Split(args["ip_list"], ",")))
	case "limit_rate":
		s := fmt.Sprintf("限速 %s%s(首 %s 后)", args["ali_limit_rate"], args["traffic_limit_unit"], args["ali_limit_rate_after"])
		if args["ali_limit_start_hour"] != "" {
			s += fmt.Sprintf(",时段 %s:00-%s:00", args["ali_limit_start_hour"], args["ali_limit_end_hour"])
		}
		return s
	case "cc_defense", "websocket", "https_force", "ipv6":
		if args["enable"] == "on" || args["enabled"] == "on" || args["switch"] == "on" {
			return "已开启"
		}
		return "未开启"
	case "gzip", "brotli", "tesla":
		if args["enable"] == "on" {
			detail := ""
			if fn == "tesla" {
				parts := []string{}
				if args["trim_js"] == "on" {
					parts = append(parts, "JS")
				}
				if args["trim_css"] == "on" {
					parts = append(parts, "CSS")
				}
				detail = "(" + strings.Join(parts, "/") + " 去冗)"
			}
			return "已开启" + detail
		}
		return "未开启"
	case "range":
		if args["enable"] == "on" {
			return "已开启(大文件分片回源,省回源带宽)"
		}
		return "未开启"
	case "set_req_host_header":
		return "回源 Host: " + args["domain_name"]
	case "oss_auth":
		return "私有桶: " + firstSegmentStr(args["oss_bucket_id"], ".")
	case "origin_response_header", "set_resp_header":
		return fmt.Sprintf("%s %s: %s", headerOp(args["header_operation_type"]), args["header_name"], args["header_value"])
	case "dynamic":
		return fmt.Sprintf("动态路由: %s(静态资源后缀 %s)", args["dynamic_route_origin"], firstSegmentStr(args["static_route_type"], ","))
	case "dm_coverage":
		switch args["coverage"] {
		case "domestic":
			return "中国内地"
		case "overseas":
			return "全球(不含中国内地)"
		case "global":
			return "全球"
		}
		return args["coverage"]
	case "forward_timeout":
		return args["forward_timeout"] + " 秒"
	case "domainVpc":
		return "VPC: " + args["vpc_name"]
	}
	// 通用兜底:key=value 串联(跳过空值)
	var parts []string
	for _, k := range sortedKeys(args) {
		parts = append(parts, k+"="+args[k])
	}
	return strings.Join(parts, " · ")
}

func redirectFlag(f string) string {
	switch f {
	case "redirect":
		return "301/302 跳转"
	case "rewrite":
		return "内部改写"
	case "break":
		return "改写终止"
	}
	return f
}

func headerOp(op string) string {
	switch op {
	case "add":
		return "添加"
	case "delete":
		return "删除"
	case "set":
		return "设置"
	}
	return op
}

func firstSegmentStr(s, sep string) string {
	if i := strings.Index(s, sep); i >= 0 {
		if len(s[:i]) > 24 {
			return s[:24] + "…"
		}
		return s[:i] + "…"
	}
	return s
}

func truncateArg(s string) string {
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// 简单插入排序(map 无序,参数少)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
