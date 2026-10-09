package aws

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/gotomicro/ego/core/elog"
)

// ResourceQueryAdapterImpl AWS资源查询适配器
// 实例规格通过 AWS EC2 API 真实查询，镜像/VPC/子网/安全组委托给 GenericResourceQueryAdapter
type ResourceQueryAdapterImpl struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
	generic         *cloudx.GenericResourceQueryAdapter
}

// NewResourceQueryAdapter 创建AWS资源查询适配器
func NewResourceQueryAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component, adapter cloudx.CloudAdapter) *ResourceQueryAdapterImpl {
	return &ResourceQueryAdapterImpl{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
		generic:         cloudx.NewGenericResourceQueryAdapter(adapter),
	}
}

func (a *ResourceQueryAdapterImpl) getClient(region string) *ec2.Client {
	if region == "" {
		region = a.defaultRegion
	}
	cfg := awssdk.Config{
		Region: region,
		Credentials: credentials.NewStaticCredentialsProvider(
			a.accessKeyID,
			a.accessKeySecret,
			"",
		),
	}
	return ec2.NewFromConfig(cfg)
}

// ListAvailableInstanceTypes 查询可用实例规格（真实 API 调用）
func (a *ResourceQueryAdapterImpl) ListAvailableInstanceTypes(ctx context.Context, region string) ([]types.InstanceTypeInfo, error) {
	client := a.getClient(region)

	result := make([]types.InstanceTypeInfo, 0)
	paginator := ec2.NewDescribeInstanceTypesPaginator(client, &ec2.DescribeInstanceTypesInput{})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("AWS: 查询实例规格失败: %w", err)
		}

		for _, it := range page.InstanceTypes {
			cpu := 0
			if it.VCpuInfo != nil && it.VCpuInfo.DefaultVCpus != nil {
				cpu = int(*it.VCpuInfo.DefaultVCpus)
			}
			memGB := float64(0)
			if it.MemoryInfo != nil && it.MemoryInfo.SizeInMiB != nil {
				// SizeInMiB 单位为 MiB，转换为 GiB
				memGB = float64(*it.MemoryInfo.SizeInMiB) / 1024
			}

			result = append(result, types.InstanceTypeInfo{
				InstanceType: string(it.InstanceType),
				CPU:          cpu,
				MemoryGB:     memGB,
				Architecture: awsArchToString(it.ProcessorInfo),
			})
		}
	}

	a.logger.Info("AWS: 查询实例规格成功",
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

// awsArchToString 从 ProcessorInfo 提取架构字符串
func awsArchToString(info *ec2types.ProcessorInfo) string {
	if info == nil || len(info.SupportedArchitectures) == 0 {
		return ""
	}
	return string(info.SupportedArchitectures[0])
}
