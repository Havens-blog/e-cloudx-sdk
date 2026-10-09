package huawei

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	kafka "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/kafka/v2"
	kafkamodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/kafka/v2/model"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/kafka/v2/region"
)

// KafkaAdapter 华为云 DMS Kafka 适配器
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
func (a *KafkaAdapter) createClient(regionID string) (*kafka.KafkaClient, error) {
	if regionID == "" {
		regionID = a.defaultRegion
	}

	auth, err := basic.NewCredentialsBuilder().
		WithAk(a.accessKeyID).
		WithSk(a.accessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建认证凭证失败: %w", err)
	}

	reg, err := region.SafeValueOf(regionID)
	if err != nil {
		return nil, fmt.Errorf("无效的地域: %s, %w", regionID, err)
	}

	hcClient, err := kafka.KafkaClientBuilder().
		WithRegion(reg).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建Kafka客户端失败: %w", err)
	}

	return kafka.NewKafkaClient(hcClient), nil
}

// ListInstances 获取 Kafka 实例列表
func (a *KafkaAdapter) ListInstances(ctx context.Context, regionID string) ([]types.KafkaInstance, error) {
	client, err := a.createClient(regionID)
	if err != nil {
		return nil, fmt.Errorf("创建Kafka客户端失败: %w", err)
	}

	var instances []types.KafkaInstance

	request := &kafkamodel.ListInstancesRequest{}
	response, err := client.ListInstances(request)
	if err != nil {
		return nil, fmt.Errorf("获取Kafka实例列表失败: %w", err)
	}

	if response.Instances == nil {
		return instances, nil
	}

	for _, inst := range *response.Instances {
		instance := a.convertToKafkaInstance(&inst, regionID)
		instances = append(instances, instance)
	}

	return instances, nil
}

// GetInstance 获取单个 Kafka 实例详情
func (a *KafkaAdapter) GetInstance(ctx context.Context, regionID, instanceID string) (*types.KafkaInstance, error) {
	client, err := a.createClient(regionID)
	if err != nil {
		return nil, fmt.Errorf("创建Kafka客户端失败: %w", err)
	}

	request := &kafkamodel.ShowInstanceRequest{
		InstanceId: instanceID,
	}

	response, err := client.ShowInstance(request)
	if err != nil {
		return nil, fmt.Errorf("获取Kafka实例详情失败: %w", err)
	}

	instance := a.convertDetailToKafkaInstance(response, regionID)
	return &instance, nil
}

// ListInstancesByIDs 批量获取 Kafka 实例
func (a *KafkaAdapter) ListInstancesByIDs(ctx context.Context, regionID string, instanceIDs []string) ([]types.KafkaInstance, error) {
	var instances []types.KafkaInstance
	for _, id := range instanceIDs {
		instance, err := a.GetInstance(ctx, regionID, id)
		if err != nil {
			a.logger.Warn("获取Kafka实例失败", elog.String("instance_id", id), elog.FieldErr(err))
			continue
		}
		instances = append(instances, *instance)
	}
	return instances, nil
}

