package tencent

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	cvm "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cvm/v20170312"
)

// ECSAdapter 腾讯云CVM适配器
type ECSAdapter struct {
	account       *domain.CloudAccount
	defaultRegion string
	logger        *elog.Component
}

// NewECSAdapter 创建腾讯云CVM适配器
func NewECSAdapter(account *domain.CloudAccount, defaultRegion string, logger *elog.Component) *ECSAdapter {
	if defaultRegion == "" {
		defaultRegion = "ap-guangzhou"
	}
	return &ECSAdapter{
		account:       account,
		defaultRegion: defaultRegion,
		logger:        logger,
	}
}

// getClient 获取CVM客户端
func (a *ECSAdapter) getClient(region string) (*cvm.Client, error) {
	credential := common.NewCredential(a.account.AccessKeyID, a.account.AccessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "cvm.tencentcloudapi.com"

	client, err := cvm.NewClient(credential, region, cpf)
	if err != nil {
		return nil, fmt.Errorf("创建腾讯云CVM客户端失败: %w", err)
	}
	return client, nil
}

// GetRegions 获取支持的地域列表
func (a *ECSAdapter) GetRegions(ctx context.Context) ([]types.Region, error) {
	client, err := a.getClient(a.defaultRegion)
	if err != nil {
		return nil, err
	}

	request := cvm.NewDescribeRegionsRequest()
	response, err := client.DescribeRegions(request)
	if err != nil {
		return nil, fmt.Errorf("获取腾讯云地域列表失败: %w", err)
	}

	regions := make([]types.Region, 0, len(response.Response.RegionSet))
	for _, r := range response.Response.RegionSet {
		if *r.RegionState == "AVAILABLE" {
			regions = append(regions, types.Region{
				ID:          *r.Region,
				Name:        *r.Region,
				LocalName:   *r.RegionName,
				Description: *r.RegionName,
			})
		}
	}

	a.logger.Info("获取腾讯云地域列表成功", elog.Int("count", len(regions)))
	return regions, nil
}

// ListInstances 获取云主机实例列表
func (a *ECSAdapter) ListInstances(ctx context.Context, region string) ([]types.ECSInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个云主机实例详情
func (a *ECSAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.ECSInstance, error) {
	instances, err := a.ListInstancesByIDs(ctx, region, []string{instanceID})
	if err != nil {
		return nil, err
	}
	if len(instances) == 0 {
		return nil, fmt.Errorf("实例不存在: %s", instanceID)
	}
	return &instances[0], nil
}

// ListInstancesByIDs 批量获取云主机实例
func (a *ECSAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.ECSInstance, error) {
	if len(instanceIDs) == 0 {
		return nil, nil
	}
	filter := &cloudx.ECSInstanceFilter{InstanceIDs: instanceIDs}
	return a.ListInstancesWithFilter(ctx, region, filter)
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
	offset := int64(0)
	limit := int64(100)

	if filter != nil && filter.PageSize > 0 {
		limit = int64(filter.PageSize)
	}
	if filter != nil && filter.PageNumber > 0 {
		offset = int64((filter.PageNumber - 1) * filter.PageSize)
	}

	for {
		request := cvm.NewDescribeInstancesRequest()
		request.Offset = &offset
		request.Limit = &limit

		// 应用过滤条件
		if filter != nil {
			if len(filter.InstanceIDs) > 0 {
				request.InstanceIds = common.StringPtrs(filter.InstanceIDs)
			}
			var filters []*cvm.Filter
			if filter.InstanceName != "" {
				filters = append(filters, &cvm.Filter{
					Name:   common.StringPtr("instance-name"),
					Values: common.StringPtrs([]string{filter.InstanceName}),
				})
			}
			if len(filter.Status) > 0 {
				filters = append(filters, &cvm.Filter{
					Name:   common.StringPtr("instance-state"),
					Values: common.StringPtrs(filter.Status),
				})
			}
			if filter.VPCID != "" {
				filters = append(filters, &cvm.Filter{
					Name:   common.StringPtr("vpc-id"),
					Values: common.StringPtrs([]string{filter.VPCID}),
				})
			}
			if filter.Zone != "" {
				filters = append(filters, &cvm.Filter{
					Name:   common.StringPtr("zone"),
					Values: common.StringPtrs([]string{filter.Zone}),
				})
			}
			if len(filters) > 0 {
				request.Filters = filters
			}
		}

		response, err := client.DescribeInstances(request)
		if err != nil {
			return nil, fmt.Errorf("获取腾讯云CVM实例列表失败: %w", err)
		}

		for _, inst := range response.Response.InstanceSet {
			instance := convertTencentInstance(inst, region)
			allInstances = append(allInstances, instance)
		}

		// 如果指定了分页，只返回一页
		if filter != nil && filter.PageNumber > 0 {
			break
		}

		if len(response.Response.InstanceSet) < int(limit) {
			break
		}
		offset += limit
	}

	a.logger.Info("获取腾讯云CVM实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertTencentInstance 转换腾讯云实例为通用格式
func convertTencentInstance(inst *cvm.Instance, region string) types.ECSInstance {
	publicIP := ""
	if len(inst.PublicIpAddresses) > 0 {
		publicIP = *inst.PublicIpAddresses[0]
	}

	privateIP := ""
	if len(inst.PrivateIpAddresses) > 0 {
		privateIP = *inst.PrivateIpAddresses[0]
	}

	securityGroups := make([]types.SecurityGroup, 0, len(inst.SecurityGroupIds))
	for _, sg := range inst.SecurityGroupIds {
		securityGroups = append(securityGroups, types.SecurityGroup{ID: *sg})
	}

	tags := make(map[string]string)
	for _, tag := range inst.Tags {
		tags[*tag.Key] = *tag.Value
	}

	instanceType := *inst.InstanceType
	instanceTypeFamily := ""
	for i, c := range instanceType {
		if c == '.' {
			instanceTypeFamily = instanceType[:i]
			break
		}
	}

	cpu, memory := 0, 0
	if inst.CPU != nil {
		cpu = int(*inst.CPU)
	}
	if inst.Memory != nil {
		memory = int(*inst.Memory) * 1024
	}

	chargeType := "PostPaid"
	if inst.InstanceChargeType != nil {
		switch *inst.InstanceChargeType {
		case "PREPAID":
			chargeType = "PrePaid"
		case "POSTPAID_BY_HOUR":
			chargeType = "PostPaid"
		case "SPOTPAID":
			chargeType = "Spot"
		}
	}

	systemDisk := types.SystemDisk{}
	if inst.SystemDisk != nil {
		if inst.SystemDisk.DiskId != nil {
			systemDisk.DiskID = *inst.SystemDisk.DiskId
		}
		if inst.SystemDisk.DiskType != nil {
			systemDisk.Category = *inst.SystemDisk.DiskType
		}
		if inst.SystemDisk.DiskSize != nil {
			systemDisk.Size = int(*inst.SystemDisk.DiskSize)
		}
	}

	dataDisks := make([]types.DataDisk, 0)
	if inst.DataDisks != nil {
		for _, disk := range inst.DataDisks {
			dataDisk := types.DataDisk{}
			if disk.DiskId != nil {
				dataDisk.DiskID = *disk.DiskId
			}
			if disk.DiskType != nil {
				dataDisk.Category = *disk.DiskType
			}
			if disk.DiskSize != nil {
				dataDisk.Size = int(*disk.DiskSize)
			}
			if disk.DeleteWithInstance != nil {
				dataDisk.DeleteWithInstance = *disk.DeleteWithInstance
			}
			if disk.Encrypt != nil {
				dataDisk.Encrypted = *disk.Encrypt
			}
			dataDisks = append(dataDisks, dataDisk)
		}
	}

	projectID := ""
	if inst.Placement != nil && inst.Placement.ProjectId != nil {
		projectID = fmt.Sprintf("%d", *inst.Placement.ProjectId)
	}

	keyPairName := ""
	if inst.LoginSettings != nil && len(inst.LoginSettings.KeyIds) > 0 && inst.LoginSettings.KeyIds[0] != nil {
		keyPairName = *inst.LoginSettings.KeyIds[0]
	}

	return types.ECSInstance{
		InstanceID:         *inst.InstanceId,
		InstanceName:       *inst.InstanceName,
		Status:             types.NormalizeStatus(*inst.InstanceState),
		Region:             region,
		Zone:               *inst.Placement.Zone,
		InstanceType:       instanceType,
		InstanceTypeFamily: instanceTypeFamily,
		CPU:                cpu,
		Memory:             memory,
		OSType:             *inst.OsName,
		OSName:             *inst.OsName,
		ImageID:            *inst.ImageId,
		PublicIP:           publicIP,
		PrivateIP:          privateIP,
		VPCID:              *inst.VirtualPrivateCloud.VpcId,
		VSwitchID:          *inst.VirtualPrivateCloud.SubnetId,
		SecurityGroups:     securityGroups,
		SystemDisk:         systemDisk,
		DataDisks:          dataDisks,
		ChargeType:         chargeType,
		CreationTime:       *inst.CreatedTime,
		ExpiredTime:        safeStringPtr(inst.ExpiredTime),
		NetworkType:        "vpc",
		ProjectID:          projectID,
		Tags:               tags,
		Provider:           string(types.ProviderTencent),
		KeyPairName:        keyPairName,
	}
}

func safeStringPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
