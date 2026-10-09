package aws

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
)

// GetCacheConfig 查询 CloudFront 分配的缓存行为。
// CloudFront 以分配 ID 为键(域名无法定位分配),domainID 必传。
func (a *CDNAdapter) GetCacheConfig(ctx context.Context, domainName, domainID string) ([]types.CDNCacheRule, error) {
	if domainID == "" {
		return nil, fmt.Errorf("AWS CloudFront 缓存配置需要分配 ID")
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
	if output.Distribution == nil || output.Distribution.DistributionConfig == nil {
		return nil, fmt.Errorf("CloudFront分配不存在: %s", domainID)
	}

	cfg := output.Distribution.DistributionConfig
	rules := make([]types.CDNCacheRule, 0, 1+len(cfg.CacheBehaviors.Items))

	// 默认缓存行为(全站 *)
	if def := cfg.DefaultCacheBehavior; def != nil {
		rules = append(rules, types.CDNCacheRule{
			Path: "*",
			Type: "all",
			TTL:  awssdk.ToInt64(def.DefaultTTL),
		})
	}

	// 路径缓存行为
	for _, b := range cfg.CacheBehaviors.Items {
		rules = append(rules, types.CDNCacheRule{
			Path: awssdk.ToString(b.PathPattern),
			Type: "full_path",
			TTL:  awssdk.ToInt64(b.DefaultTTL),
		})
	}
	return rules, nil
}
