package aliyun

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	r_kvstore "github.com/aliyun/alibaba-cloud-sdk-go/services/r-kvstore"
	"github.com/gotomicro/ego/core/elog"
)

// RedisAdapter 阿里云Redis适配器
type RedisAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewRedisAdapter 创建阿里云Redis适配器
func NewRedisAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *RedisAdapter {
	return &RedisAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// getClient 获取Redis客户端
func (a *RedisAdapter) getClient(region string) (*r_kvstore.Client, error) {
	client, err := r_kvstore.NewClientWithAccessKey(region, a.accessKeyID, a.accessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("创建阿里云Redis客户端失败: %w", err)
	}
	return client, nil
}

// ListInstances 获取Redis实例列表
func (a *RedisAdapter) ListInstances(ctx context.Context, region string) ([]types.RedisInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个Redis实例详情
func (a *RedisAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.RedisInstance, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	request := r_kvstore.CreateDescribeInstanceAttributeRequest()
	request.Scheme = "https"
	request.InstanceId = instanceID

	response, err := client.DescribeInstanceAttribute(request)
	if err != nil {
		return nil, fmt.Errorf("获取Redis实例详情失败: %w", err)
	}

	if len(response.Instances.DBInstanceAttribute) == 0 {
		return nil, fmt.Errorf("Redis实例不存在: %s", instanceID)
	}

	inst := response.Instances.DBInstanceAttribute[0]
	instance := convertAliyunRedisInstance(inst, region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取Redis实例
func (a *RedisAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.RedisInstance, error) {
	if len(instanceIDs) == 0 {
		return nil, nil
	}

	var instances []types.RedisInstance
	for _, id := range instanceIDs {
		inst, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取Redis实例失败", elog.String("instance_id", id), elog.FieldErr(err))
			continue
		}
		instances = append(instances, *inst)
	}
	return instances, nil
}

// GetInstanceStatus 获取实例状态
func (a *RedisAdapter) GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, instanceID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取实例列表
func (a *RedisAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.RedisInstanceFilter) ([]types.RedisInstance, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.RedisInstance
	pageNumber := 1
	pageSize := 100

	if filter != nil && filter.PageSize > 0 {
		pageSize = filter.PageSize
	}
	if filter != nil && filter.PageNumber > 0 {
		pageNumber = filter.PageNumber
	}

	for {
		request := r_kvstore.CreateDescribeInstancesRequest()
		request.Scheme = "https"
		request.RegionId = region
		request.PageNumber = requests.NewInteger(pageNumber)
		request.PageSize = requests.NewInteger(pageSize)

		if filter != nil {
			if filter.InstanceName != "" {
				request.SearchKey = filter.InstanceName
			}
			if len(filter.Status) > 0 {
				request.InstanceStatus = filter.Status[0]
			}
			if filter.VPCID != "" {
				request.VpcId = filter.VPCID
			}
			if filter.Architecture != "" {
				request.ArchitectureType = filter.Architecture
			}
		}

		response, err := client.DescribeInstances(request)
		if err != nil {
			return nil, fmt.Errorf("获取Redis实例列表失败: %w", err)
		}

		for _, inst := range response.Instances.KVStoreInstance {
			instance := convertAliyunRedisListItem(inst, region)
			allInstances = append(allInstances, instance)
		}

		if filter != nil && filter.PageNumber > 0 {
			break
		}

		if len(response.Instances.KVStoreInstance) < pageSize {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取阿里云Redis实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertAliyunRedisInstance 转换阿里云Redis实例详情为通用格式
func convertAliyunRedisInstance(inst r_kvstore.DBInstanceAttribute, region string) types.RedisInstance {
	return types.RedisInstance{
		InstanceID:       inst.InstanceId,
		InstanceName:     inst.InstanceName,
		Status:           types.NormalizeRedisStatus(inst.InstanceStatus),
		Region:           region,
		Zone:             inst.ZoneId,
		EngineVersion:    inst.EngineVersion,
		InstanceClass:    inst.InstanceClass,
		Architecture:     inst.ArchitectureType,
		Capacity:         int(inst.Capacity),
		Bandwidth:        int(inst.Bandwidth),
		Connections:      int(inst.Connections),
		ConnectionDomain: inst.ConnectionDomain,
		Port:             int(inst.Port),
		VPCID:            inst.VpcId,
		VSwitchID:        inst.VSwitchId,
		PrivateIP:        inst.PrivateIp,
		NodeType:         inst.NodeType,
		ChargeType:       inst.ChargeType,
		CreationTime:     inst.CreateTime,
		ExpiredTime:      inst.EndTime,
		ProjectID:        inst.ResourceGroupId,
		Description:      inst.InstanceName,
		Provider:         string(types.ProviderAliyun),
	}
}

// convertAliyunRedisListItem 转换阿里云Redis列表项为通用格式
func convertAliyunRedisListItem(inst r_kvstore.KVStoreInstance, region string) types.RedisInstance {
	return types.RedisInstance{
		InstanceID:       inst.InstanceId,
		InstanceName:     inst.InstanceName,
		Status:           types.NormalizeRedisStatus(inst.InstanceStatus),
		Region:           region,
		Zone:             inst.ZoneId,
		EngineVersion:    inst.EngineVersion,
		InstanceClass:    inst.InstanceClass,
		Architecture:     inst.ArchitectureType,
		Capacity:         int(inst.Capacity),
		Bandwidth:        int(inst.Bandwidth),
		Connections:      int(inst.Connections),
		ConnectionDomain: inst.ConnectionDomain,
		Port:             int(inst.Port),
		VPCID:            inst.VpcId,
		VSwitchID:        inst.VSwitchId,
		PrivateIP:        inst.PrivateIp,
		NodeType:         inst.NodeType,
		ChargeType:       inst.ChargeType,
		CreationTime:     inst.CreateTime,
		ExpiredTime:      inst.EndTime,
		ProjectID:        inst.ResourceGroupId,
		Description:      inst.InstanceName,
		Provider:         string(types.ProviderAliyun),
	}
}
