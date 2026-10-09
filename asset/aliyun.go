package asset

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/gotomicro/ego/core/elog"
)

// AliyunAdapter 阿里云资产适配器
type AliyunAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
	clients         map[string]*ecs.Client
}

// AliyunConfig 阿里云配置
type AliyunConfig struct {
	AccessKeyID     string
	AccessKeySecret string
	DefaultRegion   string
}

// NewAliyunAdapter 创建阿里云适配器
func NewAliyunAdapter(config AliyunConfig, logger *elog.Component) *AliyunAdapter {
	defaultRegion := config.DefaultRegion
	if defaultRegion == "" {
		defaultRegion = "cn-shenzhen"
	}

	return &AliyunAdapter{
		accessKeyID:     config.AccessKeyID,
		accessKeySecret: config.AccessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
		clients:         make(map[string]*ecs.Client),
	}
}

// GetProvider 获取云厂商类型
func (a *AliyunAdapter) GetProvider() types.CloudProvider {
	return types.ProviderAliyun
}

// ValidateCredentials 验证凭证
func (a *AliyunAdapter) ValidateCredentials(ctx context.Context) error {
	a.logger.Info("验证阿里云凭证")

	_, err := a.GetRegions(ctx)
	if err != nil {
		return fmt.Errorf("阿里云凭证验证失败: %w", err)
	}

	a.logger.Info("阿里云凭证验证成功")
	return nil
}

// GetRegions 获取支持的地域列表
func (a *AliyunAdapter) GetRegions(ctx context.Context) ([]types.Region, error) {
	a.logger.Info("获取阿里云地域列表", elog.String("default_region", a.defaultRegion))

	client, err := a.getClient(a.defaultRegion)
	if err != nil {
		return nil, fmt.Errorf("创建客户端失败: %w", err)
	}

	request := ecs.CreateDescribeRegionsRequest()
	request.Scheme = "https"

	response, err := client.DescribeRegions(request)
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
func (a *AliyunAdapter) GetECSInstances(ctx context.Context, region string) ([]types.ECSInstance, error) {
	a.logger.Info("获取阿里云ECS实例列表", elog.String("region", region))

	client, err := a.getClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建客户端失败: %w", err)
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

		response, err := client.DescribeInstances(request)
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

// getClient 获取或创建指定地域的客户端
func (a *AliyunAdapter) getClient(region string) (*ecs.Client, error) {
	if client, ok := a.clients[region]; ok {
		return client, nil
	}

	credential := credentials.NewAccessKeyCredential(a.accessKeyID, a.accessKeySecret)
	config := sdk.NewConfig()
	config.Scheme = "https"

	client, err := ecs.NewClientWithOptions(region, config, credential)
	if err != nil {
		return nil, fmt.Errorf("创建ECS客户端失败: %w", err)
	}

	a.clients[region] = client
	return client, nil
}

// convertInstance 转换阿里云实例为通用格式
func (a *AliyunAdapter) convertInstance(inst ecs.Instance, region string) types.ECSInstance {
	publicIP := ""
	if len(inst.PublicIpAddress.IpAddress) > 0 {
		publicIP = inst.PublicIpAddress.IpAddress[0]
	}
	if publicIP == "" && len(inst.EipAddress.IpAddress) > 0 {
		publicIP = inst.EipAddress.IpAddress
	}

	privateIP := ""
	if len(inst.VpcAttributes.PrivateIpAddress.IpAddress) > 0 {
		privateIP = inst.VpcAttributes.PrivateIpAddress.IpAddress[0]
	}

	securityGroups := make([]types.SecurityGroup, 0, len(inst.SecurityGroupIds.SecurityGroupId))
	for _, sg := range inst.SecurityGroupIds.SecurityGroupId {
		securityGroups = append(securityGroups, types.SecurityGroup{ID: sg})
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

	return types.ECSInstance{
		InstanceID:              inst.InstanceId,
		InstanceName:            inst.InstanceName,
		Status:                  inst.Status,
		Region:                  region,
		Zone:                    inst.ZoneId,
		InstanceType:            inst.InstanceType,
		InstanceTypeFamily:      instanceTypeFamily,
		CPU:                     inst.Cpu,
		Memory:                  inst.Memory,
		OSType:                  inst.OSType,
		OSName:                  inst.OSName,
		ImageID:                 inst.ImageId,
		PublicIP:                publicIP,
		PrivateIP:               privateIP,
		VPCID:                   inst.VpcAttributes.VpcId,
		VSwitchID:               inst.VpcAttributes.VSwitchId,
		SecurityGroups:          securityGroups,
		InternetMaxBandwidthIn:  inst.InternetMaxBandwidthIn,
		InternetMaxBandwidthOut: inst.InternetMaxBandwidthOut,
		ChargeType:              inst.InstanceChargeType,
		CreationTime:            inst.CreationTime,
		ExpiredTime:             inst.ExpiredTime,
		IoOptimized:             ioOptimized,
		NetworkType:             inst.NetworkType,
		InstanceNetworkType:     inst.InstanceNetworkType,
		Tags:                    tags,
		Description:             inst.Description,
		Provider:                string(types.ProviderAliyun),
		HostName:                inst.HostName,
		KeyPairName:             inst.KeyPairName,
	}
}
