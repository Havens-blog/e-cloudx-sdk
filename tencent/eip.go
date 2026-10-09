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

// EIPAdapter 腾讯云EIP适配器
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

	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "vpc.tencentcloudapi.com"

	return vpc.NewClient(credential, region, cpf)
}

// ListInstances 获取EIP列表
func (a *EIPAdapter) ListInstances(ctx context.Context, region string) ([]types.EIPInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	var allEIPs []types.EIPInstance
	offset := int64(0)
	limit := int64(100)

	for {
		request := vpc.NewDescribeAddressesRequest()
		request.Offset = &offset
		request.Limit = &limit

		response, err := client.DescribeAddresses(request)
		if err != nil {
			return nil, fmt.Errorf("获取EIP列表失败: %w", err)
		}

		if response.Response.AddressSet == nil {
			break
		}

		for _, addr := range response.Response.AddressSet {
			allEIPs = append(allEIPs, a.convertToEIPInstance(addr, region))
		}

		if len(response.Response.AddressSet) < int(limit) {
			break
		}
		offset += limit
	}

	a.logger.Info("获取腾讯云EIP列表成功",
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

	request := vpc.NewDescribeAddressesRequest()
	request.AddressIds = common.StringPtrs([]string{allocationID})

	response, err := client.DescribeAddresses(request)
	if err != nil {
		return nil, fmt.Errorf("获取EIP详情失败: %w", err)
	}

	if response.Response.AddressSet == nil || len(response.Response.AddressSet) == 0 {
		return nil, fmt.Errorf("EIP不存在: %s", allocationID)
	}

	instance := a.convertToEIPInstance(response.Response.AddressSet[0], region)
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

	request := vpc.NewDescribeAddressesRequest()
	request.AddressIds = common.StringPtrs(allocationIDs)

	response, err := client.DescribeAddresses(request)
	if err != nil {
		return nil, fmt.Errorf("批量获取EIP失败: %w", err)
	}

	var result []types.EIPInstance
	if response.Response.AddressSet != nil {
		for _, addr := range response.Response.AddressSet {
			result = append(result, a.convertToEIPInstance(addr, region))
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

	request := vpc.NewDescribeAddressesRequest()

	if filter != nil {
		if len(filter.AllocationIDs) > 0 {
			request.AddressIds = common.StringPtrs(filter.AllocationIDs)
		}
		if len(filter.IPAddresses) > 0 {
			request.Filters = append(request.Filters, &vpc.Filter{
				Name:   common.StringPtr("address-ip"),
				Values: common.StringPtrs(filter.IPAddresses),
			})
		}
		if filter.InstanceID != "" {
			request.Filters = append(request.Filters, &vpc.Filter{
				Name:   common.StringPtr("bindedinstance-id"),
				Values: common.StringPtrs([]string{filter.InstanceID}),
			})
		}
		if len(filter.Status) > 0 {
			request.Filters = append(request.Filters, &vpc.Filter{
				Name:   common.StringPtr("address-status"),
				Values: common.StringPtrs(filter.Status),
			})
		}
		if filter.PageSize > 0 {
			limit := int64(filter.PageSize)
			request.Limit = &limit
		}
	}

	response, err := client.DescribeAddresses(request)
	if err != nil {
		return nil, fmt.Errorf("获取EIP列表失败: %w", err)
	}

	var result []types.EIPInstance
	if response.Response.AddressSet != nil {
		for _, addr := range response.Response.AddressSet {
			result = append(result, a.convertToEIPInstance(addr, region))
		}
	}

	return result, nil
}

// convertToEIPInstance 转换为通用EIP实例
func (a *EIPAdapter) convertToEIPInstance(addr *vpc.Address, region string) types.EIPInstance {
	allocationID := ""
	if addr.AddressId != nil {
		allocationID = *addr.AddressId
	}

	eipAddress := ""
	if addr.AddressIp != nil {
		eipAddress = *addr.AddressIp
	}

	name := ""
	if addr.AddressName != nil {
		name = *addr.AddressName
	}

	status := "Available"
	if addr.AddressStatus != nil {
		status = *addr.AddressStatus
	}

	instanceID := ""
	if addr.InstanceId != nil {
		instanceID = *addr.InstanceId
	}

	// 腾讯云 InstanceType: CVM, NAT, CLB, EIP, HAVIP, ENI 等
	instanceType := ""
	if addr.InstanceType != nil {
		instanceType = *addr.InstanceType
	}

	bandwidth := 0
	if addr.Bandwidth != nil {
		bandwidth = int(*addr.Bandwidth)
	}

	chargeType := ""
	if addr.InternetChargeType != nil {
		chargeType = *addr.InternetChargeType
	}

	createTime := ""
	if addr.CreatedTime != nil {
		createTime = *addr.CreatedTime
	}

	// 绑定的弹性网卡ID
	networkInterfaceID := ""
	if addr.NetworkInterfaceId != nil {
		networkInterfaceID = *addr.NetworkInterfaceId
	}

	// 绑定的内网IP
	privateIP := ""
	if addr.PrivateAddressIp != nil {
		privateIP = *addr.PrivateAddressIp
	}

	// VPC ID
	vpcID := ""
	if addr.UnVpcId != nil {
		vpcID = *addr.UnVpcId
	}

	// 运营商信息
	isp := ""
	if addr.InternetServiceProvider != nil {
		isp = *addr.InternetServiceProvider
	}

	// 提取标签
	tags := make(map[string]string)
	if addr.TagSet != nil {
		for _, tag := range addr.TagSet {
			if tag.Key != nil && tag.Value != nil {
				tags[*tag.Key] = *tag.Value
			}
		}
	}

	return types.EIPInstance{
		AllocationID:       allocationID,
		IPAddress:          eipAddress,
		Name:               name,
		Status:             types.NormalizeEIPStatus("tencent", status),
		Region:             region,
		Bandwidth:          bandwidth,
		InternetChargeType: chargeType,
		ISP:                isp,
		InstanceID:         instanceID,
		InstanceType:       instanceType,
		PrivateIPAddress:   privateIP,
		VPCID:              vpcID,
		NetworkInterface:   networkInterfaceID,
		CreationTime:       createTime,
		Tags:               tags,
		Provider:           "tencent",
	}
}
