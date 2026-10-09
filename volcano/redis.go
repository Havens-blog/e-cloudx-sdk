package volcano

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/redis"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// RedisAdapter 火山引擎Redis适配器
type RedisAdapter struct {
	account       *domain.CloudAccount
	defaultRegion string
	logger        *elog.Component
}

// NewRedisAdapter 创建火山引擎Redis适配器
func NewRedisAdapter(account *domain.CloudAccount, defaultRegion string, logger *elog.Component) *RedisAdapter {
	return &RedisAdapter{
		account:       account,
		defaultRegion: defaultRegion,
		logger:        logger,
	}
}

// getClient 获取Redis客户端
func (a *RedisAdapter) getClient(region string) (*redis.REDIS, error) {
	config := volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(a.account.AccessKeyID, a.account.AccessKeySecret, "")).
		WithRegion(region)

	sess, err := session.NewSession(config)
	if err != nil {
		return nil, fmt.Errorf("创建火山引擎会话失败: %w", err)
	}

	client := redis.New(sess)
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

	input := &redis.DescribeDBInstanceDetailInput{
		InstanceId: volcengine.String(instanceID),
	}

	output, err := client.DescribeDBInstanceDetail(input)
	if err != nil {
		return nil, fmt.Errorf("获取Redis实例详情失败: %w", err)
	}

	instance := convertVolcanoRedisDetail(output, region)
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
	pageNumber := int32(1)
	pageSize := int32(100)

	if filter != nil && filter.PageSize > 0 {
		pageSize = int32(filter.PageSize)
	}
	if filter != nil && filter.PageNumber > 0 {
		pageNumber = int32(filter.PageNumber)
	}

	for {
		input := &redis.DescribeDBInstancesInput{
			PageNumber: volcengine.Int32(pageNumber),
			PageSize:   volcengine.Int32(pageSize),
			RegionId:   volcengine.String(region),
		}

		output, err := client.DescribeDBInstances(input)
		if err != nil {
			return nil, fmt.Errorf("获取Redis实例列表失败: %w", err)
		}

		if output.Instances == nil {
			break
		}

		for _, inst := range output.Instances {
			instance := convertVolcanoRedisListItem(inst, region)
			allInstances = append(allInstances, instance)
		}

		if filter != nil && filter.PageNumber > 0 {
			break
		}

		if len(output.Instances) < int(pageSize) {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取火山引擎Redis实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertVolcanoRedisDetail 转换火山引擎Redis实例详情为通用格式
func convertVolcanoRedisDetail(output *redis.DescribeDBInstanceDetailOutput, region string) types.RedisInstance {
	instanceID := ""
	instanceName := ""
	status := ""
	engineVersion := ""
	capacity := 0
	vpcID := ""
	vswitchID := ""
	chargeType := ""
	creationTime := ""
	port := 0
	connectionDomain := ""
	shardCount := 0
	architecture := "standard"

	if output.InstanceId != nil {
		instanceID = *output.InstanceId
	}
	if output.InstanceName != nil {
		instanceName = *output.InstanceName
	}
	if output.Status != nil {
		status = *output.Status
	}
	if output.EngineVersion != nil {
		engineVersion = *output.EngineVersion
	}
	if output.Capacity != nil && output.Capacity.Total != nil {
		capacity = int(*output.Capacity.Total) // MB
	}
	if output.VpcId != nil {
		vpcID = *output.VpcId
	}
	if output.SubnetId != nil {
		vswitchID = *output.SubnetId
	}
	if output.ChargeType != nil {
		chargeType = *output.ChargeType
	}
	if output.CreateTime != nil {
		creationTime = *output.CreateTime
	}
	if output.VisitAddrs != nil && len(output.VisitAddrs) > 0 {
		addr := output.VisitAddrs[0]
		if addr.VIP != nil {
			connectionDomain = *addr.VIP
		}
		if addr.Port != nil {
			port, _ = strconv.Atoi(*addr.Port)
		}
	}
	if output.ShardNumber != nil {
		shardCount = int(*output.ShardNumber)
		if shardCount > 1 {
			architecture = "cluster"
		}
	}

	return types.RedisInstance{
		InstanceID:       instanceID,
		InstanceName:     instanceName,
		Status:           types.NormalizeRedisStatus(status),
		Region:           region,
		EngineVersion:    engineVersion,
		Architecture:     architecture,
		Capacity:         capacity,
		ShardCount:       shardCount,
		ConnectionDomain: connectionDomain,
		Port:             port,
		VPCID:            vpcID,
		VSwitchID:        vswitchID,
		ChargeType:       chargeType,
		CreationTime:     creationTime,
		Provider:         string(types.ProviderVolcano),
	}
}

// convertVolcanoRedisListItem 转换火山引擎Redis列表项为通用格式
func convertVolcanoRedisListItem(inst *redis.InstanceForDescribeDBInstancesOutput, region string) types.RedisInstance {
	instanceID := ""
	instanceName := ""
	status := ""
	engineVersion := ""
	capacity := 0
	vpcID := ""
	chargeType := ""
	creationTime := ""
	shardCount := 0
	architecture := "standard"

	if inst.InstanceId != nil {
		instanceID = *inst.InstanceId
	}
	if inst.InstanceName != nil {
		instanceName = *inst.InstanceName
	}
	if inst.Status != nil {
		status = *inst.Status
	}
	if inst.EngineVersion != nil {
		engineVersion = *inst.EngineVersion
	}
	if inst.Capacity != nil && inst.Capacity.Total != nil {
		capacity = int(*inst.Capacity.Total)
	}
	if inst.VpcId != nil {
		vpcID = *inst.VpcId
	}
	if inst.ChargeType != nil {
		chargeType = *inst.ChargeType
	}
	if inst.CreateTime != nil {
		creationTime = *inst.CreateTime
	}
	if inst.ShardNumber != nil {
		shardCount = int(*inst.ShardNumber)
		if shardCount > 1 {
			architecture = "cluster"
		}
	}

	return types.RedisInstance{
		InstanceID:    instanceID,
		InstanceName:  instanceName,
		Status:        types.NormalizeRedisStatus(status),
		Region:        region,
		EngineVersion: engineVersion,
		Architecture:  architecture,
		Capacity:      capacity,
		ShardCount:    shardCount,
		VPCID:         vpcID,
		ChargeType:    chargeType,
		CreationTime:  creationTime,
		Provider:      string(types.ProviderVolcano),
	}
}
