package tencent

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	cvm "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cvm/v20170312"
)

// ImageAdapter 腾讯云镜像适配器
type ImageAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewImageAdapter 创建腾讯云镜像适配器
func NewImageAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *ImageAdapter {
	return &ImageAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *ImageAdapter) getClient(region string) (*cvm.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}
	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "cvm.tencentcloudapi.com"

	client, err := cvm.NewClient(credential, region, cpf)
	if err != nil {
		return nil, fmt.Errorf("创建腾讯云CVM客户端失败: %w", err)
	}
	return client, nil
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
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.ImageInstance
	offset := int64(0)
	limit := int64(100)

	for {
		request := cvm.NewDescribeImagesRequest()
		request.Offset = common.Uint64Ptr(uint64(offset))
		request.Limit = common.Uint64Ptr(uint64(limit))

		var filters []*cvm.Filter

		if filter != nil {
			if len(filter.ImageIDs) > 0 {
				request.ImageIds = common.StringPtrs(filter.ImageIDs)
			}
			if filter.ImageOwnerAlias != "" {
				imageType := "PRIVATE_IMAGE"
				switch filter.ImageOwnerAlias {
				case "self":
					imageType = "PRIVATE_IMAGE"
				case "system":
					imageType = "PUBLIC_IMAGE"
				case "marketplace":
					imageType = "MARKET_IMAGE"
				case "others":
					imageType = "SHARED_IMAGE"
				}
				filters = append(filters, &cvm.Filter{
					Name:   common.StringPtr("image-type"),
					Values: common.StringPtrs([]string{imageType}),
				})
			}
			if filter.ImageName != "" {
				filters = append(filters, &cvm.Filter{
					Name:   common.StringPtr("image-name"),
					Values: common.StringPtrs([]string{filter.ImageName}),
				})
			}
			if filter.OSType != "" {
				platform := filter.OSType
				if platform == "linux" {
					platform = "Linux"
				} else if platform == "windows" {
					platform = "Windows"
				}
				filters = append(filters, &cvm.Filter{
					Name:   common.StringPtr("platform"),
					Values: common.StringPtrs([]string{platform}),
				})
			}
			if filter.Architecture != "" {
				filters = append(filters, &cvm.Filter{
					Name:   common.StringPtr("architecture"),
					Values: common.StringPtrs([]string{filter.Architecture}),
				})
			}
			if filter.Status != "" {
				filters = append(filters, &cvm.Filter{
					Name:   common.StringPtr("image-state"),
					Values: common.StringPtrs([]string{filter.Status}),
				})
			}
		} else {
			// 默认查询私有镜像
			filters = append(filters, &cvm.Filter{
				Name:   common.StringPtr("image-type"),
				Values: common.StringPtrs([]string{"PRIVATE_IMAGE"}),
			})
		}

		if len(filters) > 0 {
			request.Filters = filters
		}

		response, err := client.DescribeImages(request)
		if err != nil {
			return nil, fmt.Errorf("获取镜像列表失败: %w", err)
		}

		if response.Response.ImageSet == nil || len(response.Response.ImageSet) == 0 {
			break
		}

		for _, img := range response.Response.ImageSet {
			instance := convertTencentImage(img, region)
			allInstances = append(allInstances, instance)
		}

		if len(response.Response.ImageSet) < int(limit) {
			break
		}
		offset += limit
	}

	a.logger.Info("获取腾讯云镜像列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

func convertTencentImage(img *cvm.Image, region string) types.ImageInstance {
	tags := make(map[string]string)
	if img.Tags != nil {
		for _, tag := range img.Tags {
			if tag.Key != nil && tag.Value != nil {
				tags[*tag.Key] = *tag.Value
			}
		}
	}

	// 确定镜像来源
	ownerAlias := "self"
	if img.ImageType != nil {
		switch *img.ImageType {
		case "PUBLIC_IMAGE":
			ownerAlias = "system"
		case "MARKET_IMAGE":
			ownerAlias = "marketplace"
		case "SHARED_IMAGE":
			ownerAlias = "others"
		}
	}

	// 确定操作系统类型
	osType := "linux"
	if img.Platform != nil {
		platform := *img.Platform
		if platform == "Windows" || platform == "windows" {
			osType = "windows"
		}
	}

	// 磁盘映射
	var diskMappings []types.ImageDiskMapping
	if img.SnapshotSet != nil {
		for _, snap := range img.SnapshotSet {
			dm := types.ImageDiskMapping{
				SnapshotID: safeStringPtr(snap.SnapshotId),
				Size:       int(safeInt64Ptr(snap.DiskSize)),
				Type:       safeStringPtr(snap.DiskUsage),
			}
			diskMappings = append(diskMappings, dm)
		}
	}

	return types.ImageInstance{
		ImageID:            safeStringPtr(img.ImageId),
		ImageName:          safeStringPtr(img.ImageName),
		Description:        safeStringPtr(img.ImageDescription),
		ImageOwnerAlias:    ownerAlias,
		OSType:             osType,
		OSName:             safeStringPtr(img.OsName),
		Platform:           safeStringPtr(img.Platform),
		Architecture:       safeStringPtr(img.Architecture),
		Status:             safeStringPtr(img.ImageState),
		Size:               int(safeInt64Ptr(img.ImageSize)),
		DiskDeviceMappings: diskMappings,
		Region:             region,
		CreationTime:       safeStringPtr(img.CreatedTime),
		Tags:               tags,
		Provider:           "tencent",
	}
}

func safeInt64Ptr(i *int64) int64 {
	if i == nil {
		return 0
	}
	return *i
}
