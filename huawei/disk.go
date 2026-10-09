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

// DiskAdapter 华为云云盘(EVS)适配器
type DiskAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component

	// diskMetricHooks Disk 指标查询测试注入钩子(非 nil 时替代真实 CES 客户端创建;仅单测使用)
	diskMetricHooks *diskMetricHooks
}

// NewDiskAdapter 创建华为云云盘适配器
func NewDiskAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *DiskAdapter {
	return &DiskAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *DiskAdapter) getClient(regionID string) (*evs.EvsClient, error) {
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

// ListInstances 获取云盘列表
func (a *DiskAdapter) ListInstances(ctx context.Context, region string) ([]types.DiskInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个云盘详情
func (a *DiskAdapter) GetInstance(ctx context.Context, region, diskID string) (*types.DiskInstance, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	request := &model.ShowVolumeRequest{VolumeId: diskID}
	response, err := client.ShowVolume(request)
	if err != nil {
		return nil, fmt.Errorf("获取云盘详情失败: %w", err)
	}

	instance := convertHuaweiVolume(response.Volume, region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取云盘
func (a *DiskAdapter) ListInstancesByIDs(ctx context.Context, region string, diskIDs []string) ([]types.DiskInstance, error) {
	var instances []types.DiskInstance
	for _, id := range diskIDs {
		inst, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取云盘失败", elog.String("disk_id", id), elog.FieldErr(err))
			continue
		}
		instances = append(instances, *inst)
	}
	return instances, nil
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
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.DiskInstance
	offset := int32(0)
	limit := int32(100)

	for {
		request := &model.ListVolumesRequest{
			Offset: &offset,
			Limit:  &limit,
		}

		if filter != nil {
			if filter.Status != "" {
				request.Status = &filter.Status
			}
		}

		response, err := client.ListVolumes(request)
		if err != nil {
			return nil, fmt.Errorf("获取云盘列表失败: %w", err)
		}

		if response.Volumes == nil || len(*response.Volumes) == 0 {
			break
		}

		for _, vol := range *response.Volumes {
			instance := convertHuaweiVolumeDetail(vol, region)
			allInstances = append(allInstances, instance)
		}

		if len(*response.Volumes) < int(limit) {
			break
		}
		offset += limit
	}

	a.logger.Info("获取华为云云盘列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// ListByInstanceID 获取实例挂载的云盘
func (a *DiskAdapter) ListByInstanceID(ctx context.Context, region, instanceID string) ([]types.DiskInstance, error) {
	all, err := a.ListInstances(ctx, region)
	if err != nil {
		return nil, err
	}
	var result []types.DiskInstance
	for _, disk := range all {
		if disk.InstanceID == instanceID {
			result = append(result, disk)
		}
	}
	return result, nil
}

func convertHuaweiVolume(vol *model.VolumeDetail, region string) types.DiskInstance {
	if vol == nil {
		return types.DiskInstance{}
	}

	tags := make(map[string]string)
	if vol.Tags != nil {
		for k, v := range vol.Tags {
			tags[k] = v
		}
	}

	var instanceID, device string
	if vol.Attachments != nil && len(vol.Attachments) > 0 {
		att := vol.Attachments[0]
		if att.ServerId != "" {
			instanceID = att.ServerId
		}
		if att.Device != "" {
			device = att.Device
		}
	}

	diskType := "data"
	if vol.Bootable == "true" {
		diskType = "system"
	}

	return types.DiskInstance{
		DiskID:       vol.Id,
		DiskName:     vol.Name,
		Description:  vol.Description,
		DiskType:     diskType,
		Category:     vol.VolumeType,
		Size:         int(vol.Size),
		IOPS:         getHuaweiIOPS(vol.Iops),
		Throughput:   getHuaweiThroughput(vol.Throughput),
		Status:       vol.Status,
		InstanceID:   instanceID,
		Device:       device,
		Encrypted:    vol.Encrypted != nil && *vol.Encrypted,
		Zone:         vol.AvailabilityZone,
		Region:       region,
		CreationTime: vol.CreatedAt,
		Tags:         tags,
		Provider:     "huawei",
	}
}

func convertHuaweiVolumeDetail(vol model.VolumeDetail, region string) types.DiskInstance {
	return convertHuaweiVolume(&vol, region)
}

func getHuaweiIOPS(iops *model.Iops) int {
	if iops == nil {
		return 0
	}
	return int(iops.TotalVal)
}

func getHuaweiThroughput(throughput *model.Throughput) int {
	if throughput == nil {
		return 0
	}
	return int(throughput.TotalVal)
}
