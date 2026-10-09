package aliyun

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/gotomicro/ego/core/elog"
)

// SnapshotAdapter 阿里云快照适配器
type SnapshotAdapter struct {
	client *Client
	logger *elog.Component
}

// NewSnapshotAdapter 创建阿里云快照适配器
func NewSnapshotAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *SnapshotAdapter {
	return &SnapshotAdapter{
		client: NewClient(accessKeyID, accessKeySecret, defaultRegion, logger),
		logger: logger,
	}
}

// ListInstances 获取快照列表
func (a *SnapshotAdapter) ListInstances(ctx context.Context, region string) ([]types.SnapshotInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个快照详情
func (a *SnapshotAdapter) GetInstance(ctx context.Context, region, snapshotID string) (*types.SnapshotInstance, error) {
	instances, err := a.ListInstancesByIDs(ctx, region, []string{snapshotID})
	if err != nil {
		return nil, err
	}
	if len(instances) == 0 {
		return nil, fmt.Errorf("快照不存在: %s", snapshotID)
	}
	return &instances[0], nil
}

// ListInstancesByIDs 批量获取快照
func (a *SnapshotAdapter) ListInstancesByIDs(ctx context.Context, region string, snapshotIDs []string) ([]types.SnapshotInstance, error) {
	if len(snapshotIDs) == 0 {
		return nil, nil
	}

	filter := &types.SnapshotFilter{SnapshotIDs: snapshotIDs}
	return a.ListInstancesWithFilter(ctx, region, filter)
}

// GetInstanceStatus 获取快照状态
func (a *SnapshotAdapter) GetInstanceStatus(ctx context.Context, region, snapshotID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, snapshotID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取快照列表
func (a *SnapshotAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.SnapshotFilter) ([]types.SnapshotInstance, error) {
	if err := a.client.WaitRateLimit(ctx); err != nil {
		return nil, err
	}

	ecsClient, err := a.client.GetECSClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.SnapshotInstance
	pageNumber := 1
	pageSize := 50

	if filter != nil && filter.PageSize > 0 {
		pageSize = filter.PageSize
	}
	if filter != nil && filter.PageNumber > 0 {
		pageNumber = filter.PageNumber
	}

	for {
		request := ecs.CreateDescribeSnapshotsRequest()
		request.Scheme = "https"
		request.RegionId = region
		request.PageNumber = requests.NewInteger(pageNumber)
		request.PageSize = requests.NewInteger(pageSize)

		// 应用过滤条件
		if filter != nil {
			if len(filter.SnapshotIDs) > 0 {
				request.SnapshotIds = fmt.Sprintf(`["%s"]`, joinStrings(filter.SnapshotIDs, `","`))
			}
			if filter.SnapshotName != "" {
				request.SnapshotName = filter.SnapshotName
			}
			if filter.SnapshotType != "" {
				request.SnapshotType = filter.SnapshotType
			}
			if filter.Status != "" {
				request.Status = filter.Status
			}
			if filter.SourceDiskID != "" {
				request.DiskId = filter.SourceDiskID
			}
			if filter.SourceDiskType != "" {
				request.SourceDiskType = filter.SourceDiskType
			}
			if filter.InstanceID != "" {
				request.InstanceId = filter.InstanceID
			}
			if filter.Encrypted != nil {
				request.Encrypted = requests.NewBoolean(*filter.Encrypted)
			}
			if filter.ResourceGroupID != "" {
				request.ResourceGroupId = filter.ResourceGroupID
			}
			if len(filter.Tags) > 0 {
				var tags []ecs.DescribeSnapshotsTag
				for k, v := range filter.Tags {
					tags = append(tags, ecs.DescribeSnapshotsTag{Key: k, Value: v})
				}
				request.Tag = &tags
			}
		}

		var response *ecs.DescribeSnapshotsResponse
		err = a.client.RetryWithBackoff(ctx, func() error {
			var e error
			response, e = ecsClient.DescribeSnapshots(request)
			return e
		})

		if err != nil {
			return nil, fmt.Errorf("获取快照列表失败: %w", err)
		}

		for _, snap := range response.Snapshots.Snapshot {
			instance := convertAliyunSnapshot(snap, region)
			allInstances = append(allInstances, instance)
		}

		// 如果指定了分页，只返回一页
		if filter != nil && filter.PageNumber > 0 {
			break
		}

		if len(response.Snapshots.Snapshot) < pageSize {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取阿里云快照列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// ListByDiskID 获取磁盘的快照列表
func (a *SnapshotAdapter) ListByDiskID(ctx context.Context, region, diskID string) ([]types.SnapshotInstance, error) {
	filter := &types.SnapshotFilter{SourceDiskID: diskID}
	return a.ListInstancesWithFilter(ctx, region, filter)
}

// ListByInstanceID 获取实例的快照列表 (系统盘+数据盘)
func (a *SnapshotAdapter) ListByInstanceID(ctx context.Context, region, instanceID string) ([]types.SnapshotInstance, error) {
	filter := &types.SnapshotFilter{InstanceID: instanceID}
	return a.ListInstancesWithFilter(ctx, region, filter)
}

// convertAliyunSnapshot 转换阿里云快照为通用格式
func convertAliyunSnapshot(snap ecs.Snapshot, region string) types.SnapshotInstance {
	tags := make(map[string]string)
	for _, tag := range snap.Tags.Tag {
		tags[tag.TagKey] = tag.TagValue
	}

	// 转换 SourceDiskSize 从 string 到 int
	sourceDiskSize := 0
	if snap.SourceDiskSize != "" {
		if size, err := strconv.Atoi(snap.SourceDiskSize); err == nil {
			sourceDiskSize = size
		}
	}

	return types.SnapshotInstance{
		SnapshotID:       snap.SnapshotId,
		SnapshotName:     snap.SnapshotName,
		Description:      snap.Description,
		SnapshotType:     snap.SnapshotType,
		Category:         snap.Category,
		InstantAccess:    snap.InstantAccess,
		Status:           snap.Status,
		Progress:         snap.Progress,
		SourceDiskSize:   sourceDiskSize,
		SourceDiskID:     snap.SourceDiskId,
		SourceDiskType:   snap.SourceDiskType,
		Encrypted:        snap.Encrypted,
		KMSKeyID:         snap.KMSKeyId,
		Usage:            snap.Usage,
		RetentionDays:    snap.RetentionDays,
		Region:           region,
		ResourceGroupID:  snap.ResourceGroupId,
		CreationTime:     snap.CreationTime,
		LastModifiedTime: snap.LastModifiedTime,
		Tags:             tags,
		Provider:         string(types.ProviderAliyun),
	}
}
