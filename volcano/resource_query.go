package volcano

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/ecs"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// ResourceQueryAdapterImpl 火山引擎资源查询适配器
// 实例规格和镜像通过火山引擎 API 真实查询，VPC/子网/安全组委托给 GenericResourceQueryAdapter
type ResourceQueryAdapterImpl struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
	generic         *cloudx.GenericResourceQueryAdapter
}

// NewResourceQueryAdapter 创建火山引擎资源查询适配器
func NewResourceQueryAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component, adapter cloudx.CloudAdapter) *ResourceQueryAdapterImpl {
	return &ResourceQueryAdapterImpl{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
		generic:         cloudx.NewGenericResourceQueryAdapter(adapter),
	}
}

func (a *ResourceQueryAdapterImpl) getClient(region string) (*ecs.ECS, error) {
	if region == "" {
		region = a.defaultRegion
	}
	config := volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(a.accessKeyID, a.accessKeySecret, "")).
		WithRegion(region)
	sess, err := session.NewSession(config)
	if err != nil {
		return nil, fmt.Errorf("创建火山引擎会话失败: %w", err)
	}
	return ecs.New(sess), nil
}

// ListAvailableInstanceTypes 查询可用实例规格（真实 API 调用，带分页）
func (a *ResourceQueryAdapterImpl) ListAvailableInstanceTypes(ctx context.Context, region string) ([]types.InstanceTypeInfo, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	result := make([]types.InstanceTypeInfo, 0)
	var nextToken *string
	maxResults := int32(100)

	for {
		input := &ecs.DescribeInstanceTypesInput{
			MaxResults: &maxResults,
			NextToken:  nextToken,
		}
		resp, err := client.DescribeInstanceTypes(input)
		if err != nil {
			return nil, fmt.Errorf("火山引擎: 查询实例规格失败: %w", err)
		}

		if resp.InstanceTypes != nil {
			for _, it := range resp.InstanceTypes {
				cpu := 0
				if it.Processor != nil && it.Processor.Cpus != nil {
					cpu = int(volcengine.Int32Value(it.Processor.Cpus))
				}
				memGB := float64(0)
				if it.Memory != nil && it.Memory.Size != nil {
					raw := float64(volcengine.Int32Value(it.Memory.Size))
					// 火山引擎 Memory.Size 单位为 MiB，转换为 GiB
					if raw > 256 {
						memGB = raw / 1024
					} else {
						memGB = raw
					}
				}
				result = append(result, types.InstanceTypeInfo{
					InstanceType: volcengine.StringValue(it.InstanceTypeId),
					CPU:          cpu,
					MemoryGB:     memGB,
				})
			}
		}

		// 检查是否还有下一页
		if resp.NextToken == nil || volcengine.StringValue(resp.NextToken) == "" {
			break
		}
		nextToken = resp.NextToken
	}

	a.logger.Info("火山引擎: 查询实例规格成功",
		elog.String("region", region), elog.Int("count", len(result)))
	return result, nil
}

// ListAvailableImages 查询可用镜像（真实 API 调用）
func (a *ResourceQueryAdapterImpl) ListAvailableImages(ctx context.Context, region string) ([]types.ImageInfo, error) {
	// 委托给 generic（复用已有的 ImageAdapter）
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
