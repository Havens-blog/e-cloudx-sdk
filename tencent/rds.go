package tencent

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	cdb "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cdb/v20170320"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

// RDSAdapter 腾讯云CDB MySQL适配器
type RDSAdapter struct {
	account       *domain.CloudAccount
	defaultRegion string
	logger        *elog.Component
}

// NewRDSAdapter 创建腾讯云RDS适配器
func NewRDSAdapter(account *domain.CloudAccount, defaultRegion string, logger *elog.Component) *RDSAdapter {
	return &RDSAdapter{
		account:       account,
		defaultRegion: defaultRegion,
		logger:        logger,
	}
}

// getClient 获取CDB客户端
func (a *RDSAdapter) getClient(region string) (*cdb.Client, error) {
	credential := common.NewCredential(a.account.AccessKeyID, a.account.AccessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "cdb.tencentcloudapi.com"
	client, err := cdb.NewClient(credential, region, cpf)
	if err != nil {
		return nil, fmt.Errorf("创建腾讯云CDB客户端失败: %w", err)
	}
	return client, nil
}

// ListInstances 获取RDS实例列表
func (a *RDSAdapter) ListInstances(ctx context.Context, region string) ([]types.RDSInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个RDS实例详情
func (a *RDSAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.RDSInstance, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	request := cdb.NewDescribeDBInstancesRequest()
	request.InstanceIds = []*string{common.StringPtr(instanceID)}

	response, err := client.DescribeDBInstances(request)
	if err != nil {
		return nil, fmt.Errorf("获取CDB实例详情失败: %w", err)
	}

	if len(response.Response.Items) == 0 {
		return nil, fmt.Errorf("CDB实例不存在: %s", instanceID)
	}

	inst := response.Response.Items[0]
	instance := convertTencentRDSInstance(inst, region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取RDS实例
func (a *RDSAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.RDSInstance, error) {
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

	request := cdb.NewDescribeDBInstancesRequest()
	request.InstanceIds = ids

	response, err := client.DescribeDBInstances(request)
	if err != nil {
		return nil, fmt.Errorf("批量获取CDB实例失败: %w", err)
	}

	var instances []types.RDSInstance
	for _, inst := range response.Response.Items {
		instances = append(instances, convertTencentRDSInstance(inst, region))
	}
	return instances, nil
}

// GetInstanceStatus 获取实例状态
func (a *RDSAdapter) GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, instanceID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取实例列表
func (a *RDSAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.RDSInstanceFilter) ([]types.RDSInstance, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.RDSInstance
	offset := int64(0)
	limit := int64(100)

	if filter != nil && filter.PageSize > 0 {
		limit = int64(filter.PageSize)
	}

	for {
		request := cdb.NewDescribeDBInstancesRequest()
		request.Offset = common.Uint64Ptr(uint64(offset))
		request.Limit = common.Uint64Ptr(uint64(limit))

		response, err := client.DescribeDBInstances(request)
		if err != nil {
			return nil, fmt.Errorf("获取CDB实例列表失败: %w", err)
		}

		for _, inst := range response.Response.Items {
			instance := convertTencentRDSInstance(inst, region)
			allInstances = append(allInstances, instance)
		}

		if filter != nil && filter.PageNumber > 0 {
			break
		}

		if len(response.Response.Items) < int(limit) {
			break
		}
		offset += limit
	}

	a.logger.Info("获取腾讯云CDB实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertTencentRDSInstance 转换腾讯云CDB实例为通用格式
func convertTencentRDSInstance(inst *cdb.InstanceInfo, region string) types.RDSInstance {
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
	if inst.EngineVersion != nil {
		engineVersion = *inst.EngineVersion
	}
	if inst.Cpu != nil {
		cpu = int(*inst.Cpu)
	}
	if inst.Memory != nil {
		memory = int(*inst.Memory)
	}
	if inst.Volume != nil {
		storage = int(*inst.Volume)
	}
	if inst.UniqVpcId != nil {
		vpcID = *inst.UniqVpcId
	}
	if inst.UniqSubnetId != nil {
		vswitchID = *inst.UniqSubnetId
	}
	if inst.PayType != nil {
		chargeType = fmt.Sprintf("%d", *inst.PayType)
	}
	if inst.CreateTime != nil {
		creationTime = *inst.CreateTime
	}
	if inst.DeadlineTime != nil {
		expiredTime = *inst.DeadlineTime
	}
	if inst.ProjectId != nil {
		projectID = fmt.Sprintf("%d", *inst.ProjectId)
	}

	return types.RDSInstance{
		InstanceID:    instanceID,
		InstanceName:  instanceName,
		Status:        types.NormalizeRDSStatus(status),
		Region:        region,
		Zone:          zone,
		Engine:        "mysql",
		EngineVersion: engineVersion,
		CPU:           cpu,
		Memory:        memory,
		Storage:       storage,
		VPCID:         vpcID,
		VSwitchID:     vswitchID,
		ChargeType:    chargeType,
		CreationTime:  creationTime,
		ExpiredTime:   expiredTime,
		ProjectID:     projectID,
		Provider:      string(types.ProviderTencent),
	}
}
