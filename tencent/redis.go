package tencent

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	redis "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/redis/v20180412"
)

// RedisAdapter 腾讯云Redis适配器
type RedisAdapter struct {
	account       *domain.CloudAccount
	defaultRegion string
	logger        *elog.Component
}

// NewRedisAdapter 创建腾讯云Redis适配器
func NewRedisAdapter(account *domain.CloudAccount, defaultRegion string, logger *elog.Component) *RedisAdapter {
	return &RedisAdapter{
		account:       account,
		defaultRegion: defaultRegion,
		logger:        logger,
	}
}

// getClient 获取Redis客户端
func (a *RedisAdapter) getClient(region string) (*redis.Client, error) {
	credential := common.NewCredential(a.account.AccessKeyID, a.account.AccessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "redis.tencentcloudapi.com"
	client, err := redis.NewClient(credential, region, cpf)
	if err != nil {
		return nil, fmt.Errorf("创建腾讯云Redis客户端失败: %w", err)
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

	request := redis.NewDescribeInstancesRequest()
	request.InstanceId = common.StringPtr(instanceID)

	response, err := client.DescribeInstances(request)
	if err != nil {
		return nil, fmt.Errorf("获取Redis实例详情失败: %w", err)
	}

	if len(response.Response.InstanceSet) == 0 {
		return nil, fmt.Errorf("Redis实例不存在: %s", instanceID)
	}

	inst := response.Response.InstanceSet[0]
	instance := convertTencentRedisInstance(inst, region)
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
	offset := uint64(0)
	limit := uint64(100)

	if filter != nil && filter.PageSize > 0 {
		limit = uint64(filter.PageSize)
	}

	for {
		request := redis.NewDescribeInstancesRequest()
		request.Offset = common.Uint64Ptr(offset)
		request.Limit = common.Uint64Ptr(limit)

		response, err := client.DescribeInstances(request)
		if err != nil {
			return nil, fmt.Errorf("获取Redis实例列表失败: %w", err)
		}

		for _, inst := range response.Response.InstanceSet {
			instance := convertTencentRedisInstance(inst, region)
			allInstances = append(allInstances, instance)
		}

		if filter != nil && filter.PageNumber > 0 {
			break
		}

		if len(response.Response.InstanceSet) < int(limit) {
			break
		}
		offset += limit
	}

	a.logger.Info("获取腾讯云Redis实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertTencentRedisInstance 转换腾讯云Redis实例为通用格式
func convertTencentRedisInstance(inst *redis.InstanceSet, region string) types.RedisInstance {
	instanceID := ""
	instanceName := ""
	status := ""
	zone := ""
	engineVersion := ""
	capacity := 0
	vpcID := ""
	vswitchID := ""
	chargeType := ""
	creationTime := ""
	expiredTime := ""
	projectID := ""

	if inst.InstanceId != nil {
		instanceID = *inst.InstanceId
	}
	if inst.InstanceName != nil {
		instanceName = *inst.InstanceName
	}
	if inst.Status != nil {
		status = fmt.Sprintf("%d", *inst.Status)
	}
	if inst.ZoneId != nil {
		zone = fmt.Sprintf("%d", *inst.ZoneId)
	}
	if inst.Engine != nil {
		engineVersion = *inst.Engine
	}
	if inst.Size != nil {
		capacity = int(*inst.Size)
	}
	if inst.UniqVpcId != nil {
		vpcID = *inst.UniqVpcId
	}
	if inst.UniqSubnetId != nil {
		vswitchID = *inst.UniqSubnetId
	}
	if inst.BillingMode != nil {
		chargeType = fmt.Sprintf("%d", *inst.BillingMode)
	}
	if inst.Createtime != nil {
		creationTime = *inst.Createtime
	}
	if inst.DeadlineTime != nil {
		expiredTime = *inst.DeadlineTime
	}
	if inst.ProjectId != nil {
		projectID = fmt.Sprintf("%d", *inst.ProjectId)
	}

	return types.RedisInstance{
		InstanceID:    instanceID,
		InstanceName:  instanceName,
		Status:        types.NormalizeRedisStatus(status),
		Region:        region,
		Zone:          zone,
		EngineVersion: engineVersion,
		Capacity:      capacity,
		VPCID:         vpcID,
		VSwitchID:     vswitchID,
		ChargeType:    chargeType,
		CreationTime:  creationTime,
		ExpiredTime:   expiredTime,
		ProjectID:     projectID,
		Provider:      string(types.ProviderTencent),
	}
}
