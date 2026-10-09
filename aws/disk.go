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

// DiskAdapter AWS云盘(EBS)适配器
type DiskAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component

	// ebsMetricHooks Disk 指标查询测试注入钩子(非 nil 时替代真实 CloudWatch 客户端创建;仅单测使用)
	ebsMetricHooks *cwMetricHooks
}

// NewDiskAdapter 创建AWS云盘适配器
func NewDiskAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *DiskAdapter {
	return &DiskAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *DiskAdapter) getClient(ctx context.Context, region string) (*ec2.Client, error) {
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
	client, err := a.getClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &ec2.DescribeVolumesInput{}

	if filter != nil {
		if len(filter.DiskIDs) > 0 {
			input.VolumeIds = filter.DiskIDs
		}
		var filters []ec2types.Filter
		if filter.Status != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("status"),
				Values: []string{filter.Status},
			})
		}
		if filter.InstanceID != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("attachment.instance-id"),
				Values: []string{filter.InstanceID},
			})
		}
		if len(filters) > 0 {
			input.Filters = filters
		}
	}

	var allInstances []types.DiskInstance
	paginator := ec2.NewDescribeVolumesPaginator(client, input)

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("获取EBS卷列表失败: %w", err)
		}
		for _, vol := range output.Volumes {
			instance := convertAWSVolume(vol, region)
			allInstances = append(allInstances, instance)
		}
	}

	a.logger.Info("获取AWS EBS卷列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// ListByInstanceID 获取实例挂载的云盘
func (a *DiskAdapter) ListByInstanceID(ctx context.Context, region, instanceID string) ([]types.DiskInstance, error) {
	filter := &types.DiskFilter{InstanceID: instanceID}
	return a.ListInstancesWithFilter(ctx, region, filter)
}

func convertAWSVolume(vol ec2types.Volume, region string) types.DiskInstance {
	tags := make(map[string]string)
	var name string
	for _, tag := range vol.Tags {
		if tag.Key != nil && tag.Value != nil {
			tags[*tag.Key] = *tag.Value
			if *tag.Key == "Name" {
				name = *tag.Value
			}
		}
	}

	var attachments []types.DiskAttachment
	var instanceID, device, attachedTime string
	for _, att := range vol.Attachments {
		attachment := types.DiskAttachment{}
		if att.InstanceId != nil {
			attachment.InstanceID = *att.InstanceId
			instanceID = *att.InstanceId
		}
		if att.Device != nil {
			attachment.Device = *att.Device
			device = *att.Device
		}
		if att.AttachTime != nil {
			attachment.AttachedTime = att.AttachTime.Format("2006-01-02T15:04:05Z")
			attachedTime = attachment.AttachedTime
		}
		attachments = append(attachments, attachment)
	}

	diskType := "data"
	if device == "/dev/xvda" || device == "/dev/sda1" {
		diskType = "system"
	}

	status := ""
	if vol.State != "" {
		status = string(vol.State)
	}

	size := 0
	if vol.Size != nil {
		size = int(*vol.Size)
	}

	iops := 0
	if vol.Iops != nil {
		iops = int(*vol.Iops)
	}

	throughput := 0
	if vol.Throughput != nil {
		throughput = int(*vol.Throughput)
	}

	return types.DiskInstance{
		DiskID:       aws.ToString(vol.VolumeId),
		DiskName:     name,
		DiskType:     diskType,
		Category:     string(vol.VolumeType),
		Size:         size,
		IOPS:         iops,
		Throughput:   throughput,
		Status:       status,
		InstanceID:   instanceID,
		Device:       device,
		AttachedTime: attachedTime,
		Encrypted:    aws.ToBool(vol.Encrypted),
		KMSKeyID:     aws.ToString(vol.KmsKeyId),
		Zone:         aws.ToString(vol.AvailabilityZone),
		Region:       region,
		CreationTime: vol.CreateTime.Format("2006-01-02T15:04:05Z"),
		Tags:         tags,
		Provider:     "aws",
		MultiAttach:  aws.ToBool(vol.MultiAttachEnabled),
		Attachments:  attachments,
	}
}
