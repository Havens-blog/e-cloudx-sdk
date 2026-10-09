package huawei

import (
	"context"
	"fmt"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	cdnmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cdn/v2/model"
)

// GetCacheConfig 查询华为云 CDN 域名缓存规则(ShowDomainFullConfig)。
func (a *CDNAdapter) GetCacheConfig(ctx context.Context, domainName, domainID string) ([]types.CDNCacheRule, error) {
	if domainName == "" {
		return nil, fmt.Errorf("华为云CDN缓存配置需要域名")
	}

	client, err := a.createClient()
	if err != nil {
		return nil, err
	}

	// enterprise_project_id 设为 "all" 以覆盖所有企业项目(与列表同步口径一致)
	allProjects := "all"
	request := &cdnmodel.ShowDomainFullConfigRequest{
		DomainName:          domainName,
		EnterpriseProjectId: &allProjects,
	}

	response, err := client.ShowDomainFullConfig(request)
	if err != nil {
		return nil, fmt.Errorf("查询CDN域名全量配置失败: %w", err)
	}
	if response.Configs == nil || response.Configs.CacheRules == nil {
		return []types.CDNCacheRule{}, nil
	}

	rules := make([]types.CDNCacheRule, 0, len(*response.Configs.CacheRules))
	for _, r := range *response.Configs.CacheRules {
		// match_type: all/file_extension/catalog/full_path/home_page
		path := "*"
		ruleType := "all"
		switch r.MatchType {
		case "file_extension":
			// 华为后缀以 ";" 分隔且带 ".",统一为 "," 无点
			path = strings.ReplaceAll(strings.TrimPrefix(valueOrEmpty(r.MatchValue), "."), ";", ",")
			ruleType = "file_ext"
		case "catalog":
			path, ruleType = valueOrEmpty(r.MatchValue), "directory"
		case "full_path":
			path, ruleType = valueOrEmpty(r.MatchValue), "full_path"
		}
		ttl := huaweiTTLSeconds(int64(pointerOrZero(r.Ttl)), r.TtlUnit)
		if r.FollowOrigin != nil && *r.FollowOrigin == "on" {
			ttl = -1 // 跟随源站
		}
		rules = append(rules, types.CDNCacheRule{
			Path:     path,
			Type:     ruleType,
			TTL:      ttl,
			Priority: int(r.Priority),
		})
	}
	return rules, nil
}

func valueOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func pointerOrZero(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

// huaweiTTLSeconds ttl_unit: s=秒 m=分 h=小时 d=天
func huaweiTTLSeconds(ttl int64, unit string) int64 {
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
