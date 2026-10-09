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

// DiskAdapter 腾讯云云盘(CBS)适配器
type DiskAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewDiskAdapter 创建腾讯云云盘适配器
func NewDiskAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *DiskAdapter {
	return &DiskAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *DiskAdapter) getClient(region string) (*cbs.Client, error) {
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
	offset := uint64(0)
	limit := uint64(100)

	for {
		request := cbs.NewDescribeDisksRequest()
		request.Offset = &offset
		request.Limit = &limit

		if filter != nil {
			if len(filter.DiskIDs) > 0 {
				request.DiskIds = common.StringPtrs(filter.DiskIDs)
			}
			var filters []*cbs.Filter
			if filter.Status != "" {
				filters = append(filters, &cbs.Filter{
					Name:   common.StringPtr("disk-state"),
					Values: common.StringPtrs([]string{filter.Status}),
				})
			}
			if filter.InstanceID != "" {
				filters = append(filters, &cbs.Filter{
					Name:   common.StringPtr("instance-id"),
					Values: common.StringPtrs([]string{filter.InstanceID}),
				})
			}
			if filter.DiskType != "" {
				filters = append(filters, &cbs.Filter{
					Name:   common.StringPtr("disk-usage"),
					Values: common.StringPtrs([]string{filter.DiskType}),
				})
			}
			if len(filters) > 0 {
				request.Filters = filters
			}
		}

		response, err := client.DescribeDisks(request)
		if err != nil {
			return nil, fmt.Errorf("获取云盘列表失败: %w", err)
		}

		if response.Response.DiskSet == nil || len(response.Response.DiskSet) == 0 {
			break
		}

		for _, disk := range response.Response.DiskSet {
			instance := convertTencentDisk(disk, region)
			allInstances = append(allInstances, instance)
		}

		if len(response.Response.DiskSet) < int(limit) {
			break
		}
		offset += limit
	}

	a.logger.Info("获取腾讯云云盘列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// ListByInstanceID 获取实例挂载的云盘
func (a *DiskAdapter) ListByInstanceID(ctx context.Context, region, instanceID string) ([]types.DiskInstance, error) {
	filter := &types.DiskFilter{InstanceID: instanceID}
	return a.ListInstancesWithFilter(ctx, region, filter)
}

func convertTencentDisk(disk *cbs.Disk, region string) types.DiskInstance {
	tags := make(map[string]string)
	if disk.Tags != nil {
		for _, tag := range disk.Tags {
			if tag.Key != nil && tag.Value != nil {
				tags[*tag.Key] = *tag.Value
			}
		}
	}

	diskType := "data"
	if disk.DiskUsage != nil && *disk.DiskUsage == "SYSTEM_DISK" {
		diskType = "system"
	}

	var instanceID string
	if disk.InstanceId != nil {
		instanceID = *disk.InstanceId
	}

	size := 0
	if disk.DiskSize != nil {
		size = int(*disk.DiskSize)
	}

	iops := 0
	if disk.ThroughputPerformance != nil {
		iops = int(*disk.ThroughputPerformance)
	}

	return types.DiskInstance{
		DiskID:             safeStringPtr(disk.DiskId),
		DiskName:           safeStringPtr(disk.DiskName),
		DiskType:           diskType,
		Category:           safeStringPtr(disk.DiskType),
		Size:               size,
		IOPS:               iops,
		Status:             safeStringPtr(disk.DiskState),
		InstanceID:         instanceID,
		Encrypted:          disk.Encrypt != nil && *disk.Encrypt,
		Zone:               safeStringPtr(disk.Placement.Zone),
		Region:             region,
		ChargeType:         convertTencentChargeType(disk.DiskChargeType),
		ExpiredTime:        safeStringPtr(disk.DeadlineTime),
		CreationTime:       safeStringPtr(disk.CreateTime),
		DeleteWithInstance: disk.DeleteWithInstance != nil && *disk.DeleteWithInstance,
		Portable:           disk.Portable != nil && *disk.Portable,
		Tags:               tags,
		Provider:           "tencent",
	}
}

func convertTencentChargeType(chargeType *string) string {
	if chargeType == nil {
		return "PostPaid"
	}
	switch *chargeType {
	case "PREPAID":
		return "PrePaid"
	case "POSTPAID_BY_HOUR":
		return "PostPaid"
	default:
		return *chargeType
	}
}
