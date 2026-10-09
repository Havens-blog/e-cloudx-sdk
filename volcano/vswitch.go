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

// VSwitchAdapter 火山引擎子网适配器
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
func (a *VSwitchAdapter) createClient(region string) (*vpc.VPC, error) {
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

// ListInstances 获取子网列表
func (a *VSwitchAdapter) ListInstances(ctx context.Context, region string) ([]types.VSwitchInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		a.logger.Error("创建火山引擎子网客户端失败",
			elog.String("region", region),
			elog.FieldErr(err))
		return nil, err
	}

	var allSubnets []types.VSwitchInstance
	pageNumber := int64(1)
	pageSize := int64(100)

	for {
		input := &vpc.DescribeSubnetsInput{
			PageNumber: &pageNumber,
			PageSize:   &pageSize,
		}

		a.logger.Debug("调用火山引擎DescribeSubnets",
			elog.String("region", region),
			elog.Int64("page", pageNumber))

		output, err := client.DescribeSubnets(input)
		if err != nil {
			a.logger.Error("调用火山引擎DescribeSubnets失败",
				elog.String("region", region),
				elog.FieldErr(err))
			return nil, fmt.Errorf("获取子网列表失败: %w", err)
		}

		if output.Subnets == nil || len(output.Subnets) == 0 {
			a.logger.Debug("火山引擎子网列表为空",
				elog.String("region", region),
				elog.Int64("page", pageNumber))
			break
		}

		for _, subnet := range output.Subnets {
			allSubnets = append(allSubnets, a.convertToVSwitchInstance(subnet, region))
		}

		if len(output.Subnets) < int(pageSize) {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取火山引擎子网列表成功",
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

	input := &vpc.DescribeSubnetsInput{
		SubnetIds: []*string{&subnetID},
	}

	output, err := client.DescribeSubnets(input)
	if err != nil {
		return nil, fmt.Errorf("获取子网详情失败: %w", err)
	}

	if output.Subnets == nil || len(output.Subnets) == 0 {
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

	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	idPtrs := make([]*string, len(subnetIDs))
	for i, id := range subnetIDs {
		idPtrs[i] = volcengine.String(id)
	}

	input := &vpc.DescribeSubnetsInput{
		SubnetIds: idPtrs,
	}

	output, err := client.DescribeSubnets(input)
	if err != nil {
		return nil, fmt.Errorf("批量获取子网失败: %w", err)
	}

	var result []types.VSwitchInstance
	if output.Subnets != nil {
		for _, subnet := range output.Subnets {
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

	input := &vpc.DescribeSubnetsInput{}

	if filter != nil {
		if len(filter.VSwitchIDs) > 0 {
			idPtrs := make([]*string, len(filter.VSwitchIDs))
			for i, id := range filter.VSwitchIDs {
				idPtrs[i] = volcengine.String(id)
			}
			input.SubnetIds = idPtrs
		}
		if filter.VPCID != "" {
			input.VpcId = volcengine.String(filter.VPCID)
		}
		if filter.PageSize > 0 {
			pageSize := int64(filter.PageSize)
			input.PageSize = &pageSize
		}
	}

	output, err := client.DescribeSubnets(input)
	if err != nil {
		return nil, fmt.Errorf("获取子网列表失败: %w", err)
	}

	var result []types.VSwitchInstance
	if output.Subnets != nil {
		for _, subnet := range output.Subnets {
			result = append(result, a.convertToVSwitchInstance(subnet, region))
		}
	}

	return result, nil
}

// convertToVSwitchInstance 转换为通用子网实例
func (a *VSwitchAdapter) convertToVSwitchInstance(subnet *vpc.SubnetForDescribeSubnetsOutput, region string) types.VSwitchInstance {
	subnetID := ""
	if subnet.SubnetId != nil {
		subnetID = *subnet.SubnetId
	}

	subnetName := ""
	if subnet.SubnetName != nil {
		subnetName = *subnet.SubnetName
	}

	status := "Available"
	if subnet.Status != nil {
		status = *subnet.Status
	}

	zoneID := ""
	if subnet.ZoneId != nil {
		zoneID = *subnet.ZoneId
	}

	cidrBlock := ""
	if subnet.CidrBlock != nil {
		cidrBlock = *subnet.CidrBlock
	}

	vpcID := ""
	if subnet.VpcId != nil {
		vpcID = *subnet.VpcId
	}

	var availableIPCount int64
	if subnet.AvailableIpAddressCount != nil {
		availableIPCount = *subnet.AvailableIpAddressCount
	}

	var totalIPCount int64
	if subnet.TotalIpv4Count != nil {
		totalIPCount = *subnet.TotalIpv4Count
	}

	description := ""
	if subnet.Description != nil {
		description = *subnet.Description
	}

	creationTime := ""
	if subnet.CreationTime != nil {
		creationTime = *subnet.CreationTime
	}

	// 提取标签
	tags := make(map[string]string)
	if subnet.Tags != nil {
		for _, tag := range subnet.Tags {
			if tag.Key != nil && tag.Value != nil {
				tags[*tag.Key] = *tag.Value
			}
		}
	}

	return types.VSwitchInstance{
		VSwitchID:        subnetID,
		VSwitchName:      subnetName,
		Status:           types.NormalizeVSwitchStatus("volcano", status),
		Region:           region,
		Zone:             zoneID,
		CidrBlock:        cidrBlock,
		VPCID:            vpcID,
		AvailableIPCount: availableIPCount,
		TotalIPCount:     totalIPCount,
		Description:      description,
		CreationTime:     creationTime,
		Tags:             tags,
		Provider:         "volcano",
	}
}
