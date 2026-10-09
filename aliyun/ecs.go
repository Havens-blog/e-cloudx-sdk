package aliyun

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/gotomicro/ego/core/elog"
)

// ECSAdapter 阿里云ECS适配器
type ECSAdapter struct {
	client *Client
	logger *elog.Component
}

// NewECSAdapter 创建阿里云ECS适配器
func NewECSAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *ECSAdapter {
	return &ECSAdapter{
		client: NewClient(accessKeyID, accessKeySecret, defaultRegion, logger),
		logger: logger,
	}
}

// GetRegions 获取支持的地域列表
func (a *ECSAdapter) GetRegions(ctx context.Context) ([]types.Region, error) {
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

	if filter != nil && filter.PageSize > 0 {
		pageSize = filter.PageSize
	}
	if filter != nil && filter.PageNumber > 0 {
		pageNumber = filter.PageNumber
	}

	for {
		request := ecs.CreateDescribeInstancesRequest()
		request.Scheme = "https"
		request.RegionId = region
		request.PageNumber = requests.NewInteger(pageNumber)
		request.PageSize = requests.NewInteger(pageSize)

		// 应用过滤条件
		if filter != nil {
			if len(filter.InstanceIDs) > 0 {
				request.InstanceIds = fmt.Sprintf(`["%s"]`, joinStrings(filter.InstanceIDs, `","`))
			}
			if filter.InstanceName != "" {
				request.InstanceName = filter.InstanceName
			}
			if len(filter.Status) > 0 {
				request.Status = filter.Status[0]
			}
			if filter.VPCID != "" {
				request.VpcId = filter.VPCID
			}
			if filter.Zone != "" {
				request.ZoneId = filter.Zone
			}
			if len(filter.Tags) > 0 {
				var tags []ecs.DescribeInstancesTag
				for k, v := range filter.Tags {
					tags = append(tags, ecs.DescribeInstancesTag{Key: k, Value: v})
				}
				request.Tag = &tags
			}
		}

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
			instance := convertAliyunInstance(inst, region)
			allInstances = append(allInstances, instance)
		}

		// 如果指定了分页，只返回一页
		if filter != nil && filter.PageNumber > 0 {
			break
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

// convertAliyunInstance 转换阿里云实例为通用格式
func convertAliyunInstance(inst ecs.Instance, region string) types.ECSInstance {
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

	securityGroups := make([]types.SecurityGroup, 0, len(inst.SecurityGroupIds.SecurityGroupId))
	for _, sgID := range inst.SecurityGroupIds.SecurityGroupId {
		securityGroups = append(securityGroups, types.SecurityGroup{ID: sgID})
	}

	tags := make(map[string]string)
	for _, tag := range inst.Tags.Tag {
		tags[tag.TagKey] = tag.TagValue
	}

	instanceTypeFamily := extractInstanceTypeFamily(inst.InstanceType, "ecs.")

	ioOptimized := "none"
	if inst.IoOptimized {
		ioOptimized = "optimized"
	}

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
		ProjectID:               inst.ResourceGroupId,
		Tags:                    tags,
		Description:             inst.Description,
		Provider:                string(types.ProviderAliyun),
		HostName:                inst.HostName,
		KeyPairName:             inst.KeyPairName,
	}
}

// joinStrings 连接字符串
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}

// extractInstanceTypeFamily 提取实例类型族
func extractInstanceTypeFamily(instanceType, prefix string) string {
	if len(instanceType) <= len(prefix) {
		return ""
	}
	if instanceType[:len(prefix)] != prefix {
		return ""
	}
	remaining := instanceType[len(prefix):]
	for i, c := range remaining {
		if c == '.' {
			return remaining[:i]
		}
	}
	return ""
}
