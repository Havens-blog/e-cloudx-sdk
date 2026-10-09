package aliyun

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/gotomicro/ego/core/elog"
)

// AssetAdapter 阿里云资产适配器
type AssetAdapter struct {
	client *Client
	logger *elog.Component
}

// NewAssetAdapter 创建阿里云资产适配器
func NewAssetAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *AssetAdapter {
	return &AssetAdapter{
		client: NewClient(accessKeyID, accessKeySecret, defaultRegion, logger),
		logger: logger,
	}
}

// GetRegions 获取支持的地域列表
func (a *AssetAdapter) GetRegions(ctx context.Context) ([]types.Region, error) {
	if err := a.client.WaitRateLimit(ctx); err != nil {
		return nil, err
	}

	ecsClient, err := a.client.GetECSClient(a.client.defaultRegion)
	if err != nil {
		return nil, err
	}

	request := ecs.CreateDescribeRegionsRequest()
	request.Scheme = "https"

	var response *ecs.DescribeRegionsResponse
	err = a.client.RetryWithBackoff(ctx, func() error {
		var e error
		response, e = ecsClient.DescribeRegions(request)
		return e
	})

	if err != nil {
		return nil, fmt.Errorf("获取地域列表失败: %w", err)
	}

	regions := make([]types.Region, 0, len(response.Regions.Region))
	for _, r := range response.Regions.Region {
		regions = append(regions, types.Region{
			ID:          r.RegionId,
			Name:        r.RegionId,
			LocalName:   r.LocalName,
			Description: r.LocalName,
		})
	}

	a.logger.Info("获取阿里云地域列表成功", elog.Int("count", len(regions)))
	return regions, nil
}

// GetECSInstances 获取云主机实例列表
func (a *AssetAdapter) GetECSInstances(ctx context.Context, region string) ([]types.ECSInstance, error) {
	if err := a.client.WaitRateLimit(ctx); err != nil {
		return nil, err
	}

	ecsClient, err := a.client.GetECSClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.ECSInstance
	pageNumber := 1
	pageSize := 100

	for {
		request := ecs.CreateDescribeInstancesRequest()
		request.Scheme = "https"
		request.RegionId = region
		request.PageNumber = requests.NewInteger(pageNumber)
		request.PageSize = requests.NewInteger(pageSize)

		var response *ecs.DescribeInstancesResponse
		err = a.client.RetryWithBackoff(ctx, func() error {
			var e error
			response, e = ecsClient.DescribeInstances(request)
			return e
		})

		if err != nil {
			return nil, fmt.Errorf("获取实例列表失败: %w", err)
		}

		for _, inst := range response.Instances.Instance {
			instance := a.convertInstance(inst, region)
			allInstances = append(allInstances, instance)
		}

		if len(response.Instances.Instance) < pageSize {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取阿里云ECS实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertInstance 转换阿里云实例为通用格式
func (a *AssetAdapter) convertInstance(inst ecs.Instance, region string) types.ECSInstance {
	publicIP := ""
	if len(inst.PublicIpAddress.IpAddress) > 0 {
		publicIP = inst.PublicIpAddress.IpAddress[0]
	}
	if publicIP == "" && inst.EipAddress.IpAddress != "" {
		publicIP = inst.EipAddress.IpAddress
	}

	privateIP := ""
	if len(inst.VpcAttributes.PrivateIpAddress.IpAddress) > 0 {
		privateIP = inst.VpcAttributes.PrivateIpAddress.IpAddress[0]
	}

	// 安全组信息
	securityGroups := make([]types.SecurityGroup, 0, len(inst.SecurityGroupIds.SecurityGroupId))
	for _, sgID := range inst.SecurityGroupIds.SecurityGroupId {
		securityGroups = append(securityGroups, types.SecurityGroup{
			ID: sgID,
		})
	}

	tags := make(map[string]string)
	for _, tag := range inst.Tags.Tag {
		tags[tag.TagKey] = tag.TagValue
	}

	instanceTypeFamily := ""
	if len(inst.InstanceType) > 4 && inst.InstanceType[:4] == "ecs." {
		remaining := inst.InstanceType[4:]
		for i, c := range remaining {
			if c == '.' {
				instanceTypeFamily = remaining[:i]
				break
			}
		}
	}

	ioOptimized := "none"
	if inst.IoOptimized {
		ioOptimized = "optimized"
	}

	// 系统盘信息 (阿里云 DescribeInstances 不直接返回系统盘详情，需要额外调用 DescribeDisks)
	systemDisk := types.SystemDisk{}

	// 数据盘信息
	dataDisks := make([]types.DataDisk, 0)

	return types.ECSInstance{
		InstanceID:              inst.InstanceId,
		InstanceName:            inst.InstanceName,
		Status:                  types.NormalizeStatus(inst.Status),
		Region:                  region,
		Zone:                    inst.ZoneId,
		InstanceType:            inst.InstanceType,
		InstanceTypeFamily:      instanceTypeFamily,
		CPU:                     inst.Cpu,
		Memory:                  inst.Memory,
		OSType:                  inst.OSType,
		OSName:                  inst.OSName,
		ImageID:                 inst.ImageId,
		ImageName:               "", // 需要额外查询
		PublicIP:                publicIP,
		PrivateIP:               privateIP,
		VPCID:                   inst.VpcAttributes.VpcId,
		VPCName:                 "", // 需要额外查询
		VSwitchID:               inst.VpcAttributes.VSwitchId,
		VSwitchName:             "", // 需要额外查询
		SecurityGroups:          securityGroups,
		InternetMaxBandwidthIn:  inst.InternetMaxBandwidthIn,
		InternetMaxBandwidthOut: inst.InternetMaxBandwidthOut,
		SystemDisk:              systemDisk,
		DataDisks:               dataDisks,
		ChargeType:              inst.InstanceChargeType,
		CreationTime:            inst.CreationTime,
		ExpiredTime:             inst.ExpiredTime,
		IoOptimized:             ioOptimized,
		NetworkType:             inst.NetworkType,
		InstanceNetworkType:     inst.InstanceNetworkType,
		ProjectID:               inst.ResourceGroupId,
		ProjectName:             "", // 资源组名称需要额外查询
		Tags:                    tags,
		Description:             inst.Description,
		Provider:                string(types.ProviderAliyun),
		HostName:                inst.HostName,
		KeyPairName:             inst.KeyPairName,
	}
}
