package volcano

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/ecs"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// ECSAdapter 火山引擎ECS适配器
type ECSAdapter struct {
	account       *domain.CloudAccount
	defaultRegion string
	logger        *elog.Component
}

// NewECSAdapter 创建火山引擎ECS适配器
func NewECSAdapter(account *domain.CloudAccount, defaultRegion string, logger *elog.Component) *ECSAdapter {
	if defaultRegion == "" {
		defaultRegion = "cn-beijing"
	}
	return &ECSAdapter{
		account:       account,
		defaultRegion: defaultRegion,
		logger:        logger,
	}
}

// getClient 获取ECS客户端
func (a *ECSAdapter) getClient(region string) (*ecs.ECS, error) {
	config := volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(
			a.account.AccessKeyID,
			a.account.AccessKeySecret,
			"",
		)).
		WithRegion(region)

	sess, err := session.NewSession(config)
	if err != nil {
		return nil, fmt.Errorf("创建火山引擎会话失败: %w", err)
	}

	return ecs.New(sess), nil
}

// GetRegions 获取支持的地域列表
func (a *ECSAdapter) GetRegions(ctx context.Context) ([]types.Region, error) {
	client, err := a.getClient(a.defaultRegion)
	if err != nil {
		return nil, err
	}

	input := &ecs.DescribeRegionsInput{}
	result, err := client.DescribeRegions(input)
	if err != nil {
		return nil, fmt.Errorf("获取火山引擎地域列表失败: %w", err)
	}

	regions := make([]types.Region, 0)
	if result.Regions != nil {
		for _, r := range result.Regions {
			regionID := volcengine.StringValue(r.RegionId)
			regions = append(regions, types.Region{
				ID:          regionID,
				Name:        regionID,
				LocalName:   getVolcanoRegionLocalName(regionID),
				Description: regionID,
			})
		}
	}

	a.logger.Info("获取火山引擎地域列表成功", elog.Int("count", len(regions)))
	return regions, nil
}

