package huawei

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	dcs "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dcs/v2"
	dcsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dcs/v2/model"
	dcsregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dcs/v2/region"
)

// RedisAdapter 华为云DCS Redis适配器
type RedisAdapter struct {
	account       *domain.CloudAccount
	defaultRegion string
	logger        *elog.Component
}

// NewRedisAdapter 创建华为云Redis适配器
func NewRedisAdapter(account *domain.CloudAccount, defaultRegion string, logger *elog.Component) *RedisAdapter {
	return &RedisAdapter{
		account:       account,
		defaultRegion: defaultRegion,
		logger:        logger,
	}
}

// getClient 获取DCS客户端
func (a *RedisAdapter) getClient(region string) (*dcs.DcsClient, error) {
	auth, err := basic.NewCredentialsBuilder().
		WithAk(a.account.AccessKeyID).
		WithSk(a.account.AccessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}

	reg, err := dcsregion.SafeValueOf(region)
	if err != nil {
		return nil, fmt.Errorf("无效的华为云地域: %s", region)
	}

	hcClient, err := dcs.DcsClientBuilder().
		WithRegion(reg).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云DCS客户端失败: %w", err)
	}

	return dcs.NewDcsClient(hcClient), nil
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

	request := &dcsmodel.ShowInstanceRequest{
		InstanceId: instanceID,
	}

	response, err := client.ShowInstance(request)
	if err != nil {
		return nil, fmt.Errorf("获取Redis实例详情失败: %w", err)
	}

	instance := convertHuaweiRedisDetail(response, region)
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
	offset := int32(0)
	limit := int32(100)

	if filter != nil && filter.PageSize > 0 {
		limit = int32(filter.PageSize)
	}

	for {
		request := &dcsmodel.ListInstancesRequest{
			Offset: &offset,
			Limit:  &limit,
		}

		response, err := client.ListInstances(request)
		if err != nil {
			return nil, fmt.Errorf("获取Redis实例列表失败: %w", err)
		}

		if response.Instances == nil || len(*response.Instances) == 0 {
			break
		}

		for _, inst := range *response.Instances {
			instance := convertHuaweiRedisListItem(&inst, region)
			allInstances = append(allInstances, instance)
		}

		if filter != nil && filter.PageNumber > 0 {
			break
		}

		if len(*response.Instances) < int(limit) {
			break
		}
		offset += limit
	}

	a.logger.Info("获取华为云Redis实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertHuaweiRedisDetail 转换华为云Redis实例详情为通用格式
func convertHuaweiRedisDetail(resp *dcsmodel.ShowInstanceResponse, region string) types.RedisInstance {
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

	if resp.InstanceId != nil {
		instanceID = *resp.InstanceId
	}
	if resp.Name != nil {
		instanceName = *resp.Name
	}
	if resp.Status != nil {
		status = *resp.Status
	}
	if resp.EngineVersion != nil {
		engineVersion = *resp.EngineVersion
	}
	if resp.Capacity != nil {
		capacity = int(*resp.Capacity) * 1024 // GB to MB
	}
	if resp.VpcId != nil {
		vpcID = *resp.VpcId
	}
	if resp.SubnetId != nil {
		vswitchID = *resp.SubnetId
	}
	if resp.ChargingMode != nil {
		chargeType = fmt.Sprintf("%d", *resp.ChargingMode)
	}
	if resp.CreatedAt != nil {
		creationTime = *resp.CreatedAt
	}
	if resp.Port != nil {
		port = int(*resp.Port)
	}
	if resp.Ip != nil {
		connectionDomain = *resp.Ip
	}

	return types.RedisInstance{
		InstanceID:       instanceID,
		InstanceName:     instanceName,
		Status:           types.NormalizeRedisStatus(status),
		Region:           region,
		EngineVersion:    engineVersion,
		Capacity:         capacity,
		ConnectionDomain: connectionDomain,
		Port:             port,
		VPCID:            vpcID,
		VSwitchID:        vswitchID,
		ChargeType:       chargeType,
		CreationTime:     creationTime,
		Provider:         string(types.ProviderHuawei),
	}
}

// convertHuaweiRedisListItem 转换华为云Redis列表项为通用格式
func convertHuaweiRedisListItem(inst *dcsmodel.InstanceListInfo, region string) types.RedisInstance {
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

	if inst.InstanceId != nil {
		instanceID = *inst.InstanceId
	}
	if inst.Name != nil {
		instanceName = *inst.Name
	}
	if inst.Status != nil {
		status = *inst.Status
	}
	if inst.EngineVersion != nil {
		engineVersion = *inst.EngineVersion
	}
	if inst.Capacity != nil {
		capacity = int(*inst.Capacity) * 1024 // GB to MB
	}
	if inst.VpcId != nil {
		vpcID = *inst.VpcId
	}
	if inst.SubnetId != nil {
		vswitchID = *inst.SubnetId
	}
	if inst.ChargingMode != nil {
		chargeType = fmt.Sprintf("%d", *inst.ChargingMode)
	}
	if inst.CreatedAt != nil {
		creationTime = *inst.CreatedAt
	}
	if inst.Port != nil {
		port = int(*inst.Port)
	}
	if inst.Ip != nil {
		connectionDomain = *inst.Ip
	}

	return types.RedisInstance{
		InstanceID:       instanceID,
		InstanceName:     instanceName,
		Status:           types.NormalizeRedisStatus(status),
		Region:           region,
		EngineVersion:    engineVersion,
		Capacity:         capacity,
		ConnectionDomain: connectionDomain,
		Port:             port,
		VPCID:            vpcID,
		VSwitchID:        vswitchID,
		ChargeType:       chargeType,
		CreationTime:     creationTime,
		Provider:         string(types.ProviderHuawei),
	}
}
