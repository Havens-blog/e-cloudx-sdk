package aws

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/gotomicro/ego/core/elog"
)

// ImageAdapter AWS镜像适配器
type ImageAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewImageAdapter 创建AWS镜像适配器
func NewImageAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *ImageAdapter {
	return &ImageAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *ImageAdapter) getClient(ctx context.Context, region string) (*ec2.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			a.accessKeyID, a.accessKeySecret, "",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("加载AWS配置失败: %w", err)
	}
	return ec2.NewFromConfig(cfg), nil
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
	client, err := a.getClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &ec2.DescribeImagesInput{}

	// 默认只查询自己的镜像
	if filter == nil {
		input.Owners = []string{"self"}
	} else {
		if len(filter.ImageIDs) > 0 {
			input.ImageIds = filter.ImageIDs
		}

		var filters []ec2types.Filter
		if filter.ImageOwnerAlias != "" {
			switch filter.ImageOwnerAlias {
			case "self":
				input.Owners = []string{"self"}
			case "system", "amazon":
				input.Owners = []string{"amazon"}
			case "marketplace":
				input.Owners = []string{"aws-marketplace"}
			}
		} else if len(filter.ImageIDs) == 0 {
			input.Owners = []string{"self"}
		}

		if filter.ImageName != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("name"),
				Values: []string{"*" + filter.ImageName + "*"},
			})
		}
		if filter.Architecture != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("architecture"),
				Values: []string{filter.Architecture},
			})
		}
		if filter.Status != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("state"),
				Values: []string{filter.Status},
			})
		}
		if filter.OSType != "" {
			platform := filter.OSType
			if platform == "linux" {
				// Linux 镜像没有 platform 字段
			} else if platform == "windows" {
				filters = append(filters, ec2types.Filter{
					Name:   aws.String("platform"),
					Values: []string{"windows"},
				})
			}
		}
		if len(filters) > 0 {
			input.Filters = filters
		}
	}

	var allInstances []types.ImageInstance
	paginator := ec2.NewDescribeImagesPaginator(client, input)

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("获取镜像列表失败: %w", err)
		}
		for _, img := range output.Images {
			instance := convertAWSImage(img, region)
			allInstances = append(allInstances, instance)
		}
	}

	a.logger.Info("获取AWS镜像列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

func convertAWSImage(img ec2types.Image, region string) types.ImageInstance {
	tags := make(map[string]string)
	for _, tag := range img.Tags {
		if tag.Key != nil && tag.Value != nil {
			tags[*tag.Key] = *tag.Value
		}
	}

	var diskMappings []types.ImageDiskMapping
	for _, mapping := range img.BlockDeviceMappings {
		dm := types.ImageDiskMapping{
			Device: aws.ToString(mapping.DeviceName),
		}
		if mapping.Ebs != nil {
			dm.Size = int(aws.ToInt32(mapping.Ebs.VolumeSize))
			dm.SnapshotID = aws.ToString(mapping.Ebs.SnapshotId)
		}
		diskMappings = append(diskMappings, dm)
	}

	// 确定镜像来源
	ownerAlias := "self"
	if aws.ToString(img.OwnerId) == "amazon" || aws.ToString(img.ImageOwnerAlias) == "amazon" {
		ownerAlias = "system"
	} else if aws.ToString(img.ImageOwnerAlias) == "aws-marketplace" {
		ownerAlias = "marketplace"
	}

	// 确定操作系统类型
	osType := "linux"
	if img.Platform == ec2types.PlatformValuesWindows {
		osType = "windows"
	}

	return types.ImageInstance{
		ImageID:            aws.ToString(img.ImageId),
		ImageName:          aws.ToString(img.Name),
		Description:        aws.ToString(img.Description),
		ImageOwnerAlias:    ownerAlias,
		IsPublic:           aws.ToBool(img.Public),
		OSType:             osType,
		OSName:             aws.ToString(img.Description),
		Platform:           aws.ToString(img.PlatformDetails),
		Architecture:       string(img.Architecture),
		Status:             string(img.State),
		DiskDeviceMappings: diskMappings,
		Region:             region,
		CreationTime:       aws.ToString(img.CreationDate),
		Tags:               tags,
		Provider:           "aws",
		BootMode:           string(img.BootMode),
	}
}