// ListInstances 获取云主机实例列表
func (a *ECSAdapter) ListInstances(ctx context.Context, region string) ([]types.ECSInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个云主机实例详情
func (a *ECSAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.ECSInstance, error) {
	instances, err := a.ListInstancesByIDs(ctx, region, []string{instanceID})
	if err != nil {
		return nil, err
	}
	if len(instances) == 0 {
		return nil, fmt.Errorf("实例不存在: %s", instanceID)
	}
	return &instances[0], nil
}

// ListInstancesByIDs 批量获取云主机实例
func (a *ECSAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.ECSInstance, error) {
	if len(instanceIDs) == 0 {
		return nil, nil
	}
	filter := &cloudx.ECSInstanceFilter{InstanceIDs: instanceIDs}
	return a.ListInstancesWithFilter(ctx, region, filter)
}

// GetInstanceStatus 获取实例状态
func (a *ECSAdapter) GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, instanceID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取实例列表
func (a *ECSAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *cloudx.ECSInstanceFilter) ([]types.ECSInstance, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.ECSInstance
	nextToken := ""
	maxResults := int32(100)

	for {
		input := &ecs.DescribeInstancesInput{MaxResults: &maxResults}
		if nextToken != "" {
			input.NextToken = &nextToken
		}

		// 应用过滤条件
		if filter != nil {
			if len(filter.InstanceIDs) > 0 {
				input.InstanceIds = volcengine.StringSlice(filter.InstanceIDs)
			}
			if filter.InstanceName != "" {
				input.InstanceName = &filter.InstanceName
			}
			if len(filter.Status) > 0 {
				input.Status = &filter.Status[0]
			}
			if filter.VPCID != "" {
				input.VpcId = &filter.VPCID
			}
			if filter.Zone != "" {
				input.ZoneId = &filter.Zone
			}
		}

		result, err := client.DescribeInstances(input)
		if err != nil {
			return nil, fmt.Errorf("获取火山引擎ECS实例列表失败: %w", err)
		}

		if result.Instances != nil {
			for _, inst := range result.Instances {
				instance := convertVolcanoInstance(inst, region)
				allInstances = append(allInstances, instance)
			}
		}

		if result.NextToken == nil || *result.NextToken == "" {
			break
		}
		nextToken = *result.NextToken
	}

	a.logger.Info("获取火山引擎ECS实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertVolcanoInstance 转换火山引擎实例为通用格式
func convertVolcanoInstance(inst *ecs.InstanceForDescribeInstancesOutput, region string) types.ECSInstance {
	publicIP := ""
	if inst.EipAddress != nil && inst.EipAddress.IpAddress != nil {
		publicIP = *inst.EipAddress.IpAddress
	}

	privateIP := ""
	if inst.NetworkInterfaces != nil && len(inst.NetworkInterfaces) > 0 {
		if inst.NetworkInterfaces[0].PrimaryIpAddress != nil {
			privateIP = *inst.NetworkInterfaces[0].PrimaryIpAddress
		}
	}

	securityGroups := make([]types.SecurityGroup, 0)
	if inst.NetworkInterfaces != nil {
		for _, ni := range inst.NetworkInterfaces {
			if ni.SecurityGroupIds != nil {
				for _, sg := range ni.SecurityGroupIds {
					if sg != nil {
						securityGroups = append(securityGroups, types.SecurityGroup{ID: *sg})
					}
				}
			}
		}
	}

	tags := make(map[string]string)
	if inst.Tags != nil {
		for _, tag := range inst.Tags {
			if tag.Key != nil && tag.Value != nil {
				tags[*tag.Key] = *tag.Value
			}
		}
	}

	vpcID, subnetID := "", ""
	if inst.VpcId != nil {
		vpcID = *inst.VpcId
	}
	if inst.NetworkInterfaces != nil && len(inst.NetworkInterfaces) > 0 {
		if inst.NetworkInterfaces[0].SubnetId != nil {
			subnetID = *inst.NetworkInterfaces[0].SubnetId
		}
	}

	chargeType := "PostPaid"
	if inst.InstanceChargeType != nil {
		switch *inst.InstanceChargeType {
		case "PrePaid":
			chargeType = "PrePaid"
		case "PostPaid":
			chargeType = "PostPaid"
		}
	}

	cpu, memory := 0, 0
	if inst.Cpus != nil {
		cpu = int(*inst.Cpus)
	}
	if inst.MemorySize != nil {
		memory = int(*inst.MemorySize)
	}

	return types.ECSInstance{
		InstanceID:     volcengine.StringValue(inst.InstanceId),
		InstanceName:   volcengine.StringValue(inst.InstanceName),
		Status:         types.NormalizeStatus(volcengine.StringValue(inst.Status)),
		Region:         region,
		Zone:           volcengine.StringValue(inst.ZoneId),
		InstanceType:   volcengine.StringValue(inst.InstanceTypeId),
		CPU:            cpu,
		Memory:         memory,
		OSType:         volcengine.StringValue(inst.OsType),
		OSName:         volcengine.StringValue(inst.OsName),
		ImageID:        volcengine.StringValue(inst.ImageId),
		PublicIP:       publicIP,
		PrivateIP:      privateIP,
		VPCID:          vpcID,
		VSwitchID:      subnetID,
		SecurityGroups: securityGroups,
		ChargeType:     chargeType,
		CreationTime:   volcengine.StringValue(inst.CreatedAt),
		ExpiredTime:    volcengine.StringValue(inst.ExpiredAt),
		NetworkType:    "vpc",
		Tags:           tags,
		Description:    volcengine.StringValue(inst.Description),
		Provider:       string(types.ProviderVolcano),
		HostName:       volcengine.StringValue(inst.Hostname),
		KeyPairName:    volcengine.StringValue(inst.KeyPairName),
	}
}

// 注意: getVolcanoRegionLocalName 函数定义在 asset.go 中
