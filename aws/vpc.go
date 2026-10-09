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

// VPCAdapter AWS VPC适配器
type VPCAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewVPCAdapter 创建VPC适配器
func NewVPCAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *VPCAdapter {
	return &VPCAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建EC2客户端
func (a *VPCAdapter) createClient(ctx context.Context, region string) (*ec2.Client, error) {
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

// ListInstances 获取VPC列表
func (a *VPCAdapter) ListInstances(ctx context.Context, region string) ([]types.VPCInstance, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	var allVPCs []types.VPCInstance
	var nextToken *string

	for {
		input := &ec2.DescribeVpcsInput{
			NextToken: nextToken,
		}

		output, err := client.DescribeVpcs(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("获取VPC列表失败: %w", err)
		}

		for _, v := range output.Vpcs {
			allVPCs = append(allVPCs, a.convertToVPCInstance(v, region))
		}

		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	a.logger.Info("获取AWS VPC列表成功",
		elog.String("region", region),
		elog.Int("count", len(allVPCs)))

	return allVPCs, nil
}

// GetInstance 获取单个VPC详情
func (a *VPCAdapter) GetInstance(ctx context.Context, region, vpcID string) (*types.VPCInstance, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &ec2.DescribeVpcsInput{
		VpcIds: []string{vpcID},
	}

	output, err := client.DescribeVpcs(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("获取VPC详情失败: %w", err)
	}

	if len(output.Vpcs) == 0 {
		return nil, fmt.Errorf("VPC不存在: %s", vpcID)
	}

	instance := a.convertToVPCInstance(output.Vpcs[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取VPC
func (a *VPCAdapter) ListInstancesByIDs(ctx context.Context, region string, vpcIDs []string) ([]types.VPCInstance, error) {
	if len(vpcIDs) == 0 {
		return nil, nil
	}

	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &ec2.DescribeVpcsInput{
		VpcIds: vpcIDs,
	}

	output, err := client.DescribeVpcs(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("批量获取VPC失败: %w", err)
	}

	var result []types.VPCInstance
	for _, v := range output.Vpcs {
		result = append(result, a.convertToVPCInstance(v, region))
	}

	return result, nil
}

// GetInstanceStatus 获取VPC状态
func (a *VPCAdapter) GetInstanceStatus(ctx context.Context, region, vpcID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, vpcID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取VPC列表
func (a *VPCAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.VPCInstanceFilter) ([]types.VPCInstance, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &ec2.DescribeVpcsInput{}

	if filter != nil {
		if len(filter.VPCIDs) > 0 {
			input.VpcIds = filter.VPCIDs
		}

		var filters []ec2types.Filter
		if filter.CidrBlock != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("cidr-block"),
				Values: []string{filter.CidrBlock},
			})
		}
		if filter.IsDefault != nil {
			val := "false"
			if *filter.IsDefault {
				val = "true"
			}
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("is-default"),
				Values: []string{val},
			})
		}
		if len(filters) > 0 {
			input.Filters = filters
		}
	}

	output, err := client.DescribeVpcs(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("获取VPC列表失败: %w", err)
	}

	var result []types.VPCInstance
	for _, v := range output.Vpcs {
		result = append(result, a.convertToVPCInstance(v, region))
	}

	return result, nil
}

// convertToVPCInstance 转换为通用VPC实例
func (a *VPCAdapter) convertToVPCInstance(v ec2types.Vpc, region string) types.VPCInstance {
	vpcID := aws.ToString(v.VpcId)
	cidrBlock := aws.ToString(v.CidrBlock)

	// 提取附加CIDR
	var secondaryCidrs []string
	for _, assoc := range v.CidrBlockAssociationSet {
		cidr := aws.ToString(assoc.CidrBlock)
		if cidr != cidrBlock {
			secondaryCidrs = append(secondaryCidrs, cidr)
		}
	}

	// 提取IPv6 CIDR
	var ipv6Cidr string
	if len(v.Ipv6CidrBlockAssociationSet) > 0 {
		ipv6Cidr = aws.ToString(v.Ipv6CidrBlockAssociationSet[0].Ipv6CidrBlock)
	}

	// 提取标签和名称
	tags := make(map[string]string)
	var vpcName string
	for _, tag := range v.Tags {
		key := aws.ToString(tag.Key)
		value := aws.ToString(tag.Value)
		tags[key] = value
		if key == "Name" {
			vpcName = value
		}
	}

	// 状态转换
	status := string(v.State)

	return types.VPCInstance{
		VPCID:          vpcID,
		VPCName:        vpcName,
		Status:         types.NormalizeVPCStatus("aws", status),
		Region:         region,
		CidrBlock:      cidrBlock,
		SecondaryCidrs: secondaryCidrs,
		IPv6CidrBlock:  ipv6Cidr,
		EnableIPv6:     ipv6Cidr != "",
		IsDefault:      aws.ToBool(v.IsDefault),
		DhcpOptionsID:  aws.ToString(v.DhcpOptionsId),
		Tags:           tags,
		Provider:       "aws",
	}
}
