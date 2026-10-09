package tencent

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	cbs "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cbs/v20170312"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

// SnapshotAdapter 腾讯云快照适配器
type SnapshotAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewSnapshotAdapter 创建腾讯云快照适配器
func NewSnapshotAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *SnapshotAdapter {
	return &SnapshotAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *SnapshotAdapter) getClient(region string) (*cbs.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}
	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "cbs.tencentcloudapi.com"

	client, err := cbs.NewClient(credential, region, cpf)
	if err != nil {
		return nil, fmt.Errorf("创建腾讯云CBS客户端失败: %w", err)
	}
	return client, nil
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
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.SnapshotInstance
	offset := uint64(0)
	limit := uint64(100)

	for {
		request := cbs.NewDescribeSnapshotsRequest()
		request.Offset = &offset
		request.Limit = &limit

		if filter != nil {
			if len(filter.SnapshotIDs) > 0 {
				request.SnapshotIds = common.StringPtrs(filter.SnapshotIDs)
			}
			var filters []*cbs.Filter
			if filter.Status != "" {
				filters = append(filters, &cbs.Filter{
					Name:   common.StringPtr("snapshot-state"),
					Values: common.StringPtrs([]string{filter.Status}),
				})
			}
			if filter.SourceDiskID != "" {
				filters = append(filters, &cbs.Filter{
					Name:   common.StringPtr("disk-id"),
					Values: common.StringPtrs([]string{filter.SourceDiskID}),
				})
			}
			if len(filters) > 0 {
				request.Filters = filters
			}
		}

		response, err := client.DescribeSnapshots(request)
		if err != nil {
			return nil, fmt.Errorf("获取快照列表失败: %w", err)
		}

		if response.Response.SnapshotSet == nil || len(response.Response.SnapshotSet) == 0 {
			break
		}

		for _, snap := range response.Response.SnapshotSet {
			instance := convertTencentSnapshot(snap, region)
			allInstances = append(allInstances, instance)
		}

		if len(response.Response.SnapshotSet) < int(limit) {
			break
		}
		offset += limit
	}

	a.logger.Info("获取腾讯云快照列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// ListByDiskID 获取磁盘的快照列表
func (a *SnapshotAdapter) ListByDiskID(ctx context.Context, region, diskID string) ([]types.SnapshotInstance, error) {
	filter := &types.SnapshotFilter{SourceDiskID: diskID}
	return a.ListInstancesWithFilter(ctx, region, filter)
}

// ListByInstanceID 获取实例的快照列表
func (a *SnapshotAdapter) ListByInstanceID(ctx context.Context, region, instanceID string) ([]types.SnapshotInstance, error) {
	// 腾讯云快照不直接关联实例，返回空列表
	return nil, nil
}

func convertTencentSnapshot(snap *cbs.Snapshot, region string) types.SnapshotInstance {
	tags := make(map[string]string)
	if snap.Tags != nil {
		for _, tag := range snap.Tags {
			if tag.Key != nil && tag.Value != nil {
				tags[*tag.Key] = *tag.Value
			}
		}
	}

	size := 0
	if snap.DiskSize != nil {
		size = int(*snap.DiskSize)
	}

	progress := ""
	if snap.Percent != nil {
		progress = fmt.Sprintf("%d%%", *snap.Percent)
	}

	snapshotType := "user"
	if snap.SnapshotType != nil && *snap.SnapshotType == "PRIVATE_SNAPSHOT" {
		snapshotType = "auto"
	}

	return types.SnapshotInstance{
		SnapshotID:     safeStringPtr(snap.SnapshotId),
		SnapshotName:   safeStringPtr(snap.SnapshotName),
		Description:    "",
		SnapshotType:   snapshotType,
		Status:         safeStringPtr(snap.SnapshotState),
		Progress:       progress,
		SourceDiskSize: size,
		SourceDiskID:   safeStringPtr(snap.DiskId),
		SourceDiskType: safeStringPtr(snap.DiskUsage),
		Encrypted:      snap.Encrypt != nil && *snap.Encrypt,
		Region:         region,
		CreationTime:   safeStringPtr(snap.CreateTime),
		Tags:           tags,
		Provider:       "tencent",
	}
}
