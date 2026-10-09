package tencent

import (
	"context"
	"fmt"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	tencentcdn "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cdn/v20180606"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
)

// GetCacheConfig 查询腾讯云 CDN/ECDN 域名缓存规则。
// 复用 DescribeDomainsConfig(详情接口本身返回完整缓存配置)。
func (a *CDNAdapter) GetCacheConfig(ctx context.Context, domainName, domainID string) ([]types.CDNCacheRule, error) {
	if domainName == "" {
		return nil, fmt.Errorf("腾讯云CDN缓存配置需要域名")
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
	if len(response.Response.Domains) == 0 {
		return nil, fmt.Errorf("CDN域名不存在: %s", domainName)
	}

	return convertTencentCache(response.Response.Domains[0].Cache), nil
}

// convertTencentCache 转换 Cache(SimpleCache 基础规则 + RuleCache 路径规则)
func convertTencentCache(cache *tencentcdn.Cache) []types.CDNCacheRule {
	if cache == nil {
		return []types.CDNCacheRule{}
	}

	rules := make([]types.CDNCacheRule, 0)
	if cache.SimpleCache != nil {
		for _, r := range cache.SimpleCache.CacheRules {
			if r == nil {
				continue
			}
			rules = append(rules, types.CDNCacheRule{
				Path: tencentRulePath(r.CacheContents),
				Type: classifyPath(tencentRulePath(r.CacheContents)),
				TTL:  int64(valueOrZero(r.CacheTime)),
			})
		}
	}
	for _, rc := range cache.RuleCache {
		if rc == nil || rc.CacheConfig == nil {
			continue
		}
		path := tencentRulePath(rc.RulePaths)
		ttl := int64(0)
		switch {
		case rc.CacheConfig.Cache != nil:
			ttl = int64(valueOrZero(rc.CacheConfig.Cache.CacheTime))
		case rc.CacheConfig.FollowOrigin != nil && switchOn(rc.CacheConfig.FollowOrigin.Switch):
			ttl = -1 // 跟随源站
		case rc.CacheConfig.NoCache != nil && switchOn(rc.CacheConfig.NoCache.Switch):
			ttl = 0
		}
		rules = append(rules, types.CDNCacheRule{
			Path: path,
			Type: classifyPath(path),
			TTL:  ttl,
		})
	}
	return rules
}

// tencentRulePath CacheContents 为空(all)时统一为 *
func tencentRulePath(contents []*string) string {
	parts := make([]string, 0, len(contents))
	for _, c := range contents {
		if c != nil {
			parts = append(parts, *c)
		}
	}
	if len(parts) == 0 {
		return "*"
	}
	return strings.Join(parts, ",")
}

func valueOrZero(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// switchOn 腾讯云 Switch 字段 "on" 判定
func switchOn(p *string) bool {
	return p != nil && *p == "on"
}

// classifyPath 按路径形态归类匹配类型(与 aliyun 适配器同规则)
func classifyPath(path string) string {
	if path == "" || path == "*" {
		return "all"
	}
	if strings.HasPrefix(path, "/") {
		return "directory"
	}
	return "file_ext"
}
