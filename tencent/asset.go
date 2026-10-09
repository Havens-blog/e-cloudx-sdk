package tencent

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	cvm "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cvm/v20170312"
)

// AssetAdapter 腾讯云资产适配器
type AssetAdapter struct {
	account       *domain.CloudAccount
	defaultRegion string
	logger        *elog.Component
}

// NewAssetAdapter 创建腾讯云资产适配器
func NewAssetAdapter(account *domain.CloudAccount, defaultRegion string, logger *elog.Component) *AssetAdapter {
	if defaultRegion == "" {
		defaultRegion = "ap-guangzhou"
	}
	return &AssetAdapter{
		account:       account,
		defaultRegion: defaultRegion,
		logger:        logger,
	}
}

// getClient 获取CVM客户端
func (a *AssetAdapter) getClient(region string) (*cvm.Client, error) {
	credential := common.NewCredential(
		a.account.AccessKeyID,
		a.account.AccessKeySecret,
	)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "cvm.tencentcloudapi.com"

	client, err := cvm.NewClient(credential, region, cpf)
	if err != nil {
		return nil, fmt.Errorf("创建腾讯云CVM客户端失败: %w", err)
	}
	return client, nil
}

// GetRegions 获取支持的地域列表
func (a *AssetAdapter) GetRegions(ctx context.Context) ([]types.Region, error) {
	client, err := a.getClient(a.defaultRegion)
	if err != nil {
		return nil, err
	}

	request := cvm.NewDescribeRegionsRequest()
	response, err := client.DescribeRegions(request)
	if err != nil {
		return nil, fmt.Errorf("获取腾讯云地域列表失败: %w", err)
	}

	regions := make([]types.Region, 0, len(response.Response.RegionSet))
	for _, r := range response.Response.RegionSet {
		if *r.RegionState == "AVAILABLE" {
			regions = append(regions, types.Region{
				ID:          *r.Region,
				Name:        *r.Region,
				LocalName:   *r.RegionName,
				Description: *r.RegionName,
			})
		}
	}

	a.logger.Info("获取腾讯云地域列表成功", elog.Int("count", len(regions)))
	return regions, nil
}

