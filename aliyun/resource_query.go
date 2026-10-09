package aliyun

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/gotomicro/ego/core/elog"
)

// ResourceQueryAdapterImpl 阿里云资源查询适配器
type ResourceQueryAdapterImpl struct {
	client *Client
	logger *elog.Component
}

// NewResourceQueryAdapter 创建阿里云资源查询适配器
func NewResourceQueryAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *ResourceQueryAdapterImpl {
	return &ResourceQueryAdapterImpl{
		client: NewClient(accessKeyID, accessKeySecret, defaultRegion, logger),
		logger: logger,
	}
}

// ListAvailableInstanceTypes 查询可用实例规格
func (a *ResourceQueryAdapterImpl) ListAvailableInstanceTypes(ctx context.Context, region string) ([]types.InstanceTypeInfo, error) {
	if err := a.client.WaitRateLimit(ctx); err != nil {
		return nil, err
	}
	ecsClient, err := a.client.GetECSClient(region)
	if err != nil {
		return nil, fmt.Errorf("阿里云: 创建客户端失败: %w", err)
	}

	request := ecs.CreateDescribeInstanceTypesRequest()
	request.Scheme = "https"

	var response *ecs.DescribeInstanceTypesResponse
	err = a.client.RetryWithBackoff(ctx, func() error {
		var e error
		response, e = ecsClient.DescribeInstanceTypes(request)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("阿里云: 查询实例规格失败: %w", err)
	}

	result := make([]types.InstanceTypeInfo, 0, len(response.InstanceTypes.InstanceType))
	for _, it := range response.InstanceTypes.InstanceType {
		// 过滤掉第一代实例规格（不支持 VPC ENI，创建时会报错）
		// 第一代规格前缀: ecs.t1, ecs.s1, ecs.s2, ecs.s3, ecs.m1, ecs.m2, ecs.c1, ecs.c2, ecs.n1
		if isLegacyInstanceType(it.InstanceTypeId) {
			continue
		}
		result = append(result, types.InstanceTypeInfo{
			InstanceType: it.InstanceTypeId,
			CPU:          it.CpuCoreCount,
			MemoryGB:     float64(it.MemorySize),
			Architecture: it.CpuArchitecture,
		})
	}
	return result, nil
}

// isLegacyInstanceType 判断是否为不支持 ENI 的第一代实例规格
func isLegacyInstanceType(instanceType string) bool {
	legacyPrefixes := []string{
		"ecs.t1.", "ecs.s1.", "ecs.s2.", "ecs.s3.",
		"ecs.m1.", "ecs.m2.", "ecs.c1.", "ecs.c2.",
		"ecs.n1.",
	}
	for _, prefix := range legacyPrefixes {
		if len(instanceType) >= len(prefix) && instanceType[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// ListAvailableImages 查询可用镜像
func (a *ResourceQueryAdapterImpl) ListAvailableImages(ctx context.Context, region string) ([]types.ImageInfo, error) {
	if err := a.client.WaitRateLimit(ctx); err != nil {
		return nil, err
	}
	ecsClient, err := a.client.GetECSClient(region)
	if err != nil {
		return nil, fmt.Errorf("阿里云: 创建客户端失败: %w", err)
	}

	request := ecs.CreateDescribeImagesRequest()
	request.Scheme = "https"
	request.RegionId = region
	request.Status = "Available"
	request.ImageOwnerAlias = "system"
	request.PageSize = requests.NewInteger(100)

	var response *ecs.DescribeImagesResponse
	err = a.client.RetryWithBackoff(ctx, func() error {
		var e error
		response, e = ecsClient.DescribeImages(request)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("阿里云: 查询镜像失败: %w", err)
	}

	result := make([]types.ImageInfo, 0, len(response.Images.Image))
	for _, img := range response.Images.Image {
		result = append(result, types.ImageInfo{
			ImageID:      img.ImageId,
			Name:         img.ImageName,
			OSType:       img.OSType,
			Platform:     img.Platform,
			Architecture: img.Architecture,
		})
	}
	return result, nil
}

// ListVPCs 查询 VPC 列表
func (a *ResourceQueryAdapterImpl) ListVPCs(ctx context.Context, region string) ([]types.VPCInfo, error) {
	if err := a.client.WaitRateLimit(ctx); err != nil {
		return nil, err
	}
	ecsClient, err := a.client.GetECSClient(region)
	if err != nil {
		return nil, fmt.Errorf("阿里云: 创建客户端失败: %w", err)
	}

	request := ecs.CreateDescribeVpcsRequest()
	request.Scheme = "https"
	request.RegionId = region

	var response *ecs.DescribeVpcsResponse
	err = a.client.RetryWithBackoff(ctx, func() error {
		var e error
		response, e = ecsClient.DescribeVpcs(request)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("阿里云: 查询VPC失败: %w", err)
	}

	result := make([]types.VPCInfo, 0, len(response.Vpcs.Vpc))
	for _, vpc := range response.Vpcs.Vpc {
		result = append(result, types.VPCInfo{
			VPCID:     vpc.VpcId,
			VPCName:   vpc.VpcName,
			CidrBlock: vpc.CidrBlock,
			Status:    vpc.Status,
		})
	}
	return result, nil
}

// ListSubnets 查询子网/交换机列表（带分页）
func (a *ResourceQueryAdapterImpl) ListSubnets(ctx context.Context, region, vpcID string) ([]types.SubnetInfo, error) {
	if err := a.client.WaitRateLimit(ctx); err != nil {
		return nil, err
	}
	ecsClient, err := a.client.GetECSClient(region)
	if err != nil {
		return nil, fmt.Errorf("阿里云: 创建客户端失败: %w", err)
	}

	var result []types.SubnetInfo
	pageNumber := 1
	pageSize := 50

	for {
		request := ecs.CreateDescribeVSwitchesRequest()
		request.Scheme = "https"
		request.RegionId = region
		request.VpcId = vpcID
		request.PageNumber = requests.NewInteger(pageNumber)
		request.PageSize = requests.NewInteger(pageSize)

		var response *ecs.DescribeVSwitchesResponse
		err = a.client.RetryWithBackoff(ctx, func() error {
			var e error
			response, e = ecsClient.DescribeVSwitches(request)
			return e
		})
		if err != nil {
			return nil, fmt.Errorf("阿里云: 查询交换机失败: %w", err)
		}

		for _, vsw := range response.VSwitches.VSwitch {
			result = append(result, types.SubnetInfo{
				SubnetID:  vsw.VSwitchId,
				Name:      vsw.VSwitchName,
				CidrBlock: vsw.CidrBlock,
				Zone:      vsw.ZoneId,
				VPCID:     vsw.VpcId,
			})
		}

		if len(response.VSwitches.VSwitch) < pageSize {
			break
		}
		pageNumber++
	}
	return result, nil
}

// ListSecurityGroups 查询安全组列表
func (a *ResourceQueryAdapterImpl) ListSecurityGroups(ctx context.Context, region, vpcID string) ([]types.SecurityGroupInfo, error) {
	if err := a.client.WaitRateLimit(ctx); err != nil {
		return nil, err
	}
	ecsClient, err := a.client.GetECSClient(region)
	if err != nil {
		return nil, fmt.Errorf("阿里云: 创建客户端失败: %w", err)
	}

	request := ecs.CreateDescribeSecurityGroupsRequest()
	request.Scheme = "https"
	request.RegionId = region
	request.VpcId = vpcID

	var response *ecs.DescribeSecurityGroupsResponse
	err = a.client.RetryWithBackoff(ctx, func() error {
		var e error
		response, e = ecsClient.DescribeSecurityGroups(request)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("阿里云: 查询安全组失败: %w", err)
	}

	result := make([]types.SecurityGroupInfo, 0, len(response.SecurityGroups.SecurityGroup))
	for _, sg := range response.SecurityGroups.SecurityGroup {
		result = append(result, types.SecurityGroupInfo{
			SecurityGroupID: sg.SecurityGroupId,
			Name:            sg.SecurityGroupName,
			Description:     sg.Description,
			VPCID:           sg.VpcId,
		})
	}
	return result, nil
}
