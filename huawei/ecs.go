package huawei

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	ecs "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2"
	ecsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2/model"
	ecsregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2/region"
)

// ECSAdapter 华为云ECS适配器
type ECSAdapter struct {
	account       *domain.CloudAccount
	defaultRegion string
	logger        *elog.Component
}

// NewECSAdapter 创建华为云ECS适配器
func NewECSAdapter(account *domain.CloudAccount, defaultRegion string, logger *elog.Component) *ECSAdapter {
	if defaultRegion == "" {
		defaultRegion = "cn-north-4"
	}
	return &ECSAdapter{
		account:       account,
		defaultRegion: defaultRegion,
		logger:        logger,
	}
}

// getClient 获取ECS客户端
func (a *ECSAdapter) getClient(region string) (*ecs.EcsClient, error) {
	auth, err := basic.NewCredentialsBuilder().
		WithAk(a.account.AccessKeyID).
		WithSk(a.account.AccessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}

	regionObj, err := ecsregion.SafeValueOf(region)
	if err != nil {
		return nil, fmt.Errorf("不支持的华为云地域: %s", region)
	}

	hcClient, err := ecs.EcsClientBuilder().
		WithRegion(regionObj).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云ECS客户端失败: %w", err)
	}

	return ecs.NewEcsClient(hcClient), nil
}

// GetRegions 获取支持的地域列表
func (a *ECSAdapter) GetRegions(ctx context.Context) ([]types.Region, error) {
	regions := []types.Region{
		{ID: "cn-north-1", Name: "cn-north-1", LocalName: "华北-北京一", Description: "China North 1 (Beijing)"},
		{ID: "cn-north-4", Name: "cn-north-4", LocalName: "华北-北京四", Description: "China North 4 (Beijing)"},
		{ID: "cn-north-9", Name: "cn-north-9", LocalName: "华北-乌兰察布一", Description: "China North 9 (Ulanqab)"},
		{ID: "cn-east-2", Name: "cn-east-2", LocalName: "华东-上海二", Description: "China East 2 (Shanghai)"},
		{ID: "cn-east-3", Name: "cn-east-3", LocalName: "华东-上海一", Description: "China East 3 (Shanghai)"},
		{ID: "cn-south-1", Name: "cn-south-1", LocalName: "华南-广州", Description: "China South 1 (Guangzhou)"},
		{ID: "cn-south-2", Name: "cn-south-2", LocalName: "华南-深圳", Description: "China South 2 (Shenzhen)"},
		{ID: "cn-southwest-2", Name: "cn-southwest-2", LocalName: "西南-贵阳一", Description: "China Southwest 2 (Guiyang)"},
		{ID: "ap-southeast-1", Name: "ap-southeast-1", LocalName: "亚太-新加坡", Description: "Asia Pacific (Singapore)"},
		{ID: "ap-southeast-2", Name: "ap-southeast-2", LocalName: "亚太-曼谷", Description: "Asia Pacific (Bangkok)"},
		{ID: "ap-southeast-3", Name: "ap-southeast-3", LocalName: "亚太-雅加达", Description: "Asia Pacific (Jakarta)"},
		{ID: "af-south-1", Name: "af-south-1", LocalName: "非洲-约翰内斯堡", Description: "Africa (Johannesburg)"},
	}

	a.logger.Info("获取华为云地域列表成功", elog.Int("count", len(regions)))
	return regions, nil
}

