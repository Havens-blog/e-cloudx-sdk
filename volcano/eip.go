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

// EIPAdapter 火山引擎EIP适配器
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

// createClient 创建VPC客户端（EIP属于VPC服务）
func (a *EIPAdapter) createClient(region string) (*vpc.VPC, error) {
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

// ListInstances 获取EIP列表
func (a *EIPAdapter) ListInstances(ctx context.Context, region string) ([]types.EIPInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		a.logger.Error("创建火山引擎EIP客户端失败",
			elog.String("region", region),
			elog.FieldErr(err))
		return nil, err
	}

	var allEIPs []types.EIPInstance
	pageNumber := int64(1)
	pageSize := int64(100)

	for {
		input := &vpc.DescribeEipAddressesInput{
			PageNumber: &pageNumber,
			PageSize:   &pageSize,
		}

		a.logger.Debug("调用火山引擎DescribeEipAddresses",
			elog.String("region", region),
			elog.Int64("page", pageNumber))

		output, err := client.DescribeEipAddresses(input)
		if err != nil {
			a.logger.Error("调用火山引擎DescribeEipAddresses失败",
				elog.String("region", region),
				elog.FieldErr(err))
			return nil, fmt.Errorf("获取EIP列表失败: %w", err)
		}

		if output.EipAddresses == nil || len(output.EipAddresses) == 0 {
			a.logger.Debug("火山引擎EIP列表为空",
				elog.String("region", region),
				elog.Int64("page", pageNumber))
			break
		}

		for _, eip := range output.EipAddresses {
			allEIPs = append(allEIPs, a.convertToEIPInstance(eip, region))
		}

		if len(output.EipAddresses) < int(pageSize) {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取火山引擎EIP列表成功",
		elog.String("region", region),
		elog.Int("count", len(allEIPs)))

	return allEIPs, nil
}

// GetInstance 获取单个EIP详情
func (a *EIPAdapter) GetInstance(ctx context.Context, region, allocationID string) (*types.EIPInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	input := &vpc.DescribeEipAddressesInput{
		AllocationIds: []*string{&allocationID},
	}

	output, err := client.DescribeEipAddresses(input)
	if err != nil {
		return nil, fmt.Errorf("获取EIP详情失败: %w", err)
	}

	if output.EipAddresses == nil || len(output.EipAddresses) == 0 {
		return nil, fmt.Errorf("EIP不存在: %s", allocationID)
	}

	instance := a.convertToEIPInstance(output.EipAddresses[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取EIP
func (a *EIPAdapter) ListInstancesByIDs(ctx context.Context, region string, allocationIDs []string) ([]types.EIPInstance, error) {
	if len(allocationIDs) == 0 {
		return nil, nil
	}

	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	idPtrs := make([]*string, len(allocationIDs))
	for i, id := range allocationIDs {
		idPtrs[i] = volcengine.String(id)
	}

	input := &vpc.DescribeEipAddressesInput{
		AllocationIds: idPtrs,
	}

	output, err := client.DescribeEipAddresses(input)
	if err != nil {
		return nil, fmt.Errorf("批量获取EIP失败: %w", err)
	}

	var result []types.EIPInstance
	if output.EipAddresses != nil {
		for _, eip := range output.EipAddresses {
			result = append(result, a.convertToEIPInstance(eip, region))
		}
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
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	input := &vpc.DescribeEipAddressesInput{}

	if filter != nil {
		if len(filter.AllocationIDs) > 0 {
			idPtrs := make([]*string, len(filter.AllocationIDs))
			for i, id := range filter.AllocationIDs {
				idPtrs[i] = volcengine.String(id)
			}
			input.AllocationIds = idPtrs
		}
		if len(filter.IPAddresses) > 0 {
			ipPtrs := make([]*string, len(filter.IPAddresses))
			for i, ip := range filter.IPAddresses {
				ipPtrs[i] = volcengine.String(ip)
			}
			input.EipAddresses = ipPtrs
		}
		if filter.InstanceID != "" {
			input.AssociatedInstanceId = &filter.InstanceID
		}
		if len(filter.Status) > 0 {
			input.Status = &filter.Status[0]
		}
		if filter.PageSize > 0 {
			pageSize := int64(filter.PageSize)
			input.PageSize = &pageSize
		}
	}

	output, err := client.DescribeEipAddresses(input)
	if err != nil {
		return nil, fmt.Errorf("获取EIP列表失败: %w", err)
	}

	var result []types.EIPInstance
	if output.EipAddresses != nil {
		for _, eip := range output.EipAddresses {
			result = append(result, a.convertToEIPInstance(eip, region))
		}
	}

	return result, nil
}

// convertToEIPInstance 转换为通用EIP实例
func (a *EIPAdapter) convertToEIPInstance(eip *vpc.EipAddressForDescribeEipAddressesOutput, region string) types.EIPInstance {
	allocationID := ""
	if eip.AllocationId != nil {
		allocationID = *eip.AllocationId
	}

	eipAddress := ""
	if eip.EipAddress != nil {
		eipAddress = *eip.EipAddress
	}

	name := ""
	if eip.Name != nil {
		name = *eip.Name
	}

	status := "Available"
	if eip.Status != nil {
		status = *eip.Status
	}

	description := ""
	if eip.Description != nil {
		description = *eip.Description
	}

	instanceID := ""
	if eip.InstanceId != nil {
		instanceID = *eip.InstanceId
	}

	// 火山引擎 InstanceType: EcsInstance, Nat, ClbInstance, AlbInstance, HaVip 等
	instanceType := ""
	if eip.InstanceType != nil {
		instanceType = *eip.InstanceType
	}

	bandwidth := 0
	if eip.Bandwidth != nil {
		bandwidth = int(*eip.Bandwidth)
	}

	chargeType := ""
	if eip.BillingType != nil {
		chargeType = fmt.Sprintf("%d", *eip.BillingType)
	}

	isp := ""
	if eip.ISP != nil {
		isp = *eip.ISP
	}

	createTime := ""
	if eip.AllocationTime != nil {
		createTime = *eip.AllocationTime
	}

	expiredTime := ""
	if eip.ExpiredTime != nil {
		expiredTime = *eip.ExpiredTime
	}

	projectName := ""
	if eip.ProjectName != nil {
		projectName = *eip.ProjectName
	}

	// 提取标签
	tags := make(map[string]string)
	if eip.Tags != nil {
		for _, tag := range eip.Tags {
			if tag.Key != nil && tag.Value != nil {
				tags[*tag.Key] = *tag.Value
			}
		}
	}

	return types.EIPInstance{
		AllocationID: allocationID,
		IPAddress:    eipAddress,
		Name:         name,
		Status:       types.NormalizeEIPStatus("volcano", status),
		Region:       region,
		Description:  description,
		Bandwidth:    bandwidth,
		ISP:          isp,
		InstanceID:   instanceID,
		InstanceType: instanceType,
		ChargeType:   chargeType,
		CreationTime: createTime,
		ExpiredTime:  expiredTime,
		ProjectName:  projectName,
		Tags:         tags,
		Provider:     "volcano",
	}
}