// GetECSInstances 获取CVM实例列表
func (a *AssetAdapter) GetECSInstances(ctx context.Context, region string) ([]types.ECSInstance, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.ECSInstance
	offset := int64(0)
	limit := int64(100)

	for {
		request := cvm.NewDescribeInstancesRequest()
		request.Offset = &offset
		request.Limit = &limit

		response, err := client.DescribeInstances(request)
		if err != nil {
			return nil, fmt.Errorf("获取CVM实例列表失败: %w", err)
		}

		for _, inst := range response.Response.InstanceSet {
			instance := a.convertInstance(inst, region)
			allInstances = append(allInstances, instance)
		}

		if len(response.Response.InstanceSet) < int(limit) {
			break
		}
		offset += limit
	}

	a.logger.Info("获取腾讯云CVM实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertInstance 转换腾讯云实例为通用格式
func (a *AssetAdapter) convertInstance(inst *cvm.Instance, region string) types.ECSInstance {
	// 获取公网IP
	publicIP := ""
	if len(inst.PublicIpAddresses) > 0 {
		publicIP = *inst.PublicIpAddresses[0]
	}

	// 获取私网IP
	privateIP := ""
	if len(inst.PrivateIpAddresses) > 0 {
		privateIP = *inst.PrivateIpAddresses[0]
	}

	// 获取安全组
	securityGroups := make([]types.SecurityGroup, 0, len(inst.SecurityGroupIds))
	for _, sg := range inst.SecurityGroupIds {
		securityGroups = append(securityGroups, types.SecurityGroup{
			ID: *sg,
		})
	}

	// 获取标签
	tags := make(map[string]string)
	for _, tag := range inst.Tags {
		tags[*tag.Key] = *tag.Value
	}

	// 获取实例类型族
	instanceType := *inst.InstanceType
	instanceTypeFamily := ""
	// 从实例类型中提取类型族，如 S5.LARGE8 -> S5
	if len(instanceType) > 0 {
		for i, c := range instanceType {
			if c == '.' {
				instanceTypeFamily = instanceType[:i]
				break
			}
		}
	}

	// 获取CPU和内存
	cpu := 0
	memory := 0
	if inst.CPU != nil {
		cpu = int(*inst.CPU)
	}
	if inst.Memory != nil {
		memory = int(*inst.Memory) * 1024 // 转换为MB
	}

	// 获取计费类型
	chargeType := "PostPaid"
	if inst.InstanceChargeType != nil {
		switch *inst.InstanceChargeType {
		case "PREPAID":
			chargeType = "PrePaid"
		case "POSTPAID_BY_HOUR":
			chargeType = "PostPaid"
		case "SPOTPAID":
			chargeType = "Spot"
		}
	}

	// 获取系统盘信息
	systemDisk := types.SystemDisk{}
	if inst.SystemDisk != nil {
		if inst.SystemDisk.DiskId != nil {
			systemDisk.DiskID = *inst.SystemDisk.DiskId
		}
		if inst.SystemDisk.DiskType != nil {
			systemDisk.Category = *inst.SystemDisk.DiskType
		}
		if inst.SystemDisk.DiskSize != nil {
			systemDisk.Size = int(*inst.SystemDisk.DiskSize)
		}
	}

	// 获取数据盘信息
	dataDisks := make([]types.DataDisk, 0)
	if inst.DataDisks != nil {
		for _, disk := range inst.DataDisks {
			dataDisk := types.DataDisk{}
			if disk.DiskId != nil {
				dataDisk.DiskID = *disk.DiskId
			}
			if disk.DiskType != nil {
				dataDisk.Category = *disk.DiskType
			}
			if disk.DiskSize != nil {
				dataDisk.Size = int(*disk.DiskSize)
			}
			if disk.DeleteWithInstance != nil {
				dataDisk.DeleteWithInstance = *disk.DeleteWithInstance
			}
			if disk.Encrypt != nil {
				dataDisk.Encrypted = *disk.Encrypt
			}
			dataDisks = append(dataDisks, dataDisk)
		}
	}

	// 获取项目ID
	projectID := ""
	if inst.Placement != nil && inst.Placement.ProjectId != nil {
		projectID = fmt.Sprintf("%d", *inst.Placement.ProjectId)
	}

	return types.ECSInstance{
		InstanceID:         *inst.InstanceId,
		InstanceName:       *inst.InstanceName,
		Status:             types.NormalizeStatus(*inst.InstanceState),
		Region:             region,
		Zone:               *inst.Placement.Zone,
		InstanceType:       instanceType,
		InstanceTypeFamily: instanceTypeFamily,
		CPU:                cpu,
		Memory:             memory,
		OSType:             *inst.OsName,
		OSName:             *inst.OsName,
		ImageID:            *inst.ImageId,
		PublicIP:           publicIP,
		PrivateIP:          privateIP,
		VPCID:              *inst.VirtualPrivateCloud.VpcId,
		VSwitchID:          *inst.VirtualPrivateCloud.SubnetId,
		SecurityGroups:     securityGroups,
		SystemDisk:         systemDisk,
		DataDisks:          dataDisks,
		ChargeType:         chargeType,
		CreationTime:       *inst.CreatedTime,
		ExpiredTime:        safeString(inst.ExpiredTime),
		NetworkType:        "vpc",
		ProjectID:          projectID,
		Tags:               tags,
		Description:        "",
		Provider:           string(types.ProviderTencent),
		KeyPairName:        safeStringSlice(inst.LoginSettings.KeyIds),
	}
}

// safeString 安全获取字符串指针的值
func safeString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// safeStringSlice 安全获取字符串切片的第一个值
func safeStringSlice(s []*string) string {
	if len(s) == 0 || s[0] == nil {
		return ""
	}
	return *s[0]
}
