package aws

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/gotomicro/ego/core/elog"
)

// ECSAdapter AWS EC2适配器
type ECSAdapter struct {
	account       *domain.CloudAccount
	defaultRegion string
	logger        *elog.Component
	clients       map[string]*ec2.Client
	mu            sync.RWMutex
}

// NewECSAdapter 创建AWS EC2适配器
func NewECSAdapter(account *domain.CloudAccount, defaultRegion string, logger *elog.Component) *ECSAdapter {
	if defaultRegion == "" {
		defaultRegion = "us-east-1"
	}
	return &ECSAdapter{
		account:       account,
		defaultRegion: defaultRegion,
		logger:        logger,
		clients:       make(map[string]*ec2.Client),
	}
}

// getClient 获取或创建指定地域的EC2客户端
func (a *ECSAdapter) getClient(region string) *ec2.Client {
	a.mu.RLock()
	if client, ok := a.clients[region]; ok {
		a.mu.RUnlock()
		return client
	}
	a.mu.RUnlock()

	a.mu.Lock()
	defer a.mu.Unlock()

	if client, ok := a.clients[region]; ok {
		return client
	}

	cfg := aws.Config{
		Region: region,
		Credentials: credentials.NewStaticCredentialsProvider(
			a.account.AccessKeyID,
			a.account.AccessKeySecret,
			"",
		),
	}

	client := ec2.NewFromConfig(cfg)
	a.clients[region] = client
	return client
}

// GetRegions 获取支持的地域列表
func (a *ECSAdapter) GetRegions(ctx context.Context) ([]types.Region, error) {
	client := a.getClient(a.defaultRegion)

	input := &ec2.DescribeRegionsInput{AllRegions: aws.Bool(false)}
	result, err := client.DescribeRegions(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("获取AWS地域列表失败: %w", err)
	}

	regions := make([]types.Region, 0, len(result.Regions))
	for _, r := range result.Regions {
		regionID := aws.ToString(r.RegionName)
		regions = append(regions, types.Region{
			ID:          regionID,
			Name:        regionID,
			LocalName:   getAWSRegionLocalName(regionID),
			Description: aws.ToString(r.Endpoint),
		})
	}

	a.logger.Info("获取AWS地域列表成功", elog.Int("count", len(regions)))
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
	client := a.getClient(region)

	var allInstances []types.ECSInstance
	var nextToken *string

	for {
		input := &ec2.DescribeInstancesInput{
			MaxResults: aws.Int32(100),
			NextToken:  nextToken,
		}

		// 应用过滤条件
		if filter != nil {
			if len(filter.InstanceIDs) > 0 {
				input.InstanceIds = filter.InstanceIDs
			}
			var filters []ec2types.Filter
			if filter.InstanceName != "" {
				filters = append(filters, ec2types.Filter{
					Name:   aws.String("tag:Name"),
					Values: []string{filter.InstanceName},
				})
			}
			if len(filter.Status) > 0 {
				filters = append(filters, ec2types.Filter{
					Name:   aws.String("instance-state-name"),
					Values: filter.Status,
				})
			}
			if filter.VPCID != "" {
				filters = append(filters, ec2types.Filter{
					Name:   aws.String("vpc-id"),
					Values: []string{filter.VPCID},
				})
			}
			if filter.Zone != "" {
				filters = append(filters, ec2types.Filter{
					Name:   aws.String("availability-zone"),
					Values: []string{filter.Zone},
				})
			}
			for k, v := range filter.Tags {
				filters = append(filters, ec2types.Filter{
					Name:   aws.String("tag:" + k),
					Values: []string{v},
				})
			}
			if len(filters) > 0 {
				input.Filters = filters
			}
		}

		result, err := client.DescribeInstances(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("获取AWS EC2实例列表失败: %w", err)
		}

		for _, reservation := range result.Reservations {
			for _, inst := range reservation.Instances {
				instance := convertAWSInstance(inst, region)
				allInstances = append(allInstances, instance)
			}
		}

		if result.NextToken == nil {
			break
		}
		nextToken = result.NextToken
	}

	a.logger.Info("获取AWS EC2实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertAWSInstance 转换AWS实例为通用格式
func convertAWSInstance(inst ec2types.Instance, region string) types.ECSInstance {
	instanceName := ""
	tags := make(map[string]string)
	for _, tag := range inst.Tags {
		key := aws.ToString(tag.Key)
		value := aws.ToString(tag.Value)
		tags[key] = value
		if key == "Name" {
			instanceName = value
		}
	}

	securityGroups := make([]types.SecurityGroup, 0, len(inst.SecurityGroups))
	for _, sg := range inst.SecurityGroups {
		securityGroups = append(securityGroups, types.SecurityGroup{
			ID:   aws.ToString(sg.GroupId),
			Name: aws.ToString(sg.GroupName),
		})
	}

	instanceType := string(inst.InstanceType)
	instanceTypeFamily := ""
	for i, c := range instanceType {
		if c == '.' {
			instanceTypeFamily = instanceType[:i]
			break
		}
	}

	status := "unknown"
	if inst.State != nil {
		status = types.NormalizeStatus(string(inst.State.Name))
	}

	creationTime := ""
	if inst.LaunchTime != nil {
		creationTime = inst.LaunchTime.Format(time.RFC3339)
	}

	chargeType := "PostPaid"
	if inst.InstanceLifecycle == ec2types.InstanceLifecycleTypeSpot {
		chargeType = "Spot"
	}

	systemDisk := types.SystemDisk{}
	if inst.RootDeviceName != nil {
		systemDisk.Device = aws.ToString(inst.RootDeviceName)
	}

	cpu := 0
	if inst.CpuOptions != nil {
		cpu = int(aws.ToInt32(inst.CpuOptions.CoreCount) * aws.ToInt32(inst.CpuOptions.ThreadsPerCore))
	}

	return types.ECSInstance{
		InstanceID:         aws.ToString(inst.InstanceId),
		InstanceName:       instanceName,
		Status:             status,
		Region:             region,
		Zone:               aws.ToString(inst.Placement.AvailabilityZone),
		InstanceType:       instanceType,
		InstanceTypeFamily: instanceTypeFamily,
		CPU:                cpu,
		OSType:             string(inst.Platform),
		ImageID:            aws.ToString(inst.ImageId),
		PublicIP:           aws.ToString(inst.PublicIpAddress),
		PrivateIP:          aws.ToString(inst.PrivateIpAddress),
		VPCID:              aws.ToString(inst.VpcId),
		VSwitchID:          aws.ToString(inst.SubnetId),
		SecurityGroups:     securityGroups,
		SystemDisk:         systemDisk,
		ChargeType:         chargeType,
		CreationTime:       creationTime,
		NetworkType:        "vpc",
		Tags:               tags,
		Provider:           string(types.ProviderAWS),
		HostName:           aws.ToString(inst.PrivateDnsName),
		KeyPairName:        aws.ToString(inst.KeyName),
	}
}

// 注意: getAWSRegionLocalName 函数定义在 asset.go 中
