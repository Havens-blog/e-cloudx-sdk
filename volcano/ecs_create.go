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

// ECSCreateAdapterImpl 火山引擎 ECS 实例创建适配器
type ECSCreateAdapterImpl struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewECSCreateAdapter 创建火山引擎 ECS 创建适配器
func NewECSCreateAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *ECSCreateAdapterImpl {
	return &ECSCreateAdapterImpl{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *ECSCreateAdapterImpl) getClient(region string) (*ecs.ECS, error) {
	if region == "" {
		region = a.defaultRegion
	}
	config := volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(a.accessKeyID, a.accessKeySecret, "")).
		WithRegion(region)
	sess, err := session.NewSession(config)
	if err != nil {
		return nil, fmt.Errorf("创建火山引擎会话失败: %w", err)
	}
	return ecs.New(sess), nil
}

// CreateInstances 调用火山引擎 RunInstances API 创建实例
func (a *ECSCreateAdapterImpl) CreateInstances(ctx context.Context, params types.CreateInstanceParams) (*types.CreateInstanceResult, error) {
	client, err := a.getClient(params.Region)
	if err != nil {
		return nil, err
	}

	count := int32(params.Count)
	input := &ecs.RunInstancesInput{
		ImageId:      volcengine.String(params.ImageID),
		InstanceName: volcengine.String(params.InstanceName),
		ZoneId:       volcengine.String(params.Zone),
		Count:        &count,
	}

	// 实例规格：火山引擎同时支持 InstanceTypeId 和 InstanceType
	if params.InstanceType != "" {
		input.InstanceTypeId = volcengine.String(params.InstanceType)
	}

	// 主机名
	if params.HostName != "" {
		input.HostName = volcengine.String(params.HostName)
	}

	// 网络：通过 NetworkInterfaces 设置子网和安全组
	sgIDs := make([]*string, len(params.SecurityGroupIDs))
	for i, id := range params.SecurityGroupIDs {
		sgIDs[i] = volcengine.String(id)
	}
	input.NetworkInterfaces = []*ecs.NetworkInterfaceForRunInstancesInput{
		{
			SubnetId:         volcengine.String(params.SubnetID),
			SecurityGroupIds: sgIDs,
		},
	}

	// 系统盘 + 数据盘（火山引擎用 Volumes）
	var volumes []*ecs.VolumeForRunInstancesInput
	// 系统盘
	if params.SystemDiskType != "" || params.SystemDiskSize > 0 {
		sysVol := &ecs.VolumeForRunInstancesInput{}
		if params.SystemDiskType != "" {
			sysVol.VolumeType = volcengine.String(params.SystemDiskType)
		}
		if params.SystemDiskSize > 0 {
			size := int32(params.SystemDiskSize)
			sysVol.Size = &size
		}
		volumes = append(volumes, sysVol)
	}
	// 数据盘
	for _, d := range params.DataDisks {
		size := int32(d.Size)
		vol := &ecs.VolumeForRunInstancesInput{
			Size: &size,
		}
		if d.Category != "" {
			vol.VolumeType = volcengine.String(d.Category)
		}
		volumes = append(volumes, vol)
	}
	if len(volumes) > 0 {
		input.Volumes = volumes
	}

	// 计费方式
	if params.ChargeType != "" {
		input.InstanceChargeType = volcengine.String(params.ChargeType)
	}

	// 密钥对
	if params.KeyPairName != "" {
		input.KeyPairName = volcengine.String(params.KeyPairName)
	}

	// 标签
	if len(params.Tags) > 0 {
		tags := make([]*ecs.TagForRunInstancesInput, 0, len(params.Tags))
		for k, v := range params.Tags {
			tags = append(tags, &ecs.TagForRunInstancesInput{
				Key:   volcengine.String(k),
				Value: volcengine.String(v),
			})
		}
		input.Tags = tags
	}

	// 描述
	if params.InstanceName != "" {
		input.Description = volcengine.String(params.InstanceName)
	}

	output, err := client.RunInstances(input)
	if err != nil {
		return nil, fmt.Errorf("火山引擎创建实例失败: %w", err)
	}

	var instanceIDs []string
	for _, id := range output.InstanceIds {
		if id != nil {
			instanceIDs = append(instanceIDs, *id)
		}
	}

	a.logger.Info("火山引擎创建实例成功",
		elog.Int("count", len(instanceIDs)),
		elog.String("region", params.Region))

	return &types.CreateInstanceResult{InstanceIDs: instanceIDs}, nil
}
