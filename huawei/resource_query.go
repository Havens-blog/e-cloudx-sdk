package huawei

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	ecs "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2"
	ecsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2/model"
	ecsregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2/region"
)

// ResourceQueryAdapterImpl 华为云资源查询适配器
// 实例规格通过华为云 ECS API 真实查询，镜像/VPC/子网/安全组委托给 GenericResourceQueryAdapter
type ResourceQueryAdapterImpl struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
	generic         *cloudx.GenericResourceQueryAdapter
}

// NewResourceQueryAdapter 创建华为云资源查询适配器
func NewResourceQueryAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component, adapter cloudx.CloudAdapter) *ResourceQueryAdapterImpl {
	return &ResourceQueryAdapterImpl{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
		generic:         cloudx.NewGenericResourceQueryAdapter(adapter),
	}
}

func (a *ResourceQueryAdapterImpl) getClient(region string) (*ecs.EcsClient, error) {
	if region == "" {
		region = a.defaultRegion
	}
	auth, err := basic.NewCredentialsBuilder().
		WithAk(a.accessKeyID).
		WithSk(a.accessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("华为云: 创建凭证失败: %w", err)
	}

	regionObj, err := ecsregion.SafeValueOf(region)
	if err != nil {
		return nil, fmt.Errorf("华为云: 不支持的地域: %s", region)
	}

	hcClient, err := ecs.EcsClientBuilder().
		WithRegion(regionObj).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("华为云: 创建ECS客户端失败: %w", err)
	}

	return ecs.NewEcsClient(hcClient), nil
}

// ListAvailableInstanceTypes 查询可用实例规格（真实 API 调用）
func (a *ResourceQueryAdapterImpl) ListAvailableInstanceTypes(ctx context.Context, region string) ([]types.InstanceTypeInfo, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	request := &ecsmodel.ListFlavorsRequest{}
	response, err := client.ListFlavors(request)
	if err != nil {
		return nil, fmt.Errorf("华为云: 查询实例规格失败: %w", err)
	}

	result := make([]types.InstanceTypeInfo, 0)
	if response.Flavors != nil {
		for _, f := range *response.Flavors {
			cpu, _ := strconv.Atoi(f.Vcpus)
			// Ram 单位为 MB，转换为 GB
			memGB := float64(f.Ram) / 1024

			result = append(result, types.InstanceTypeInfo{
				InstanceType: f.Id,
				CPU:          cpu,
				MemoryGB:     memGB,
			})
		}
	}

	a.logger.Info("华为云: 查询实例规格成功",
		elog.String("region", region), elog.Int("count", len(result)))
	return result, nil
}

// ListAvailableImages 查询可用镜像（委托给 generic）
func (a *ResourceQueryAdapterImpl) ListAvailableImages(ctx context.Context, region string) ([]types.ImageInfo, error) {
	return a.generic.ListAvailableImages(ctx, region)
}

// ListVPCs 查询 VPC 列表（委托给 generic）
func (a *ResourceQueryAdapterImpl) ListVPCs(ctx context.Context, region string) ([]types.VPCInfo, error) {
	return a.generic.ListVPCs(ctx, region)
}

// ListSubnets 查询子网列表（委托给 generic）
func (a *ResourceQueryAdapterImpl) ListSubnets(ctx context.Context, region, vpcID string) ([]types.SubnetInfo, error) {
	return a.generic.ListSubnets(ctx, region, vpcID)
}

// ListSecurityGroups 查询安全组列表（委托给 generic）
func (a *ResourceQueryAdapterImpl) ListSecurityGroups(ctx context.Context, region, vpcID string) ([]types.SecurityGroupInfo, error) {
	return a.generic.ListSecurityGroups(ctx, region, vpcID)
}

// Ensure compile-time interface compliance
var _ cloudx.ResourceQueryAdapter = (*ResourceQueryAdapterImpl)(nil)
