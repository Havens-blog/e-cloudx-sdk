package aws

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/gotomicro/ego/core/elog"
)

// ENIAdapter AWS弹性网卡适配器
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

// createClient 创建EC2客户端
func (a *ENIAdapter) createClient(ctx context.Context, region string) (*ec2.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			a.accessKeyID, a.accessKeySecret, "",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("加载AWS配置失败: %w", err)
	}
	return ec2.NewFromConfig(cfg), nil
}

// ListInstances 获取弹性网卡列表
func (a *ENIAdapter) ListInstances(ctx context.Context, region string) ([]types.ENIInstance, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	var allENIs []types.ENIInstance
	var nextToken *string

	for {
		input := &ec2.DescribeNetworkInterfacesInput{
			MaxResults: aws.Int32(100),
			NextToken:  nextToken,
		}

		output, err := client.DescribeNetworkInterfaces(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("获取弹性网卡列表失败: %w", err)
		}

		for _, eni := range output.NetworkInterfaces {
			allENIs = append(allENIs, a.convertToENIInstance(eni, region))
		}

		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	a.logger.Info("获取AWS弹性网卡列表成功",
		elog.String("region", region),
		elog.Int("count", len(allENIs)))

	return allENIs, nil
}

// GetInstance 获取单个弹性网卡详情
func (a *ENIAdapter) GetInstance(ctx context.Context, region, eniID string) (*types.ENIInstance, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []string{eniID},
	}

	output, err := client.DescribeNetworkInterfaces(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("获取弹性网卡详情失败: %w", err)
	}

	if len(output.NetworkInterfaces) == 0 {
		return nil, fmt.Errorf("弹性网卡不存在: %s", eniID)
	}

	instance := a.convertToENIInstance(output.NetworkInterfaces[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取弹性网卡
func (a *ENIAdapter) ListInstancesByIDs(ctx context.Context, region string, eniIDs []string) ([]types.ENIInstance, error) {
	if len(eniIDs) == 0 {
		return nil, nil
	}

	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: eniIDs,
	}

	output, err := client.DescribeNetworkInterfaces(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("批量获取弹性网卡失败: %w", err)
	}

	var result []types.ENIInstance
	for _, eni := range output.NetworkInterfaces {
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
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &ec2.DescribeNetworkInterfacesInput{}

	if filter != nil {
		if len(filter.ENIIDs) > 0 {
			input.NetworkInterfaceIds = filter.ENIIDs
		}

		var filters []ec2types.Filter
		if filter.VPCID != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("vpc-id"),
				Values: []string{filter.VPCID},
			})
		}
		if filter.SubnetID != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("subnet-id"),
				Values: []string{filter.SubnetID},
			})
		}
		if filter.InstanceID != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("attachment.instance-id"),
				Values: []string{filter.InstanceID},
			})
		}
		if len(filter.Status) > 0 {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("status"),
				Values: filter.Status,
			})
		}
		if filter.PrimaryPrivateIP != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("private-ip-address"),
				Values: []string{filter.PrimaryPrivateIP},
			})
		}
		if filter.SecurityGroupID != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("group-id"),
				Values: []string{filter.SecurityGroupID},
			})
		}
		if len(filters) > 0 {
			input.Filters = filters
		}
		if filter.PageSize > 0 {
			input.MaxResults = aws.Int32(int32(filter.PageSize))
		}
	}

	output, err := client.DescribeNetworkInterfaces(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("获取弹性网卡列表失败: %w", err)
	}

	var result []types.ENIInstance
	for _, eni := range output.NetworkInterfaces {
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
func (a *ENIAdapter) convertToENIInstance(eni ec2types.NetworkInterface, region string) types.ENIInstance {
	eniID := aws.ToString(eni.NetworkInterfaceId)
	description := aws.ToString(eni.Description)
	status := string(eni.Status)
	vpcID := aws.ToString(eni.VpcId)
	subnetID := aws.ToString(eni.SubnetId)
	primaryIP := aws.ToString(eni.PrivateIpAddress)
	macAddress := aws.ToString(eni.MacAddress)
	zone := aws.ToString(eni.AvailabilityZone)

	// 确定网卡类型
	eniType := types.ENITypeSecondary
	if eni.Attachment != nil && eni.Attachment.DeviceIndex != nil && *eni.Attachment.DeviceIndex == 0 {
		eniType = types.ENITypePrimary
	}

	// 提取实例ID和设备索引
	instanceID := ""
	deviceIndex := 0
	if eni.Attachment != nil {
		instanceID = aws.ToString(eni.Attachment.InstanceId)
		if eni.Attachment.DeviceIndex != nil {
			deviceIndex = int(*eni.Attachment.DeviceIndex)
		}
	}

	// 提取私网IP列表
	var privateIPs []string
	for _, ip := range eni.PrivateIpAddresses {
		if ip.PrivateIpAddress != nil {
			privateIPs = append(privateIPs, *ip.PrivateIpAddress)
		}
	}

	// 提取IPv6地址列表
	var ipv6Addrs []string
	for _, ipv6 := range eni.Ipv6Addresses {
		if ipv6.Ipv6Address != nil {
			ipv6Addrs = append(ipv6Addrs, *ipv6.Ipv6Address)
		}
	}

	// 提取安全组ID列表
	var securityGroupIDs []string
	for _, sg := range eni.Groups {
		if sg.GroupId != nil {
			securityGroupIDs = append(securityGroupIDs, *sg.GroupId)
		}
	}

	// 提取关联的公网IP (EIP)
	var publicIP string
	var eipAddresses []string
	if eni.Association != nil {
		publicIP = aws.ToString(eni.Association.PublicIp)
		if publicIP != "" {
			eipAddresses = append(eipAddresses, publicIP)
		}
	}

	// 提取标签和名称
	tags := make(map[string]string)
	var name string
	for _, tag := range eni.TagSet {
		key := aws.ToString(tag.Key)
		value := aws.ToString(tag.Value)
		tags[key] = value
		if key == "Name" {
			name = value
		}
	}

	return types.ENIInstance{
		ENIID:              eniID,
		ENIName:            name,
		Description:        description,
		Status:             types.NormalizeENIStatus("aws", status),
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
		Tags:               tags,
		Provider:           "aws",
	}
}
