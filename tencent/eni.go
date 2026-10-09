package tencent

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	vpc "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/vpc/v20170312"
)

// ENIAdapter 腾讯云弹性网卡适配器
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
func (a *ENIAdapter) createClient(region string) (*vpc.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}

	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "vpc.tencentcloudapi.com"

	return vpc.NewClient(credential, region, cpf)
}

// ListInstances 获取弹性网卡列表
func (a *ENIAdapter) ListInstances(ctx context.Context, region string) ([]types.ENIInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	var allENIs []types.ENIInstance
	offset := uint64(0)
	limit := uint64(100)

	for {
		request := vpc.NewDescribeNetworkInterfacesRequest()
		request.Offset = &offset
		request.Limit = &limit

		response, err := client.DescribeNetworkInterfaces(request)
		if err != nil {
			return nil, fmt.Errorf("获取弹性网卡列表失败: %w", err)
		}

		if response.Response.NetworkInterfaceSet == nil {
			break
		}

		for _, eni := range response.Response.NetworkInterfaceSet {
			allENIs = append(allENIs, a.convertToENIInstance(eni, region))
		}

		if len(response.Response.NetworkInterfaceSet) < int(limit) {
			break
		}
		offset += limit
	}

	a.logger.Info("获取腾讯云弹性网卡列表成功",
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

	request := vpc.NewDescribeNetworkInterfacesRequest()
	request.NetworkInterfaceIds = common.StringPtrs([]string{eniID})

	response, err := client.DescribeNetworkInterfaces(request)
	if err != nil {
		return nil, fmt.Errorf("获取弹性网卡详情失败: %w", err)
	}

	if response.Response.NetworkInterfaceSet == nil || len(response.Response.NetworkInterfaceSet) == 0 {
		return nil, fmt.Errorf("弹性网卡不存在: %s", eniID)
	}

	instance := a.convertToENIInstance(response.Response.NetworkInterfaceSet[0], region)
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

	request := vpc.NewDescribeNetworkInterfacesRequest()
	request.NetworkInterfaceIds = common.StringPtrs(eniIDs)

	response, err := client.DescribeNetworkInterfaces(request)
	if err != nil {
		return nil, fmt.Errorf("批量获取弹性网卡失败: %w", err)
	}

	var result []types.ENIInstance
	if response.Response.NetworkInterfaceSet != nil {
		for _, eni := range response.Response.NetworkInterfaceSet {
			result = append(result, a.convertToENIInstance(eni, region))
		}
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

	request := vpc.NewDescribeNetworkInterfacesRequest()

	if filter != nil {
		if len(filter.ENIIDs) > 0 {
			request.NetworkInterfaceIds = common.StringPtrs(filter.ENIIDs)
		}

		var filters []*vpc.Filter
		if filter.VPCID != "" {
			filters = append(filters, &vpc.Filter{
				Name:   common.StringPtr("vpc-id"),
				Values: common.StringPtrs([]string{filter.VPCID}),
			})
		}
		if filter.SubnetID != "" {
			filters = append(filters, &vpc.Filter{
				Name:   common.StringPtr("subnet-id"),
				Values: common.StringPtrs([]string{filter.SubnetID}),
			})
		}
		if filter.InstanceID != "" {
			filters = append(filters, &vpc.Filter{
				Name:   common.StringPtr("bindedinstance-id"),
				Values: common.StringPtrs([]string{filter.InstanceID}),
			})
		}
		if filter.SecurityGroupID != "" {
			filters = append(filters, &vpc.Filter{
				Name:   common.StringPtr("bindedsg-id"),
				Values: common.StringPtrs([]string{filter.SecurityGroupID}),
			})
		}
		if len(filters) > 0 {
			request.Filters = filters
		}
		if filter.PageSize > 0 {
			limit := uint64(filter.PageSize)
			request.Limit = &limit
		}
	}

	response, err := client.DescribeNetworkInterfaces(request)
	if err != nil {
		return nil, fmt.Errorf("获取弹性网卡列表失败: %w", err)
	}

	var result []types.ENIInstance
	if response.Response.NetworkInterfaceSet != nil {
		for _, eni := range response.Response.NetworkInterfaceSet {
			result = append(result, a.convertToENIInstance(eni, region))
		}
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
func (a *ENIAdapter) convertToENIInstance(eni *vpc.NetworkInterface, region string) types.ENIInstance {
	eniID := ""
	if eni.NetworkInterfaceId != nil {
		eniID = *eni.NetworkInterfaceId
	}

	name := ""
	if eni.NetworkInterfaceName != nil {
		name = *eni.NetworkInterfaceName
	}

	description := ""
	if eni.NetworkInterfaceDescription != nil {
		description = *eni.NetworkInterfaceDescription
	}

	status := ""
	if eni.State != nil {
		status = *eni.State
	}

	vpcID := ""
	if eni.VpcId != nil {
		vpcID = *eni.VpcId
	}

	subnetID := ""
	if eni.SubnetId != nil {
		subnetID = *eni.SubnetId
	}

	zone := ""
	if eni.Zone != nil {
		zone = *eni.Zone
	}

	macAddress := ""
	if eni.MacAddress != nil {
		macAddress = *eni.MacAddress
	}

	// 确定网卡类型
	eniType := types.ENITypeSecondary
	if eni.Primary != nil && *eni.Primary {
		eniType = types.ENITypePrimary
	}

	// 提取实例ID
	instanceID := ""
	if eni.Attachment != nil && eni.Attachment.InstanceId != nil {
		instanceID = *eni.Attachment.InstanceId
	}

	deviceIndex := 0
	if eni.Attachment != nil && eni.Attachment.DeviceIndex != nil {
		deviceIndex = int(*eni.Attachment.DeviceIndex)
	}

	// 提取私网IP列表
	var privateIPs []string
	var primaryIP string
	if eni.PrivateIpAddressSet != nil {
		for _, ip := range eni.PrivateIpAddressSet {
			if ip.PrivateIpAddress != nil {
				privateIPs = append(privateIPs, *ip.PrivateIpAddress)
				if ip.Primary != nil && *ip.Primary {
					primaryIP = *ip.PrivateIpAddress
				}
			}
		}
	}

	// 提取IPv6地址列表
	var ipv6Addrs []string
	if eni.Ipv6AddressSet != nil {
		for _, ipv6 := range eni.Ipv6AddressSet {
			if ipv6.Address != nil {
				ipv6Addrs = append(ipv6Addrs, *ipv6.Address)
			}
		}
	}

	// 提取安全组ID列表
	var securityGroupIDs []string
	if eni.GroupSet != nil {
		for _, sg := range eni.GroupSet {
			if sg != nil {
				securityGroupIDs = append(securityGroupIDs, *sg)
			}
		}
	}

	// 提取关联的EIP
	var publicIP string
	var eipAddresses []string
	if eni.PrivateIpAddressSet != nil {
		for _, ip := range eni.PrivateIpAddressSet {
			if ip.PublicIpAddress != nil && *ip.PublicIpAddress != "" {
				eipAddresses = append(eipAddresses, *ip.PublicIpAddress)
				if publicIP == "" {
					publicIP = *ip.PublicIpAddress
				}
			}
		}
	}

	// 提取标签
	tags := make(map[string]string)
	if eni.TagSet != nil {
		for _, tag := range eni.TagSet {
			if tag.Key != nil && tag.Value != nil {
				tags[*tag.Key] = *tag.Value
			}
		}
	}

	creationTime := ""
	if eni.CreatedTime != nil {
		creationTime = *eni.CreatedTime
	}

	return types.ENIInstance{
		ENIID:              eniID,
		ENIName:            name,
		Description:        description,
		Status:             types.NormalizeENIStatus("tencent", status),
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
		Provider:           "tencent",
	}
}
