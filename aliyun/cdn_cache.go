package aliyun

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/cdn"
	"github.com/gotomicro/ego/core/elog"
)

// 缓存相关函数名白名单(2026-09-07 全景探测,10+ 域名实测):
//   - filetype_based_ttl_set: 文件后缀规则(CDN/DCDN 现行名;set_ttl 是
//     旧 API 名,DCDN 根本不支持——曾传 set_ttl 导致 DCDN 域名 500)
//   - path_based_ttl_set: 目录/路径规则(如 /js/ 300s)
//   - path_force_ttl_code: 按状态码强制 TTL(如 301=0,302=0)
//   - set_hashkey_args: URL 参数过滤(缓存键)
// CDN 侧 FunctionNames 支持逗号分隔多函数;DCDN 不传 FunctionNames 拉全量
// 后白名单过滤(传不支持的函数名会 400 整个查询,拉全量免疫函数名漂移)。
const cdnCacheFunctionNames = "filetype_based_ttl_set,path_based_ttl_set,path_force_ttl_code,set_hashkey_args"

// GetCacheConfig 查询 CDN/DCDN 域名的缓存规则。
// CDN 域名走 cdn API;非 CDN 域名(DCDN 全站加速)报错时回退 dcdn API。
func (a *CDNAdapter) GetCacheConfig(ctx context.Context, domainName, domainID string) ([]types.CDNCacheRule, error) {
	if domainName == "" {
		return nil, fmt.Errorf("阿里云CDN缓存配置需要域名")
	}

	rules, err := a.getCDNCacheConfig(domainName)
	if err != nil {
		a.logger.Debug("CDN缓存配置查询失败,尝试DCDN", elog.String("domain", domainName), elog.FieldErr(err))
		rules, err = a.getDCDNCacheConfig(domainName)
		if err != nil {
			return nil, err
		}
	}
	return rules, nil
}

// getCDNCacheConfig CDN 域名:DescribeCdnDomainConfigs(多函数白名单)。
func (a *CDNAdapter) getCDNCacheConfig(domainName string) ([]types.CDNCacheRule, error) {
	client, err := a.createClient()
	if err != nil {
		return nil, err
	}

	request := cdn.CreateDescribeCdnDomainConfigsRequest()
	request.DomainName = domainName
	request.FunctionNames = cdnCacheFunctionNames

	response, err := client.DescribeCdnDomainConfigs(request)
	if err != nil {
		return nil, fmt.Errorf("查询CDN域名配置失败: %w", err)
	}

	rules := make([]types.CDNCacheRule, 0)
	for _, cfg := range response.DomainConfigs.DomainConfig {
		if rule, ok := parseCacheFunction(cfg.FunctionName, cfg.FunctionArgs.FunctionArg); ok {
			rules = append(rules, rule)
		}
	}
	return rules, nil
}

// parseCacheFunction 按函数名分发解析(白名单外的函数忽略)。
func parseCacheFunction(name string, args []cdn.FunctionArg) (types.CDNCacheRule, bool) {
	switch name {
	case "filetype_based_ttl_set":
		return parseTTLRule(args, "file_ext")
	case "path_based_ttl_set":
		return parseTTLRule(args, "directory")
	case "path_force_ttl_code":
		return parseForceTTLCode(args)
	case "set_hashkey_args":
		return parseHashkeyArgs(args)
	}
	return types.CDNCacheRule{}, false
}

// parseTTLRule 文件后缀/路径 TTL 规则(filetype_based_ttl_set 与
// path_based_ttl_set 参数同构:path|file_type + ttl + weight + 行为开关)。
func parseTTLRule(args []cdn.FunctionArg, fallbackType string) (types.CDNCacheRule, bool) {
	rule := types.CDNCacheRule{Type: fallbackType}
	for _, arg := range args {
		switch arg.ArgName {
		case "file_type", "path":
			rule.Path = arg.ArgValue
		case "ttl":
			rule.TTL, _ = strconv.ParseInt(arg.ArgValue, 10, 64)
		case "weight":
			rule.Priority, _ = strconv.Atoi(arg.ArgValue)
		case "swift_follow_cachetime":
			rule.FollowOriginCache = arg.ArgValue == "on"
		case "force_revalidate":
			rule.ForceRevalidate = arg.ArgValue == "on"
		case "swift_no_cache_low":
			rule.NoCacheLowFreq = arg.ArgValue == "on"
		case "swift_origin_cache_high":
			rule.CacheHighFreq = arg.ArgValue == "on"
		}
	}
	if rule.Path == "" && rule.TTL == 0 {
		return types.CDNCacheRule{}, false
	}
	rule.Path = normalizeAliyunPath(rule.Path)
	// path_based_ttl_set 的 path 可为目录(/js/)或全路径,按形态归类
	if fallbackType == "directory" && rule.Path != "*" && strings.Contains(rule.Path, ".") &&
		!strings.HasSuffix(rule.Path, "/") {
		rule.Type = "full_path"
	}
	if rule.FollowOriginCache {
		rule.TTL = -1 // 遵循源站缓存时长(统一模型语义)
	}
	return rule, true
}

