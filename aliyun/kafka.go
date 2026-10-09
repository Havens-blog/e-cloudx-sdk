package aliyun

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	alikafka "github.com/alibabacloud-go/alikafka-20190916/v3/client"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/gotomicro/ego/core/elog"
)

// KafkaAdapter 阿里云 Kafka 适配器
type KafkaAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewKafkaAdapter 创建 Kafka 适配器
func NewKafkaAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *KafkaAdapter {
	return &KafkaAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建 Kafka 客户端
func (a *KafkaAdapter) createClient(region string) (*alikafka.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}

	config := &openapi.Config{
		AccessKeyId:     tea.String(a.accessKeyID),
		AccessKeySecret: tea.String(a.accessKeySecret),
		RegionId:        tea.String(region),
	}
	config.Endpoint = tea.String(fmt.Sprintf("alikafka.%s.aliyuncs.com", region))

	return alikafka.NewClient(config)
}

// ListInstances 获取 Kafka 实例列表
func (a *KafkaAdapter) ListInstances(ctx context.Context, region string) ([]types.KafkaInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建Kafka客户端失败: %w", err)
	}

	var instances []types.KafkaInstance

	request := &alikafka.GetInstanceListRequest{
		RegionId: tea.String(region),
	}

	response, err := client.GetInstanceList(request)
	if err != nil {
		return nil, fmt.Errorf("获取Kafka实例列表失败: %w", err)
	}

	if response.Body == nil || response.Body.InstanceList == nil {
		return instances, nil
	}

	for _, inst := range response.Body.InstanceList.InstanceVO {
		instance := a.convertToKafkaInstance(inst, region)
		instances = append(instances, instance)
	}

	return instances, nil
}

// GetInstance 获取单个 Kafka 实例详情
func (a *KafkaAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.KafkaInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建Kafka客户端失败: %w", err)
	}

	request := &alikafka.GetInstanceListRequest{
		RegionId:   tea.String(region),
		InstanceId: []*string{tea.String(instanceID)},
	}

	response, err := client.GetInstanceList(request)
	if err != nil {
		return nil, fmt.Errorf("获取Kafka实例详情失败: %w", err)
	}

	if response.Body == nil || response.Body.InstanceList == nil || len(response.Body.InstanceList.InstanceVO) == 0 {
		return nil, fmt.Errorf("Kafka实例不存在: %s", instanceID)
	}

	instance := a.convertToKafkaInstance(response.Body.InstanceList.InstanceVO[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取 Kafka 实例
func (a *KafkaAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.KafkaInstance, error) {
	var instances []types.KafkaInstance
	for _, id := range instanceIDs {
		instance, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取Kafka实例失败", elog.String("instance_id", id), elog.FieldErr(err))
			continue
		}
		instances = append(instances, *instance)
	}
	return instances, nil
}

// GetInstanceStatus 获取实例状态
func (a *KafkaAdapter) GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, instanceID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取实例列表
func (a *KafkaAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.KafkaInstanceFilter) ([]types.KafkaInstance, error) {
	instances, err := a.ListInstances(ctx, region)
	if err != nil {
		return nil, err
	}

	if filter == nil {
		return instances, nil
	}

	var filtered []types.KafkaInstance
	for _, inst := range instances {
		// 按状态过滤
		if len(filter.Status) > 0 && !slices.Contains(filter.Status, inst.Status) {
			continue
		}
		// 按名称过滤
		if filter.InstanceName != "" && inst.InstanceName != filter.InstanceName {
			continue
		}
		// 按VPC过滤
		if filter.VPCID != "" && inst.VPCID != filter.VPCID {
			continue
		}
		filtered = append(filtered, inst)
	}

	return filtered, nil
}

// convertToKafkaInstance 转换为统一的 Kafka 实例结构
func (a *KafkaAdapter) convertToKafkaInstance(inst *alikafka.GetInstanceListResponseBodyInstanceListInstanceVO, region string) types.KafkaInstance {
	instance := types.KafkaInstance{
		Region:   region,
		Provider: "aliyun",
	}

	if inst.InstanceId != nil {
		instance.InstanceID = *inst.InstanceId
	}
	if inst.Name != nil {
		instance.InstanceName = *inst.Name
	}
	if inst.ServiceStatus != nil {
		instance.Status = types.KafkaStatus("aliyun", fmt.Sprintf("%d", *inst.ServiceStatus))
	}
	if inst.SpecType != nil {
		instance.SpecType = *inst.SpecType
	}
	if inst.MsgRetain != nil {
		instance.MessageRetention = int(*inst.MsgRetain)
	}
	if inst.DiskSize != nil {
		instance.DiskSize = int64(*inst.DiskSize)
	}
	if inst.DiskType != nil {
		instance.DiskType = fmt.Sprintf("%d", *inst.DiskType)
	}
	if inst.IoMax != nil {
		instance.IOMax = int(*inst.IoMax)
	}
	if inst.TopicNumLimit != nil {
		instance.TopicQuota = int(*inst.TopicNumLimit)
	}
	if inst.VpcId != nil {
		instance.VPCID = *inst.VpcId
	}
	if inst.VSwitchId != nil {
		instance.VSwitchID = *inst.VSwitchId
	}
	if inst.SecurityGroup != nil {
		instance.SecurityGroupID = *inst.SecurityGroup
	}
	if inst.EndPoint != nil {
		instance.BootstrapServers = *inst.EndPoint
	}
	if inst.SslEndPoint != nil {
		instance.SSLEndpoint = *inst.SslEndPoint
	}
	if inst.PaidType != nil {
		if *inst.PaidType == 0 {
			instance.ChargeType = "PrePaid"
		} else {
			instance.ChargeType = "PostPaid"
		}
	}
	if inst.CreateTime != nil {
		instance.CreationTime = time.UnixMilli(*inst.CreateTime)
	}
	if inst.ExpiredTime != nil {
		instance.ExpiredTime = time.UnixMilli(*inst.ExpiredTime)
	}
	if inst.ResourceGroupId != nil {
		instance.ResourceGroupID = *inst.ResourceGroupId
	}
	if inst.ZoneId != nil {
		instance.Zone = *inst.ZoneId
	}
	if inst.SslDomainEndpoint != nil {
		instance.SASLEndpoint = *inst.SslDomainEndpoint
	}

	// 解析标签
	if inst.Tags != nil && inst.Tags.TagVO != nil {
		instance.Tags = make(map[string]string)
		for _, tag := range inst.Tags.TagVO {
			if tag.Key != nil && tag.Value != nil {
				instance.Tags[*tag.Key] = *tag.Value
			}
		}
	}

	return instance
}
