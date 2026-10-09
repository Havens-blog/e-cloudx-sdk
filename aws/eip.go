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

// EIPAdapter AWS EIP适配器
type EIPAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewEIPAdapter 创建EIP适配器
func NewEIPAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *EIPAdapter {
	return &EIPAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建EC2客户端
func (a *EIPAdapter) createClient(ctx context.Context, region string) (*ec2.Client, error) {
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

// ListInstances 获取EIP列表
func (a *EIPAdapter) ListInstances(ctx context.Context, region string) ([]types.EIPInstance, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &ec2.DescribeAddressesInput{}

	output, err := client.DescribeAddresses(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("获取EIP列表失败: %w", err)
	}

	var allEIPs []types.EIPInstance
	for _, addr := range output.Addresses {
		allEIPs = append(allEIPs, a.convertToEIPInstance(addr, region))
	}

	a.logger.Info("获取AWS EIP列表成功",
		elog.String("region", region),
		elog.Int("count", len(allEIPs)))

	return allEIPs, nil
}

// GetInstance 获取单个EIP详情
func (a *EIPAdapter) GetInstance(ctx context.Context, region, allocationID string) (*types.EIPInstance, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &ec2.DescribeAddressesInput{
		AllocationIds: []string{allocationID},
	}

	output, err := client.DescribeAddresses(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("获取EIP详情失败: %w", err)
	}

	if len(output.Addresses) == 0 {
		return nil, fmt.Errorf("EIP不存在: %s", allocationID)
	}

	instance := a.convertToEIPInstance(output.Addresses[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取EIP
func (a *EIPAdapter) ListInstancesByIDs(ctx context.Context, region string, allocationIDs []string) ([]types.EIPInstance, error) {
	if len(allocationIDs) == 0 {
		return nil, nil
	}

	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &ec2.DescribeAddressesInput{
		AllocationIds: allocationIDs,
	}

	output, err := client.DescribeAddresses(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("批量获取EIP失败: %w", err)
	}

	var result []types.EIPInstance
	for _, addr := range output.Addresses {
		result = append(result, a.convertToEIPInstance(addr, region))
	}

	return result, nil
}

// GetInstanceStatus 获取EIP状态
func (a *EIPAdapter) GetInstanceStatus(ctx context.Context, region, allocationID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, allocationID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取EIP列表
func (a *EIPAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.EIPInstanceFilter) ([]types.EIPInstance, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &ec2.DescribeAddressesInput{}

	if filter != nil {
		if len(filter.AllocationIDs) > 0 {
			input.AllocationIds = filter.AllocationIDs
		}

		var filters []ec2types.Filter
		if len(filter.IPAddresses) > 0 {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("public-ip"),
				Values: filter.IPAddresses,
			})
		}
		if filter.InstanceID != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("instance-id"),
				Values: []string{filter.InstanceID},
			})
		}
		if len(filters) > 0 {
			input.Filters = filters
		}
	}

	output, err := client.DescribeAddresses(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("获取EIP列表失败: %w", err)
	}

	var result []types.EIPInstance
	for _, addr := range output.Addresses {
		result = append(result, a.convertToEIPInstance(addr, region))
	}

	return result, nil
}

// convertToEIPInstance 转换为通用EIP实例
func (a *EIPAdapter) convertToEIPInstance(addr ec2types.Address, region string) types.EIPInstance {
	allocationID := aws.ToString(addr.AllocationId)
	publicIP := aws.ToString(addr.PublicIp)
	instanceID := aws.ToString(addr.InstanceId)
	networkInterfaceID := aws.ToString(addr.NetworkInterfaceId)

	// 确定状态
	status := "Available"
	if instanceID != "" || networkInterfaceID != "" {
		status = "InUse"
	}

	// 确定绑定的实例类型
	// AWS EIP 可以绑定到: EC2 实例、NAT Gateway、Network Interface、Network Load Balancer 等
	instanceType := ""
	if instanceID != "" {
		instanceType = "EcsInstance"
	} else if networkInterfaceID != "" {
		// 通过 NetworkInterfaceOwnerId 判断是否为 ELB/NAT 等托管服务
		ownerID := aws.ToString(addr.NetworkInterfaceOwnerId)
		if ownerID == "amazon-elb" {
			instanceType = "SlbInstance"
		} else if ownerID == "amazon-aws" {
			instanceType = "Nat"
		} else {
			instanceType = "NetworkInterface"
		}
	}

	// 提取标签和名称
	tags := make(map[string]string)
	var name string
	for _, tag := range addr.Tags {
		key := aws.ToString(tag.Key)
		value := aws.ToString(tag.Value)
		tags[key] = value
		if key == "Name" {
			name = value
		}
	}

	return types.EIPInstance{
		AllocationID:     allocationID,
		IPAddress:        publicIP,
		Name:             name,
		Status:           types.NormalizeEIPStatus("aws", status),
		Region:           region,
		Netmode:          "public",
		InstanceID:       instanceID,
		InstanceType:     instanceType,
		PrivateIPAddress: aws.ToString(addr.PrivateIpAddress),
		VPCID:            aws.ToString(addr.NetworkBorderGroup),
		NetworkInterface: networkInterfaceID,
		Tags:             tags,
		Provider:         "aws",
	}
}
