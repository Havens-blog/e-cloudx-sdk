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

// SnapshotAdapter AWS快照适配器
type SnapshotAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewSnapshotAdapter 创建AWS快照适配器
func NewSnapshotAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *SnapshotAdapter {
	return &SnapshotAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *SnapshotAdapter) getClient(ctx context.Context, region string) (*ec2.Client, error) {
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

// ListInstances 获取快照列表
func (a *SnapshotAdapter) ListInstances(ctx context.Context, region string) ([]types.SnapshotInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个快照详情
func (a *SnapshotAdapter) GetInstance(ctx context.Context, region, snapshotID string) (*types.SnapshotInstance, error) {
	instances, err := a.ListInstancesByIDs(ctx, region, []string{snapshotID})
	if err != nil {
		return nil, err
	}
	if len(instances) == 0 {
		return nil, fmt.Errorf("快照不存在: %s", snapshotID)
	}
	return &instances[0], nil
}

// ListInstancesByIDs 批量获取快照
func (a *SnapshotAdapter) ListInstancesByIDs(ctx context.Context, region string, snapshotIDs []string) ([]types.SnapshotInstance, error) {
	if len(snapshotIDs) == 0 {
		return nil, nil
	}
	filter := &types.SnapshotFilter{SnapshotIDs: snapshotIDs}
	return a.ListInstancesWithFilter(ctx, region, filter)
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
	client, err := a.getClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &ec2.DescribeSnapshotsInput{
		OwnerIds: []string{"self"}, // 只获取自己的快照
	}

	if filter != nil {
		if len(filter.SnapshotIDs) > 0 {
			input.SnapshotIds = filter.SnapshotIDs
		}
		var filters []ec2types.Filter
		if filter.Status != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("status"),
				Values: []string{filter.Status},
			})
		}
		if filter.SourceDiskID != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("volume-id"),
				Values: []string{filter.SourceDiskID},
			})
		}
		if len(filters) > 0 {
			input.Filters = filters
		}
	}

	var allInstances []types.SnapshotInstance
	paginator := ec2.NewDescribeSnapshotsPaginator(client, input)

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("获取快照列表失败: %w", err)
		}
		for _, snap := range output.Snapshots {
			instance := convertAWSSnapshot(snap, region)
			allInstances = append(allInstances, instance)
		}
	}

	a.logger.Info("获取AWS快照列表成功",
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
	// AWS快照不直接关联实例，需要先获取实例的卷，再获取卷的快照
	// 这里简化处理，返回空列表
	return nil, nil
}

func convertAWSSnapshot(snap ec2types.Snapshot, region string) types.SnapshotInstance {
	tags := make(map[string]string)
	var name string
	for _, tag := range snap.Tags {
		if tag.Key != nil && tag.Value != nil {
			tags[*tag.Key] = *tag.Value
			if *tag.Key == "Name" {
				name = *tag.Value
			}
		}
	}

	status := ""
	if snap.State != "" {
		status = string(snap.State)
	}

	size := 0
	if snap.VolumeSize != nil {
		size = int(*snap.VolumeSize)
	}

	return types.SnapshotInstance{
		SnapshotID:     aws.ToString(snap.SnapshotId),
		SnapshotName:   name,
		Description:    aws.ToString(snap.Description),
		SnapshotType:   "user",
		Status:         status,
		Progress:       aws.ToString(snap.Progress),
		SourceDiskSize: size,
		SourceDiskID:   aws.ToString(snap.VolumeId),
		Encrypted:      aws.ToBool(snap.Encrypted),
		KMSKeyID:       aws.ToString(snap.KmsKeyId),
		Region:         region,
		CreationTime:   snap.StartTime.Format("2006-01-02T15:04:05Z"),
		Tags:           tags,
		Provider:       "aws",
	}
}
