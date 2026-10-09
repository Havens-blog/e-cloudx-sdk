package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	"github.com/gotomicro/ego/core/elog"
)

// CDNAdapter AWS CloudFront CDN适配器
type CDNAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewCDNAdapter 创建CDN适配器
func NewCDNAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *CDNAdapter {
	return &CDNAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建CloudFront客户端
func (a *CDNAdapter) createClient(ctx context.Context) (*cloudfront.Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"), // CloudFront是全局服务
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			a.accessKeyID, a.accessKeySecret, "",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("加载AWS配置失败: %w", err)
	}
	return cloudfront.NewFromConfig(cfg), nil
}

// ListInstances 获取CDN分配列表
func (a *CDNAdapter) ListInstances(ctx context.Context, region string) ([]types.CDNInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个CDN分配详情
func (a *CDNAdapter) GetInstance(ctx context.Context, region, distributionID string) (*types.CDNInstance, error) {
	client, err := a.createClient(ctx)
	if err != nil {
		return nil, err
	}

	output, err := client.GetDistribution(ctx, &cloudfront.GetDistributionInput{
		Id: awssdk.String(distributionID),
	})
	if err != nil {
		return nil, fmt.Errorf("获取CloudFront分配详情失败: %w", err)
	}

	if output.Distribution == nil {
		return nil, fmt.Errorf("CloudFront分配不存在: %s", distributionID)
	}

	instance := a.convertDistributionToInstance(output.Distribution)
	return &instance, nil
}

// ListInstancesByIDs 批量获取CDN分配
func (a *CDNAdapter) ListInstancesByIDs(ctx context.Context, region string, distributionIDs []string) ([]types.CDNInstance, error) {
	var result []types.CDNInstance
	for _, id := range distributionIDs {
		inst, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取CloudFront分配失败", elog.String("id", id), elog.FieldErr(err))
			continue
		}
		result = append(result, *inst)
	}
	return result, nil
}

// GetInstanceStatus 获取分配状态
func (a *CDNAdapter) GetInstanceStatus(ctx context.Context, region, distributionID string) (string, error) {
	inst, err := a.GetInstance(ctx, region, distributionID)
	if err != nil {
		return "", err
	}
	return inst.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取分配列表
func (a *CDNAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.CDNInstanceFilter) ([]types.CDNInstance, error) {
	client, err := a.createClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("创建CloudFront客户端失败: %w", err)
	}

	var allInstances []types.CDNInstance
	var marker *string

	for {
		input := &cloudfront.ListDistributionsInput{
			Marker: marker,
		}

		output, err := client.ListDistributions(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("获取CloudFront分配列表失败: %w", err)
		}

		if output.DistributionList == nil || output.DistributionList.Items == nil {
			break
		}

		for _, item := range output.DistributionList.Items {
			inst := a.convertSummaryToInstance(item)

			// 客户端过滤
			if filter != nil {
				if filter.DomainName != "" && !strings.Contains(inst.DomainName, filter.DomainName) {
					continue
				}
				if filter.Status != "" && inst.Status != filter.Status {
					continue
				}
			}

			allInstances = append(allInstances, inst)
		}

		if output.DistributionList.IsTruncated == nil || !*output.DistributionList.IsTruncated {
			break
		}
		marker = output.DistributionList.NextMarker
	}

	// 批量获取标签
	if len(allInstances) > 0 {
		a.fillTags(ctx, client, allInstances)
	}

	a.logger.Info("获取AWS CloudFront分配列表成功", elog.Int("count", len(allInstances)))
	return allInstances, nil
}

