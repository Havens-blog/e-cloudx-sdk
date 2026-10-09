package aws

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/gotomicro/ego/core/elog"
)

// AssetAdapter AWS资产适配器
type AssetAdapter struct {
	account       *domain.CloudAccount
	defaultRegion string
	logger        *elog.Component
	clients       map[string]*ec2.Client
	mu            sync.RWMutex
}

// NewAssetAdapter 创建AWS资产适配器
func NewAssetAdapter(account *domain.CloudAccount, defaultRegion string, logger *elog.Component) *AssetAdapter {
	if defaultRegion == "" {
		defaultRegion = "us-east-1"
	}
	return &AssetAdapter{
		account:       account,
		defaultRegion: defaultRegion,
		logger:        logger,
		clients:       make(map[string]*ec2.Client),
	}
}

// getClient 获取或创建指定地域的EC2客户端
func (a *AssetAdapter) getClient(region string) *ec2.Client {
	a.mu.RLock()
	if client, ok := a.clients[region]; ok {
		a.mu.RUnlock()
		return client
	}
	a.mu.RUnlock()

	a.mu.Lock()
	defer a.mu.Unlock()

	// 双重检查
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
func (a *AssetAdapter) GetRegions(ctx context.Context) ([]types.Region, error) {
	client := a.getClient(a.defaultRegion)

	input := &ec2.DescribeRegionsInput{
		AllRegions: aws.Bool(false), // 只返回启用的地域
	}

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

// GetECSInstances 获取EC2实例列表
func (a *AssetAdapter) GetECSInstances(ctx context.Context, region string) ([]types.ECSInstance, error) {
	client := a.getClient(region)

	var allInstances []types.ECSInstance
	var nextToken *string

	for {
		input := &ec2.DescribeInstancesInput{
			MaxResults: aws.Int32(100),
			NextToken:  nextToken,
		}

		result, err := client.DescribeInstances(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("获取EC2实例列表失败: %w", err)
		}

		for _, reservation := range result.Reservations {
			for _, inst := range reservation.Instances {
				instance := a.convertInstance(inst, region)
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

// convertInstance 转换AWS实例为通用格式
func (a *AssetAdapter) convertInstance(inst ec2types.Instance, region string) types.ECSInstance {
	// 获取实例名称
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

	// 获取公网IP
	publicIP := aws.ToString(inst.PublicIpAddress)

	// 获取私网IP
	privateIP := aws.ToString(inst.PrivateIpAddress)

	// 获取安全组
	securityGroups := make([]types.SecurityGroup, 0, len(inst.SecurityGroups))
	for _, sg := range inst.SecurityGroups {
		securityGroups = append(securityGroups, types.SecurityGroup{
			ID:   aws.ToString(sg.GroupId),
			Name: aws.ToString(sg.GroupName),
		})
	}

	// 获取实例类型族
	instanceType := string(inst.InstanceType)
	instanceTypeFamily := ""
	if len(instanceType) > 0 {
		for i, c := range instanceType {
			if c == '.' {
				instanceTypeFamily = instanceType[:i]
				break
			}
		}
	}

	// 转换状态
	status := "unknown"
	if inst.State != nil {
		status = types.NormalizeStatus(string(inst.State.Name))
	}

	// 转换时间
	creationTime := ""
	if inst.LaunchTime != nil {
		creationTime = inst.LaunchTime.Format(time.RFC3339)
	}

	// 获取计费类型
	chargeType := "PostPaid" // 按需
	if inst.InstanceLifecycle == ec2types.InstanceLifecycleTypeSpot {
		chargeType = "Spot"
	}

	// 系统盘信息
	systemDisk := types.SystemDisk{}
	if inst.RootDeviceName != nil {
		systemDisk.Device = aws.ToString(inst.RootDeviceName)
	}

	return types.ECSInstance{
		InstanceID:         aws.ToString(inst.InstanceId),
		InstanceName:       instanceName,
		Status:             status,
		Region:             region,
		Zone:               aws.ToString(inst.Placement.AvailabilityZone),
		InstanceType:       instanceType,
		InstanceTypeFamily: instanceTypeFamily,
		CPU:                int(aws.ToInt32(inst.CpuOptions.CoreCount) * aws.ToInt32(inst.CpuOptions.ThreadsPerCore)),
		Memory:             0, // AWS API 不直接返回内存，需要根据实例类型查询
		OSType:             string(inst.Platform),
		OSName:             "",
		ImageID:            aws.ToString(inst.ImageId),
		PublicIP:           publicIP,
		PrivateIP:          privateIP,
		VPCID:              aws.ToString(inst.VpcId),
		VSwitchID:          aws.ToString(inst.SubnetId),
		SecurityGroups:     securityGroups,
		SystemDisk:         systemDisk,
		ChargeType:         chargeType,
		CreationTime:       creationTime,
		NetworkType:        "vpc",
		Tags:               tags,
		Description:        "",
		Provider:           string(types.ProviderAWS),
		HostName:           aws.ToString(inst.PrivateDnsName),
		KeyPairName:        aws.ToString(inst.KeyName),
	}
}

// getAWSRegionLocalName 获取AWS地域的本地名称
func getAWSRegionLocalName(regionID string) string {
	regionNames := map[string]string{
		"us-east-1":      "美国东部(弗吉尼亚北部)",
		"us-east-2":      "美国东部(俄亥俄)",
		"us-west-1":      "美国西部(加利福尼亚北部)",
		"us-west-2":      "美国西部(俄勒冈)",
		"ap-east-1":      "亚太地区(香港)",
		"ap-south-1":     "亚太地区(孟买)",
		"ap-northeast-1": "亚太地区(东京)",
		"ap-northeast-2": "亚太地区(首尔)",
		"ap-northeast-3": "亚太地区(大阪)",
		"ap-southeast-1": "亚太地区(新加坡)",
		"ap-southeast-2": "亚太地区(悉尼)",
		"ca-central-1":   "加拿大(中部)",
		"eu-central-1":   "欧洲(法兰克福)",
		"eu-west-1":      "欧洲(爱尔兰)",
		"eu-west-2":      "欧洲(伦敦)",
		"eu-west-3":      "欧洲(巴黎)",
		"eu-north-1":     "欧洲(斯德哥尔摩)",
		"sa-east-1":      "南美洲(圣保罗)",
		"cn-north-1":     "中国(北京)",
		"cn-northwest-1": "中国(宁夏)",
	}
	if name, ok := regionNames[regionID]; ok {
		return name
	}
	return regionID
}
