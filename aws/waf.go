package aws

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	wafv2 "github.com/aws/aws-sdk-go-v2/service/wafv2"
	wafv2types "github.com/aws/aws-sdk-go-v2/service/wafv2/types"
	"github.com/gotomicro/ego/core/elog"
)

// WAFAdapter AWS WAFv2适配器
type WAFAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewWAFAdapter 创建WAF适配器
func NewWAFAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *WAFAdapter {
	return &WAFAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建WAFv2客户端
func (a *WAFAdapter) createClient(ctx context.Context, region string) (*wafv2.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			a.accessKeyID, a.accessKeySecret, "",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("加载AWS配置失败: %w", err)
	}
	return wafv2.NewFromConfig(cfg), nil
}

// ListInstances 获取WAF Web ACL列表
func (a *WAFAdapter) ListInstances(ctx context.Context, region string) ([]types.WAFInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个WAF Web ACL详情
func (a *WAFAdapter) GetInstance(ctx context.Context, region, aclID string) (*types.WAFInstance, error) {
	// AWS WAF需要Name和ID以及Scope来获取详情
	// 先通过列表查找
	instances, err := a.ListInstances(ctx, region)
	if err != nil {
		return nil, err
	}
	for _, inst := range instances {
		if inst.InstanceID == aclID {
			return &inst, nil
		}
	}
	return nil, fmt.Errorf("WAF Web ACL不存在: %s", aclID)
}

// ListInstancesByIDs 批量获取WAF Web ACL
func (a *WAFAdapter) ListInstancesByIDs(ctx context.Context, region string, aclIDs []string) ([]types.WAFInstance, error) {
	var result []types.WAFInstance
	for _, id := range aclIDs {
		inst, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取WAF Web ACL失败", elog.String("acl_id", id), elog.FieldErr(err))
			continue
		}
		result = append(result, *inst)
	}
	return result, nil
}