// fillTags 批量获取CloudFront分配的标签
func (a *CDNAdapter) fillTags(ctx context.Context, client *cloudfront.Client, instances []types.CDNInstance) {
	for i := range instances {
		if instances[i].DomainID == "" {
			continue
		}
		arn := fmt.Sprintf("arn:aws:cloudfront::%s:distribution/%s", "", instances[i].DomainID)
		// 尝试用 ListTagsForResource
		tagOutput, err := client.ListTagsForResource(ctx, &cloudfront.ListTagsForResourceInput{
			Resource: awssdk.String(arn),
		})
		if err != nil {
			a.logger.Debug("获取CloudFront标签失败", elog.String("id", instances[i].DomainID), elog.FieldErr(err))
			continue
		}
		if tagOutput.Tags != nil && tagOutput.Tags.Items != nil {
			tags := make(map[string]string)
			for _, t := range tagOutput.Tags.Items {
				if t.Key != nil {
					val := ""
					if t.Value != nil {
						val = *t.Value
					}
					tags[*t.Key] = val
				}
			}
			instances[i].Tags = tags
		}
	}
}

// convertDistributionToInstance 转换Distribution为通用CDN实例
func (a *CDNAdapter) convertDistributionToInstance(dist *cftypes.Distribution) types.CDNInstance {
	domainID := awssdk.ToString(dist.Id)
	domainName := awssdk.ToString(dist.DomainName)
	status := awssdk.ToString(dist.Status)

	var origins []types.CDNOrigin
	httpsEnabled := false

	if dist.DistributionConfig != nil {
		if dist.DistributionConfig.Origins != nil {
			for _, o := range dist.DistributionConfig.Origins.Items {
				origins = append(origins, types.CDNOrigin{
					Address: awssdk.ToString(o.DomainName),
					Type:    "domain",
				})
			}
		}
		if dist.DistributionConfig.ViewerCertificate != nil {
			httpsEnabled = true
		}
	}

	createTime := ""
	if dist.LastModifiedTime != nil {
		createTime = dist.LastModifiedTime.Format("2006-01-02T15:04:05Z")
	}

	return types.CDNInstance{
		DomainID:     domainID,
		DomainName:   domainName,
		Cname:        domainName,
		Status:       status,
		Region:       "global",
		ServiceArea:  "global",
		Origins:      origins,
		OriginType:   a.inferOriginType(origins),
		OriginHost:   a.inferOriginHost(origins),
		HTTPSEnabled: httpsEnabled,
		CreationTime: createTime,
		Provider:     "aws",
		Tags:         make(map[string]string),
	}
}

// convertSummaryToInstance 转换DistributionSummary为通用CDN实例
func (a *CDNAdapter) convertSummaryToInstance(item cftypes.DistributionSummary) types.CDNInstance {
	domainID := awssdk.ToString(item.Id)
	domainName := awssdk.ToString(item.DomainName)
	status := awssdk.ToString(item.Status)

	var origins []types.CDNOrigin
	if item.Origins != nil {
		for _, o := range item.Origins.Items {
			origins = append(origins, types.CDNOrigin{
				Address: awssdk.ToString(o.DomainName),
				Type:    "domain",
			})
		}
	}

	httpsEnabled := item.ViewerCertificate != nil

	createTime := ""
	if item.LastModifiedTime != nil {
		createTime = item.LastModifiedTime.Format("2006-01-02T15:04:05Z")
	}

	// 获取别名域名
	if item.Aliases != nil && item.Aliases.Items != nil && len(item.Aliases.Items) > 0 {
		domainName = item.Aliases.Items[0]
	}

	return types.CDNInstance{
		DomainID:     domainID,
		DomainName:   domainName,
		Cname:        awssdk.ToString(item.DomainName),
		Status:       status,
		Region:       "global",
		ServiceArea:  "global",
		Origins:      origins,
		OriginType:   a.inferOriginType(origins),
		OriginHost:   a.inferOriginHost(origins),
		HTTPSEnabled: httpsEnabled,
		CreationTime: createTime,
		Provider:     "aws",
		Tags:         make(map[string]string),
	}
}

// inferOriginType 推断源站类型
func (a *CDNAdapter) inferOriginType(origins []types.CDNOrigin) string {
	if len(origins) > 0 {
		return origins[0].Type
	}
	return ""
}

// inferOriginHost 推断源站地址
func (a *CDNAdapter) inferOriginHost(origins []types.CDNOrigin) string {
	if len(origins) > 0 {
		return origins[0].Address
	}
	return ""
}
