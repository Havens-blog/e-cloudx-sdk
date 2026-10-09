package volcano

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/storageebs"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// SnapshotAdapter 火山引擎快照适配器
type SnapshotAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewSnapshotAdapter 创建火山引擎快照适配器
func NewSnapshotAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *SnapshotAdapter {
	return &SnapshotAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *SnapshotAdapter) getClient(region string) (*storageebs.STORAGEEBS, error) {
	if region == "" {
		region = a.defaultRegion
	}
	config := volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(
			a.accessKeyID,
			a.accessKeySecret,
			"",
		)).
		WithRegion(region)

	sess, err := session.NewSession(config)
	if err != nil {
		return nil, fmt.Errorf("创建火山引擎会话失败: %w", err)
	}

	return storageebs.New(sess), nil
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
	pageNumber := int32(1)
	pageSize := int32(100)

	for {
		input := &storageebs.DescribeSnapshotsInput{
			PageNumber: &pageNumber,
			PageSize:   &pageSize,
		}

		if filter != nil {
			if len(filter.SnapshotIDs) > 0 {
				input.SnapshotIds = volcengine.StringSlice(filter.SnapshotIDs)
			}
			if filter.Status != "" {
				input.SnapshotStatus = volcengine.StringSlice([]string{filter.Status})
			}
			if filter.SourceDiskID != "" {
				input.VolumeId = &filter.SourceDiskID
			}
		}

		result, err := client.DescribeSnapshots(input)
		if err != nil {
			return nil, fmt.Errorf("获取快照列表失败: %w", err)
		}

		if result.Snapshots == nil || len(result.Snapshots) == 0 {
			break
		}

		for _, snap := range result.Snapshots {
			instance := convertVolcanoSnapshot(snap, region)
			allInstances = append(allInstances, instance)
		}

		if len(result.Snapshots) < int(pageSize) {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取火山引擎快照列表成功",
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
	// 火山引擎快照不直接关联实例，返回空列表
	return nil, nil
}

func convertVolcanoSnapshot(snap *storageebs.SnapshotForDescribeSnapshotsOutput, region string) types.SnapshotInstance {
	tags := make(map[string]string)
	if snap.Tags != nil {
		for _, tag := range snap.Tags {
			if tag.Key != nil && tag.Value != nil {
				tags[*tag.Key] = *tag.Value
			}
		}
	}

	size := 0
	if snap.VolumeSize != nil {
		size = int(*snap.VolumeSize)
	}

	progress := ""
	if snap.Progress != nil {
		progress = fmt.Sprintf("%d%%", *snap.Progress)
	}

	creationTime := ""
	if snap.CreationTime != nil {
		creationTime = *snap.CreationTime
	}

	return types.SnapshotInstance{
		SnapshotID:     volcengine.StringValue(snap.SnapshotId),
		SnapshotName:   volcengine.StringValue(snap.SnapshotName),
		Description:    volcengine.StringValue(snap.Description),
		SnapshotType:   volcengine.StringValue(snap.SnapshotType),
		Status:         volcengine.StringValue(snap.Status),
		Progress:       progress,
		SourceDiskSize: size,
		SourceDiskID:   volcengine.StringValue(snap.VolumeId),
		SourceDiskType: volcengine.StringValue(snap.VolumeKind),
		Region:         region,
		CreationTime:   creationTime,
		Tags:           tags,
		Provider:       "volcano",
	}
}
