package aliyun

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	vpc "github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"github.com/gotomicro/ego/core/elog"
)

// VPCAdapter 阿里云VPC适配器
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

// createClient 创建VPC客户端
func (a *VPCAdapter) createClient(region string) (*vpc.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}
	return vpc.NewClientWithAccessKey(region, a.accessKeyID, a.accessKeySecret)
}

// ListInstances 获取VPC列表
func (a *VPCAdapter) ListInstances(ctx context.Context, region string) ([]types.VPCInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建VPC客户端失败: %w", err)
	}

	var allVPCs []types.VPCInstance
	pageNumber := 1
	pageSize := 50

	for {
		request := vpc.CreateDescribeVpcsRequest()
		request.RegionId = region
		request.PageNumber = requests.NewInteger(pageNumber)
		request.PageSize = requests.NewInteger(pageSize)

		response, err := client.DescribeVpcs(request)
		if err != nil {
			return nil, fmt.Errorf("获取VPC列表失败: %w", err)
		}

		for _, v := range response.Vpcs.Vpc {
			allVPCs = append(allVPCs, a.convertToVPCInstance(v, region))
		}

		if len(response.Vpcs.Vpc) < pageSize {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取阿里云VPC列表成功",
		elog.String("region", region),
		elog.Int("count", len(allVPCs)))

	return allVPCs, nil
}

// GetInstance 获取单个VPC详情
func (a *VPCAdapter) GetInstance(ctx context.Context, region, vpcID string) (*types.VPCInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建VPC客户端失败: %w", err)
	}

	request := vpc.CreateDescribeVpcsRequest()
	request.RegionId = region
	request.VpcId = vpcID

	response, err := client.DescribeVpcs(request)
	if err != nil {
		return nil, fmt.Errorf("获取VPC详情失败: %w", err)
	}

	if len(response.Vpcs.Vpc) == 0 {
		return nil, fmt.Errorf("VPC不存在: %s", vpcID)
	}

	instance := a.convertToVPCInstance(response.Vpcs.Vpc[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取VPC
func (a *VPCAdapter) ListInstancesByIDs(ctx context.Context, region string, vpcIDs []string) ([]types.VPCInstance, error) {
	var result []types.VPCInstance
	for _, vpcID := range vpcIDs {
		instance, err := a.GetInstance(ctx, region, vpcID)
		if err != nil {
			a.logger.Warn("获取VPC失败", elog.String("vpc_id", vpcID), elog.FieldErr(err))
			continue
		}
		result = append(result, *instance)
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
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建VPC客户端失败: %w", err)
	}

	request := vpc.CreateDescribeVpcsRequest()
	request.RegionId = region

	if filter != nil {
		if len(filter.VPCIDs) > 0 && len(filter.VPCIDs) == 1 {
			request.VpcId = filter.VPCIDs[0]
		}
		if filter.VPCName != "" {
			request.VpcName = filter.VPCName
		}
		if filter.IsDefault != nil && *filter.IsDefault {
			request.IsDefault = "true"
		}
		if filter.PageNumber > 0 {
			request.PageNumber = requests.NewInteger(filter.PageNumber)
		}
		if filter.PageSize > 0 {
			request.PageSize = requests.NewInteger(filter.PageSize)
		}
	}

	response, err := client.DescribeVpcs(request)
	if err != nil {
		return nil, fmt.Errorf("获取VPC列表失败: %w", err)
	}

	var result []types.VPCInstance
	for _, v := range response.Vpcs.Vpc {
		result = append(result, a.convertToVPCInstance(v, region))
	}

	return result, nil
}

// convertToVPCInstance 转换为通用VPC实例
func (a *VPCAdapter) convertToVPCInstance(v vpc.Vpc, region string) types.VPCInstance {
	// 提取附加CIDR
	var secondaryCidrs []string
	for _, cidr := range v.SecondaryCidrBlocks.SecondaryCidrBlock {
		secondaryCidrs = append(secondaryCidrs, cidr)
	}

	// 提取标签
	tags := make(map[string]string)
	for _, tag := range v.Tags.Tag {
		tags[tag.Key] = tag.Value
	}

	return types.VPCInstance{
		VPCID:           v.VpcId,
		VPCName:         v.VpcName,
		Status:          types.NormalizeVPCStatus("aliyun", v.Status),
		Region:          region,
		Description:     v.Description,
		CidrBlock:       v.CidrBlock,
		SecondaryCidrs:  secondaryCidrs,
		IPv6CidrBlock:   v.Ipv6CidrBlock,
		EnableIPv6:      v.Ipv6CidrBlock != "",
		IsDefault:       v.IsDefault,
		VSwitchCount:    len(v.VSwitchIds.VSwitchId),
		RouteTableCount: len(v.RouterTableIds.RouterTableIds),
		NatGatewayCount: len(v.NatGatewayIds.NatGatewayIds),
		CreationTime:    v.CreationTime,
		ProjectID:       v.ResourceGroupId,
		Tags:            tags,
		Provider:        "aliyun",
	}
}
