package aliyun

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/gotomicro/ego/core/elog"
)

// ENIAdapter 阿里云弹性网卡适配器
type ENIAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewENIAdapter 创建弹性网卡适配器
func NewENIAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *ENIAdapter {
	return &ENIAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建ECS客户端（ENI属于ECS服务）
func (a *ENIAdapter) createClient(region string) (*ecs.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}
	return ecs.NewClientWithAccessKey(region, a.accessKeyID, a.accessKeySecret)
}

// ListInstances 获取弹性网卡列表
func (a *ENIAdapter) ListInstances(ctx context.Context, region string) ([]types.ENIInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建ECS客户端失败: %w", err)
	}

	var allENIs []types.ENIInstance
	pageNumber := 1
	pageSize := 50

	for {
		request := ecs.CreateDescribeNetworkInterfacesRequest()
		request.RegionId = region
		request.PageNumber = requests.NewInteger(pageNumber)
		request.PageSize = requests.NewInteger(pageSize)

		response, err := client.DescribeNetworkInterfaces(request)
		if err != nil {
			return nil, fmt.Errorf("获取弹性网卡列表失败: %w", err)
		}

		for _, eni := range response.NetworkInterfaceSets.NetworkInterfaceSet {
			allENIs = append(allENIs, a.convertToENIInstance(eni, region))
		}

		if len(response.NetworkInterfaceSets.NetworkInterfaceSet) < pageSize {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取阿里云弹性网卡列表成功",
		elog.String("region", region),
		elog.Int("count", len(allENIs)))

	return allENIs, nil
}

// GetInstance 获取单个弹性网卡详情
func (a *ENIAdapter) GetInstance(ctx context.Context, region, eniID string) (*types.ENIInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建ECS客户端失败: %w", err)
	}

	request := ecs.CreateDescribeNetworkInterfacesRequest()
	request.RegionId = region
	request.NetworkInterfaceId = &[]string{eniID}

	response, err := client.DescribeNetworkInterfaces(request)
	if err != nil {
		return nil, fmt.Errorf("获取弹性网卡详情失败: %w", err)
	}

	if len(response.NetworkInterfaceSets.NetworkInterfaceSet) == 0 {
		return nil, fmt.Errorf("弹性网卡不存在: %s", eniID)
	}

	instance := a.convertToENIInstance(response.NetworkInterfaceSets.NetworkInterfaceSet[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取弹性网卡
func (a *ENIAdapter) ListInstancesByIDs(ctx context.Context, region string, eniIDs []string) ([]types.ENIInstance, error) {
	if len(eniIDs) == 0 {
		return nil, nil
	}

	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建ECS客户端失败: %w", err)
	}

	// 阿里云支持批量查询
	request := ecs.CreateDescribeNetworkInterfacesRequest()
	request.RegionId = region
	request.NetworkInterfaceId = &eniIDs

	response, err := client.DescribeNetworkInterfaces(request)
	if err != nil {
		return nil, fmt.Errorf("批量获取弹性网卡失败: %w", err)
	}

	var result []types.ENIInstance
	for _, eni := range response.NetworkInterfaceSets.NetworkInterfaceSet {
		result = append(result, a.convertToENIInstance(eni, region))
	}

	return result, nil
}

// GetInstanceStatus 获取弹性网卡状态
func (a *ENIAdapter) GetInstanceStatus(ctx context.Context, region, eniID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, eniID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取弹性网卡列表
func (a *ENIAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.ENIInstanceFilter) ([]types.ENIInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建ECS客户端失败: %w", err)
	}

	request := ecs.CreateDescribeNetworkInterfacesRequest()
	request.RegionId = region

	if filter != nil {
		if len(filter.ENIIDs) > 0 {
			request.NetworkInterfaceId = &filter.ENIIDs
		}
		if filter.ENIName != "" {
			request.NetworkInterfaceName = filter.ENIName
		}
		if len(filter.Status) > 0 {
			request.Status = filter.Status[0]
		}
		if filter.Type != "" {
			request.Type = filter.Type
		}
		if filter.VPCID != "" {
			request.VpcId = filter.VPCID
		}
		if filter.SubnetID != "" {
			request.VSwitchId = filter.SubnetID
		}
		if filter.InstanceID != "" {
			request.InstanceId = filter.InstanceID
		}
		if filter.PrimaryPrivateIP != "" {
			request.PrimaryIpAddress = filter.PrimaryPrivateIP
		}
		if filter.SecurityGroupID != "" {
			request.SecurityGroupId = filter.SecurityGroupID
		}
		if filter.PageNumber > 0 {
			request.PageNumber = requests.NewInteger(filter.PageNumber)
		}
		if filter.PageSize > 0 {
			request.PageSize = requests.NewInteger(filter.PageSize)
		}
	}

	response, err := client.DescribeNetworkInterfaces(request)
	if err != nil {
		return nil, fmt.Errorf("获取弹性网卡列表失败: %w", err)
	}

	var result []types.ENIInstance
	for _, eni := range response.NetworkInterfaceSets.NetworkInterfaceSet {
		result = append(result, a.convertToENIInstance(eni, region))
	}

	return result, nil
}

// ListByInstanceID 获取实例绑定的弹性网卡
func (a *ENIAdapter) ListByInstanceID(ctx context.Context, region, instanceID string) ([]types.ENIInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, &types.ENIInstanceFilter{
		InstanceID: instanceID,
	})
}

// convertToENIInstance 转换为通用弹性网卡实例
func (a *ENIAdapter) convertToENIInstance(eni ecs.NetworkInterfaceSet, region string) types.ENIInstance {
	// 提取私网IP列表
	var privateIPs []string
	for _, ip := range eni.PrivateIpSets.PrivateIpSet {
		privateIPs = append(privateIPs, ip.PrivateIpAddress)
	}

	// 提取IPv6地址列表
	var ipv6Addrs []string
	for _, ipv6 := range eni.Ipv6Sets.Ipv6Set {
		ipv6Addrs = append(ipv6Addrs, ipv6.Ipv6Address)
	}

	// 提取安全组ID列表
	securityGroupIDs := eni.SecurityGroupIds.SecurityGroupId

	// 提取标签
	tags := make(map[string]string)
	for _, tag := range eni.Tags.Tag {
		tags[tag.TagKey] = tag.TagValue
	}

	// 提取关联的EIP
	var eipAddresses []string
	if eni.AssociatedPublicIp.PublicIpAddress != "" {
		eipAddresses = append(eipAddresses, eni.AssociatedPublicIp.PublicIpAddress)
	}

	return types.ENIInstance{
		ENIID:              eni.NetworkInterfaceId,
		ENIName:            eni.NetworkInterfaceName,
		Description:        eni.Description,
		Status:             types.NormalizeENIStatus("aliyun", eni.Status),
		Type:               eni.Type,
		Region:             region,
		Zone:               eni.ZoneId,
		VPCID:              eni.VpcId,
		SubnetID:           eni.VSwitchId,
		PrimaryPrivateIP:   eni.PrivateIpAddress,
		PrivateIPAddresses: privateIPs,
		MacAddress:         eni.MacAddress,
		IPv6Addresses:      ipv6Addrs,
		InstanceID:         eni.InstanceId,
		SecurityGroupIDs:   securityGroupIDs,
		PublicIP:           eni.AssociatedPublicIp.PublicIpAddress,
		EIPAddresses:       eipAddresses,
		ResourceGroupID:    eni.ResourceGroupId,
		CreationTime:       eni.CreationTime,
		Tags:               tags,
		Provider:           "aliyun",
	}
}
