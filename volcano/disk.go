package volcano

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/storageebs"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// DiskAdapter 火山引擎云盘适配器
type DiskAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewDiskAdapter 创建火山引擎云盘适配器
func NewDiskAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *DiskAdapter {
	return &DiskAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *DiskAdapter) getClient(region string) (*storageebs.STORAGEEBS, error) {
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
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.DiskInstance
	pageNumber := int32(1)
	pageSize := int32(100)

	for {
		input := &storageebs.DescribeVolumesInput{
			PageNumber: &pageNumber,
			PageSize:   &pageSize,
		}

		if filter != nil {
			if len(filter.DiskIDs) > 0 {
				input.VolumeIds = volcengine.StringSlice(filter.DiskIDs)
			}
			if filter.Status != "" {
				input.VolumeStatus = &filter.Status
			}
			if filter.InstanceID != "" {
				input.InstanceId = &filter.InstanceID
			}
			if filter.DiskType != "" {
				input.VolumeType = &filter.DiskType
			}
		}

		result, err := client.DescribeVolumes(input)
		if err != nil {
			return nil, fmt.Errorf("获取云盘列表失败: %w", err)
		}

		if result.Volumes == nil || len(result.Volumes) == 0 {
			break
		}

		for _, vol := range result.Volumes {
			instance := convertVolcanoVolume(vol, region)
			allInstances = append(allInstances, instance)
		}

		if len(result.Volumes) < int(pageSize) {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取火山引擎云盘列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// ListByInstanceID 获取实例挂载的云盘
func (a *DiskAdapter) ListByInstanceID(ctx context.Context, region, instanceID string) ([]types.DiskInstance, error) {
	filter := &types.DiskFilter{InstanceID: instanceID}
	return a.ListInstancesWithFilter(ctx, region, filter)
}

func convertVolcanoVolume(vol *storageebs.VolumeForDescribeVolumesOutput, region string) types.DiskInstance {
	tags := make(map[string]string)
	if vol.Tags != nil {
		for _, tag := range vol.Tags {
			if tag.Key != nil && tag.Value != nil {
				tags[*tag.Key] = *tag.Value
			}
		}
	}

	diskType := "data"
	if vol.Kind != nil && *vol.Kind == "system" {
		diskType = "system"
	}

	var instanceID string
	if vol.InstanceId != nil {
		instanceID = *vol.InstanceId
	}

	size := 0
	if vol.Size != nil {
		// Size is json.Number, convert to int
		sizeStr := vol.Size.String()
		if s, err := strconv.Atoi(sizeStr); err == nil {
			size = s
		}
	}

	billingType := ""
	if vol.BillingType != nil {
		billingType = fmt.Sprintf("%d", *vol.BillingType)
	}

	return types.DiskInstance{
		DiskID:             volcengine.StringValue(vol.VolumeId),
		DiskName:           volcengine.StringValue(vol.VolumeName),
		Description:        volcengine.StringValue(vol.Description),
		DiskType:           diskType,
		Category:           volcengine.StringValue(vol.VolumeType),
		Size:               size,
		Status:             volcengine.StringValue(vol.Status),
		InstanceID:         instanceID,
		Zone:               volcengine.StringValue(vol.ZoneId),
		Region:             region,
		ChargeType:         billingType,
		ExpiredTime:        volcengine.StringValue(vol.ExpiredTime),
		CreationTime:       volcengine.StringValue(vol.CreatedAt),
		DeleteWithInstance: vol.DeleteWithInstance != nil && *vol.DeleteWithInstance,
		Tags:               tags,
		Provider:           "volcano",
	}
}
