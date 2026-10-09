package aliyun

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/gotomicro/ego/core/elog"
)

// ECSCreateAdapterImpl 阿里云 ECS 实例创建适配器
type ECSCreateAdapterImpl struct {
	client *Client
	logger *elog.Component
}

// NewECSCreateAdapter 创建阿里云 ECS 创建适配器
func NewECSCreateAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *ECSCreateAdapterImpl {
	return &ECSCreateAdapterImpl{
		client: NewClient(accessKeyID, accessKeySecret, defaultRegion, logger),
		logger: logger,
	}
}

// CreateInstances 调用阿里云 ECS RunInstances API 创建实例
func (a *ECSCreateAdapterImpl) CreateInstances(ctx context.Context, params types.CreateInstanceParams) (*types.CreateInstanceResult, error) {
	if err := a.client.WaitRateLimit(ctx); err != nil {
		return nil, err
	}

	ecsClient, err := a.client.GetECSClient(params.Region)
	if err != nil {
		return nil, fmt.Errorf("创建ECS客户端失败: %w", err)
	}

	request := ecs.CreateRunInstancesRequest()
	request.Scheme = "https"
	request.RegionId = params.Region
	request.ZoneId = params.Zone
	request.InstanceType = params.InstanceType
	request.ImageId = params.ImageID
	request.VSwitchId = params.SubnetID
	request.InstanceName = params.InstanceName
	request.HostName = params.HostName
	request.Amount = requests.NewInteger(params.Count)

	// 安全组：优先使用单数形式（兼容不支持 ENI 的老旧实例规格）
	// SecurityGroupIds（复数）需要 ENI 支持，第一代实例会报 InvalidInstanceType.ElasticNetworkInterfaceNotSupported
	if len(params.SecurityGroupIDs) == 1 {
		request.SecurityGroupId = params.SecurityGroupIDs[0]
	} else if len(params.SecurityGroupIDs) > 1 {
		request.SecurityGroupIds = &params.SecurityGroupIDs
	}

	// 系统盘
	if params.SystemDiskType != "" {
		request.SystemDiskCategory = params.SystemDiskType
	}
	if params.SystemDiskSize > 0 {
		request.SystemDiskSize = strconv.Itoa(params.SystemDiskSize)
	}

	// 数据盘
	if len(params.DataDisks) > 0 {
		dataDiskList := make([]ecs.RunInstancesDataDisk, 0, len(params.DataDisks))
		for _, d := range params.DataDisks {
			dataDiskList = append(dataDiskList, ecs.RunInstancesDataDisk{
				Category: d.Category,
				Size:     strconv.Itoa(d.Size),
			})
		}
		request.DataDisk = &dataDiskList
	}

	// 公网带宽
	if params.BandwidthOut > 0 {
		request.InternetMaxBandwidthOut = requests.NewInteger(params.BandwidthOut)
	}

	// 计费方式
	if params.ChargeType != "" {
		request.InstanceChargeType = params.ChargeType
	}

	// 密钥对
	if params.KeyPairName != "" {
		request.KeyPairName = params.KeyPairName
	}

	// 标签
	if len(params.Tags) > 0 {
		tagList := make([]ecs.RunInstancesTag, 0, len(params.Tags))
		for k, v := range params.Tags {
			tagList = append(tagList, ecs.RunInstancesTag{Key: k, Value: v})
		}
		request.Tag = &tagList
	}

	var response *ecs.RunInstancesResponse
	err = a.client.RetryWithBackoff(ctx, func() error {
		var e error
		response, e = ecsClient.RunInstances(request)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("阿里云创建实例失败: %w", err)
	}

	result := &types.CreateInstanceResult{
		InstanceIDs: response.InstanceIdSets.InstanceIdSet,
	}

	a.logger.Info("阿里云创建实例成功",
		elog.Int("count", len(result.InstanceIDs)),
		elog.String("region", params.Region))

	return result, nil
}
