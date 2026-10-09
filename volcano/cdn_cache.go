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

// GetCacheConfig 查询火山引擎 CDN/DCDN 域名缓存规则。
// CDN 域名走 DescribeCdnConfig;失败时回退 DCDN ListDomainConfig(全站加速域名)。
func (a *CDNAdapter) GetCacheConfig(ctx context.Context, domainName, domainID string) ([]types.CDNCacheRule, error) {
	if domainName == "" {
		return nil, fmt.Errorf("火山引擎CDN缓存配置需要域名")
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

// getCDNCacheConfig CDN 域名:DescribeCdnConfig 的 Cache 规则
func (a *CDNAdapter) getCDNCacheConfig(domainName string) ([]types.CDNCacheRule, error) {
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
	if output.DomainConfig == nil || output.DomainConfig.Cache == nil {
		return []types.CDNCacheRule{}, nil
	}

	rules := make([]types.CDNCacheRule, 0, len(output.DomainConfig.Cache))
	for _, c := range output.DomainConfig.Cache {
		if c == nil || c.CacheAction == nil {
			continue
		}
		path := "*"
		ruleType := "all"
		if c.Condition != nil {
			for _, cr := range c.Condition.ConditionRule {
				if cr == nil {
					continue
				}
				obj := stringValue(cr.Object)
				val := stringValue(cr.Value)
				if obj == "file_extension" && val != "" {
					path, ruleType = val, "file_ext"
				} else if obj == "path" && val != "" {
					path, ruleType = val, classifyPath(val)
				}
			}
		}
		ttl := int64(0) // 不缓存
		if stringValue(c.CacheAction.Action) == "cache" {
			ttl = int64(pointerOrZero64(c.CacheAction.Ttl))
		}
		rules = append(rules, types.CDNCacheRule{Path: path, Type: ruleType, TTL: ttl})
	}
	return rules, nil
}

// getDCDNCacheConfig DCDN 域名:ListDomainConfig(按域名过滤)的 Cache.CacheRules
func (a *CDNAdapter) getDCDNCacheConfig(domainName string) ([]types.CDNCacheRule, error) {
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
		if d == nil || d.Domain == nil || *d.Domain != domainName || d.Cache == nil {
			continue
		}
		rules := make([]types.CDNCacheRule, 0, len(d.Cache.CacheRules))
		for _, r := range d.Cache.CacheRules {
			if r == nil {
				continue
			}
			path := stringValue(r.Contents)
			if path == "" {
				path = "*"
			}
			ttl := volcanoUnitSeconds(int64(pointerOrZero32(r.CacheTime)), stringValue(r.CacheTimeUnit))
			switch stringValue(r.Policy) {
			case "follow_origin":
				ttl = -1 // 跟随源站
			case "no_cache":
				ttl = 0
			}
			rules = append(rules, types.CDNCacheRule{
				Path: path,
				Type: classifyPath(path),
				TTL:  ttl,
			})
		}
		return rules, nil
	}
	return nil, fmt.Errorf("DCDN域名不存在: %s", domainName)
}

func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func pointerOrZero64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func pointerOrZero32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

// volcanoUnitSeconds dcdn 单位换算: s/m/h/d
func volcanoUnitSeconds(ttl int64, unit string) int64 {
	switch unit {
	case "m":
		return ttl * 60
	case "h":
		return ttl * 3600
	case "d":
		return ttl * 86400
	default:
		return ttl
	}
}

// classifyPath 按路径形态归类匹配类型(与 aliyun/tencent 适配器同规则)
func classifyPath(path string) string {
	if path == "" || path == "*" {
		return "all"
	}
	if strings.HasPrefix(path, "/") {
		return "directory"
	}
	return "file_ext"
}
