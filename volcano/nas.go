package volcano

import (
	"context"
	"fmt"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/filenas"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// NASAdapter 火山引擎NAS适配器
type NASAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
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
func (a *NASAdapter) createClient(region string) (*filenas.FILENAS, error) {
	if region == "" {
		region = a.defaultRegion
	}

	config := volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(a.accessKeyID, a.accessKeySecret, "")).
		WithRegion(region)

	sess, err := session.NewSession(config)
	if err != nil {
		return nil, fmt.Errorf("创建会话失败: %w", err)
	}

	return filenas.New(sess), nil
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
		input := &filenas.DescribeFileSystemsInput{
			PageNumber: &pageNumber,
			PageSize:   &pageSize,
		}

		output, err := client.DescribeFileSystems(input)
		if err != nil {
			return nil, fmt.Errorf("获取NAS文件系统列表失败: %w", err)
		}

		if output.FileSystems == nil || len(output.FileSystems) == 0 {
			break
		}

		for _, fs := range output.FileSystems {
			instance := a.convertToNASInstance(fs, region)
			instances = append(instances, instance)
		}

		if len(output.FileSystems) < int(pageSize) {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取火山引擎NAS文件系统列表成功",
		elog.String("region", region),
		elog.Int("count", len(instances)))

	return instances, nil
}

// GetInstance 获取单个NAS文件系统详情
func (a *NASAdapter) GetInstance(ctx context.Context, region, fileSystemID string) (*types.NASInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建NAS客户端失败: %w", err)
	}

	input := &filenas.DescribeFileSystemsInput{
		FileSystemIds: &fileSystemID,
	}

	output, err := client.DescribeFileSystems(input)
	if err != nil {
		return nil, fmt.Errorf("获取NAS文件系统详情失败: %w", err)
	}

	if output.FileSystems == nil || len(output.FileSystems) == 0 {
		return nil, fmt.Errorf("NAS文件系统不存在: %s", fileSystemID)
	}

	instance := a.convertToNASInstance(output.FileSystems[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取NAS文件系统
func (a *NASAdapter) ListInstancesByIDs(ctx context.Context, region string, fileSystemIDs []string) ([]types.NASInstance, error) {
	var instances []types.NASInstance
	for _, id := range fileSystemIDs {
		instance, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取NAS文件系统失败",
				elog.String("file_system_id", id),
				elog.FieldErr(err))
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

	input := &filenas.DescribeFileSystemsInput{}

	if filter != nil {
		if filter.FileSystemType != "" {
			input.FileSystemType = &filter.FileSystemType
		}
		if filter.PageNumber > 0 {
			pn := int32(filter.PageNumber)
			input.PageNumber = &pn
		}
		if filter.PageSize > 0 {
			ps := int32(filter.PageSize)
			input.PageSize = &ps
		}

		// 添加过滤条件
		var filters []*filenas.FilterForDescribeFileSystemsInput
		if filter.ProtocolType != "" {
			filters = append(filters, &filenas.FilterForDescribeFileSystemsInput{
				Key:   volcengine.String("ProtocolType"),
				Value: volcengine.String(filter.ProtocolType),
			})
		}
		if len(filter.Status) > 0 {
			filters = append(filters, &filenas.FilterForDescribeFileSystemsInput{
				Key:   volcengine.String("Status"),
				Value: volcengine.String(filter.Status[0]),
			})
		}
		if len(filters) > 0 {
			input.Filters = filters
		}
	}

	output, err := client.DescribeFileSystems(input)
	if err != nil {
		return nil, fmt.Errorf("获取NAS文件系统列表失败: %w", err)
	}

	var instances []types.NASInstance
	if output.FileSystems != nil {
		for _, fs := range output.FileSystems {
			instance := a.convertToNASInstance(fs, region)
			instances = append(instances, instance)
		}
	}

	return instances, nil
}

// convertToNASInstance 转换为统一的NAS实例结构
func (a *NASAdapter) convertToNASInstance(fs *filenas.FileSystemForDescribeFileSystemsOutput, region string) types.NASInstance {
	instance := types.NASInstance{
		Region:   region,
		Provider: "volcano",
	}

	if fs.FileSystemId != nil {
		instance.FileSystemID = *fs.FileSystemId
	}
	if fs.FileSystemName != nil {
		instance.FileSystemName = *fs.FileSystemName
	}
	if fs.Description != nil {
		instance.Description = *fs.Description
	}
	if fs.Status != nil {
		// 状态统一走 types.NASStatus 标准化(原本地 map 与 types/nas.go 的 volcano 分表一致)
		instance.Status = types.NASStatus("volcano", *fs.Status)
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
		if fs.Capacity.Total != nil {
			instance.Capacity = *fs.Capacity.Total
		}
		if fs.Capacity.Used != nil {
			instance.UsedCapacity = *fs.Capacity.Used
		}
	}
	if fs.ChargeType != nil {
		instance.ChargeType = *fs.ChargeType
	}
	if fs.CreateTime != nil {
		if t, err := time.Parse("2006-01-02T15:04:05Z", *fs.CreateTime); err == nil {
			instance.CreationTime = t
		} else if t, err := time.Parse(time.RFC3339, *fs.CreateTime); err == nil {
			instance.CreationTime = t
		}
	}

	// 解析标签
	if fs.Tags != nil {
		instance.Tags = make(map[string]string)
		for _, tag := range fs.Tags {
			if tag.Key != nil && tag.Value != nil {
				instance.Tags[*tag.Key] = *tag.Value
			}
		}
	}

	return instance
}
