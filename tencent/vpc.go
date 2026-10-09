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

// VPCAdapter 腾讯云VPC适配器
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

	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "vpc.tencentcloudapi.com"

	return vpc.NewClient(credential, region, cpf)
}

// ListInstances 获取VPC列表
func (a *VPCAdapter) ListInstances(ctx context.Context, region string) ([]types.VPCInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	var allVPCs []types.VPCInstance
	offset := uint64(0)
	limit := uint64(100)

	for {
		request := vpc.NewDescribeVpcsRequest()
		request.Offset = common.StringPtr(fmt.Sprintf("%d", offset))
		request.Limit = common.StringPtr(fmt.Sprintf("%d", limit))

		response, err := client.DescribeVpcs(request)
		if err != nil {
			return nil, fmt.Errorf("获取VPC列表失败: %w", err)
		}

		if response.Response.VpcSet == nil {
			break
		}

		for _, v := range response.Response.VpcSet {
			allVPCs = append(allVPCs, a.convertToVPCInstance(v, region))
		}

		if len(response.Response.VpcSet) < int(limit) {
			break
		}
		offset += limit
	}

	a.logger.Info("获取腾讯云VPC列表成功",
		elog.String("region", region),
		elog.Int("count", len(allVPCs)))

	return allVPCs, nil
}

// GetInstance 获取单个VPC详情
func (a *VPCAdapter) GetInstance(ctx context.Context, region, vpcID string) (*types.VPCInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	request := vpc.NewDescribeVpcsRequest()
	request.VpcIds = common.StringPtrs([]string{vpcID})

	response, err := client.DescribeVpcs(request)
	if err != nil {
		return nil, fmt.Errorf("获取VPC详情失败: %w", err)
	}

	if response.Response.VpcSet == nil || len(response.Response.VpcSet) == 0 {
		return nil, fmt.Errorf("VPC不存在: %s", vpcID)
	}

	instance := a.convertToVPCInstance(response.Response.VpcSet[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取VPC
func (a *VPCAdapter) ListInstancesByIDs(ctx context.Context, region string, vpcIDs []string) ([]types.VPCInstance, error) {
	if len(vpcIDs) == 0 {
		return nil, nil
	}

	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	request := vpc.NewDescribeVpcsRequest()
	request.VpcIds = common.StringPtrs(vpcIDs)

	response, err := client.DescribeVpcs(request)
	if err != nil {
		return nil, fmt.Errorf("批量获取VPC失败: %w", err)
	}

	var result []types.VPCInstance
	if response.Response.VpcSet != nil {
		for _, v := range response.Response.VpcSet {
			result = append(result, a.convertToVPCInstance(v, region))
		}
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
		return nil, err
	}

	request := vpc.NewDescribeVpcsRequest()

	if filter != nil {
		if len(filter.VPCIDs) > 0 {
			request.VpcIds = common.StringPtrs(filter.VPCIDs)
		}
		if filter.VPCName != "" {
			request.Filters = append(request.Filters, &vpc.Filter{
				Name:   common.StringPtr("vpc-name"),
				Values: common.StringPtrs([]string{filter.VPCName}),
			})
		}
		if filter.CidrBlock != "" {
			request.Filters = append(request.Filters, &vpc.Filter{
				Name:   common.StringPtr("cidr-block"),
				Values: common.StringPtrs([]string{filter.CidrBlock}),
			})
		}
		if filter.IsDefault != nil && *filter.IsDefault {
			request.Filters = append(request.Filters, &vpc.Filter{
				Name:   common.StringPtr("is-default"),
				Values: common.StringPtrs([]string{"true"}),
			})
		}
		if filter.PageSize > 0 {
			request.Limit = common.StringPtr(fmt.Sprintf("%d", filter.PageSize))
		}
	}

	response, err := client.DescribeVpcs(request)
	if err != nil {
		return nil, fmt.Errorf("获取VPC列表失败: %w", err)
	}

	var result []types.VPCInstance
	if response.Response.VpcSet != nil {
		for _, v := range response.Response.VpcSet {
			result = append(result, a.convertToVPCInstance(v, region))
		}
	}

	return result, nil
}

// convertToVPCInstance 转换为通用VPC实例
func (a *VPCAdapter) convertToVPCInstance(v *vpc.Vpc, region string) types.VPCInstance {
	vpcID := ""
	if v.VpcId != nil {
		vpcID = *v.VpcId
	}

	vpcName := ""
	if v.VpcName != nil {
		vpcName = *v.VpcName
	}

	cidrBlock := ""
	if v.CidrBlock != nil {
		cidrBlock = *v.CidrBlock
	}

	isDefault := false
	if v.IsDefault != nil {
		isDefault = *v.IsDefault
	}

	// 提取附加CIDR
	var secondaryCidrs []string
	if v.AssistantCidrSet != nil {
		for _, cidr := range v.AssistantCidrSet {
			if cidr.CidrBlock != nil {
				secondaryCidrs = append(secondaryCidrs, *cidr.CidrBlock)
			}
		}
	}

	// 提取IPv6 CIDR
	var ipv6Cidr string
	if v.Ipv6CidrBlock != nil {
		ipv6Cidr = *v.Ipv6CidrBlock
	}

	// 提取标签
	tags := make(map[string]string)
	if v.TagSet != nil {
		for _, tag := range v.TagSet {
			if tag.Key != nil && tag.Value != nil {
				tags[*tag.Key] = *tag.Value
			}
		}
	}

	createTime := ""
	if v.CreatedTime != nil {
		createTime = *v.CreatedTime
	}

	return types.VPCInstance{
		VPCID:          vpcID,
		VPCName:        vpcName,
		Status:         types.NormalizeVPCStatus("tencent", "Available"), // 腾讯云 VPC 无状态字段，保持存在性语义 available
		Region:         region,
		CidrBlock:      cidrBlock,
		SecondaryCidrs: secondaryCidrs,
		IPv6CidrBlock:  ipv6Cidr,
		EnableIPv6:     ipv6Cidr != "",
		IsDefault:      isDefault,
		CreationTime:   createTime,
		Tags:           tags,
		Provider:       "tencent",
	}
}
