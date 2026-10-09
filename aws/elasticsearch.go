package aws

import (
	"context"
	"fmt"
	"slices"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	osTypes "github.com/aws/aws-sdk-go-v2/service/opensearch/types"
	"github.com/gotomicro/ego/core/elog"
)

// ElasticsearchAdapter AWS OpenSearch 适配器
type ElasticsearchAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewElasticsearchAdapter 创建 OpenSearch 适配器
func NewElasticsearchAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *ElasticsearchAdapter {
	return &ElasticsearchAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建 OpenSearch 客户端
func (a *ElasticsearchAdapter) createClient(ctx context.Context, region string) (*opensearch.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			a.accessKeyID,
			a.accessKeySecret,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("加载AWS配置失败: %w", err)
	}

	return opensearch.NewFromConfig(cfg), nil
}

// ListInstances 获取 OpenSearch 域列表
func (a *ElasticsearchAdapter) ListInstances(ctx context.Context, region string) ([]types.ElasticsearchInstance, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("创建OpenSearch客户端失败: %w", err)
	}

	// 首先获取域名列表
	listInput := &opensearch.ListDomainNamesInput{}
	listOutput, err := client.ListDomainNames(ctx, listInput)
	if err != nil {
		return nil, fmt.Errorf("获取OpenSearch域列表失败: %w", err)
	}

	if len(listOutput.DomainNames) == 0 {
		return []types.ElasticsearchInstance{}, nil
	}

	// 批量获取域详情
	domainNames := make([]string, 0, len(listOutput.DomainNames))
	for _, d := range listOutput.DomainNames {
		if d.DomainName != nil {
			domainNames = append(domainNames, *d.DomainName)
		}
	}

	descInput := &opensearch.DescribeDomainsInput{
		DomainNames: domainNames,
	}
	descOutput, err := client.DescribeDomains(ctx, descInput)
	if err != nil {
		return nil, fmt.Errorf("获取OpenSearch域详情失败: %w", err)
	}

	var instances []types.ElasticsearchInstance
	for _, domain := range descOutput.DomainStatusList {
		instance := a.convertToElasticsearchInstance(&domain, region)
		instances = append(instances, instance)
	}

	return instances, nil
}

// GetInstance 获取单个 OpenSearch 域详情
func (a *ElasticsearchAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.ElasticsearchInstance, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("创建OpenSearch客户端失败: %w", err)
	}

	input := &opensearch.DescribeDomainInput{
		DomainName: aws.String(instanceID),
	}

	output, err := client.DescribeDomain(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("获取OpenSearch域详情失败: %w", err)
	}

	if output.DomainStatus == nil {
		return nil, fmt.Errorf("OpenSearch域不存在: %s", instanceID)
	}

	instance := a.convertToElasticsearchInstance(output.DomainStatus, region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取 OpenSearch 域
func (a *ElasticsearchAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.ElasticsearchInstance, error) {
	var instances []types.ElasticsearchInstance
	for _, id := range instanceIDs {
		instance, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取OpenSearch域失败", elog.String("domain_name", id), elog.FieldErr(err))
			continue
		}
		instances = append(instances, *instance)
	}
	return instances, nil
}

// GetInstanceStatus 获取域状态
func (a *ElasticsearchAdapter) GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, instanceID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取域列表
func (a *ElasticsearchAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.ElasticsearchInstanceFilter) ([]types.ElasticsearchInstance, error) {
	instances, err := a.ListInstances(ctx, region)
	if err != nil {
		return nil, err
	}

	if filter == nil {
		return instances, nil
	}

	var filtered []types.ElasticsearchInstance
	for _, inst := range instances {
		if len(filter.Status) > 0 && !slices.Contains(filter.Status, inst.Status) {
			continue
		}
		if filter.InstanceName != "" && inst.InstanceName != filter.InstanceName {
			continue
		}
		if filter.Version != "" && inst.Version != filter.Version {
			continue
		}
		if filter.VPCID != "" && inst.VPCID != filter.VPCID {
			continue
		}
		filtered = append(filtered, inst)
	}

	return filtered, nil
}

