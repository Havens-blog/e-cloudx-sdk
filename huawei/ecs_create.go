package huawei

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	ecs "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2"
	ecsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2/model"
	ecsregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2/region"
)

// ECSCreateAdapterImpl 华为云 ECS 实例创建适配器
type ECSCreateAdapterImpl struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewECSCreateAdapter 创建华为云 ECS 创建适配器
func NewECSCreateAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *ECSCreateAdapterImpl {
	return &ECSCreateAdapterImpl{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *ECSCreateAdapterImpl) getClient(region string) (*ecs.EcsClient, error) {
	if region == "" {
		region = a.defaultRegion
	}
	auth, err := basic.NewCredentialsBuilder().
		WithAk(a.accessKeyID).
		WithSk(a.accessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}

	r, err := ecsregion.SafeValueOf(region)
	if err != nil {
		return nil, fmt.Errorf("华为云不支持地域 %s: %w", region, err)
	}

	client, err := ecs.EcsClientBuilder().
		WithRegion(r).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云ECS客户端失败: %w", err)
	}
	return ecs.NewEcsClient(client), nil
}

// CreateInstances 调用华为云 CreateServers API 创建实例
func (a *ECSCreateAdapterImpl) CreateInstances(ctx context.Context, params types.CreateInstanceParams) (*types.CreateInstanceResult, error) {
	client, err := a.getClient(params.Region)
	if err != nil {
		return nil, err
	}

	count := int32(params.Count)

	// 系统盘
	rootVolume := &ecsmodel.PrePaidServerRootVolume{
		Volumetype: ecsmodel.GetPrePaidServerRootVolumeVolumetypeEnum().SSD,
	}
	if params.SystemDiskSize > 0 {
		size := int32(params.SystemDiskSize)
		rootVolume.Size = &size
	}

	// 网络 (NIC)
	subnetID := params.SubnetID
	nics := []ecsmodel.PrePaidServerNic{
		{SubnetId: &subnetID},
	}

	// 安全组
	sgList := make([]ecsmodel.PrePaidServerSecurityGroup, 0, len(params.SecurityGroupIDs))
	for _, sgID := range params.SecurityGroupIDs {
		id := sgID
		sgList = append(sgList, ecsmodel.PrePaidServerSecurityGroup{Id: &id})
	}

	serverBody := &ecsmodel.PrePaidServer{
		ImageRef:       params.ImageID,
		FlavorRef:      params.InstanceType,
		Name:           params.InstanceName,
		Vpcid:          params.VPCID,
		Nics:           nics,
		RootVolume:     rootVolume,
		SecurityGroups: &sgList,
		Count:          &count,
	}

	// 可用区
	if params.Zone != "" {
		serverBody.AvailabilityZone = &params.Zone
	}

	// 数据盘
	if len(params.DataDisks) > 0 {
		dataVolumes := make([]ecsmodel.PrePaidServerDataVolume, 0, len(params.DataDisks))
		for _, d := range params.DataDisks {
			vol := ecsmodel.PrePaidServerDataVolume{
				Size:       int32(d.Size),
				Volumetype: ecsmodel.GetPrePaidServerDataVolumeVolumetypeEnum().SSD,
			}
			dataVolumes = append(dataVolumes, vol)
		}
		serverBody.DataVolumes = &dataVolumes
	}

	// 公网带宽
	if params.BandwidthOut > 0 {
		bwSize := int32(params.BandwidthOut)
		serverBody.Publicip = &ecsmodel.PrePaidServerPublicip{
			Eip: &ecsmodel.PrePaidServerEip{
				Iptype: "5_bgp",
				Bandwidth: &ecsmodel.PrePaidServerEipBandwidth{
					Size:      &bwSize,
					Sharetype: ecsmodel.GetPrePaidServerEipBandwidthSharetypeEnum().PER,
				},
			},
		}
	}

	// 密钥对
	if params.KeyPairName != "" {
		serverBody.KeyName = &params.KeyPairName
	}

	// 标签
	if len(params.Tags) > 0 {
		tags := make([]ecsmodel.PrePaidServerTag, 0, len(params.Tags))
		for k, v := range params.Tags {
			tags = append(tags, ecsmodel.PrePaidServerTag{Key: k, Value: v})
		}
		serverBody.ServerTags = &tags
	}

	request := &ecsmodel.CreateServersRequest{
		Body: &ecsmodel.CreateServersRequestBody{
			Server: serverBody,
		},
	}

	response, err := client.CreateServers(request)
	if err != nil {
		return nil, fmt.Errorf("华为云创建实例失败: %w", err)
	}

	var instanceIDs []string
	if response.ServerIds != nil {
		instanceIDs = *response.ServerIds
	}

	a.logger.Info("华为云创建实例成功",
		elog.Int("count", len(instanceIDs)),
		elog.String("region", params.Region))

	return &types.CreateInstanceResult{InstanceIDs: instanceIDs}, nil
}
