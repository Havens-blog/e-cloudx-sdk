package aliyun

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	es "github.com/alibabacloud-go/elasticsearch-20170613/v4/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/gotomicro/ego/core/elog"
)

// ElasticsearchAdapter 阿里云 Elasticsearch 适配器
type ElasticsearchAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewElasticsearchAdapter 创建 Elasticsearch 适配器
func NewElasticsearchAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *ElasticsearchAdapter {
	return &ElasticsearchAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建 Elasticsearch 客户端
func (a *ElasticsearchAdapter) createClient(region string) (*es.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}
	config := &openapi.Config{
		AccessKeyId:     tea.String(a.accessKeyID),
		AccessKeySecret: tea.String(a.accessKeySecret),
		RegionId:        tea.String(region),
	}
	config.Endpoint = tea.String(fmt.Sprintf("elasticsearch.%s.aliyuncs.com", region))
	return es.NewClient(config)
}

// ListInstances 获取 Elasticsearch 实例列表
func (a *ElasticsearchAdapter) ListInstances(ctx context.Context, region string) ([]types.ElasticsearchInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建ES客户端失败: %w", err)
	}

	var instances []types.ElasticsearchInstance
	page := int32(1)
	size := int32(50)

	for {
		request := &es.ListInstanceRequest{
			Page: tea.Int32(page),
			Size: tea.Int32(size),
		}

		response, err := client.ListInstance(request)
		if err != nil {
			return nil, fmt.Errorf("获取ES实例列表失败: %w", err)
		}

		if response.Body == nil || response.Body.Result == nil || len(response.Body.Result) == 0 {
			break
		}

		for _, inst := range response.Body.Result {
			instance := a.convertToElasticsearchInstance(inst, region)
			instances = append(instances, instance)
		}

		if len(response.Body.Result) < int(size) {
			break
		}
		page++
	}

	return instances, nil
}

// GetInstance 获取单个 Elasticsearch 实例详情
func (a *ElasticsearchAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.ElasticsearchInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建ES客户端失败: %w", err)
	}

	response, err := client.DescribeInstance(tea.String(instanceID))
	if err != nil {
		return nil, fmt.Errorf("获取ES实例详情失败: %w", err)
	}

	if response.Body == nil || response.Body.Result == nil {
		return nil, fmt.Errorf("ES实例不存在: %s", instanceID)
	}

	instance := a.convertDetailToElasticsearchInstance(response.Body.Result, region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取 Elasticsearch 实例
func (a *ElasticsearchAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.ElasticsearchInstance, error) {
	var instances []types.ElasticsearchInstance
	for _, id := range instanceIDs {
		instance, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取ES实例失败", elog.String("instance_id", id), elog.FieldErr(err))
			continue
		}
		instances = append(instances, *instance)
	}
	return instances, nil
}

// GetInstanceStatus 获取实例状态
func (a *ElasticsearchAdapter) GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, instanceID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取实例列表
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
func (a *ElasticsearchAdapter) convertToElasticsearchInstance(inst *es.ListInstanceResponseBodyResult, region string) types.ElasticsearchInstance {
	instance := types.ElasticsearchInstance{
		Region:   region,
		Provider: "aliyun",
	}

	if inst.InstanceId != nil {
		instance.InstanceID = *inst.InstanceId
	}
	if inst.Description != nil {
		instance.InstanceName = *inst.Description
		instance.Description = *inst.Description
	}
	if inst.Status != nil {
		instance.Status = types.ElasticsearchStatus("aliyun", *inst.Status)
	}
	if inst.EsVersion != nil {
		instance.Version = *inst.EsVersion
	}
	if inst.NodeAmount != nil {
		instance.NodeCount = int(*inst.NodeAmount)
	}
	if inst.NodeSpec != nil && inst.NodeSpec.Spec != nil {
		instance.NodeSpec = *inst.NodeSpec.Spec
	}
	if inst.NodeSpec != nil && inst.NodeSpec.Disk != nil {
		instance.NodeDiskSize = int(*inst.NodeSpec.Disk)
	}
	if inst.NodeSpec != nil && inst.NodeSpec.DiskType != nil {
		instance.NodeDiskType = *inst.NodeSpec.DiskType
	}
	if inst.PaymentType != nil {
		if *inst.PaymentType == "prepaid" {
			instance.ChargeType = "PrePaid"
		} else {
			instance.ChargeType = "PostPaid"
		}
	}
	if inst.CreatedAt != nil {
		if t, err := time.Parse("2006-01-02T15:04:05.000Z", *inst.CreatedAt); err == nil {
			instance.CreationTime = t
		}
	}
	if inst.UpdatedAt != nil {
		if t, err := time.Parse("2006-01-02T15:04:05.000Z", *inst.UpdatedAt); err == nil {
			instance.UpdateTime = t
		}
	}
	if inst.ResourceGroupId != nil {
		instance.ResourceGroupID = *inst.ResourceGroupId
	}

	// 解析标签
	if inst.Tags != nil {
		instance.Tags = make(map[string]string)
		for _, tag := range inst.Tags {
			if tag.TagKey != nil && tag.TagValue != nil {
				instance.Tags[*tag.TagKey] = *tag.TagValue
			}
		}
	}

	return instance
}

