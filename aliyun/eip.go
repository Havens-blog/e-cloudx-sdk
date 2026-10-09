package aliyun

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	vpc "github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"github.com/gotomicro/ego/core/elog"
)

// EIPAdapter 阿里云EIP适配器
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
func (a *EIPAdapter) createClient(region string) (*vpc.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}
	return vpc.NewClientWithAccessKey(region, a.accessKeyID, a.accessKeySecret)
}

// ListInstances 获取EIP列表
func (a *EIPAdapter) ListInstances(ctx context.Context, region string) ([]types.EIPInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建VPC客户端失败: %w", err)
	}

	var allEIPs []types.EIPInstance
	pageNumber := 1
	pageSize := 50

	for {
		request := vpc.CreateDescribeEipAddressesRequest()
		request.RegionId = region
		request.PageNumber = requests.NewInteger(pageNumber)
		request.PageSize = requests.NewInteger(pageSize)

		response, err := client.DescribeEipAddresses(request)
		if err != nil {
			return nil, fmt.Errorf("获取EIP列表失败: %w", err)
		}

		for _, eip := range response.EipAddresses.EipAddress {
			allEIPs = append(allEIPs, a.convertToEIPInstance(eip, region))
		}

		if len(response.EipAddresses.EipAddress) < pageSize {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取阿里云EIP列表成功",
		elog.String("region", region),
		elog.Int("count", len(allEIPs)))

	return allEIPs, nil
}

// GetInstance 获取单个EIP详情
func (a *EIPAdapter) GetInstance(ctx context.Context, region, allocationID string) (*types.EIPInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建VPC客户端失败: %w", err)
	}

	request := vpc.CreateDescribeEipAddressesRequest()
	request.RegionId = region
	request.AllocationId = allocationID

	response, err := client.DescribeEipAddresses(request)
	if err != nil {
		return nil, fmt.Errorf("获取EIP详情失败: %w", err)
	}

	if len(response.EipAddresses.EipAddress) == 0 {
		return nil, fmt.Errorf("EIP不存在: %s", allocationID)
	}

	instance := a.convertToEIPInstance(response.EipAddresses.EipAddress[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取EIP
func (a *EIPAdapter) ListInstancesByIDs(ctx context.Context, region string, allocationIDs []string) ([]types.EIPInstance, error) {
	var result []types.EIPInstance
	for _, id := range allocationIDs {
		instance, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取EIP失败", elog.String("allocation_id", id), elog.FieldErr(err))
			continue
		}
		result = append(result, *instance)
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
		return nil, fmt.Errorf("创建VPC客户端失败: %w", err)
	}

	request := vpc.CreateDescribeEipAddressesRequest()
	request.RegionId = region

	if filter != nil {
		if len(filter.AllocationIDs) > 0 && len(filter.AllocationIDs) == 1 {
			request.AllocationId = filter.AllocationIDs[0]
		}
		if len(filter.IPAddresses) > 0 && len(filter.IPAddresses) == 1 {
			request.EipAddress = filter.IPAddresses[0]
		}
		if len(filter.Status) > 0 {
			request.Status = filter.Status[0]
		}
		if filter.InstanceID != "" {
			request.AssociatedInstanceId = filter.InstanceID
		}
		if filter.InstanceType != "" {
			request.AssociatedInstanceType = filter.InstanceType
		}
		if filter.PageNumber > 0 {
			request.PageNumber = requests.NewInteger(filter.PageNumber)
		}
		if filter.PageSize > 0 {
			request.PageSize = requests.NewInteger(filter.PageSize)
		}
	}

	response, err := client.DescribeEipAddresses(request)
	if err != nil {
		return nil, fmt.Errorf("获取EIP列表失败: %w", err)
	}

	var result []types.EIPInstance
	for _, eip := range response.EipAddresses.EipAddress {
		result = append(result, a.convertToEIPInstance(eip, region))
	}

	return result, nil
}

// convertToEIPInstance 转换为通用EIP实例
func (a *EIPAdapter) convertToEIPInstance(eip vpc.EipAddress, region string) types.EIPInstance {
	// 提取标签
	tags := make(map[string]string)
	for _, tag := range eip.Tags.Tag {
		tags[tag.Key] = tag.Value
	}

	// 转换带宽
	bandwidth, _ := strconv.Atoi(eip.Bandwidth)

	return types.EIPInstance{
		AllocationID:       eip.AllocationId,
		IPAddress:          eip.IpAddress,
		Name:               eip.Name,
		Status:             types.NormalizeEIPStatus("aliyun", eip.Status),
		Region:             region,
		Description:        eip.Description,
		Bandwidth:          bandwidth,
		InternetChargeType: eip.InternetChargeType,
		ISP:                eip.ISP,
		Netmode:            eip.Netmode,
		InstanceID:         eip.InstanceId,
		InstanceType:       eip.InstanceType,
		PrivateIPAddress:   eip.PrivateIpAddress,
		ChargeType:         eip.ChargeType,
		CreationTime:       eip.AllocationTime,
		ExpiredTime:        eip.ExpiredTime,
		ResourceGroupID:    eip.ResourceGroupId,
		Tags:               tags,
		Provider:           "aliyun",
	}
}
