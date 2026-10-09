package huawei

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	ecs "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2"
	ecsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2/model"
	ecsregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2/region"
)

// AssetAdapter 华为云资产适配器
type AssetAdapter struct {
	account       *domain.CloudAccount
	defaultRegion string
	logger        *elog.Component
}

// NewAssetAdapter 创建华为云资产适配器
func NewAssetAdapter(account *domain.CloudAccount, defaultRegion string, logger *elog.Component) *AssetAdapter {
	if defaultRegion == "" {
		defaultRegion = "cn-north-4"
	}
	return &AssetAdapter{
		account:       account,
		defaultRegion: defaultRegion,
		logger:        logger,
	}
}

// getClient 获取ECS客户端
func (a *AssetAdapter) getClient(region string) (*ecs.EcsClient, error) {
	auth, err := basic.NewCredentialsBuilder().
		WithAk(a.account.AccessKeyID).
		WithSk(a.account.AccessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}

	// 获取地域对象
	regionObj, err := ecsregion.SafeValueOf(region)
	if err != nil {
		// 如果地域不在预定义列表中，跳过该地域
		return nil, fmt.Errorf("不支持的华为云地域: %s", region)
	}

	hcClient, err := ecs.EcsClientBuilder().
		WithRegion(regionObj).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云ECS客户端失败: %w", err)
	}

	client := ecs.NewEcsClient(hcClient)
	return client, nil
}

// GetRegions 获取支持的地域列表
func (a *AssetAdapter) GetRegions(ctx context.Context) ([]types.Region, error) {
	// 华为云地域列表（静态定义，因为 ECS SDK 没有直接获取地域列表的 API）
	regions := []types.Region{
		{ID: "cn-north-1", Name: "cn-north-1", LocalName: "华北-北京一", Description: "China North 1 (Beijing)"},
		{ID: "cn-north-4", Name: "cn-north-4", LocalName: "华北-北京四", Description: "China North 4 (Beijing)"},
		{ID: "cn-north-9", Name: "cn-north-9", LocalName: "华北-乌兰察布一", Description: "China North 9 (Ulanqab)"},
		{ID: "cn-east-2", Name: "cn-east-2", LocalName: "华东-上海二", Description: "China East 2 (Shanghai)"},
		{ID: "cn-east-3", Name: "cn-east-3", LocalName: "华东-上海一", Description: "China East 3 (Shanghai)"},
		{ID: "cn-south-1", Name: "cn-south-1", LocalName: "华南-广州", Description: "China South 1 (Guangzhou)"},
		{ID: "cn-south-2", Name: "cn-south-2", LocalName: "华南-深圳", Description: "China South 2 (Shenzhen)"},
		{ID: "cn-southwest-2", Name: "cn-southwest-2", LocalName: "西南-贵阳一", Description: "China Southwest 2 (Guiyang)"},
		{ID: "ap-southeast-1", Name: "ap-southeast-1", LocalName: "亚太-新加坡", Description: "Asia Pacific (Singapore)"},
		{ID: "ap-southeast-2", Name: "ap-southeast-2", LocalName: "亚太-曼谷", Description: "Asia Pacific (Bangkok)"},
		{ID: "ap-southeast-3", Name: "ap-southeast-3", LocalName: "亚太-雅加达", Description: "Asia Pacific (Jakarta)"},
		{ID: "af-south-1", Name: "af-south-1", LocalName: "非洲-约翰内斯堡", Description: "Africa (Johannesburg)"},
		{ID: "la-north-2", Name: "la-north-2", LocalName: "拉美-墨西哥城二", Description: "Latin America (Mexico City)"},
		{ID: "la-south-2", Name: "la-south-2", LocalName: "拉美-圣地亚哥", Description: "Latin America (Santiago)"},
	}

	a.logger.Info("获取华为云地域列表成功", elog.Int("count", len(regions)))
	return regions, nil
}

