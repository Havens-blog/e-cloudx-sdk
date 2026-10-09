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

// VPCAdapter 火山引擎VPC适配器
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
func (a *VPCAdapter) createClient(region string) (*vpc.VPC, error) {
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

// ListInstances 获取VPC列表
func (a *VPCAdapter) ListInstances(ctx context.Context, region string) ([]types.VPCInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		a.logger.Error("创建火山引擎VPC客户端失败",
			elog.String("region", region),
			elog.FieldErr(err))
		return nil, err
	}

	var allVPCs []types.VPCInstance
	pageNumber := int64(1)
	pageSize := int64(100)

	for {
		input := &vpc.DescribeVpcsInput{
			PageNumber: &pageNumber,
			PageSize:   &pageSize,
		}

		a.logger.Debug("调用火山引擎DescribeVpcs",
			elog.String("region", region),
			elog.Int64("page", pageNumber))

		output, err := client.DescribeVpcs(input)
		if err != nil {
			a.logger.Error("调用火山引擎DescribeVpcs失败",
				elog.String("region", region),
				elog.FieldErr(err))
			return nil, fmt.Errorf("获取VPC列表失败: %w", err)
		}

		if output.Vpcs == nil || len(output.Vpcs) == 0 {
			a.logger.Debug("火山引擎VPC列表为空",
				elog.String("region", region),
				elog.Int64("page", pageNumber))
			break
		}

		for _, v := range output.Vpcs {
			allVPCs = append(allVPCs, a.convertToVPCInstance(v, region))
		}

		if len(output.Vpcs) < int(pageSize) {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取火山引擎VPC列表成功",
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

	input := &vpc.DescribeVpcsInput{
		VpcIds: []*string{&vpcID},
	}

	output, err := client.DescribeVpcs(input)
	if err != nil {
		return nil, fmt.Errorf("获取VPC详情失败: %w", err)
	}

	if output.Vpcs == nil || len(output.Vpcs) == 0 {
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

	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	vpcIDPtrs := make([]*string, len(vpcIDs))
	for i, id := range vpcIDs {
		vpcIDPtrs[i] = volcengine.String(id)
	}

	input := &vpc.DescribeVpcsInput{
		VpcIds: vpcIDPtrs,
	}

	output, err := client.DescribeVpcs(input)
	if err != nil {
		return nil, fmt.Errorf("批量获取VPC失败: %w", err)
	}

	var result []types.VPCInstance
	if output.Vpcs != nil {
		for _, v := range output.Vpcs {
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

	input := &vpc.DescribeVpcsInput{}

	if filter != nil {
		if len(filter.VPCIDs) > 0 {
			vpcIDPtrs := make([]*string, len(filter.VPCIDs))
			for i, id := range filter.VPCIDs {
				vpcIDPtrs[i] = volcengine.String(id)
			}
			input.VpcIds = vpcIDPtrs
		}
		if filter.VPCName != "" {
			input.VpcName = &filter.VPCName
		}
		if filter.PageSize > 0 {
			pageSize := int64(filter.PageSize)
			input.PageSize = &pageSize
		}
	}

	output, err := client.DescribeVpcs(input)
	if err != nil {
		return nil, fmt.Errorf("获取VPC列表失败: %w", err)
	}

	var result []types.VPCInstance
	if output.Vpcs != nil {
		for _, v := range output.Vpcs {
			result = append(result, a.convertToVPCInstance(v, region))
		}
	}

	return result, nil
}

// convertToVPCInstance 转换为通用VPC实例
func (a *VPCAdapter) convertToVPCInstance(v *vpc.VpcForDescribeVpcsOutput, region string) types.VPCInstance {
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

	status := "Available"
	if v.Status != nil {
		status = *v.Status
	}

	description := ""
	if v.Description != nil {
		description = *v.Description
	}

	createTime := ""
	if v.CreationTime != nil {
		createTime = *v.CreationTime
	}

	// 提取附加CIDR
	var secondaryCidrs []string
	if v.SecondaryCidrBlocks != nil {
		for _, cidr := range v.SecondaryCidrBlocks {
			if cidr != nil {
				secondaryCidrs = append(secondaryCidrs, *cidr)
			}
		}
	}

	// 提取标签
	tags := make(map[string]string)
	if v.Tags != nil {
		for _, tag := range v.Tags {
			if tag.Key != nil && tag.Value != nil {
				tags[*tag.Key] = *tag.Value
			}
		}
	}

	return types.VPCInstance{
		VPCID:          vpcID,
		VPCName:        vpcName,
		Status:         types.NormalizeVPCStatus("volcano", status),
		Region:         region,
		Description:    description,
		CidrBlock:      cidrBlock,
		SecondaryCidrs: secondaryCidrs,
		CreationTime:   createTime,
		Tags:           tags,
		Provider:       "volcano",
	}
}