// GetInstanceStatus 获取实例状态
func (a *KafkaAdapter) GetInstanceStatus(ctx context.Context, regionID, instanceID string) (string, error) {
	instance, err := a.GetInstance(ctx, regionID, instanceID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取实例列表
func (a *KafkaAdapter) ListInstancesWithFilter(ctx context.Context, regionID string, filter *types.KafkaInstanceFilter) ([]types.KafkaInstance, error) {
	instances, err := a.ListInstances(ctx, regionID)
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
func (a *KafkaAdapter) convertToKafkaInstance(inst *kafkamodel.ShowInstanceResp, regionID string) types.KafkaInstance {
	instance := types.KafkaInstance{
		Region:   regionID,
		Provider: "huawei",
	}

	if inst.InstanceId != nil {
		instance.InstanceID = *inst.InstanceId
	}
	if inst.Name != nil {
		instance.InstanceName = *inst.Name
	}
	if inst.Status != nil {
		instance.Status = types.KafkaStatus("huawei", *inst.Status)
	}
	if inst.Engine != nil {
		instance.Version = *inst.Engine
	}
	if inst.EngineVersion != nil {
		instance.Version = *inst.EngineVersion
	}
	if inst.Specification != nil {
		instance.SpecType = *inst.Specification
	}
	if inst.StorageSpace != nil {
		instance.DiskSize = int64(*inst.StorageSpace)
	}
	if inst.UsedStorageSpace != nil {
		instance.DiskUsed = int64(*inst.UsedStorageSpace)
	}
	if inst.PartitionNum != nil {
		if partNum, err := strconv.Atoi(*inst.PartitionNum); err == nil {
			instance.PartitionQuota = partNum
		}
	}
	if inst.VpcId != nil {
		instance.VPCID = *inst.VpcId
	}
	if inst.SubnetId != nil {
		instance.VSwitchID = *inst.SubnetId
	}
	if inst.SecurityGroupId != nil {
		instance.SecurityGroupID = *inst.SecurityGroupId
	}
	if inst.ConnectAddress != nil {
		instance.BootstrapServers = *inst.ConnectAddress
	}
	if inst.SslEnable != nil && *inst.SslEnable {
		instance.SSLEnabled = true
	}
	if inst.ChargingMode != nil {
		if *inst.ChargingMode == 1 {
			instance.ChargeType = "PrePaid"
		} else {
			instance.ChargeType = "PostPaid"
		}
	}
	if inst.CreatedAt != nil {
		// 华为云时间戳是毫秒字符串
		if ts, err := strconv.ParseInt(*inst.CreatedAt, 10, 64); err == nil {
			instance.CreationTime = time.UnixMilli(ts)
		}
	}
	if inst.AvailableZones != nil && len(*inst.AvailableZones) > 0 {
		instance.Zone = (*inst.AvailableZones)[0]
		instance.ZoneIDs = *inst.AvailableZones
	}

	// 解析标签
	if inst.Tags != nil {
		instance.Tags = make(map[string]string)
		for _, tag := range *inst.Tags {
			if tag.Key != nil && tag.Value != nil {
				instance.Tags[*tag.Key] = *tag.Value
			}
		}
	}

	return instance
}

// convertDetailToKafkaInstance 从详情转换
func (a *KafkaAdapter) convertDetailToKafkaInstance(resp *kafkamodel.ShowInstanceResponse, regionID string) types.KafkaInstance {
	instance := types.KafkaInstance{
		Region:   regionID,
		Provider: "huawei",
	}

	if resp.InstanceId != nil {
		instance.InstanceID = *resp.InstanceId
	}
	if resp.Name != nil {
		instance.InstanceName = *resp.Name
	}
	if resp.Status != nil {
		instance.Status = types.KafkaStatus("huawei", *resp.Status)
	}
	if resp.EngineVersion != nil {
		instance.Version = *resp.EngineVersion
	}
	if resp.Specification != nil {
		instance.SpecType = *resp.Specification
	}
	if resp.StorageSpace != nil {
		instance.DiskSize = int64(*resp.StorageSpace)
	}
	if resp.UsedStorageSpace != nil {
		instance.DiskUsed = int64(*resp.UsedStorageSpace)
	}
	if resp.PartitionNum != nil {
		if partNum, err := strconv.Atoi(*resp.PartitionNum); err == nil {
			instance.PartitionQuota = partNum
		}
	}
	if resp.VpcId != nil {
		instance.VPCID = *resp.VpcId
	}
	if resp.SubnetId != nil {
		instance.VSwitchID = *resp.SubnetId
	}
	if resp.SecurityGroupId != nil {
		instance.SecurityGroupID = *resp.SecurityGroupId
	}
	if resp.ConnectAddress != nil {
		instance.BootstrapServers = *resp.ConnectAddress
	}
	if resp.SslEnable != nil && *resp.SslEnable {
		instance.SSLEnabled = true
	}
	if resp.ChargingMode != nil {
		if *resp.ChargingMode == 1 {
			instance.ChargeType = "PrePaid"
		} else {
			instance.ChargeType = "PostPaid"
		}
	}
	if resp.CreatedAt != nil {
		if ts, err := strconv.ParseInt(*resp.CreatedAt, 10, 64); err == nil {
			instance.CreationTime = time.UnixMilli(ts)
		}
	}
	if resp.AvailableZones != nil && len(*resp.AvailableZones) > 0 {
		instance.Zone = (*resp.AvailableZones)[0]
		instance.ZoneIDs = *resp.AvailableZones
	}

	if resp.Tags != nil {
		instance.Tags = make(map[string]string)
		for _, tag := range *resp.Tags {
			if tag.Key != nil && tag.Value != nil {
				instance.Tags[*tag.Key] = *tag.Value
			}
		}
	}

	return instance
}