// GetInstanceStatus 获取实例状态
func (a *WAFAdapter) GetInstanceStatus(ctx context.Context, region, aclID string) (string, error) {
	inst, err := a.GetInstance(ctx, region, aclID)
	if err != nil {
		return "", err
	}
	return inst.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取Web ACL列表
func (a *WAFAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.WAFInstanceFilter) ([]types.WAFInstance, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("创建WAFv2客户端失败: %w", err)
	}

	var allInstances []types.WAFInstance

	// 获取Regional Web ACLs
	regionalACLs, err := a.listWebACLs(ctx, client, wafv2types.ScopeRegional, region, nil)
	if err != nil {
		a.logger.Warn("获取Regional WAF Web ACL失败", elog.FieldErr(err))
	} else {
		allInstances = append(allInstances, regionalACLs...)
	}

	// 获取CloudFront Web ACLs (仅在us-east-1)
	if region == "us-east-1" || region == "" {
		cfClient, err := a.createClient(ctx, "us-east-1")
		if err == nil {
			// CloudFront 关联从分配侧反查(ListResourcesForWebACL 对 CF scope 不回资源)。
			cfIndex := a.buildCloudFrontIndex(ctx)
			cfACLs, err := a.listWebACLs(ctx, cfClient, wafv2types.ScopeCloudfront, "us-east-1", cfIndex)
			if err != nil {
				a.logger.Warn("获取CloudFront WAF Web ACL失败", elog.FieldErr(err))
			} else {
				allInstances = append(allInstances, cfACLs...)
			}
		}
	}

	// 客户端过滤
	if filter != nil && filter.InstanceName != "" {
		var filtered []types.WAFInstance
		for _, inst := range allInstances {
			if inst.InstanceName == filter.InstanceName {
				filtered = append(filtered, inst)
			}
		}
		allInstances = filtered
	}

	a.logger.Info("获取AWS WAF Web ACL列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))
	return allInstances, nil
}

// listWebACLs 获取指定Scope的Web ACL列表。
// cfIndex 仅在 CloudFront scope 使用(WebACL ARN → 分配聚合),Regional 传 nil。
func (a *WAFAdapter) listWebACLs(ctx context.Context, client *wafv2.Client, scope wafv2types.Scope, region string, cfIndex map[string]cfResources) ([]types.WAFInstance, error) {
	var allInstances []types.WAFInstance
	var nextMarker *string

	for {
		input := &wafv2.ListWebACLsInput{
			Scope:      scope,
			NextMarker: nextMarker,
		}

		output, err := client.ListWebACLs(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("获取WAF Web ACL列表失败: %w", err)
		}

		for _, acl := range output.WebACLs {
			ruleCount := 0
			protectionMode := ""
			tags := make(map[string]string)
			var protectedHosts, sourceIPs []string
			// 获取详情以获取规则数,并按 scope 回捞关联资源解析为 (防护域名, 源站地址)。
			if acl.Name != nil && acl.Id != nil {
				detail, err := client.GetWebACL(ctx, &wafv2.GetWebACLInput{
					Name:  acl.Name,
					Id:    acl.Id,
					Scope: scope,
				})
				if err == nil && detail.WebACL != nil && detail.WebACL.ARN != nil {
					ruleCount = len(detail.WebACL.Rules)
					// 防护模式:AWS 无全局观察/拦截开关,用默认动作近似——Allow=观察(仅记录不拦截) Block=拦截
					if detail.WebACL.DefaultAction != nil {
						if detail.WebACL.DefaultAction.Block != nil {
							protectionMode = "block"
						} else {
							protectionMode = "observe"
						}
					}
					arn := awssdk.ToString(detail.WebACL.ARN)
					// 回捞 WebACL 标签(best-effort,失败静默保持空映射)
					if tagResp, terr := client.ListTagsForResource(ctx, &wafv2.ListTagsForResourceInput{
						ResourceARN: detail.WebACL.ARN,
					}); terr == nil && tagResp.TagInfoForResource != nil {
						for _, t := range tagResp.TagInfoForResource.TagList {
							tags[awssdk.ToString(t.Key)] = awssdk.ToString(t.Value)
						}
					}
					if scope == wafv2types.ScopeCloudfront {
						// CloudFront 关联从分配侧反查(WebACLId == WebACL ARN)。
						if r, ok := cfIndex[arn]; ok {
							protectedHosts = append(protectedHosts, r.hosts...)
							sourceIPs = append(sourceIPs, r.sources...)
						}
					} else {
						resources, rerr := client.ListResourcesForWebACL(ctx, &wafv2.ListResourcesForWebACLInput{
							WebACLArn: detail.WebACL.ARN,
						})
						if rerr != nil {
							a.logger.Warn("获取 WebACL 关联资源失败",
								elog.String("acl", awssdk.ToString(acl.Name)), elog.FieldErr(rerr))
						} else if resources != nil {
							for _, resArn := range resources.ResourceArns {
								hosts, sources := a.resolveWAFResources(ctx, resArn, region)
								protectedHosts = append(protectedHosts, hosts...)
								sourceIPs = append(sourceIPs, sources...)
							}
						}
					}
				}
			}

			allInstances = append(allInstances, types.WAFInstance{
				InstanceID:     awssdk.ToString(acl.Id),
				InstanceName:   awssdk.ToString(acl.Name),
				Status:         "active",
				Region:         region,
				RuleCount:      ruleCount,
				DomainCount:    len(dedupStrings(protectedHosts)),
				ProtectedHosts: dedupStrings(protectedHosts),
				SourceIPs:      dedupStrings(sourceIPs),
				ProtectionMode: protectionMode,
				WAFEnabled:     true,
				Provider:       "aws",
				// CreationTime:AWS WAFv2 各相关接口(ListWebACLs/GetWebACL/ListTagsForResource)
				// 均不返回 WebACL 创建时间,SDK(aws-sdk-go-v2 wafv2)亦无对应输出字段,
				// 故留空不填;如需精确创建时间只能经 Config/CloudTrail 追溯,当前不引入额外请求。
				CreationTime: "",
				// Tags 来源:GetWebACL 详情不携带标签,按 ARN 调 ListTagsForResource N+1 回填
				// (每条 WebACL 一次,成本=实例数;失败静默保持空映射),见上方 tagResp 处理。
				Tags: tags,
			})
		}

		if output.NextMarker == nil {
			break
		}
		nextMarker = output.NextMarker
	}

	return allInstances, nil
}