// GetECSInstances 获取ECS实例列表
func (a *AssetAdapter) GetECSInstances(ctx context.Context, region string) ([]types.ECSInstance, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.ECSInstance
	offset := int32(1)
	limit := int32(100)

	for {
		request := &ecsmodel.ListServersDetailsRequest{
			Offset: &offset,
			Limit:  &limit,
		}

		response, err := client.ListServersDetails(request)
		if err != nil {
			return nil, fmt.Errorf("获取华为云ECS实例列表失败: %w", err)
		}

		if response.Servers == nil || len(*response.Servers) == 0 {
			break
		}

		for _, inst := range *response.Servers {
			instance := a.convertInstance(inst, region)
			allInstances = append(allInstances, instance)
		}

		if len(*response.Servers) < int(limit) {
			break
		}
		offset++
	}

	a.logger.Info("获取华为云ECS实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertInstance 转换华为云实例为通用格式
func (a *AssetAdapter) convertInstance(inst ecsmodel.ServerDetail, region string) types.ECSInstance {
	// 获取公网IP
	publicIP := ""
	if inst.Addresses != nil {
		for _, addrs := range inst.Addresses {
			for _, addr := range addrs {
				if addr.OSEXTIPStype != nil && addr.OSEXTIPStype.Value() == "floating" {
					publicIP = addr.Addr
					break
				}
			}
			if publicIP != "" {
				break
			}
		}
	}

	// 获取私网IP
	privateIP := ""
	if inst.Addresses != nil {
		for _, addrs := range inst.Addresses {
			for _, addr := range addrs {
				if addr.OSEXTIPStype != nil && addr.OSEXTIPStype.Value() == "fixed" {
					privateIP = addr.Addr
					break
				}
			}
			if privateIP != "" {
				break
			}
		}
	}

	// 获取安全组
	securityGroups := make([]types.SecurityGroup, 0)
	for _, sg := range inst.SecurityGroups {
		securityGroups = append(securityGroups, types.SecurityGroup{
			ID:   sg.Id,
			Name: sg.Name,
		})
	}

	// 获取标签
	tags := make(map[string]string)
	if inst.Tags != nil {
		for _, tag := range *inst.Tags {
			// 华为云标签格式为 "key=value"
			tags[tag] = ""
		}
	}

	// 获取实例类型信息
	instanceType := ""
	if inst.Flavor.Id != "" {
		instanceType = inst.Flavor.Id
	}

	// 获取CPU和内存
	cpu := 0
	memory := 0
	if inst.Flavor.Vcpus != "" {
		fmt.Sscanf(inst.Flavor.Vcpus, "%d", &cpu)
	}
	if inst.Flavor.Ram != "" {
		fmt.Sscanf(inst.Flavor.Ram, "%d", &memory)
	}

	// 获取计费类型
	chargeType := "PostPaid"
	if inst.Metadata != nil {
		if ct, ok := inst.Metadata["charging_mode"]; ok {
			switch ct {
			case "0":
				chargeType = "PostPaid"
			case "1":
				chargeType = "PrePaid"
			case "2":
				chargeType = "Spot"
			}
		}
	}

	// 获取系统盘信息
	systemDisk := types.SystemDisk{}
	for _, vol := range inst.OsExtendedVolumesvolumesAttached {
		if vol.BootIndex != nil && *vol.BootIndex == "0" {
			systemDisk.DiskID = vol.Id
			break
		}
	}

	// 获取VPC和子网
	vpcID := ""
	subnetID := ""
	if inst.Metadata != nil {
		if v, ok := inst.Metadata["vpc_id"]; ok {
			vpcID = v
		}
	}

	// 获取可用区
	zone := inst.OSEXTAZavailabilityZone

	// 获取状态
	status := types.NormalizeStatus(inst.Status)

	// 获取创建时间
	creationTime := ""
	if inst.Created != "" {
		creationTime = inst.Created
	}

	// 获取密钥对名称
	keyPairName := inst.KeyName

	// 获取镜像ID
	imageID := ""
	if inst.Image.Id != "" {
		imageID = inst.Image.Id
	}

	// 获取企业项目ID
	projectID := ""
	if inst.EnterpriseProjectId != nil {
		projectID = *inst.EnterpriseProjectId
	}

	return types.ECSInstance{
		InstanceID:         inst.Id,
		InstanceName:       inst.Name,
		Status:             status,
		Region:             region,
		Zone:               zone,
		InstanceType:       instanceType,
		InstanceTypeFamily: "",
		CPU:                cpu,
		Memory:             memory,
		OSType:             "",
		OSName:             "",
		ImageID:            imageID,
		PublicIP:           publicIP,
		PrivateIP:          privateIP,
		VPCID:              vpcID,
		VSwitchID:          subnetID,
		SecurityGroups:     securityGroups,
		SystemDisk:         systemDisk,
		ChargeType:         chargeType,
		CreationTime:       creationTime,
		NetworkType:        "vpc",
		ProjectID:          projectID,
		Tags:               tags,
		Description:        safeStringPtr(inst.Description),
		Provider:           string(types.ProviderHuawei),
		HostName:           inst.OSEXTSRVATTRhostname,
		KeyPairName:        keyPairName,
	}
}

// safeString 安全获取字符串指针的值
func safeString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// safeStringPtr 安全获取字符串指针的值
func safeStringPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
