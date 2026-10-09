package tencent

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	cvm "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cvm/v20170312"
)

// ResourceQueryAdapterImpl 腾讯云资源查询适配器
// 实例规格通过腾讯云 CVM API 真实查询，镜像/VPC/子网/安全组委托给 GenericResourceQueryAdapter
type ResourceQueryAdapterImpl struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
	generic         *cloudx.GenericResourceQueryAdapter
}

// NewResourceQueryAdapter 创建腾讯云资源查询适配器
func NewResourceQueryAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component, adapter cloudx.CloudAdapter) *ResourceQueryAdapterImpl {
	return &ResourceQueryAdapterImpl{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
		generic:         cloudx.NewGenericResourceQueryAdapter(adapter),
	}
}

func (a *ResourceQueryAdapterImpl) getClient(region string) (*cvm.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}
	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "cvm.tencentcloudapi.com"

	client, err := cvm.NewClient(credential, region, cpf)
	if err != nil {
		return nil, fmt.Errorf("腾讯云: 创建CVM客户端失败: %w", err)
	}
	return client, nil
}

// ListAvailableInstanceTypes 查询可用实例规格（真实 API 调用）
func (a *ResourceQueryAdapterImpl) ListAvailableInstanceTypes(ctx context.Context, region string) ([]types.InstanceTypeInfo, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	request := cvm.NewDescribeInstanceTypeConfigsRequest()
	response, err := client.DescribeInstanceTypeConfigs(request)
	if err != nil {
		return nil, fmt.Errorf("腾讯云: 查询实例规格失败: %w", err)
	}

	result := make([]types.InstanceTypeInfo, 0, len(response.Response.InstanceTypeConfigSet))
	for _, it := range response.Response.InstanceTypeConfigSet {
		cpu := 0
		if it.CPU != nil {
			cpu = int(*it.CPU)
		}
		memGB := float64(0)
		if it.Memory != nil {
			// Memory 单位已经是 GiB
			memGB = float64(*it.Memory)
		}
		instanceType := ""
		if it.InstanceType != nil {
			instanceType = *it.InstanceType
		}
		result = append(result, types.InstanceTypeInfo{
			InstanceType: instanceType,
			CPU:          cpu,
			MemoryGB:     memGB,
		})
	}

	a.logger.Info("腾讯云: 查询实例规格成功",
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
