package volcano

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/ecs"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// ImageAdapter 火山引擎镜像适配器
type ImageAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewImageAdapter 创建火山引擎镜像适配器
func NewImageAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *ImageAdapter {
	return &ImageAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *ImageAdapter) getClient(region string) (*ecs.ECS, error) {
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

	return ecs.New(sess), nil
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
	nextToken := ""
	maxResults := int32(100)

	for {
		input := &ecs.DescribeImagesInput{
			MaxResults: &maxResults,
		}
		if nextToken != "" {
			input.NextToken = &nextToken
		}

		// 默认查询私有镜像
		visibility := "private"
		if filter != nil {
			if len(filter.ImageIDs) > 0 {
				input.ImageIds = volcengine.StringSlice(filter.ImageIDs)
			}
			if filter.ImageOwnerAlias != "" {
				switch filter.ImageOwnerAlias {
				case "self":
					visibility = "private"
				case "system":
					visibility = "public"
				case "others":
					visibility = "shared"
				}
			}
			if filter.ImageName != "" {
				input.ImageName = &filter.ImageName
			}
			if filter.OSType != "" {
				input.OsType = &filter.OSType
			}
			if filter.Status != "" {
				input.Status = volcengine.StringSlice([]string{filter.Status})
			}
		}
		input.Visibility = &visibility

		result, err := client.DescribeImages(input)
		if err != nil {
			return nil, fmt.Errorf("获取镜像列表失败: %w", err)
		}

		if result.Images == nil || len(result.Images) == 0 {
			break
		}

		for _, img := range result.Images {
			instance := convertVolcanoImage(img, region)
			allInstances = append(allInstances, instance)
		}

		if result.NextToken == nil || *result.NextToken == "" {
			break
		}
		nextToken = *result.NextToken
	}

	a.logger.Info("获取火山引擎镜像列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

func convertVolcanoImage(img *ecs.ImageForDescribeImagesOutput, region string) types.ImageInstance {
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
	if img.Visibility != nil {
		switch *img.Visibility {
		case "public":
			ownerAlias = "system"
		case "shared":
			ownerAlias = "others"
		}
	}

	// 确定操作系统类型
	osType := "linux"
	if img.OsType != nil {
		if *img.OsType == "Windows" || *img.OsType == "windows" {
			osType = "windows"
		}
	}

	return types.ImageInstance{
		ImageID:         volcengine.StringValue(img.ImageId),
		ImageName:       volcengine.StringValue(img.ImageName),
		Description:     volcengine.StringValue(img.Description),
		ImageOwnerAlias: ownerAlias,
		OSType:          osType,
		OSName:          volcengine.StringValue(img.OsName),
		Platform:        volcengine.StringValue(img.Platform),
		Architecture:    volcengine.StringValue(img.Architecture),
		Status:          volcengine.StringValue(img.Status),
		Size:            int(volcengine.Int32Value(img.Size)),
		Region:          region,
		CreationTime:    volcengine.StringValue(img.CreatedAt),
		Tags:            tags,
		Provider:        "volcano",
		BootMode:        volcengine.StringValue(img.BootMode),
	}
}
