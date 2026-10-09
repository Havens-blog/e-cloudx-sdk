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

// VSwitchAdapter 腾讯云子网适配器
type VSwitchAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewVSwitchAdapter 创建子网适配器
func NewVSwitchAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *VSwitchAdapter {
	return &VSwitchAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建VPC客户端
func (a *VSwitchAdapter) createClient(region string) (*vpc.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}

	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "vpc.tencentcloudapi.com"

	return vpc.NewClient(credential, region, cpf)
}

// ListInstances 获取子网列表
func (a *VSwitchAdapter) ListInstances(ctx context.Context, region string) ([]types.VSwitchInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	var allSubnets []types.VSwitchInstance
	offset := "0"
	limit := "100"

	for {
		request := vpc.NewDescribeSubnetsRequest()
		request.Offset = &offset
		request.Limit = &limit

		response, err := client.DescribeSubnets(request)
		if err != nil {
			return nil, fmt.Errorf("获取子网列表失败: %w", err)
		}

		if response.Response.SubnetSet == nil {
			break
		}

		for _, subnet := range response.Response.SubnetSet {
			allSubnets = append(allSubnets, a.convertToVSwitchInstance(subnet, region))
		}

		if len(response.Response.SubnetSet) < 100 {
			break
		}
		offset = fmt.Sprintf("%d", len(allSubnets))
	}

	a.logger.Info("获取腾讯云子网列表成功",
		elog.String("region", region),
		elog.Int("count", len(allSubnets)))

	return allSubnets, nil
}

// GetInstance 获取单个子网详情
func (a *VSwitchAdapter) GetInstance(ctx context.Context, region, subnetID string) (*types.VSwitchInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	request := vpc.NewDescribeSubnetsRequest()
	request.SubnetIds = common.StringPtrs([]string{subnetID})

	response, err := client.DescribeSubnets(request)
	if err != nil {
		return nil, fmt.Errorf("获取子网详情失败: %w", err)
	}

	if response.Response.SubnetSet == nil || len(response.Response.SubnetSet) == 0 {
		return nil, fmt.Errorf("子网不存在: %s", subnetID)
	}

	instance := a.convertToVSwitchInstance(response.Response.SubnetSet[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取子网
func (a *VSwitchAdapter) ListInstancesByIDs(ctx context.Context, region string, subnetIDs []string) ([]types.VSwitchInstance, error) {
	if len(subnetIDs) == 0 {
		return nil, nil
	}

	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	request := vpc.NewDescribeSubnetsRequest()
	request.SubnetIds = common.StringPtrs(subnetIDs)

	response, err := client.DescribeSubnets(request)
	if err != nil {
		return nil, fmt.Errorf("批量获取子网失败: %w", err)
	}

	var result []types.VSwitchInstance
	if response.Response.SubnetSet != nil {
		for _, subnet := range response.Response.SubnetSet {
			result = append(result, a.convertToVSwitchInstance(subnet, region))
		}
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
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	request := vpc.NewDescribeSubnetsRequest()

	if filter != nil {
		if len(filter.VSwitchIDs) > 0 {
			request.SubnetIds = common.StringPtrs(filter.VSwitchIDs)
		}
		if filter.VPCID != "" {
			request.Filters = append(request.Filters, &vpc.Filter{
				Name:   common.StringPtr("vpc-id"),
				Values: common.StringPtrs([]string{filter.VPCID}),
			})
		}
		if filter.Zone != "" {
			request.Filters = append(request.Filters, &vpc.Filter{
				Name:   common.StringPtr("zone"),
				Values: common.StringPtrs([]string{filter.Zone}),
			})
		}
		if filter.VSwitchName != "" {
			request.Filters = append(request.Filters, &vpc.Filter{
				Name:   common.StringPtr("subnet-name"),
				Values: common.StringPtrs([]string{filter.VSwitchName}),
			})
		}
		if filter.PageSize > 0 {
			limit := fmt.Sprintf("%d", filter.PageSize)
			request.Limit = &limit
		}
	}

	response, err := client.DescribeSubnets(request)
	if err != nil {
		return nil, fmt.Errorf("获取子网列表失败: %w", err)
	}

	var result []types.VSwitchInstance
	if response.Response.SubnetSet != nil {
		for _, subnet := range response.Response.SubnetSet {
			result = append(result, a.convertToVSwitchInstance(subnet, region))
		}
	}

	return result, nil
}

// convertToVSwitchInstance 转换为通用子网实例
func (a *VSwitchAdapter) convertToVSwitchInstance(subnet *vpc.Subnet, region string) types.VSwitchInstance {
	subnetID := ""
	if subnet.SubnetId != nil {
		subnetID = *subnet.SubnetId
	}

	name := ""
	if subnet.SubnetName != nil {
		name = *subnet.SubnetName
	}

	cidrBlock := ""
	if subnet.CidrBlock != nil {
		cidrBlock = *subnet.CidrBlock
	}

	ipv6CidrBlock := ""
	if subnet.Ipv6CidrBlock != nil {
		ipv6CidrBlock = *subnet.Ipv6CidrBlock
	}

	vpcID := ""
	if subnet.VpcId != nil {
		vpcID = *subnet.VpcId
	}

	zone := ""
	if subnet.Zone != nil {
		zone = *subnet.Zone
	}

	isDefault := false
	if subnet.IsDefault != nil {
		isDefault = *subnet.IsDefault
	}

	createTime := ""
	if subnet.CreatedTime != nil {
		createTime = *subnet.CreatedTime
	}

	routeTableID := ""
	if subnet.RouteTableId != nil {
		routeTableID = *subnet.RouteTableId
	}

	var availableIPCount int64
	if subnet.AvailableIpAddressCount != nil {
		availableIPCount = int64(*subnet.AvailableIpAddressCount)
	}

	var totalIPCount int64
	if subnet.TotalIpAddressCount != nil {
		totalIPCount = int64(*subnet.TotalIpAddressCount)
	}

	// 提取标签
	tags := make(map[string]string)
	if subnet.TagSet != nil {
		for _, tag := range subnet.TagSet {
			if tag.Key != nil && tag.Value != nil {
				tags[*tag.Key] = *tag.Value
			}
		}
	}

	return types.VSwitchInstance{
		VSwitchID:        subnetID,
		VSwitchName:      name,
		Status:           types.NormalizeVSwitchStatus("tencent", "Available"), // 腾讯云子网无状态字段，保持存在性语义 available
		Region:           region,
		Zone:             zone,
		CidrBlock:        cidrBlock,
		IPv6CidrBlock:    ipv6CidrBlock,
		EnableIPv6:       ipv6CidrBlock != "",
		IsDefault:        isDefault,
		VPCID:            vpcID,
		AvailableIPCount: availableIPCount,
		TotalIPCount:     totalIPCount,
		RouteTableID:     routeTableID,
		CreationTime:     createTime,
		Tags:             tags,
		Provider:         "tencent",
	}
}
