package huawei

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	rds "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/rds/v3"
	rdsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/rds/v3/model"
	rdsregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/rds/v3/region"
)

// RDSAdapter 华为云RDS适配器
type RDSAdapter struct {
	account       *domain.CloudAccount
	defaultRegion string
	logger        *elog.Component
}

// NewRDSAdapter 创建华为云RDS适配器
func NewRDSAdapter(account *domain.CloudAccount, defaultRegion string, logger *elog.Component) *RDSAdapter {
	return &RDSAdapter{
		account:       account,
		defaultRegion: defaultRegion,
		logger:        logger,
	}
}

// getClient 获取RDS客户端
func (a *RDSAdapter) getClient(region string) (*rds.RdsClient, error) {
	auth, err := basic.NewCredentialsBuilder().
		WithAk(a.account.AccessKeyID).
		WithSk(a.account.AccessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}

	reg, err := rdsregion.SafeValueOf(region)
	if err != nil {
		return nil, fmt.Errorf("无效的华为云地域: %s", region)
	}

	hcClient, err := rds.RdsClientBuilder().
		WithRegion(reg).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云RDS客户端失败: %w", err)
	}

	return rds.NewRdsClient(hcClient), nil
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

	request := &rdsmodel.ListInstancesRequest{
		Id: &instanceID,
	}

	response, err := client.ListInstances(request)
	if err != nil {
		return nil, fmt.Errorf("获取RDS实例详情失败: %w", err)
	}

	if response.Instances == nil || len(*response.Instances) == 0 {
		return nil, fmt.Errorf("RDS实例不存在: %s", instanceID)
	}

	inst := (*response.Instances)[0]
	instance := convertHuaweiRDSListItem(&inst, region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取RDS实例
func (a *RDSAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.RDSInstance, error) {
	if len(instanceIDs) == 0 {
		return nil, nil
	}

	var instances []types.RDSInstance
	for _, id := range instanceIDs {
		inst, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取RDS实例失败", elog.String("instance_id", id), elog.FieldErr(err))
			continue
		}
		instances = append(instances, *inst)
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
	offset := int32(0)
	limit := int32(100)

	if filter != nil && filter.PageSize > 0 {
		limit = int32(filter.PageSize)
	}

	for {
		request := &rdsmodel.ListInstancesRequest{
			Offset: &offset,
			Limit:  &limit,
		}

		response, err := client.ListInstances(request)
		if err != nil {
			return nil, fmt.Errorf("获取RDS实例列表失败: %w", err)
		}

		if response.Instances == nil {
			break
		}

		for _, inst := range *response.Instances {
			instance := convertHuaweiRDSListItem(&inst, region)
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

	a.logger.Info("获取华为云RDS实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertHuaweiRDSListItem 转换华为云RDS列表项为通用格式
func convertHuaweiRDSListItem(inst *rdsmodel.InstanceResponse, region string) types.RDSInstance {
	cpu := 0
	memory := 0

	if inst.Cpu != nil {
		cpu, _ = strconv.Atoi(*inst.Cpu)
	}
	if inst.Mem != nil {
		mem, _ := strconv.Atoi(*inst.Mem)
		memory = mem * 1024 // GB to MB
	}

	storage := 0
	if inst.Volume.Size != 0 {
		storage = int(inst.Volume.Size)
	}

	engine := ""
	engineVersion := ""
	if inst.Datastore.Type.Value() != "" {
		engine = inst.Datastore.Type.Value()
	}
	if inst.Datastore.Version != "" {
		engineVersion = inst.Datastore.Version
	}

	chargeType := ""
	if inst.ChargeInfo.ChargeMode.Value() != "" {
		chargeType = inst.ChargeInfo.ChargeMode.Value()
	}

	return types.RDSInstance{
		InstanceID:    inst.Id,
		InstanceName:  inst.Name,
		Status:        types.NormalizeRDSStatus(inst.Status),
		Region:        region,
		Engine:        engine,
		EngineVersion: engineVersion,
		CPU:           cpu,
		Memory:        memory,
		Storage:       storage,
		VPCID:         inst.VpcId,
		VSwitchID:     inst.SubnetId,
		ChargeType:    chargeType,
		CreationTime:  inst.Created,
		Provider:      string(types.ProviderHuawei),
	}
}
