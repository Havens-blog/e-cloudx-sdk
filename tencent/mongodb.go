package tencent

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	mongodb "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/mongodb/v20190725"
)

// MongoDBAdapter 腾讯云MongoDB适配器
type MongoDBAdapter struct {
	account       *domain.CloudAccount
	defaultRegion string
	logger        *elog.Component
}

// NewMongoDBAdapter 创建腾讯云MongoDB适配器
func NewMongoDBAdapter(account *domain.CloudAccount, defaultRegion string, logger *elog.Component) *MongoDBAdapter {
	return &MongoDBAdapter{
		account:       account,
		defaultRegion: defaultRegion,
		logger:        logger,
	}
}

// getClient 获取MongoDB客户端
func (a *MongoDBAdapter) getClient(region string) (*mongodb.Client, error) {
	credential := common.NewCredential(a.account.AccessKeyID, a.account.AccessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "mongodb.tencentcloudapi.com"
	client, err := mongodb.NewClient(credential, region, cpf)
	if err != nil {
		return nil, fmt.Errorf("创建腾讯云MongoDB客户端失败: %w", err)
	}
	return client, nil
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

	request := mongodb.NewDescribeDBInstancesRequest()
	request.InstanceIds = []*string{common.StringPtr(instanceID)}

	response, err := client.DescribeDBInstances(request)
	if err != nil {
		return nil, fmt.Errorf("获取MongoDB实例详情失败: %w", err)
	}

	if len(response.Response.InstanceDetails) == 0 {
		return nil, fmt.Errorf("MongoDB实例不存在: %s", instanceID)
	}

	inst := response.Response.InstanceDetails[0]
	instance := convertTencentMongoDBInstance(inst, region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取MongoDB实例
func (a *MongoDBAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.MongoDBInstance, error) {
	if len(instanceIDs) == 0 {
		return nil, nil
	}

	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	ids := make([]*string, len(instanceIDs))
	for i, id := range instanceIDs {
		ids[i] = common.StringPtr(id)
	}

	request := mongodb.NewDescribeDBInstancesRequest()
	request.InstanceIds = ids

	response, err := client.DescribeDBInstances(request)
	if err != nil {
		return nil, fmt.Errorf("批量获取MongoDB实例失败: %w", err)
	}

	var instances []types.MongoDBInstance
	for _, inst := range response.Response.InstanceDetails {
		instances = append(instances, convertTencentMongoDBInstance(inst, region))
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
	offset := uint64(0)
	limit := uint64(100)

	if filter != nil && filter.PageSize > 0 {
		limit = uint64(filter.PageSize)
	}

	for {
		request := mongodb.NewDescribeDBInstancesRequest()
		request.Offset = common.Uint64Ptr(offset)
		request.Limit = common.Uint64Ptr(limit)

		response, err := client.DescribeDBInstances(request)
		if err != nil {
			return nil, fmt.Errorf("获取MongoDB实例列表失败: %w", err)
		}

		for _, inst := range response.Response.InstanceDetails {
			instance := convertTencentMongoDBInstance(inst, region)
			allInstances = append(allInstances, instance)
		}

		if filter != nil && filter.PageNumber > 0 {
			break
		}

		if len(response.Response.InstanceDetails) < int(limit) {
			break
		}
		offset += limit
	}

	a.logger.Info("获取腾讯云MongoDB实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertTencentMongoDBInstance 转换腾讯云MongoDB实例为通用格式
func convertTencentMongoDBInstance(inst *mongodb.InstanceDetail, region string) types.MongoDBInstance {
	instanceID := ""
	instanceName := ""
	status := ""
	zone := ""
	engineVersion := ""
	cpu := 0
	memory := 0
	storage := 0
	vpcID := ""
	vswitchID := ""
	chargeType := ""
	creationTime := ""
	expiredTime := ""
	projectID := ""
	clusterType := ""
	replicaSetNum := 0

	if inst.InstanceId != nil {
		instanceID = *inst.InstanceId
	}
	if inst.InstanceName != nil {
		instanceName = *inst.InstanceName
	}
	if inst.Status != nil {
		status = fmt.Sprintf("%d", *inst.Status)
	}
	if inst.Zone != nil {
		zone = *inst.Zone
	}
	if inst.MongoVersion != nil {
		engineVersion = *inst.MongoVersion
	}
	if inst.CpuNum != nil {
		cpu = int(*inst.CpuNum)
	}
	if inst.Memory != nil {
		memory = int(*inst.Memory)
	}
	if inst.Volume != nil {
		storage = int(*inst.Volume)
	}
	if inst.VpcId != nil {
		vpcID = *inst.VpcId
	}
	if inst.SubnetId != nil {
		vswitchID = *inst.SubnetId
	}
	if inst.PayMode != nil {
		chargeType = fmt.Sprintf("%d", *inst.PayMode)
	}
	if inst.CreateTime != nil {
		creationTime = *inst.CreateTime
	}
	if inst.DeadLine != nil {
		expiredTime = *inst.DeadLine
	}
	if inst.ProjectId != nil {
		projectID = fmt.Sprintf("%d", *inst.ProjectId)
	}
	if inst.ClusterType != nil {
		clusterType = fmt.Sprintf("%d", *inst.ClusterType)
	}
	_ = replicaSetNum // 避免未使用警告

	return types.MongoDBInstance{
		InstanceID:     instanceID,
		InstanceName:   instanceName,
		Status:         types.NormalizeMongoDBStatus(status),
		Region:         region,
		Zone:           zone,
		EngineVersion:  engineVersion,
		DBInstanceType: clusterType,
		CPU:            cpu,
		Memory:         memory,
		Storage:        storage,
		VPCID:          vpcID,
		VSwitchID:      vswitchID,
		ShardCount:     replicaSetNum,
		ChargeType:     chargeType,
		CreationTime:   creationTime,
		ExpiredTime:    expiredTime,
		ProjectID:      projectID,
		Provider:       string(types.ProviderTencent),
	}
}
