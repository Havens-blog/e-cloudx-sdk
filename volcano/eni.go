package volcano

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/vpc"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// ENIAdapter 火山引擎弹性网卡适配器
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

// createClient 创建VPC客户端（ENI属于VPC服务）
func (a *ENIAdapter) createClient(region string) (*vpc.VPC, error) {
	if region == "" {
		region = a.defaultRegion
	}

	config := volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(a.accessKeyID, a.accessKeySecret, "")).
		WithRegion(region)

	sess, err := session.NewSession(config)
	if err != nil {
		return nil, fmt.Errorf("创建会话失败: %w", err)
	}

	return vpc.New(sess), nil
}

// ListInstances 获取弹性网卡列表
func (a *ENIAdapter) ListInstances(ctx context.Context, region string) ([]types.ENIInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	var allENIs []types.ENIInstance
	pageNumber := int64(1)
	pageSize := int64(100)

	for {
		input := &vpc.DescribeNetworkInterfacesInput{
			PageNumber: &pageNumber,
			PageSize:   &pageSize,
		}

		output, err := client.DescribeNetworkInterfaces(input)
		if err != nil {
			return nil, fmt.Errorf("获取弹性网卡列表失败: %w", err)
		}

		if len(output.NetworkInterfaceSets) == 0 {
			break
		}

		for _, eni := range output.NetworkInterfaceSets {
			allENIs = append(allENIs, a.convertToENIInstance(eni, region))
		}

		if len(output.NetworkInterfaceSets) < int(pageSize) {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取火山引擎弹性网卡列表成功",
		elog.String("region", region),
		elog.Int("count", len(allENIs)))

	return allENIs, nil
}

// GetInstance 获取单个弹性网卡详情
func (a *ENIAdapter) GetInstance(ctx context.Context, region, eniID string) (*types.ENIInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	input := &vpc.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []*string{volcengine.String(eniID)},
	}

	output, err := client.DescribeNetworkInterfaces(input)
	if err != nil {
		return nil, fmt.Errorf("获取弹性网卡详情失败: %w", err)
	}

	if len(output.NetworkInterfaceSets) == 0 {
		return nil, fmt.Errorf("弹性网卡不存在: %s", eniID)
	}

	instance := a.convertToENIInstance(output.NetworkInterfaceSets[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取弹性网卡
func (a *ENIAdapter) ListInstancesByIDs(ctx context.Context, region string, eniIDs []string) ([]types.ENIInstance, error) {
	if len(eniIDs) == 0 {
		return nil, nil
	}

	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	idPtrs := make([]*string, len(eniIDs))
	for i, id := range eniIDs {
		idPtrs[i] = volcengine.String(id)
	}

	input := &vpc.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: idPtrs,
	}

	output, err := client.DescribeNetworkInterfaces(input)
	if err != nil {
		return nil, fmt.Errorf("批量获取弹性网卡失败: %w", err)
	}

	var result []types.ENIInstance
	for _, eni := range output.NetworkInterfaceSets {
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
		return nil, err
	}

	input := &vpc.DescribeNetworkInterfacesInput{}

	if filter != nil {
		if len(filter.ENIIDs) > 0 {
			idPtrs := make([]*string, len(filter.ENIIDs))
			for i, id := range filter.ENIIDs {
				idPtrs[i] = volcengine.String(id)
			}
			input.NetworkInterfaceIds = idPtrs
		}
		if filter.ENIName != "" {
			input.NetworkInterfaceName = volcengine.String(filter.ENIName)
		}
		if len(filter.Status) > 0 {
			input.Status = volcengine.String(filter.Status[0])
		}
		if filter.Type != "" {
			input.Type = volcengine.String(filter.Type)
		}
		if filter.VPCID != "" {
			input.VpcId = volcengine.String(filter.VPCID)
		}
		if filter.SubnetID != "" {
			input.SubnetId = volcengine.String(filter.SubnetID)
		}
		if filter.InstanceID != "" {
			input.InstanceId = volcengine.String(filter.InstanceID)
		}
		if filter.PrimaryPrivateIP != "" {
			input.PrimaryIpAddresses = []*string{volcengine.String(filter.PrimaryPrivateIP)}
		}
		if filter.SecurityGroupID != "" {
			input.SecurityGroupId = volcengine.String(filter.SecurityGroupID)
		}
		if filter.PageSize > 0 {
			pageSize := int64(filter.PageSize)
			input.PageSize = &pageSize
		}
	}

	output, err := client.DescribeNetworkInterfaces(input)
	if err != nil {
		return nil, fmt.Errorf("获取弹性网卡列表失败: %w", err)
	}

	var result []types.ENIInstance
	for _, eni := range output.NetworkInterfaceSets {
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
func (a *ENIAdapter) convertToENIInstance(eni *vpc.NetworkInterfaceSetForDescribeNetworkInterfacesOutput, region string) types.ENIInstance {
	eniID := ""
	if eni.NetworkInterfaceId != nil {
		eniID = *eni.NetworkInterfaceId
	}

	name := ""
	if eni.NetworkInterfaceName != nil {
		name = *eni.NetworkInterfaceName
	}

	description := ""
	if eni.Description != nil {
		description = *eni.Description
	}

	status := ""
	if eni.Status != nil {
		status = *eni.Status
	}

	eniType := types.ENITypeSecondary
	if eni.Type != nil {
		eniType = *eni.Type
	}

	vpcID := ""
	if eni.VpcId != nil {
		vpcID = *eni.VpcId
	}

	subnetID := ""
	if eni.SubnetId != nil {
		subnetID = *eni.SubnetId
	}

	primaryIP := ""
	if eni.PrimaryIpAddress != nil {
		primaryIP = *eni.PrimaryIpAddress
	}

	macAddress := ""
	if eni.MacAddress != nil {
		macAddress = *eni.MacAddress
	}

	zone := ""
	if eni.ZoneId != nil {
		zone = *eni.ZoneId
	}

	instanceID := ""
	deviceIndex := 0
	if eni.DeviceId != nil {
		instanceID = *eni.DeviceId
	}

	// 提取私网IP列表
	var privateIPs []string
	if eni.PrivateIpSets != nil {
		for _, ip := range eni.PrivateIpSets.PrivateIpSet {
			if ip.PrivateIpAddress != nil {
				privateIPs = append(privateIPs, *ip.PrivateIpAddress)
			}
		}
	}

	// 提取IPv6地址列表
	var ipv6Addrs []string
	if eni.IPv6Sets != nil {
		for _, ipv6 := range eni.IPv6Sets {
			if ipv6 != nil {
				ipv6Addrs = append(ipv6Addrs, *ipv6)
			}
		}
	}

	// 提取安全组ID列表
	var securityGroupIDs []string
	if eni.SecurityGroupIds != nil {
		for _, sg := range eni.SecurityGroupIds {
			if sg != nil {
				securityGroupIDs = append(securityGroupIDs, *sg)
			}
		}
	}

	// 提取关联的EIP
	var publicIP string
	var eipAddresses []string
	if eni.AssociatedElasticIp != nil && eni.AssociatedElasticIp.EipAddress != nil {
		publicIP = *eni.AssociatedElasticIp.EipAddress
		eipAddresses = append(eipAddresses, publicIP)
	}

	// 提取标签
	tags := make(map[string]string)
	if eni.Tags != nil {
		for _, tag := range eni.Tags {
			if tag.Key != nil && tag.Value != nil {
				tags[*tag.Key] = *tag.Value
			}
		}
	}

	creationTime := ""
	if eni.CreatedAt != nil {
		creationTime = *eni.CreatedAt
	}

	return types.ENIInstance{
		ENIID:              eniID,
		ENIName:            name,
		Description:        description,
		Status:             types.NormalizeENIStatus("volcano", status),
		Type:               eniType,
		Region:             region,
		Zone:               zone,
		VPCID:              vpcID,
		SubnetID:           subnetID,
		PrimaryPrivateIP:   primaryIP,
		PrivateIPAddresses: privateIPs,
		MacAddress:         macAddress,
		IPv6Addresses:      ipv6Addrs,
		InstanceID:         instanceID,
		DeviceIndex:        deviceIndex,
		SecurityGroupIDs:   securityGroupIDs,
		PublicIP:           publicIP,
		EIPAddresses:       eipAddresses,
		CreationTime:       creationTime,
		Tags:               tags,
		Provider:           "volcano",
	}
}
