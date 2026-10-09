package huawei

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	evs "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/evs/v2"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/evs/v2/model"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/evs/v2/region"
)

// SnapshotAdapter 华为云快照适配器
type SnapshotAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewSnapshotAdapter 创建华为云快照适配器
func NewSnapshotAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *SnapshotAdapter {
	return &SnapshotAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *SnapshotAdapter) getClient(regionID string) (*evs.EvsClient, error) {
	if regionID == "" {
		regionID = a.defaultRegion
	}
	auth, err := basic.NewCredentialsBuilder().
		WithAk(a.accessKeyID).
		WithSk(a.accessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}

	reg, err := region.SafeValueOf(regionID)
	if err != nil {
		return nil, fmt.Errorf("无效的华为云地域: %s", regionID)
	}

	client, err := evs.EvsClientBuilder().
		WithRegion(reg).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云EVS客户端失败: %w", err)
	}
	return evs.NewEvsClient(client), nil
}

// ListInstances 获取快照列表
func (a *SnapshotAdapter) ListInstances(ctx context.Context, region string) ([]types.SnapshotInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个快照详情
func (a *SnapshotAdapter) GetInstance(ctx context.Context, region, snapshotID string) (*types.SnapshotInstance, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	request := &model.ShowSnapshotRequest{SnapshotId: snapshotID}
	response, err := client.ShowSnapshot(request)
	if err != nil {
		return nil, fmt.Errorf("获取快照详情失败: %w", err)
	}

	instance := convertHuaweiSnapshotDetails(response.Snapshot, region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取快照
func (a *SnapshotAdapter) ListInstancesByIDs(ctx context.Context, region string, snapshotIDs []string) ([]types.SnapshotInstance, error) {
	var instances []types.SnapshotInstance
	for _, id := range snapshotIDs {
		inst, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取快照失败", elog.String("snapshot_id", id), elog.FieldErr(err))
			continue
		}
		instances = append(instances, *inst)
	}
	return instances, nil
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
	offset := int32(0)
	limit := int32(100)

	for {
		request := &model.ListSnapshotsRequest{
			Offset: &offset,
			Limit:  &limit,
		}

		if filter != nil {
			if filter.Status != "" {
				request.Status = &filter.Status
			}
			if filter.SourceDiskID != "" {
				request.VolumeId = &filter.SourceDiskID
			}
		}

		response, err := client.ListSnapshots(request)
		if err != nil {
			return nil, fmt.Errorf("获取快照列表失败: %w", err)
		}

		if response.Snapshots == nil || len(*response.Snapshots) == 0 {
			break
		}

		for _, snap := range *response.Snapshots {
			instance := convertHuaweiSnapshotList(snap, region)
			allInstances = append(allInstances, instance)
		}

		if len(*response.Snapshots) < int(limit) {
			break
		}
		offset += limit
	}

	a.logger.Info("获取华为云快照列表成功",
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
	// 华为云快照不直接关联实例，返回空列表
	return nil, nil
}

func convertHuaweiSnapshotDetails(snap *model.SnapshotDetails, region string) types.SnapshotInstance {
	if snap == nil {
		return types.SnapshotInstance{}
	}

	id := ""
	if snap.Id != nil {
		id = *snap.Id
	}
	name := ""
	if snap.Name != nil {
		name = *snap.Name
	}
	desc := ""
	if snap.Description != nil {
		desc = *snap.Description
	}
	status := ""
	if snap.Status != nil {
		status = *snap.Status
	}
	volumeId := ""
	if snap.VolumeId != nil {
		volumeId = *snap.VolumeId
	}
	createdAt := ""
	if snap.CreatedAt != nil {
		createdAt = *snap.CreatedAt
	}
	size := 0
	if snap.Size != nil {
		size = int(*snap.Size)
	}

	return types.SnapshotInstance{
		SnapshotID:     id,
		SnapshotName:   name,
		Description:    desc,
		SnapshotType:   "user",
		Status:         status,
		SourceDiskSize: size,
		SourceDiskID:   volumeId,
		Region:         region,
		CreationTime:   createdAt,
		Tags:           make(map[string]string),
		Provider:       "huawei",
	}
}

func convertHuaweiSnapshotList(snap model.SnapshotList, region string) types.SnapshotInstance {
	name := ""
	if snap.Name != nil {
		name = *snap.Name
	}
	desc := ""
	if snap.Description != nil {
		desc = *snap.Description
	}

	return types.SnapshotInstance{
		SnapshotID:     snap.Id, // string, not pointer
		SnapshotName:   name,
		Description:    desc,
		SnapshotType:   "user",
		Status:         snap.Status,    // string, not pointer
		SourceDiskSize: int(snap.Size), // int32, not pointer
		SourceDiskID:   snap.VolumeId,  // string, not pointer
		Region:         region,
		CreationTime:   snap.CreatedAt, // string, not pointer
		Tags:           make(map[string]string),
		Provider:       "huawei",
	}
}
