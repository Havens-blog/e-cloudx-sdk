package aws

import (
	"context"
	"fmt"
	"slices"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"
	"github.com/gotomicro/ego/core/elog"
)

// EFSAdapter AWS EFS (Elastic File System) 适配器
type EFSAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component

	// cwHooks NAS 指标查询(CloudWatch)测试注入钩子(非 nil 时替代真实
	// CloudWatch 客户端创建;仅单测使用),见 nas_metrics.go
	cwHooks *cwMetricHooks
}

// NewEFSAdapter 创建 EFS 适配器
func NewEFSAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *EFSAdapter {
	return &EFSAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建 EFS 客户端
func (a *EFSAdapter) createClient(ctx context.Context, region string) (*efs.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			a.accessKeyID,
			a.accessKeySecret,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("加载AWS配置失败: %w", err)
	}

	return efs.NewFromConfig(cfg), nil
}

// ListInstances 获取 EFS 文件系统列表
func (a *EFSAdapter) ListInstances(ctx context.Context, region string) ([]types.NASInstance, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	var instances []types.NASInstance
	var marker *string

	for {
		input := &efs.DescribeFileSystemsInput{
			Marker:   marker,
			MaxItems: aws.Int32(100),
		}

		output, err := client.DescribeFileSystems(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("获取EFS文件系统列表失败: %w", err)
		}

		for _, fs := range output.FileSystems {
			instance := a.convertToNASInstance(&fs, region)
			instances = append(instances, instance)
		}

		if output.NextMarker == nil {
			break
		}
		marker = output.NextMarker
	}

	return instances, nil
}

// GetInstance 获取单个 EFS 文件系统详情
func (a *EFSAdapter) GetInstance(ctx context.Context, region, fileSystemID string) (*types.NASInstance, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &efs.DescribeFileSystemsInput{
		FileSystemId: aws.String(fileSystemID),
	}

	output, err := client.DescribeFileSystems(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("获取EFS文件系统详情失败: %w", err)
	}

	if len(output.FileSystems) == 0 {
		return nil, fmt.Errorf("EFS文件系统不存在: %s", fileSystemID)
	}

	instance := a.convertToNASInstance(&output.FileSystems[0], region)

	// 获取挂载点信息
	mountTargets, err := a.getMountTargets(ctx, client, fileSystemID)
	if err == nil {
		instance.MountTargets = mountTargets
		if len(mountTargets) > 0 {
			instance.VPCID = mountTargets[0].VPCID
			instance.VSwitchID = mountTargets[0].VSwitchID
		}
	}

	return &instance, nil
}

// getMountTargets 获取挂载点列表
func (a *EFSAdapter) getMountTargets(ctx context.Context, client *efs.Client, fileSystemID string) ([]types.MountTarget, error) {
	input := &efs.DescribeMountTargetsInput{
		FileSystemId: aws.String(fileSystemID),
	}

	output, err := client.DescribeMountTargets(ctx, input)
	if err != nil {
		return nil, err
	}

	var mountTargets []types.MountTarget
	for _, mt := range output.MountTargets {
		mountTarget := types.MountTarget{
			NetworkType: "VPC",
		}
		if mt.MountTargetId != nil {
			mountTarget.MountTargetID = *mt.MountTargetId
		}
		if mt.IpAddress != nil {
			mountTarget.MountTargetDomain = *mt.IpAddress
		}
		if mt.VpcId != nil {
			mountTarget.VPCID = *mt.VpcId
		}
		if mt.SubnetId != nil {
			mountTarget.VSwitchID = *mt.SubnetId
		}
		if mt.LifeCycleState != "" {
			mountTarget.Status = string(mt.LifeCycleState)
		}
		mountTargets = append(mountTargets, mountTarget)
	}

	return mountTargets, nil
}

// ListInstancesByIDs 批量获取 EFS 文件系统
func (a *EFSAdapter) ListInstancesByIDs(ctx context.Context, region string, fileSystemIDs []string) ([]types.NASInstance, error) {
	var instances []types.NASInstance
	for _, id := range fileSystemIDs {
		instance, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取EFS文件系统失败", elog.String("file_system_id", id), elog.FieldErr(err))
			continue
		}
		instances = append(instances, *instance)
	}
	return instances, nil
}

// GetInstanceStatus 获取文件系统状态
func (a *EFSAdapter) GetInstanceStatus(ctx context.Context, region, fileSystemID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, fileSystemID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取文件系统列表
func (a *EFSAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.NASInstanceFilter) ([]types.NASInstance, error) {
	instances, err := a.ListInstances(ctx, region)
	if err != nil {
		return nil, err
	}

	if filter == nil {
		return instances, nil
	}

	var filtered []types.NASInstance
	for _, inst := range instances {
		// 按状态过滤
		if len(filter.Status) > 0 && !slices.Contains(filter.Status, inst.Status) {
			continue
		}
		// 按文件系统类型过滤
		if filter.FileSystemType != "" && inst.FileSystemType != filter.FileSystemType {
			continue
		}
		filtered = append(filtered, inst)
	}

	return filtered, nil
}

// convertToNASInstance 转换为统一的 NAS 实例结构
func (a *EFSAdapter) convertToNASInstance(fs *efstypes.FileSystemDescription, region string) types.NASInstance {
	instance := types.NASInstance{
		Region:       region,
		Provider:     string(domain.CloudProviderAWS),
		ProtocolType: "NFS", // EFS 只支持 NFS
	}

	if fs.FileSystemId != nil {
		instance.FileSystemID = *fs.FileSystemId
	}
	if fs.Name != nil {
		instance.FileSystemName = *fs.Name
		instance.Description = *fs.Name
	}
	if fs.LifeCycleState != "" {
		// 状态统一走 types.NASStatus 标准化(原本地映射表已并入 types/nas.go 的 aws 分表)
		instance.Status = types.NASStatus("aws", string(fs.LifeCycleState))
	}
	if fs.PerformanceMode != "" {
		instance.FileSystemType = string(fs.PerformanceMode) // generalPurpose / maxIO
	}
	if fs.ThroughputMode != "" {
		instance.StorageType = string(fs.ThroughputMode) // bursting / provisioned / elastic
	}
	if fs.SizeInBytes != nil {
		instance.MeteredSize = fs.SizeInBytes.Value
		instance.UsedCapacity = fs.SizeInBytes.Value / (1024 * 1024 * 1024) // 转换为 GB
	}
	if fs.Encrypted != nil && *fs.Encrypted {
		instance.EncryptType = 1
		if fs.KmsKeyId != nil {
			instance.KMSKeyID = *fs.KmsKeyId
		}
	}
	if fs.CreationTime != nil {
		instance.CreationTime = *fs.CreationTime
	}

	// 解析标签
	if len(fs.Tags) > 0 {
		instance.Tags = make(map[string]string)
		for _, tag := range fs.Tags {
			if tag.Key != nil && tag.Value != nil {
				instance.Tags[*tag.Key] = *tag.Value
			}
		}
	}

	return instance
}
