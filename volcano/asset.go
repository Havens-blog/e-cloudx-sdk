package volcano

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/ecs"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// AssetAdapter 火山云资产适配器
type AssetAdapter struct {
	account       *domain.CloudAccount
	defaultRegion string
	logger        *elog.Component
}

// NewAssetAdapter 创建火山云资产适配器
func NewAssetAdapter(account *domain.CloudAccount, defaultRegion string, logger *elog.Component) *AssetAdapter {
	if defaultRegion == "" {
		defaultRegion = "cn-beijing"
	}
	return &AssetAdapter{
		account:       account,
		defaultRegion: defaultRegion,
		logger:        logger,
	}
}

// getClient 获取ECS客户端
func (a *AssetAdapter) getClient(region string) (*ecs.ECS, error) {
	config := volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(
			a.account.AccessKeyID,
			a.account.AccessKeySecret,
			"",
		)).
		WithRegion(region)

	sess, err := session.NewSession(config)
	if err != nil {
		return nil, fmt.Errorf("创建火山云会话失败: %w", err)
	}

	client := ecs.New(sess)
	return client, nil
}

// GetRegions 获取支持的地域列表
func (a *AssetAdapter) GetRegions(ctx context.Context) ([]types.Region, error) {
	client, err := a.getClient(a.defaultRegion)
	if err != nil {
		return nil, err
	}

	input := &ecs.DescribeRegionsInput{}
	result, err := client.DescribeRegions(input)
	if err != nil {
		return nil, fmt.Errorf("获取火山云地域列表失败: %w", err)
	}

	regions := make([]types.Region, 0)
	if result.Regions != nil {
		for _, r := range result.Regions {
			regions = append(regions, types.Region{
				ID:          volcengine.StringValue(r.RegionId),
				Name:        volcengine.StringValue(r.RegionId),
				LocalName:   getVolcanoRegionLocalName(volcengine.StringValue(r.RegionId)),
				Description: volcengine.StringValue(r.RegionId),
			})
		}
	}

	a.logger.Info("获取火山云地域列表成功", elog.Int("count", len(regions)))
	return regions, nil
}

// GetECSInstances 获取ECS实例列表
func (a *AssetAdapter) GetECSInstances(ctx context.Context, region string) ([]types.ECSInstance, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.ECSInstance
	nextToken := ""
	maxResults := int32(100)

	for {
		input := &ecs.DescribeInstancesInput{
			MaxResults: &maxResults,
		}
		if nextToken != "" {
			input.NextToken = &nextToken
		}

		result, err := client.DescribeInstances(input)
		if err != nil {
			return nil, fmt.Errorf("获取火山云ECS实例列表失败: %w", err)
		}

		if result.Instances != nil {
			for _, inst := range result.Instances {
				instance := a.convertInstance(inst, region)
				allInstances = append(allInstances, instance)
			}
		}

		if result.NextToken == nil || *result.NextToken == "" {
			break
		}
		nextToken = *result.NextToken
	}

	a.logger.Info("获取火山云ECS实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertInstance 转换火山云实例为通用格式
func (a *AssetAdapter) convertInstance(inst *ecs.InstanceForDescribeInstancesOutput, region string) types.ECSInstance {
	// 获取公网IP
	publicIP := ""
	if inst.EipAddress != nil && inst.EipAddress.IpAddress != nil {
		publicIP = *inst.EipAddress.IpAddress
	}

	// 获取私网IP
	privateIP := ""
	if inst.NetworkInterfaces != nil && len(inst.NetworkInterfaces) > 0 {
		if inst.NetworkInterfaces[0].PrimaryIpAddress != nil {
			privateIP = *inst.NetworkInterfaces[0].PrimaryIpAddress
		}
	}

	// 获取安全组
	securityGroups := make([]types.SecurityGroup, 0)
	if inst.NetworkInterfaces != nil {
		for _, ni := range inst.NetworkInterfaces {
			if ni.SecurityGroupIds != nil {
				for _, sg := range ni.SecurityGroupIds {
					if sg != nil {
						securityGroups = append(securityGroups, types.SecurityGroup{
							ID: *sg,
						})
					}
				}
			}
		}
	}

	// 获取标签
	tags := make(map[string]string)
	if inst.Tags != nil {
		for _, tag := range inst.Tags {
			if tag.Key != nil && tag.Value != nil {
				tags[*tag.Key] = *tag.Value
			}
		}
	}

	// 获取VPC和子网
	vpcID := ""
	subnetID := ""
	if inst.VpcId != nil {
		vpcID = *inst.VpcId
	}
	if inst.NetworkInterfaces != nil && len(inst.NetworkInterfaces) > 0 {
		if inst.NetworkInterfaces[0].SubnetId != nil {
			subnetID = *inst.NetworkInterfaces[0].SubnetId
		}
	}

	// 获取计费类型
	chargeType := "PostPaid"
	if inst.InstanceChargeType != nil {
		switch *inst.InstanceChargeType {
		case "PrePaid":
			chargeType = "PrePaid"
		case "PostPaid":
			chargeType = "PostPaid"
		}
	}

	// 获取CPU和内存
	cpu := 0
	memory := 0
	if inst.Cpus != nil {
		cpu = int(*inst.Cpus)
	}
	if inst.MemorySize != nil {
		memory = int(*inst.MemorySize)
	}

	return types.ECSInstance{
		InstanceID:         volcengine.StringValue(inst.InstanceId),
		InstanceName:       volcengine.StringValue(inst.InstanceName),
		Status:             types.NormalizeStatus(volcengine.StringValue(inst.Status)),
		Region:             region,
		Zone:               volcengine.StringValue(inst.ZoneId),
		InstanceType:       volcengine.StringValue(inst.InstanceTypeId),
		InstanceTypeFamily: "",
		CPU:                cpu,
		Memory:             memory,
		OSType:             volcengine.StringValue(inst.OsType),
		OSName:             volcengine.StringValue(inst.OsName),
		ImageID:            volcengine.StringValue(inst.ImageId),
		PublicIP:           publicIP,
		PrivateIP:          privateIP,
		VPCID:              vpcID,
		VSwitchID:          subnetID,
		SecurityGroups:     securityGroups,
		ChargeType:         chargeType,
		CreationTime:       volcengine.StringValue(inst.CreatedAt),
		ExpiredTime:        volcengine.StringValue(inst.ExpiredAt),
		NetworkType:        "vpc",
		Tags:               tags,
		Description:        volcengine.StringValue(inst.Description),
		Provider:           string(types.ProviderVolcano),
		HostName:           volcengine.StringValue(inst.Hostname),
		KeyPairName:        volcengine.StringValue(inst.KeyPairName),
	}
}

// getVolcanoRegionLocalName 获取火山云地域的本地名称
func getVolcanoRegionLocalName(regionID string) string {
	regionNames := map[string]string{
		"cn-beijing":     "华北2(北京)",
		"cn-shanghai":    "华东2(上海)",
		"cn-guangzhou":   "华南1(广州)",
		"ap-southeast-1": "亚太东南(柔佛)",
		"ap-southeast-3": "亚太东南(曼谷)",
	}
	if name, ok := regionNames[regionID]; ok {
		return name
	}
	return regionID
}
