package tencent

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	es "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/es/v20180416"
)

// ElasticsearchAdapter 腾讯云 ES 适配器
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

// createClient 创建 ES 客户端
func (a *ElasticsearchAdapter) createClient(region string) (*es.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}

	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "es.tencentcloudapi.com"

	return es.NewClient(credential, region, cpf)
}

// ListInstances 获取 Elasticsearch 实例列表
func (a *ElasticsearchAdapter) ListInstances(ctx context.Context, region string) ([]types.ElasticsearchInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建ES客户端失败: %w", err)
	}

	var instances []types.ElasticsearchInstance
	offset := uint64(0)
	limit := uint64(100)

	for {
		request := es.NewDescribeInstancesRequest()
		request.Offset = common.Uint64Ptr(offset)
		request.Limit = common.Uint64Ptr(limit)

		response, err := client.DescribeInstances(request)
		if err != nil {
			return nil, fmt.Errorf("获取ES实例列表失败: %w", err)
		}

		if response.Response == nil || response.Response.InstanceList == nil || len(response.Response.InstanceList) == 0 {
			break
		}

		for _, inst := range response.Response.InstanceList {
			instance := a.convertToElasticsearchInstance(inst, region)
			instances = append(instances, instance)
		}

		if len(response.Response.InstanceList) < int(limit) {
			break
		}
		offset += limit
	}

	return instances, nil
}

// GetInstance 获取单个 Elasticsearch 实例详情
func (a *ElasticsearchAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.ElasticsearchInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建ES客户端失败: %w", err)
	}

	request := es.NewDescribeInstancesRequest()
	request.InstanceIds = common.StringPtrs([]string{instanceID})

	response, err := client.DescribeInstances(request)
	if err != nil {
		return nil, fmt.Errorf("获取ES实例详情失败: %w", err)
	}

	if response.Response == nil || response.Response.InstanceList == nil || len(response.Response.InstanceList) == 0 {
		return nil, fmt.Errorf("ES实例不存在: %s", instanceID)
	}

	instance := a.convertToElasticsearchInstance(response.Response.InstanceList[0], region)
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
func (a *ElasticsearchAdapter) convertToElasticsearchInstance(inst *es.InstanceInfo, region string) types.ElasticsearchInstance {
	instance := types.ElasticsearchInstance{
		Region:   region,
		Provider: "tencent",
	}

	if inst.InstanceId != nil {
		instance.InstanceID = *inst.InstanceId
	}
	if inst.InstanceName != nil {
		instance.InstanceName = *inst.InstanceName
	}
	if inst.Status != nil {
		instance.Status = types.ElasticsearchStatus("tencent", fmt.Sprintf("%d", *inst.Status))
	}
	if inst.EsVersion != nil {
		instance.Version = *inst.EsVersion
	}
	if inst.NodeNum != nil {
		instance.NodeCount = int(*inst.NodeNum)
	}
	if inst.NodeType != nil {
		instance.NodeSpec = *inst.NodeType
	}
	if inst.DiskSize != nil {
		instance.NodeDiskSize = int(*inst.DiskSize)
	}
	if inst.DiskType != nil {
		instance.NodeDiskType = *inst.DiskType
	}

	// 网络信息
	if inst.VpcUid != nil {
		instance.VPCID = *inst.VpcUid
	}
	if inst.SubnetUid != nil {
		instance.VSwitchID = *inst.SubnetUid
	}

	// 访问端点
	if inst.EsVip != nil {
		instance.PrivateEndpoint = *inst.EsVip
	}
	if inst.EsPort != nil {
		instance.Port = int(*inst.EsPort)
	}
	if inst.KibanaUrl != nil {
		instance.KibanaEndpoint = *inst.KibanaUrl
	}

	// 计费信息
	if inst.ChargeType != nil {
		if *inst.ChargeType == "PREPAID" {
			instance.ChargeType = "PrePaid"
		} else {
			instance.ChargeType = "PostPaid"
		}
	}
	if inst.CreateTime != nil {
		if t, err := time.Parse("2006-01-02 15:04:05", *inst.CreateTime); err == nil {
			instance.CreationTime = t
		}
	}
	if inst.UpdateTime != nil {
		if t, err := time.Parse("2006-01-02 15:04:05", *inst.UpdateTime); err == nil {
			instance.UpdateTime = t
		}
	}
	if inst.Deadline != nil {
		if t, err := time.Parse("2006-01-02 15:04:05", *inst.Deadline); err == nil {
			instance.ExpiredTime = t
		}
	}
	if inst.Zone != nil {
		instance.Zone = *inst.Zone
	}
	if inst.MultiZoneInfo != nil && len(inst.MultiZoneInfo) > 0 {
		instance.ZoneIDs = make([]string, len(inst.MultiZoneInfo))
		for i, z := range inst.MultiZoneInfo {
			if z.Zone != nil {
				instance.ZoneIDs[i] = *z.Zone
			}
		}
	}

	// 解析标签
	if inst.TagList != nil {
		instance.Tags = make(map[string]string)
		for _, tag := range inst.TagList {
			if tag.TagKey != nil && tag.TagValue != nil {
				instance.Tags[*tag.TagKey] = *tag.TagValue
			}
		}
	}

	return instance
}