// convertToElasticsearchInstance 转换为统一的 Elasticsearch 实例结构
func (a *ElasticsearchAdapter) convertToElasticsearchInstance(domain *osTypes.DomainStatus, region string) types.ElasticsearchInstance {
	instance := types.ElasticsearchInstance{
		Region:   region,
		Provider: "aws",
	}

	if domain.DomainId != nil {
		instance.InstanceID = *domain.DomainId
	}
	if domain.DomainName != nil {
		instance.InstanceName = *domain.DomainName
	}

	// 状态判断
	if domain.Processing != nil && *domain.Processing {
		instance.Status = "processing"
	} else if domain.Created != nil && *domain.Created {
		instance.Status = "running"
	} else if domain.Deleted != nil && *domain.Deleted {
		instance.Status = "deleted"
	} else {
		instance.Status = "unknown"
	}
	instance.Status = types.ElasticsearchStatus("aws", instance.Status)

	// 版本信息
	if domain.EngineVersion != nil {
		instance.Version = *domain.EngineVersion
	}

	// 集群配置
	if domain.ClusterConfig != nil {
		if domain.ClusterConfig.InstanceType != "" {
			instance.NodeSpec = string(domain.ClusterConfig.InstanceType)
		}
		if domain.ClusterConfig.InstanceCount != nil {
			instance.NodeCount = int(*domain.ClusterConfig.InstanceCount)
		}
		// 专用主节点
		if domain.ClusterConfig.DedicatedMasterEnabled != nil && *domain.ClusterConfig.DedicatedMasterEnabled {
			if domain.ClusterConfig.DedicatedMasterCount != nil {
				instance.MasterCount = int(*domain.ClusterConfig.DedicatedMasterCount)
			}
			if domain.ClusterConfig.DedicatedMasterType != "" {
				instance.MasterSpec = string(domain.ClusterConfig.DedicatedMasterType)
			}
		}
		// 可用区
		if domain.ClusterConfig.ZoneAwarenessEnabled != nil && *domain.ClusterConfig.ZoneAwarenessEnabled {
			if domain.ClusterConfig.ZoneAwarenessConfig != nil && domain.ClusterConfig.ZoneAwarenessConfig.AvailabilityZoneCount != nil {
				instance.ZoneIDs = make([]string, *domain.ClusterConfig.ZoneAwarenessConfig.AvailabilityZoneCount)
			}
		}
	}

	// EBS 存储配置
	if domain.EBSOptions != nil && domain.EBSOptions.EBSEnabled != nil && *domain.EBSOptions.EBSEnabled {
		if domain.EBSOptions.VolumeSize != nil {
			instance.NodeDiskSize = int(*domain.EBSOptions.VolumeSize)
		}
		if domain.EBSOptions.VolumeType != "" {
			instance.NodeDiskType = string(domain.EBSOptions.VolumeType)
		}
	}

	// VPC 配置
	if domain.VPCOptions != nil {
		if domain.VPCOptions.VPCId != nil {
			instance.VPCID = *domain.VPCOptions.VPCId
		}
		if domain.VPCOptions.SubnetIds != nil && len(domain.VPCOptions.SubnetIds) > 0 {
			instance.VSwitchID = domain.VPCOptions.SubnetIds[0]
		}
		if domain.VPCOptions.SecurityGroupIds != nil && len(domain.VPCOptions.SecurityGroupIds) > 0 {
			instance.SecurityGroupID = domain.VPCOptions.SecurityGroupIds[0]
		}
		if domain.VPCOptions.AvailabilityZones != nil {
			instance.ZoneIDs = domain.VPCOptions.AvailabilityZones
			if len(domain.VPCOptions.AvailabilityZones) > 0 {
				instance.Zone = domain.VPCOptions.AvailabilityZones[0]
			}
		}
	}

	// 端点
	if domain.Endpoint != nil {
		instance.PrivateEndpoint = *domain.Endpoint
	}
	if domain.Endpoints != nil {
		if vpc, ok := domain.Endpoints["vpc"]; ok {
			instance.PrivateEndpoint = vpc
		}
	}
	if domain.DomainEndpointOptions != nil {
		if domain.DomainEndpointOptions.CustomEndpoint != nil {
			instance.PublicEndpoint = *domain.DomainEndpointOptions.CustomEndpoint
			instance.EnablePublicAccess = true
		}
		if domain.DomainEndpointOptions.EnforceHTTPS != nil {
			instance.SSLEnabled = *domain.DomainEndpointOptions.EnforceHTTPS
		}
	}

	// Kibana/Dashboard 端点 - 使用 Endpoints map 获取
	if domain.Endpoints != nil {
		if kibana, ok := domain.Endpoints["kibana"]; ok {
			instance.KibanaEndpoint = kibana
		}
	}

	// 认证配置
	if domain.AdvancedSecurityOptions != nil {
		if domain.AdvancedSecurityOptions.Enabled != nil {
			instance.AuthEnabled = *domain.AdvancedSecurityOptions.Enabled
		}
	}

	// ARN
	if domain.ARN != nil {
		instance.ResourceGroupID = *domain.ARN
	}

	// 标签需要单独 API 调用获取，这里暂不处理

	return instance
}