// parseForceTTLCode 按状态码强制 TTL 规则(code_string 如 "301=0,302=0")。
func parseForceTTLCode(args []cdn.FunctionArg) (types.CDNCacheRule, bool) {
	rule := types.CDNCacheRule{Type: "status_code", Path: "*"}
	for _, arg := range args {
		switch arg.ArgName {
		case "path":
			rule.Path = normalizeAliyunPath(arg.ArgValue)
		case "code_string":
			rule.CodeString = arg.ArgValue
		case "weight":
			rule.Priority, _ = strconv.Atoi(arg.ArgValue)
		}
	}
	if rule.CodeString == "" {
		return types.CDNCacheRule{}, false
	}
	return rule, true
}

// parseHashkeyArgs URL 参数过滤规则(缓存键):disable=on 表示保留全部
// URL 参数参与缓存;enable + hashkey_args 列表表示仅指定参数参与。
func parseHashkeyArgs(args []cdn.FunctionArg) (types.CDNCacheRule, bool) {
	disabled := false
	keepArgs := ""
	for _, arg := range args {
		switch arg.ArgName {
		case "disable":
			disabled = arg.ArgValue == "on"
		case "hashkey_args":
			keepArgs = arg.ArgValue
		case "weight":
			// 不展示
		}
	}
	if disabled {
		return types.CDNCacheRule{Type: "query_filter", Path: "*", QueryArgs: "保留全部 URL 参数(不忽略)"}, true
	}
	if keepArgs != "" {
		return types.CDNCacheRule{Type: "query_filter", Path: "*", QueryArgs: "仅参数参与缓存: " + keepArgs}, true
	}
	return types.CDNCacheRule{}, false
}

// getDCDNCacheConfig DCDN 域名:DescribeDcdnDomainConfigs(通用请求)。
// 不传 FunctionNames 拉全量后白名单过滤——传不支持的函数名会 400
// (曾传 set_ttl 报 InvalidFunctionName),全量免疫函数名漂移。
func (a *CDNAdapter) getDCDNCacheConfig(domainName string) ([]types.CDNCacheRule, error) {
	client, err := sdk.NewClientWithAccessKey(a.defaultRegion, a.accessKeyID, a.accessKeySecret)
	if err != nil {
		return nil, err
	}

	request := requests.NewCommonRequest()
	request.Method = "POST"
	request.Scheme = "https"
	request.Domain = "dcdn.aliyuncs.com"
	request.Version = "2018-01-15"
	request.ApiName = "DescribeDcdnDomainConfigs"
	request.QueryParams["DomainName"] = domainName

	response, err := client.ProcessCommonRequest(request)
	if err != nil {
		return nil, fmt.Errorf("查询DCDN域名配置失败: %w", err)
	}

	var resp struct {
		DomainConfigs struct {
			DomainConfig []struct {
				FunctionName string `json:"FunctionName"`
				// FunctionArgs 是对象包装 {"FunctionArg":[...]}(与 CDN SDK
				// 展开后的形态不同,直接按原始 JSON 建模)
				FunctionArgs struct {
					FunctionArg []struct {
						ArgName  string `json:"ArgName"`
						ArgValue string `json:"ArgValue"`
					} `json:"FunctionArg"`
				} `json:"FunctionArgs"`
			} `json:"DomainConfig"`
		} `json:"DomainConfigs"`
	}
	if err := json.Unmarshal(response.GetHttpContentBytes(), &resp); err != nil {
		return nil, fmt.Errorf("解析DCDN配置响应失败: %w", err)
	}

	rules := make([]types.CDNCacheRule, 0)
	for _, cfg := range resp.DomainConfigs.DomainConfig {
		args := make([]cdn.FunctionArg, 0, len(cfg.FunctionArgs.FunctionArg))
		for _, arg := range cfg.FunctionArgs.FunctionArg {
			args = append(args, cdn.FunctionArg{ArgName: arg.ArgName, ArgValue: arg.ArgValue})
		}
		if rule, ok := parseCacheFunction(cfg.FunctionName, args); ok {
			rules = append(rules, rule)
		}
	}
	return rules, nil
}

// normalizeAliyunPath file_type 为空或 "0" 表示全站
func normalizeAliyunPath(path string) string {
	if path == "" || path == "0" {
		return "*"
	}
	return path
}