// convertDetailToElasticsearchInstance 从详情转换
func (a *ElasticsearchAdapter) convertDetailToElasticsearchInstance(inst *es.DescribeInstanceResponseBodyResult, region string) types.ElasticsearchInstance {
	instance := types.ElasticsearchInstance{
		Region:   region,
		Provider: "aliyun",
	}

	if inst.InstanceId != nil {
		instance.InstanceID = *inst.InstanceId
	}
	if inst.Description != nil {
		instance.InstanceName = *inst.Description
		instance.Description = *inst.Description
	}
	if inst.Status != nil {
		instance.Status = types.ElasticsearchStatus("aliyun", *inst.Status)
	}
	if inst.EsVersion != nil {
		instance.Version = *inst.EsVersion
	}
	if inst.NodeAmount != nil {
		instance.NodeCount = int(*inst.NodeAmount)
	}
	if inst.NodeSpec != nil && inst.NodeSpec.Spec != nil {
		instance.NodeSpec = *inst.NodeSpec.Spec
	}
	if inst.NodeSpec != nil && inst.NodeSpec.Disk != nil {
		instance.NodeDiskSize = int(*inst.NodeSpec.Disk)
	}
	if inst.NodeSpec != nil && inst.NodeSpec.DiskType != nil {
		instance.NodeDiskType = *inst.NodeSpec.DiskType
	}

	// 专用主节点
	if inst.AdvancedDedicateMaster != nil && *inst.AdvancedDedicateMaster {
		if inst.MasterConfiguration != nil {
			if inst.MasterConfiguration.Amount != nil {
				instance.MasterCount = int(*inst.MasterConfiguration.Amount)
			}
			if inst.MasterConfiguration.Spec != nil {
				instance.MasterSpec = *inst.MasterConfiguration.Spec
			}
		}
	}

	// 协调节点
	if inst.HaveClientNode != nil && *inst.HaveClientNode {
		if inst.ClientNodeConfiguration != nil {
			if inst.ClientNodeConfiguration.Amount != nil {
				instance.ClientCount = int(*inst.ClientNodeConfiguration.Amount)
			}
			if inst.ClientNodeConfiguration.Spec != nil {
				instance.ClientSpec = *inst.ClientNodeConfiguration.Spec
			}
		}
	}

	// Kibana
	if inst.HaveKibana != nil && *inst.HaveKibana {
		instance.KibanaCount = 1
		if inst.KibanaConfiguration != nil && inst.KibanaConfiguration.Spec != nil {
			instance.KibanaSpec = *inst.KibanaConfiguration.Spec
		}
	}

	// 网络信息
	if inst.NetworkConfig != nil {
		if inst.NetworkConfig.VpcId != nil {
			instance.VPCID = *inst.NetworkConfig.VpcId
		}
		if inst.NetworkConfig.VswitchId != nil {
			instance.VSwitchID = *inst.NetworkConfig.VswitchId
		}
	}

	// 访问端点
	if inst.Domain != nil {
		instance.PrivateEndpoint = *inst.Domain
	}
	if inst.PublicDomain != nil {
		instance.PublicEndpoint = *inst.PublicDomain
	}
	if inst.KibanaDomain != nil {
		instance.KibanaEndpoint = *inst.KibanaDomain
	}
	if inst.Port != nil {
		instance.Port = int(*inst.Port)
	}
	if inst.EnablePublic != nil {
		instance.EnablePublicAccess = *inst.EnablePublic
	}

	// 计费信息
	if inst.PaymentType != nil {
		if *inst.PaymentType == "prepaid" {
			instance.ChargeType = "PrePaid"
		} else {
			instance.ChargeType = "PostPaid"
		}
	}
	if inst.CreatedAt != nil {
		if t, err := time.Parse("2006-01-02T15:04:05.000Z", *inst.CreatedAt); err == nil {
			instance.CreationTime = t
		}
	}
	if inst.UpdatedAt != nil {
		if t, err := time.Parse("2006-01-02T15:04:05.000Z", *inst.UpdatedAt); err == nil {
			instance.UpdateTime = t
		}
	}
	if inst.ResourceGroupId != nil {
		instance.ResourceGroupID = *inst.ResourceGroupId
	}

	// 解析标签
	if inst.Tags != nil {
		instance.Tags = make(map[string]string)
		for _, tag := range inst.Tags {
			if tag.TagKey != nil && tag.TagValue != nil {
				instance.Tags[*tag.TagKey] = *tag.TagValue
			}
		}
	}

	return instance
}
