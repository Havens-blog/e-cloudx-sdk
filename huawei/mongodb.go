package huawei

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	dds "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dds/v3"
	ddsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dds/v3/model"
	ddsregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dds/v3/region"
)

// MongoDBAdapter 华为云DDS MongoDB适配器
type MongoDBAdapter struct {
	account       *domain.CloudAccount
	defaultRegion string
	logger        *elog.Component
}

// NewMongoDBAdapter 创建华为云MongoDB适配器
func NewMongoDBAdapter(account *domain.CloudAccount, defaultRegion string, logger *elog.Component) *MongoDBAdapter {
	return &MongoDBAdapter{
		account:       account,
		defaultRegion: defaultRegion,
		logger:        logger,
	}
}

// getClient 获取DDS客户端
func (a *MongoDBAdapter) getClient(region string) (*dds.DdsClient, error) {
	auth, err := basic.NewCredentialsBuilder().
		WithAk(a.account.AccessKeyID).
		WithSk(a.account.AccessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}

	reg, err := ddsregion.SafeValueOf(region)
	if err != nil {
		return nil, fmt.Errorf("无效的华为云地域: %s", region)
	}

	hcClient, err := dds.DdsClientBuilder().
		WithRegion(reg).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云DDS客户端失败: %w", err)
	}

	return dds.NewDdsClient(hcClient), nil
}

// ListInstances 获取MongoDB实例列表
func (a *MongoDBAdapter) ListInstances(ctx context.Context, region string) ([]types.MongoDBInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个MongoDB实例详情
func (a *MongoDBAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.MongoDBInstance, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	request := &ddsmodel.ListInstancesRequest{
		Id: &instanceID,
	}

	response, err := client.ListInstances(request)
	if err != nil {
		return nil, fmt.Errorf("获取MongoDB实例详情失败: %w", err)
	}

	if response.Instances == nil || len(*response.Instances) == 0 {
		return nil, fmt.Errorf("MongoDB实例不存在: %s", instanceID)
	}

	inst := (*response.Instances)[0]
	instance := convertHuaweiMongoDBListItem(&inst, region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取MongoDB实例
func (a *MongoDBAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.MongoDBInstance, error) {
	if len(instanceIDs) == 0 {
		return nil, nil
	}

	var instances []types.MongoDBInstance
	for _, id := range instanceIDs {
		inst, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取MongoDB实例失败", elog.String("instance_id", id), elog.FieldErr(err))
			continue
		}
		instances = append(instances, *inst)
	}
	return instances, nil
}

// GetInstanceStatus 获取实例状态
func (a *MongoDBAdapter) GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, instanceID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取实例列表
func (a *MongoDBAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.MongoDBInstanceFilter) ([]types.MongoDBInstance, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.MongoDBInstance
	offset := int32(0)
	limit := int32(100)

	if filter != nil && filter.PageSize > 0 {
		limit = int32(filter.PageSize)
	}

	for {
		request := &ddsmodel.ListInstancesRequest{
			Offset: &offset,
			Limit:  &limit,
		}

		response, err := client.ListInstances(request)
		if err != nil {
			return nil, fmt.Errorf("获取MongoDB实例列表失败: %w", err)
		}

		if response.Instances == nil || len(*response.Instances) == 0 {
			break
		}

		for _, inst := range *response.Instances {
			instance := convertHuaweiMongoDBListItem(&inst, region)
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

	a.logger.Info("获取华为云MongoDB实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertHuaweiMongoDBListItem 转换华为云MongoDB列表项为通用格式
func convertHuaweiMongoDBListItem(inst *ddsmodel.QueryInstanceResponse, region string) types.MongoDBInstance {
	// 华为云DDS SDK使用非指针类型
	instanceID := inst.Id
	instanceName := inst.Name
	status := inst.Status
	engineVersion := inst.Datastore.Version
	dbInstanceType := inst.Mode
	vpcID := inst.VpcId
	vswitchID := inst.SubnetId
	creationTime := inst.Created

	chargeType := ""
	if inst.PayMode != nil {
		chargeType = *inst.PayMode
	}

	port := 0
	if inst.Port != "" {
		port, _ = strconv.Atoi(inst.Port)
	}

	return types.MongoDBInstance{
		InstanceID:     instanceID,
		InstanceName:   instanceName,
		Status:         types.NormalizeMongoDBStatus(status),
		Region:         region,
		EngineVersion:  engineVersion,
		DBInstanceType: dbInstanceType,
		Port:           port,
		VPCID:          vpcID,
		VSwitchID:      vswitchID,
		ChargeType:     chargeType,
		CreationTime:   creationTime,
		Provider:       string(types.ProviderHuawei),
	}
}
