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

// ECSCreateAdapterImpl 腾讯云 CVM 实例创建适配器
type ECSCreateAdapterImpl struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewECSCreateAdapter 创建腾讯云 CVM 创建适配器
func NewECSCreateAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *ECSCreateAdapterImpl {
	return &ECSCreateAdapterImpl{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *ECSCreateAdapterImpl) getClient(region string) (*cvm.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}
	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "cvm.tencentcloudapi.com"
	return cvm.NewClient(credential, region, cpf)
}

// CreateInstances 调用腾讯云 RunInstances API 创建实例
func (a *ECSCreateAdapterImpl) CreateInstances(ctx context.Context, params types.CreateInstanceParams) (*types.CreateInstanceResult, error) {
	client, err := a.getClient(params.Region)
	if err != nil {
		return nil, fmt.Errorf("创建腾讯云CVM客户端失败: %w", err)
	}

	request := cvm.NewRunInstancesRequest()
	request.Placement = &cvm.Placement{Zone: common.StringPtr(params.Zone)}
	request.InstanceType = common.StringPtr(params.InstanceType)
	request.ImageId = common.StringPtr(params.ImageID)
	request.InstanceCount = common.Int64Ptr(int64(params.Count))

	if params.InstanceName != "" {
		request.InstanceName = common.StringPtr(params.InstanceName)
	}
	if params.HostName != "" {
		request.HostName = common.StringPtr(params.HostName)
	}

	// 网络
	request.VirtualPrivateCloud = &cvm.VirtualPrivateCloud{
		VpcId:    common.StringPtr(params.VPCID),
		SubnetId: common.StringPtr(params.SubnetID),
	}

	// 安全组
	if len(params.SecurityGroupIDs) > 0 {
		request.SecurityGroupIds = common.StringPtrs(params.SecurityGroupIDs)
	}

	// 系统盘
	if params.SystemDiskType != "" || params.SystemDiskSize > 0 {
		request.SystemDisk = &cvm.SystemDisk{}
		if params.SystemDiskType != "" {
			request.SystemDisk.DiskType = common.StringPtr(params.SystemDiskType)
		}
		if params.SystemDiskSize > 0 {
			request.SystemDisk.DiskSize = common.Int64Ptr(int64(params.SystemDiskSize))
		}
	}

	// 数据盘
	if len(params.DataDisks) > 0 {
		for _, d := range params.DataDisks {
			dd := &cvm.DataDisk{DiskSize: common.Int64Ptr(int64(d.Size))}
			if d.Category != "" {
				dd.DiskType = common.StringPtr(d.Category)
			}
			request.DataDisks = append(request.DataDisks, dd)
		}
	}

	// 公网带宽
	if params.BandwidthOut > 0 {
		request.InternetAccessible = &cvm.InternetAccessible{
			InternetMaxBandwidthOut: common.Int64Ptr(int64(params.BandwidthOut)),
		}
	}

	// 计费方式
	if params.ChargeType != "" {
		request.InstanceChargeType = common.StringPtr(params.ChargeType)
	}

	// 密钥对
	if params.KeyPairName != "" {
		request.LoginSettings = &cvm.LoginSettings{
			KeyIds: common.StringPtrs([]string{params.KeyPairName}),
		}
	}

	// 标签
	if len(params.Tags) > 0 {
		tagSpec := &cvm.TagSpecification{ResourceType: common.StringPtr("instance")}
		for k, v := range params.Tags {
			tagSpec.Tags = append(tagSpec.Tags, &cvm.Tag{
				Key:   common.StringPtr(k),
				Value: common.StringPtr(v),
			})
		}
		request.TagSpecification = []*cvm.TagSpecification{tagSpec}
	}

	response, err := client.RunInstances(request)
	if err != nil {
		return nil, fmt.Errorf("腾讯云创建实例失败: %w", err)
	}

	var instanceIDs []string
	for _, id := range response.Response.InstanceIdSet {
		if id != nil {
			instanceIDs = append(instanceIDs, *id)
		}
	}

	a.logger.Info("腾讯云创建实例成功",
		elog.Int("count", len(instanceIDs)),
		elog.String("region", params.Region))

	return &types.CreateInstanceResult{InstanceIDs: instanceIDs}, nil
}
