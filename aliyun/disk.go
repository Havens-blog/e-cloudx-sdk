package aliyun

import (
	"context"
	"fmt"
	"sync"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/gotomicro/ego/core/elog"
)

// DiskAdapter 阿里云云盘适配器
type DiskAdapter struct {
	client *Client
	logger *elog.Component

	// accessKeyID/accessKeySecret Disk 指标路径 CMS 客户端凭证(与 client 同源)
	accessKeyID     string
	accessKeySecret string
	// metricMu/metricClients Disk 指标 CMS 客户端按 region 缓存(与 NASAdapter 同型)
	metricMu      sync.Mutex
	metricClients map[string]cmsMetricClient
	// diskMetricHooks Disk 指标查询测试注入钩子(非 nil 时替代真实依赖;仅单测使用)
	diskMetricHooks *diskMetricHooks
}

// NewDiskAdapter 创建阿里云云盘适配器
func NewDiskAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *DiskAdapter {
	return &DiskAdapter{
		client:          NewClient(accessKeyID, accessKeySecret, defaultRegion, logger),
		logger:          logger,
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
	}
}

// ListInstances 获取云盘列表
func (a *DiskAdapter) ListInstances(ctx context.Context, region string) ([]types.DiskInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个云盘详情
func (a *DiskAdapter) GetInstance(ctx context.Context, region, diskID string) (*types.DiskInstance, error) {
	instances, err := a.ListInstancesByIDs(ctx, region, []string{diskID})
	if err != nil {
		return nil, err
	}
	if len(instances) == 0 {
		return nil, fmt.Errorf("云盘不存在: %s", diskID)
	}
	return &instances[0], nil
}

// ListInstancesByIDs 批量获取云盘
func (a *DiskAdapter) ListInstancesByIDs(ctx context.Context, region string, diskIDs []string) ([]types.DiskInstance, error) {
	if len(diskIDs) == 0 {
		return nil, nil
	}

	filter := &types.DiskFilter{DiskIDs: diskIDs}
	return a.ListInstancesWithFilter(ctx, region, filter)
}

// GetInstanceStatus 获取云盘状态
func (a *DiskAdapter) GetInstanceStatus(ctx context.Context, region, diskID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, diskID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取云盘列表
func (a *DiskAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.DiskFilter) ([]types.DiskInstance, error) {
	if err := a.client.WaitRateLimit(ctx); err != nil {
		return nil, err
	}

	ecsClient, err := a.client.GetECSClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.DiskInstance
	pageNumber := 1
	pageSize := 50

	if filter != nil && filter.PageSize > 0 {
		pageSize = filter.PageSize
	}
	if filter != nil && filter.PageNumber > 0 {
		pageNumber = filter.PageNumber
	}

	for {
		request := ecs.CreateDescribeDisksRequest()
		request.Scheme = "https"
		request.RegionId = region
		request.PageNumber = requests.NewInteger(pageNumber)
		request.PageSize = requests.NewInteger(pageSize)

		// 应用过滤条件
		if filter != nil {
			if len(filter.DiskIDs) > 0 {
				request.DiskIds = fmt.Sprintf(`["%s"]`, joinStrings(filter.DiskIDs, `","`))
			}
			if filter.DiskName != "" {
				request.DiskName = filter.DiskName
			}
			if filter.DiskType != "" {
				request.DiskType = filter.DiskType
			}
			if filter.Category != "" {
				request.Category = filter.Category
			}
			if filter.Status != "" {
				request.Status = filter.Status
			}
			if filter.InstanceID != "" {
				request.InstanceId = filter.InstanceID
			}
			if filter.Portable != nil {
				request.Portable = requests.NewBoolean(*filter.Portable)
			}
			if filter.Encrypted != nil {
				request.Encrypted = requests.NewBoolean(*filter.Encrypted)
			}
			if filter.ResourceGroupID != "" {
				request.ResourceGroupId = filter.ResourceGroupID
			}
			if len(filter.Tags) > 0 {
				var tags []ecs.DescribeDisksTag
				for k, v := range filter.Tags {
					tags = append(tags, ecs.DescribeDisksTag{Key: k, Value: v})
				}
				request.Tag = &tags
			}
		}

		var response *ecs.DescribeDisksResponse
		err = a.client.RetryWithBackoff(ctx, func() error {
			var e error
			response, e = ecsClient.DescribeDisks(request)
			return e
		})

		if err != nil {
			return nil, fmt.Errorf("获取云盘列表失败: %w", err)
		}

		for _, disk := range response.Disks.Disk {
			instance := convertAliyunDisk(disk, region)
			allInstances = append(allInstances, instance)
		}

		// 如果指定了分页，只返回一页
		if filter != nil && filter.PageNumber > 0 {
			break
		}

		if len(response.Disks.Disk) < pageSize {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取阿里云云盘列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// ListByInstanceID 获取实例挂载的云盘
func (a *DiskAdapter) ListByInstanceID(ctx context.Context, region, instanceID string) ([]types.DiskInstance, error) {
	filter := &types.DiskFilter{InstanceID: instanceID}
	return a.ListInstancesWithFilter(ctx, region, filter)
}

// convertAliyunDisk 转换阿里云云盘为通用格式
func convertAliyunDisk(disk ecs.Disk, region string) types.DiskInstance {
	tags := make(map[string]string)
	for _, tag := range disk.Tags.Tag {
		tags[tag.TagKey] = tag.TagValue
	}

	// 处理多重挂载
	var attachments []types.DiskAttachment
	for _, att := range disk.Attachments.Attachment {
		attachments = append(attachments, types.DiskAttachment{
			InstanceID:   att.InstanceId,
			Device:       att.Device,
			AttachedTime: att.AttachedTime,
		})
	}

	return types.DiskInstance{
		DiskID:               disk.DiskId,
		DiskName:             disk.DiskName,
		Description:          disk.Description,
		DiskType:             disk.Type,
		Category:             disk.Category,
		PerformanceLevel:     disk.PerformanceLevel,
		Size:                 disk.Size,
		IOPS:                 disk.IOPS,
		Throughput:           disk.Throughput,
		Status:               disk.Status,
		Portable:             disk.Portable,
		DeleteAutoSnapshot:   disk.DeleteAutoSnapshot,
		DeleteWithInstance:   disk.DeleteWithInstance,
		EnableAutoSnapshot:   disk.EnableAutoSnapshot,
		InstanceID:           disk.InstanceId,
		Device:               disk.Device,
		AttachedTime:         disk.AttachedTime,
		Encrypted:            disk.Encrypted,
		KMSKeyID:             disk.KMSKeyId,
		SourceSnapshotID:     disk.SourceSnapshotId,
		AutoSnapshotPolicyID: disk.AutoSnapshotPolicyId,
		Zone:                 disk.ZoneId,
		Region:               region,
		ImageID:              disk.ImageId,
		ChargeType:           disk.DiskChargeType,
		ExpiredTime:          disk.ExpiredTime,
		ResourceGroupID:      disk.ResourceGroupId,
		CreationTime:         disk.CreationTime,
		Tags:                 tags,
		Provider:             string(types.ProviderAliyun),
		MultiAttach:          disk.MultiAttach == "Enabled",
		Attachments:          attachments,
	}
}
