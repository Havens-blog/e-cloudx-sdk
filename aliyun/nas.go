package aliyun

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	nas "github.com/alibabacloud-go/nas-20170626/v3/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/gotomicro/ego/core/elog"
)

// NASAdapter 阿里云NAS适配器
type NASAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component

	// NAS 指标查询(CMS)状态,见 nas_metrics.go
	metricMu      sync.Mutex
	metricClients map[string]cmsMetricClient
	userIDOnce    sync.Once
	userIDValue   string
	userIDErr     error
	// metricHooks 测试注入钩子(非 nil 时替代真实 CMS 客户端创建与账号 ID 解析)
	metricHooks *nasMetricHooks
}

// NewNASAdapter 创建NAS适配器
func NewNASAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *NASAdapter {
	return &NASAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建NAS客户端
func (a *NASAdapter) createClient(region string) (*nas.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}

	config := &openapi.Config{
		AccessKeyId:     tea.String(a.accessKeyID),
		AccessKeySecret: tea.String(a.accessKeySecret),
		RegionId:        tea.String(region),
	}
	config.Endpoint = tea.String(fmt.Sprintf("nas.%s.aliyuncs.com", region))

	return nas.NewClient(config)
}

// ListInstances 获取NAS文件系统列表
func (a *NASAdapter) ListInstances(ctx context.Context, region string) ([]types.NASInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建NAS客户端失败: %w", err)
	}

	var instances []types.NASInstance
	pageNumber := int32(1)
	pageSize := int32(100)

	for {
		request := &nas.DescribeFileSystemsRequest{
			PageNumber: tea.Int32(pageNumber),
			PageSize:   tea.Int32(pageSize),
		}

		response, err := client.DescribeFileSystems(request)
		if err != nil {
			return nil, fmt.Errorf("获取NAS文件系统列表失败: %w", err)
		}

		if response.Body == nil || response.Body.FileSystems == nil {
			break
		}

		for _, fs := range response.Body.FileSystems.FileSystem {
			instance := a.convertToNASInstance(fs, region)
			instances = append(instances, instance)
		}

		if len(response.Body.FileSystems.FileSystem) < int(pageSize) {
			break
		}
		pageNumber++
	}

	return instances, nil
}

