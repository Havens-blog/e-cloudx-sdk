package aliyun

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/gotomicro/ego/core/elog"
)

// ImageAdapter 阿里云镜像适配器
type ImageAdapter struct {
	client *Client
	logger *elog.Component
}

// NewImageAdapter 创建阿里云镜像适配器
func NewImageAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *ImageAdapter {
	return &ImageAdapter{
		client: NewClient(accessKeyID, accessKeySecret, defaultRegion, logger),
		logger: logger,
	}
}

// ListInstances 获取镜像列表
func (a *ImageAdapter) ListInstances(ctx context.Context, region string) ([]types.ImageInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个镜像详情
func (a *ImageAdapter) GetInstance(ctx context.Context, region, imageID string) (*types.ImageInstance, error) {
	instances, err := a.ListInstancesByIDs(ctx, region, []string{imageID})
	if err != nil {
		return nil, err
	}
	if len(instances) == 0 {
		return nil, fmt.Errorf("镜像不存在: %s", imageID)
	}
	return &instances[0], nil
}

// ListInstancesByIDs 批量获取镜像
func (a *ImageAdapter) ListInstancesByIDs(ctx context.Context, region string, imageIDs []string) ([]types.ImageInstance, error) {
	if len(imageIDs) == 0 {
		return nil, nil
	}

	filter := &types.ImageFilter{ImageIDs: imageIDs}
	return a.ListInstancesWithFilter(ctx, region, filter)
}

// ListInstancesWithFilter 带过滤条件获取镜像列表
func (a *ImageAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.ImageFilter) ([]types.ImageInstance, error) {
	if err := a.client.WaitRateLimit(ctx); err != nil {
		return nil, err
	}

	ecsClient, err := a.client.GetECSClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.ImageInstance
	pageNumber := 1
	pageSize := 50

	if filter != nil && filter.PageSize > 0 {
		pageSize = filter.PageSize
	}
	if filter != nil && filter.PageNumber > 0 {
		pageNumber = filter.PageNumber
	}

	for {
		request := ecs.CreateDescribeImagesRequest()
		request.Scheme = "https"
		request.RegionId = region
		request.PageNumber = requests.NewInteger(pageNumber)
		request.PageSize = requests.NewInteger(pageSize)

		// 应用过滤条件
		if filter != nil {
			if len(filter.ImageIDs) > 0 {
				request.ImageId = filter.ImageIDs[0] // 阿里云只支持单个ID查询
			}
			if filter.ImageName != "" {
				request.ImageName = filter.ImageName
			}
			if filter.ImageOwnerAlias != "" {
				request.ImageOwnerAlias = filter.ImageOwnerAlias
			}
			if filter.OSType != "" {
				request.OSType = filter.OSType
			}
			if filter.Architecture != "" {
				request.Architecture = filter.Architecture
			}
			if filter.Status != "" {
				request.Status = filter.Status
			}
			if filter.ResourceGroupID != "" {
				request.ResourceGroupId = filter.ResourceGroupID
			}
			if len(filter.Tags) > 0 {
				var tags []ecs.DescribeImagesTag
				for k, v := range filter.Tags {
					tags = append(tags, ecs.DescribeImagesTag{Key: k, Value: v})
				}
				request.Tag = &tags
			}
		} else {
			// 默认只查询自定义镜像
			request.ImageOwnerAlias = "self"
		}

		var response *ecs.DescribeImagesResponse
		err = a.client.RetryWithBackoff(ctx, func() error {
			var e error
			response, e = ecsClient.DescribeImages(request)
			return e
		})

		if err != nil {
			return nil, fmt.Errorf("获取镜像列表失败: %w", err)
		}

		for _, img := range response.Images.Image {
			instance := convertAliyunImage(img, region)
			allInstances = append(allInstances, instance)
		}

		// 如果指定了分页，只返回一页
		if filter != nil && filter.PageNumber > 0 {
			break
		}

		if len(response.Images.Image) < pageSize {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取阿里云镜像列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertAliyunImage 转换阿里云镜像为通用格式
func convertAliyunImage(img ecs.Image, region string) types.ImageInstance {
	tags := make(map[string]string)
	for _, tag := range img.Tags.Tag {
		tags[tag.TagKey] = tag.TagValue
	}

	var diskMappings []types.ImageDiskMapping
	for _, disk := range img.DiskDeviceMappings.DiskDeviceMapping {
		size := 0
		if disk.Size != "" {
			fmt.Sscanf(disk.Size, "%d", &size)
		}
		diskMappings = append(diskMappings, types.ImageDiskMapping{
			Device:     disk.Device,
			Size:       size,
			SnapshotID: disk.SnapshotId,
			Type:       disk.Type,
			Format:     disk.Format,
		})
	}

	return types.ImageInstance{
		ImageID:              img.ImageId,
		ImageName:            img.ImageName,
		Description:          img.Description,
		ImageVersion:         img.ImageVersion,
		ImageFamily:          img.ImageFamily,
		ImageOwnerAlias:      img.ImageOwnerAlias,
		IsSelfShared:         img.IsSelfShared == "true",
		IsPublic:             img.IsPublic,
		IsCopied:             img.IsCopied,
		OSType:               img.OSType,
		OSName:               img.OSName,
		OSNameEn:             img.OSNameEn,
		Platform:             img.Platform,
		Architecture:         img.Architecture,
		Status:               img.Status,
		Progress:             img.Progress,
		Size:                 img.Size,
		DiskDeviceMappings:   diskMappings,
		Usage:                img.Usage,
		Region:               region,
		ResourceGroupID:      img.ResourceGroupId,
		CreationTime:         img.CreationTime,
		Tags:                 tags,
		Provider:             string(types.ProviderAliyun),
		IsSupportCloudinit:   img.IsSupportCloudinit,
		IsSupportIoOptimized: img.IsSupportIoOptimized,
		BootMode:             img.BootMode,
	}
}
