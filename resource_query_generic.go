package cloudx

import (
	"context"

	"github.com/Havens-blog/e-cloudx-sdk/types"
)

// GenericResourceQueryAdapter 通用资源查询适配器
// 复用已有的 VPCAdapter、VSwitchAdapter、SecurityGroupAdapter、ImageAdapter 来提供资源查询能力
// 适用于所有已实现这些适配器的云厂商
type GenericResourceQueryAdapter struct {
	vpcAdapter           VPCAdapter
	vSwitchAdapter       VSwitchAdapter
	securityGroupAdapter SecurityGroupAdapter
	imageAdapter         ImageAdapter
	ecsAdapter           ECSAdapter
}

// NewGenericResourceQueryAdapter 从 CloudAdapter 创建通用资源查询适配器
func NewGenericResourceQueryAdapter(adapter CloudAdapter) *GenericResourceQueryAdapter {
	return &GenericResourceQueryAdapter{
		vpcAdapter:           adapter.VPC(),
		vSwitchAdapter:       adapter.VSwitch(),
		securityGroupAdapter: adapter.SecurityGroup(),
		imageAdapter:         adapter.Image(),
		ecsAdapter:           adapter.ECS(),
	}
}

// ListAvailableInstanceTypes 查询可用实例规格
// 注意：通用实现暂时返回空列表，因为没有统一的实例规格查询接口
// 各厂商如果有专用的 ResourceQueryAdapter 实现，会优先使用
func (a *GenericResourceQueryAdapter) ListAvailableInstanceTypes(ctx context.Context, region string) ([]types.InstanceTypeInfo, error) {
	// 通用适配器无法查询实例规格（需要厂商专用 API）
	return []types.InstanceTypeInfo{}, nil
}

// ListAvailableImages 查询可用镜像（复用 ImageAdapter）
// 查询公共镜像（系统镜像），用于创建实例时的镜像选择
func (a *GenericResourceQueryAdapter) ListAvailableImages(ctx context.Context, region string) ([]types.ImageInfo, error) {
	if a.imageAdapter == nil {
		return []types.ImageInfo{}, nil
	}

	// 使用 filter 查询公共镜像（系统镜像），而非默认的私有镜像
	images, err := a.imageAdapter.ListInstancesWithFilter(ctx, region, &types.ImageFilter{
		ImageOwnerAlias: "system",
		Status:          "Available",
	})
	if err != nil {
		return nil, err
	}

	result := make([]types.ImageInfo, 0, len(images))
	for _, img := range images {
		result = append(result, types.ImageInfo{
			ImageID:      img.ImageID,
			Name:         img.ImageName,
			OSType:       img.OSType,
			Platform:     img.Platform,
			Architecture: img.Architecture,
		})
	}
	return result, nil
}

// ListVPCs 查询 VPC 列表（复用 VPCAdapter）
func (a *GenericResourceQueryAdapter) ListVPCs(ctx context.Context, region string) ([]types.VPCInfo, error) {
	if a.vpcAdapter == nil {
		return []types.VPCInfo{}, nil
	}

	vpcs, err := a.vpcAdapter.ListInstances(ctx, region)
	if err != nil {
		return nil, err
	}

	result := make([]types.VPCInfo, 0, len(vpcs))
	for _, vpc := range vpcs {
		result = append(result, types.VPCInfo{
			VPCID:     vpc.VPCID,
			VPCName:   vpc.VPCName,
			CidrBlock: vpc.CidrBlock,
			Status:    vpc.Status,
		})
	}
	return result, nil
}

// ListSubnets 查询子网列表（复用 VSwitchAdapter，按 VPC 过滤）
func (a *GenericResourceQueryAdapter) ListSubnets(ctx context.Context, region, vpcID string) ([]types.SubnetInfo, error) {
	if a.vSwitchAdapter == nil {
		return []types.SubnetInfo{}, nil
	}

	vswitches, err := a.vSwitchAdapter.ListInstances(ctx, region)
	if err != nil {
		return nil, err
	}

	result := make([]types.SubnetInfo, 0)
	for _, vsw := range vswitches {
		// 按 VPC ID 过滤
		if vpcID != "" && vsw.VPCID != vpcID {
			continue
		}
		result = append(result, types.SubnetInfo{
			SubnetID:  vsw.VSwitchID,
			Name:      vsw.VSwitchName,
			CidrBlock: vsw.CidrBlock,
			Zone:      vsw.Zone,
			VPCID:     vsw.VPCID,
		})
	}
	return result, nil
}

// ListSecurityGroups 查询安全组列表（复用 SecurityGroupAdapter，按 VPC 过滤）
func (a *GenericResourceQueryAdapter) ListSecurityGroups(ctx context.Context, region, vpcID string) ([]types.SecurityGroupInfo, error) {
	if a.securityGroupAdapter == nil {
		return []types.SecurityGroupInfo{}, nil
	}

	sgs, err := a.securityGroupAdapter.ListInstances(ctx, region)
	if err != nil {
		return nil, err
	}

	result := make([]types.SecurityGroupInfo, 0)
	for _, sg := range sgs {
		// 按 VPC ID 过滤
		if vpcID != "" && sg.VPCID != vpcID {
			continue
		}
		result = append(result, types.SecurityGroupInfo{
			SecurityGroupID: sg.SecurityGroupID,
			Name:            sg.SecurityGroupName,
			Description:     sg.Description,
			VPCID:           sg.VPCID,
		})
	}
	return result, nil
}

// Ensure compile-time interface compliance
var _ ResourceQueryAdapter = (*GenericResourceQueryAdapter)(nil)