// GetInstance 获取单个NAS文件系统详情
func (a *NASAdapter) GetInstance(ctx context.Context, region, fileSystemID string) (*types.NASInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建NAS客户端失败: %w", err)
	}

	request := &nas.DescribeFileSystemsRequest{
		FileSystemId: tea.String(fileSystemID),
	}

	response, err := client.DescribeFileSystems(request)
	if err != nil {
		return nil, fmt.Errorf("获取NAS文件系统详情失败: %w", err)
	}

	if response.Body == nil || response.Body.FileSystems == nil || len(response.Body.FileSystems.FileSystem) == 0 {
		return nil, fmt.Errorf("NAS文件系统不存在: %s", fileSystemID)
	}

	instance := a.convertToNASInstance(response.Body.FileSystems.FileSystem[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取NAS文件系统
func (a *NASAdapter) ListInstancesByIDs(ctx context.Context, region string, fileSystemIDs []string) ([]types.NASInstance, error) {
	var instances []types.NASInstance
	for _, id := range fileSystemIDs {
		instance, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取NAS文件系统失败", elog.String("file_system_id", id), elog.FieldErr(err))
			continue
		}
		instances = append(instances, *instance)
	}
	return instances, nil
}

// GetInstanceStatus 获取文件系统状态
func (a *NASAdapter) GetInstanceStatus(ctx context.Context, region, fileSystemID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, fileSystemID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取文件系统列表
func (a *NASAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.NASInstanceFilter) ([]types.NASInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建NAS客户端失败: %w", err)
	}

	request := &nas.DescribeFileSystemsRequest{}

	if filter != nil {
		if filter.FileSystemType != "" {
			request.FileSystemType = tea.String(filter.FileSystemType)
		}
		if filter.VPCID != "" {
			request.VpcId = tea.String(filter.VPCID)
		}
		if filter.PageNumber > 0 {
			request.PageNumber = tea.Int32(int32(filter.PageNumber))
		}
		if filter.PageSize > 0 {
			request.PageSize = tea.Int32(int32(filter.PageSize))
		}
	}

	response, err := client.DescribeFileSystems(request)
	if err != nil {
		return nil, fmt.Errorf("获取NAS文件系统列表失败: %w", err)
	}

	var instances []types.NASInstance
	if response.Body != nil && response.Body.FileSystems != nil {
		for _, fs := range response.Body.FileSystems.FileSystem {
			instance := a.convertToNASInstance(fs, region)

			// 应用额外过滤条件
			if filter != nil {
				if filter.ProtocolType != "" && instance.ProtocolType != filter.ProtocolType {
					continue
				}
				if len(filter.Status) > 0 && !slices.Contains(filter.Status, instance.Status) {
					continue
				}
			}

			instances = append(instances, instance)
		}
	}

	return instances, nil
}

// convertToNASInstance 转换为统一的NAS实例结构
func (a *NASAdapter) convertToNASInstance(fs *nas.DescribeFileSystemsResponseBodyFileSystemsFileSystem, region string) types.NASInstance {
	instance := types.NASInstance{
		Region:   region,
		Provider: "aliyun",
	}

	if fs.FileSystemId != nil {
		instance.FileSystemID = *fs.FileSystemId
	}
	if fs.Description != nil {
		instance.Description = *fs.Description
	}
	if fs.Status != nil {
		instance.Status = types.NASStatus("aliyun", *fs.Status)
	}
	if fs.ZoneId != nil {
		instance.Zone = *fs.ZoneId
	}
	if fs.FileSystemType != nil {
		instance.FileSystemType = *fs.FileSystemType
	}
	if fs.ProtocolType != nil {
		instance.ProtocolType = *fs.ProtocolType
	}
	if fs.StorageType != nil {
		instance.StorageType = *fs.StorageType
	}
	if fs.Capacity != nil {
		instance.Capacity = *fs.Capacity
	}
	if fs.MeteredSize != nil {
		instance.MeteredSize = *fs.MeteredSize
		// MeteredSize 是字节，转换为 GB 赋值给 UsedCapacity
		instance.UsedCapacity = *fs.MeteredSize / (1024 * 1024 * 1024)
	}
	if fs.ChargeType != nil {
		instance.ChargeType = *fs.ChargeType
	}
	if fs.CreateTime != nil {
		if t, err := time.Parse("2006-01-02T15:04:05Z", *fs.CreateTime); err == nil {
			instance.CreationTime = t
		}
	}
	if fs.EncryptType != nil {
		instance.EncryptType = int(*fs.EncryptType)
	}
	if fs.KMSKeyId != nil {
		instance.KMSKeyID = *fs.KMSKeyId
	}

	// 解析挂载点
	if fs.MountTargets != nil && fs.MountTargets.MountTarget != nil {
		for _, mt := range fs.MountTargets.MountTarget {
			mountTarget := types.MountTarget{}
			if mt.MountTargetDomain != nil {
				mountTarget.MountTargetDomain = *mt.MountTargetDomain
			}
			if mt.NetworkType != nil {
				mountTarget.NetworkType = *mt.NetworkType
			}
			if mt.VpcId != nil {
				mountTarget.VPCID = *mt.VpcId
				instance.VPCID = *mt.VpcId
			}
			if mt.VswId != nil {
				mountTarget.VSwitchID = *mt.VswId
				instance.VSwitchID = *mt.VswId
			}
			if mt.AccessGroupName != nil {
				mountTarget.AccessGroupName = *mt.AccessGroupName
			}
			if mt.Status != nil {
				mountTarget.Status = *mt.Status
			}
			instance.MountTargets = append(instance.MountTargets, mountTarget)
		}
	}

	// 解析标签
	if fs.Tags != nil && fs.Tags.Tag != nil {
		instance.Tags = make(map[string]string)
		for _, tag := range fs.Tags.Tag {
			if tag.Key != nil && tag.Value != nil {
				instance.Tags[*tag.Key] = *tag.Value
			}
		}
	}

	return instance
}
