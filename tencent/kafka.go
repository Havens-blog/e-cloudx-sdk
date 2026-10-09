package tencent

import (
	"context"
	"fmt"
	"slices"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	ckafka "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ckafka/v20190819"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

// KafkaAdapter 腾讯云 CKafka 适配器
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

// createClient 创建 CKafka 客户端
func (a *KafkaAdapter) createClient(region string) (*ckafka.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}

	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "ckafka.tencentcloudapi.com"

	return ckafka.NewClient(credential, region, cpf)
}

// ListInstances 获取 Kafka 实例列表
func (a *KafkaAdapter) ListInstances(ctx context.Context, region string) ([]types.KafkaInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建CKafka客户端失败: %w", err)
	}

	var instances []types.KafkaInstance
	offset := int64(0)
	limit := int64(100)

	for {
		request := ckafka.NewDescribeInstancesDetailRequest()
		request.Offset = common.Int64Ptr(offset)
		request.Limit = common.Int64Ptr(limit)

		response, err := client.DescribeInstancesDetail(request)
		if err != nil {
			return nil, fmt.Errorf("获取CKafka实例列表失败: %w", err)
		}

		if response.Response == nil || response.Response.Result == nil ||
			response.Response.Result.InstanceList == nil || len(response.Response.Result.InstanceList) == 0 {
			break
		}

		for _, inst := range response.Response.Result.InstanceList {
			instance := a.convertToKafkaInstance(inst, region)
			instances = append(instances, instance)
		}

		if len(response.Response.Result.InstanceList) < int(limit) {
			break
		}
		offset += limit
	}

	return instances, nil
}

// GetInstance 获取单个 Kafka 实例详情
func (a *KafkaAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.KafkaInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建CKafka客户端失败: %w", err)
	}

	request := ckafka.NewDescribeInstancesDetailRequest()
	request.InstanceId = common.StringPtr(instanceID)

	response, err := client.DescribeInstancesDetail(request)
	if err != nil {
		return nil, fmt.Errorf("获取CKafka实例详情失败: %w", err)
	}

	if response.Response == nil || response.Response.Result == nil ||
		response.Response.Result.InstanceList == nil || len(response.Response.Result.InstanceList) == 0 {
		return nil, fmt.Errorf("CKafka实例不存在: %s", instanceID)
	}

	instance := a.convertToKafkaInstance(response.Response.Result.InstanceList[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取 Kafka 实例
func (a *KafkaAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.KafkaInstance, error) {
	var instances []types.KafkaInstance
	for _, id := range instanceIDs {
		instance, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取CKafka实例失败", elog.String("instance_id", id), elog.FieldErr(err))
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
		if len(filter.Status) > 0 && !slices.Contains(filter.Status, inst.Status) {
			continue
		}
		if filter.InstanceName != "" && inst.InstanceName != filter.InstanceName {
			continue
		}
		if filter.VPCID != "" && inst.VPCID != filter.VPCID {
			continue
		}
		filtered = append(filtered, inst)
	}

	return filtered, nil
}

// convertToKafkaInstance 转换为统一的 Kafka 实例结构
func (a *KafkaAdapter) convertToKafkaInstance(inst *ckafka.InstanceDetail, region string) types.KafkaInstance {
	instance := types.KafkaInstance{
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
		instance.Status = types.KafkaStatus("tencent", fmt.Sprintf("%d", *inst.Status))
	}
	if inst.Version != nil {
		instance.Version = *inst.Version
	}
	if inst.DiskSize != nil {
		instance.DiskSize = int64(*inst.DiskSize)
	}
	if inst.DiskType != nil {
		instance.DiskType = *inst.DiskType
	}
	if inst.VpcId != nil {
		instance.VPCID = *inst.VpcId
	}
	if inst.SubnetId != nil {
		instance.VSwitchID = *inst.SubnetId
	}
	if inst.RenewFlag != nil {
		if *inst.RenewFlag == 1 {
			instance.ChargeType = "PrePaid"
		} else {
			instance.ChargeType = "PostPaid"
		}
	}
	if inst.ZoneId != nil {
		instance.Zone = fmt.Sprintf("%d", *inst.ZoneId)
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
