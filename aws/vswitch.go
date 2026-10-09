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

// VSwitchAdapter AWS Subnet适配器（映射为VSwitch）
type VSwitchAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewVSwitchAdapter 创建VSwitch适配器
func NewVSwitchAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *VSwitchAdapter {
	return &VSwitchAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建EC2客户端
func (a *VSwitchAdapter) createClient(ctx context.Context, region string) (*ec2.Client, error) {
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

// ListInstances 获取子网列表
func (a *VSwitchAdapter) ListInstances(ctx context.Context, region string) ([]types.VSwitchInstance, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &ec2.DescribeSubnetsInput{}

	output, err := client.DescribeSubnets(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("获取子网列表失败: %w", err)
	}

	var result []types.VSwitchInstance
	for _, subnet := range output.Subnets {
		result = append(result, a.convertToVSwitchInstance(subnet, region))
	}

	a.logger.Info("获取AWS子网列表成功",
		elog.String("region", region),
		elog.Int("count", len(result)))

	return result, nil
}

// GetInstance 获取单个子网详情
func (a *VSwitchAdapter) GetInstance(ctx context.Context, region, subnetID string) (*types.VSwitchInstance, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &ec2.DescribeSubnetsInput{
		SubnetIds: []string{subnetID},
	}

	output, err := client.DescribeSubnets(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("获取子网详情失败: %w", err)
	}

	if len(output.Subnets) == 0 {
		return nil, fmt.Errorf("子网不存在: %s", subnetID)
	}

	instance := a.convertToVSwitchInstance(output.Subnets[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取子网
func (a *VSwitchAdapter) ListInstancesByIDs(ctx context.Context, region string, subnetIDs []string) ([]types.VSwitchInstance, error) {
	if len(subnetIDs) == 0 {
		return nil, nil
	}

	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &ec2.DescribeSubnetsInput{
		SubnetIds: subnetIDs,
	}

	output, err := client.DescribeSubnets(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("批量获取子网失败: %w", err)
	}

	var result []types.VSwitchInstance
	for _, subnet := range output.Subnets {
		result = append(result, a.convertToVSwitchInstance(subnet, region))
	}

	return result, nil
}

// GetInstanceStatus 获取子网状态
func (a *VSwitchAdapter) GetInstanceStatus(ctx context.Context, region, subnetID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, subnetID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取子网列表
func (a *VSwitchAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.VSwitchInstanceFilter) ([]types.VSwitchInstance, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &ec2.DescribeSubnetsInput{}

	if filter != nil {
		if len(filter.VSwitchIDs) > 0 {
			input.SubnetIds = filter.VSwitchIDs
		}

		var filters []ec2types.Filter
		if filter.VPCID != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("vpc-id"),
				Values: []string{filter.VPCID},
			})
		}
		if filter.Zone != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("availability-zone"),
				Values: []string{filter.Zone},
			})
		}
		if filter.IsDefault != nil {
			val := "false"
			if *filter.IsDefault {
				val = "true"
			}
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("default-for-az"),
				Values: []string{val},
			})
		}
		if len(filters) > 0 {
			input.Filters = filters
		}
	}

	output, err := client.DescribeSubnets(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("获取子网列表失败: %w", err)
	}

	var result []types.VSwitchInstance
	for _, subnet := range output.Subnets {
		result = append(result, a.convertToVSwitchInstance(subnet, region))
	}

	return result, nil
}

// convertToVSwitchInstance 转换为通用VSwitch实例
func (a *VSwitchAdapter) convertToVSwitchInstance(subnet ec2types.Subnet, region string) types.VSwitchInstance {
	subnetID := aws.ToString(subnet.SubnetId)

	// 提取IPv6 CIDR
	var ipv6CidrBlock string
	if len(subnet.Ipv6CidrBlockAssociationSet) > 0 {
		ipv6CidrBlock = aws.ToString(subnet.Ipv6CidrBlockAssociationSet[0].Ipv6CidrBlock)
	}

	// 可用IP数量
	var availableIPCount int64
	if subnet.AvailableIpAddressCount != nil {
		availableIPCount = int64(*subnet.AvailableIpAddressCount)
	}

	// 是否默认子网
	isDefault := false
	if subnet.DefaultForAz != nil {
		isDefault = *subnet.DefaultForAz
	}

	// 提取标签和名称
	tags := make(map[string]string)
	var name string
	for _, tag := range subnet.Tags {
		key := aws.ToString(tag.Key)
		value := aws.ToString(tag.Value)
		tags[key] = value
		if key == "Name" {
			name = value
		}
	}

	return types.VSwitchInstance{
		VSwitchID:        subnetID,
		VSwitchName:      name,
		Status:           types.NormalizeVSwitchStatus("aws", string(subnet.State)),
		Region:           region,
		Zone:             aws.ToString(subnet.AvailabilityZone),
		CidrBlock:        aws.ToString(subnet.CidrBlock),
		IPv6CidrBlock:    ipv6CidrBlock,
		EnableIPv6:       len(subnet.Ipv6CidrBlockAssociationSet) > 0,
		IsDefault:        isDefault,
		VPCID:            aws.ToString(subnet.VpcId),
		AvailableIPCount: availableIPCount,
		Tags:             tags,
		Provider:         "aws",
	}
}