// ListInstances 获取云主机实例列表
func (a *ECSAdapter) ListInstances(ctx context.Context, region string) ([]types.ECSInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个云主机实例详情
func (a *ECSAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.ECSInstance, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	request := &ecsmodel.ShowServerRequest{ServerId: instanceID}
	response, err := client.ShowServer(request)
	if err != nil {
		return nil, fmt.Errorf("获取华为云ECS实例详情失败: %w", err)
	}

	if response.Server == nil {
		return nil, fmt.Errorf("实例不存在: %s", instanceID)
	}

	instance := convertHuaweiServerDetail(*response.Server, region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取云主机实例
func (a *ECSAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.ECSInstance, error) {
	if len(instanceIDs) == 0 {
		return nil, nil
	}

	var instances []types.ECSInstance
	for _, id := range instanceIDs {
		inst, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取实例失败", elog.String("instance_id", id), elog.FieldErr(err))
			continue
		}
		instances = append(instances, *inst)
	}
	return instances, nil
}

// GetInstanceStatus 获取实例状态
func (a *ECSAdapter) GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, instanceID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取实例列表
func (a *ECSAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *cloudx.ECSInstanceFilter) ([]types.ECSInstance, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.ECSInstance
	offset := int32(1)
	limit := int32(100)

	if filter != nil && filter.PageSize > 0 {
		limit = int32(filter.PageSize)
	}
	if filter != nil && filter.PageNumber > 0 {
		offset = int32(filter.PageNumber)
	}

	for {
		request := &ecsmodel.ListServersDetailsRequest{
			Offset: &offset,
			Limit:  &limit,
		}

		// 应用过滤条件
		if filter != nil {
			if filter.InstanceName != "" {
				request.Name = &filter.InstanceName
			}
			if len(filter.Status) > 0 {
				request.Status = &filter.Status[0]
			}
		}

		response, err := client.ListServersDetails(request)
		if err != nil {
			return nil, fmt.Errorf("获取华为云ECS实例列表失败: %w", err)
		}

		if response.Servers == nil || len(*response.Servers) == 0 {
			break
		}

		for _, inst := range *response.Servers {
			instance := convertHuaweiInstance(inst, region)
			allInstances = append(allInstances, instance)
		}

		// 如果指定了分页，只返回一页
		if filter != nil && filter.PageNumber > 0 {
			break
		}

		if len(*response.Servers) < int(limit) {
			break
		}
		offset++
	}

	a.logger.Info("获取华为云ECS实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertHuaweiInstance 转换华为云实例为通用格式
func convertHuaweiInstance(inst ecsmodel.ServerDetail, region string) types.ECSInstance {
	return convertHuaweiServerDetail(inst, region)
}

// convertHuaweiServerDetail 转换华为云服务器详情为通用格式
func convertHuaweiServerDetail(inst ecsmodel.ServerDetail, region string) types.ECSInstance {
	publicIP, privateIP := "", ""
	if inst.Addresses != nil {
		for _, addrs := range inst.Addresses {
			for _, addr := range addrs {
				if addr.OSEXTIPStype != nil && addr.OSEXTIPStype.Value() == "floating" {
					publicIP = addr.Addr
				}
				if addr.OSEXTIPStype != nil && addr.OSEXTIPStype.Value() == "fixed" {
					privateIP = addr.Addr
				}
			}
		}
	}

	securityGroups := make([]types.SecurityGroup, 0)
	for _, sg := range inst.SecurityGroups {
		securityGroups = append(securityGroups, types.SecurityGroup{ID: sg.Id, Name: sg.Name})
	}

	tags := make(map[string]string)
	if inst.Tags != nil {
		for _, tag := range *inst.Tags {
			tags[tag] = ""
		}
	}

	cpu, memory := 0, 0
	if inst.Flavor.Vcpus != "" {
		fmt.Sscanf(inst.Flavor.Vcpus, "%d", &cpu)
	}
	if inst.Flavor.Ram != "" {
		fmt.Sscanf(inst.Flavor.Ram, "%d", &memory)
	}

	chargeType := "PostPaid"
	if inst.Metadata != nil {
		if ct, ok := inst.Metadata["charging_mode"]; ok {
			switch ct {
			case "0":
				chargeType = "PostPaid"
			case "1":
				chargeType = "PrePaid"
			case "2":
				chargeType = "Spot"
			}
		}
	}

	systemDisk := types.SystemDisk{}
	for _, vol := range inst.OsExtendedVolumesvolumesAttached {
		if vol.BootIndex != nil && *vol.BootIndex == "0" {
			systemDisk.DiskID = vol.Id
			break
		}
	}

	vpcID := ""
	if inst.Metadata != nil {
		if v, ok := inst.Metadata["vpc_id"]; ok {
			vpcID = v
		}
	}

	projectID := ""
	if inst.EnterpriseProjectId != nil {
		projectID = *inst.EnterpriseProjectId
	}

	description := ""
	if inst.Description != nil {
		description = *inst.Description
	}

	return types.ECSInstance{
		InstanceID:     inst.Id,
		InstanceName:   inst.Name,
		Status:         types.NormalizeStatus(inst.Status),
		Region:         region,
		Zone:           inst.OSEXTAZavailabilityZone,
		InstanceType:   inst.Flavor.Id,
		CPU:            cpu,
		Memory:         memory,
		ImageID:        inst.Image.Id,
		PublicIP:       publicIP,
		PrivateIP:      privateIP,
		VPCID:          vpcID,
		SecurityGroups: securityGroups,
		SystemDisk:     systemDisk,
		ChargeType:     chargeType,
		CreationTime:   inst.Created,
		NetworkType:    "vpc",
		ProjectID:      projectID,
		Tags:           tags,
		Description:    description,
		Provider:       string(types.ProviderHuawei),
		HostName:       inst.OSEXTSRVATTRhostname,
		KeyPairName:    inst.KeyName,
	}
}
