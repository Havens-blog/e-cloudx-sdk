package huawei

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	ims "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ims/v2"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ims/v2/model"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ims/v2/region"
)

// ImageAdapter 华为云镜像适配器
type ImageAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewImageAdapter 创建华为云镜像适配器
func NewImageAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *ImageAdapter {
	return &ImageAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *ImageAdapter) getClient(regionID string) (*ims.ImsClient, error) {
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

	client, err := ims.ImsClientBuilder().
		WithRegion(reg).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云IMS客户端失败: %w", err)
	}
	return ims.NewImsClient(client), nil
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

	var instances []types.ImageInstance
	for _, id := range imageIDs {
		client, err := a.getClient(region)
		if err != nil {
			return nil, err
		}

		request := &model.GlanceShowImageRequest{ImageId: id}
		response, err := client.GlanceShowImage(request)
		if err != nil {
			a.logger.Warn("获取镜像详情失败", elog.String("image_id", id), elog.FieldErr(err))
			continue
		}
		instance := convertHuaweiImageDetail(response, region)
		instances = append(instances, instance)
	}
	return instances, nil
}

// ListInstancesWithFilter 带过滤条件获取镜像列表
func (a *ImageAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.ImageFilter) ([]types.ImageInstance, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.ImageInstance
	marker := ""
	limit := int32(100)

	for {
		request := &model.GlanceListImagesRequest{
			Limit: &limit,
		}
		if marker != "" {
			request.Marker = &marker
		}

		// 默认查询私有镜像
		imageType := model.GetGlanceListImagesRequestImagetypeEnum().PRIVATE
		if filter != nil {
			switch filter.ImageOwnerAlias {
			case "self":
				imageType = model.GetGlanceListImagesRequestImagetypeEnum().PRIVATE
			case "system":
				imageType = model.GetGlanceListImagesRequestImagetypeEnum().GOLD
			case "marketplace":
				imageType = model.GetGlanceListImagesRequestImagetypeEnum().MARKET
			}
			if filter.OSType != "" {
				osType := model.GlanceListImagesRequestOsType{}
				if filter.OSType == "linux" {
					osType = model.GetGlanceListImagesRequestOsTypeEnum().LINUX
				} else if filter.OSType == "windows" {
					osType = model.GetGlanceListImagesRequestOsTypeEnum().WINDOWS
				}
				request.OsType = &osType
			}
			if filter.Status != "" {
				status := model.GlanceListImagesRequestStatus{}
				switch filter.Status {
				case "active", "Available":
					status = model.GetGlanceListImagesRequestStatusEnum().ACTIVE
				case "queued":
					status = model.GetGlanceListImagesRequestStatusEnum().QUEUED
				case "saving":
					status = model.GetGlanceListImagesRequestStatusEnum().SAVING
				}
				request.Status = &status
			}
			if filter.ImageName != "" {
				request.Name = &filter.ImageName
			}
		}
		request.Imagetype = &imageType

		response, err := client.GlanceListImages(request)
		if err != nil {
			return nil, fmt.Errorf("获取镜像列表失败: %w", err)
		}

		if response.Images == nil || len(*response.Images) == 0 {
			break
		}

		for _, img := range *response.Images {
			instance := convertHuaweiImage(img, region)
			allInstances = append(allInstances, instance)
		}

		if len(*response.Images) < int(limit) {
			break
		}
		images := *response.Images
		marker = images[len(images)-1].Id
	}

	a.logger.Info("获取华为云镜像列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

func convertHuaweiImage(img model.GlanceShowImageListResponseBody, region string) types.ImageInstance {
	tags := make(map[string]string)
	if img.Tags != nil {
		for i, tag := range img.Tags {
			tags[fmt.Sprintf("tag_%d", i)] = tag
		}
	}

	// 确定镜像来源
	ownerAlias := "self"
	switch img.Imagetype.Value() {
	case "gold":
		ownerAlias = "system"
	case "market":
		ownerAlias = "marketplace"
	case "shared":
		ownerAlias = "others"
	}

	// 确定操作系统类型
	osType := "linux"
	if img.OsType.Value() == "Windows" {
		osType = "windows"
	}

	// 确定架构
	arch := "x86_64"
	if img.SupportArm != nil && img.SupportArm.Value() == "true" {
		arch = "arm64"
	}

	// 确定状态
	status := img.Status.Value()

	// 确定平台
	platform := ""
	if img.Platform != nil {
		platform = img.Platform.Value()
	}

	return types.ImageInstance{
		ImageID:         img.Id,
		ImageName:       img.Name,
		Description:     safeStringHuawei(img.Description),
		ImageOwnerAlias: ownerAlias,
		OSType:          osType,
		OSName:          safeStringHuawei(img.OsVersion),
		Platform:        platform,
		Architecture:    arch,
		Status:          status,
		Size:            int(img.MinDisk),
		Region:          region,
		CreationTime:    img.CreatedAt,
		Tags:            tags,
		Provider:        "huawei",
	}
}

func convertHuaweiImageDetail(response *model.GlanceShowImageResponse, region string) types.ImageInstance {
	tags := make(map[string]string)
	if response.Tags != nil {
		for i, tag := range *response.Tags {
			tags[fmt.Sprintf("tag_%d", i)] = tag
		}
	}

	ownerAlias := "self"
	if response.Imagetype != nil {
		switch response.Imagetype.Value() {
		case "gold":
			ownerAlias = "system"
		case "market":
			ownerAlias = "marketplace"
		case "shared":
			ownerAlias = "others"
		}
	}

	osType := "linux"
	if response.OsType != nil && response.OsType.Value() == "Windows" {
		osType = "windows"
	}

	arch := "x86_64"
	if response.SupportArm != nil && response.SupportArm.Value() == "true" {
		arch = "arm64"
	}

	status := "unknown"
	if response.Status != nil {
		status = response.Status.Value()
	}

	minDisk := 0
	if response.MinDisk != nil {
		minDisk = int(*response.MinDisk)
	}

	platform := ""
	if response.Platform != nil {
		platform = response.Platform.Value()
	}

	return types.ImageInstance{
		ImageID:         safeStringHuawei(response.Id),
		ImageName:       safeStringHuawei(response.Name),
		Description:     safeStringHuawei(response.Description),
		ImageOwnerAlias: ownerAlias,
		OSType:          osType,
		OSName:          safeStringHuawei(response.OsVersion),
		Platform:        platform,
		Architecture:    arch,
		Status:          status,
		Size:            minDisk,
		Region:          region,
		CreationTime:    safeStringHuawei(response.CreatedAt),
		Tags:            tags,
		Provider:        "huawei",
	}
}

func safeStringHuawei(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
