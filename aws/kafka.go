package aws

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	mskTypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/gotomicro/ego/core/elog"
)

// KafkaAdapter AWS MSK 适配器
type KafkaAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewKafkaAdapter 创建 MSK 适配器
func NewKafkaAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *KafkaAdapter {
	return &KafkaAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建 MSK 客户端
func (a *KafkaAdapter) createClient(ctx context.Context, region string) (*kafka.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			a.accessKeyID,
			a.accessKeySecret,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("加载AWS配置失败: %w", err)
	}

	return kafka.NewFromConfig(cfg), nil
}

// ListInstances 获取 MSK 集群列表
func (a *KafkaAdapter) ListInstances(ctx context.Context, region string) ([]types.KafkaInstance, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("创建MSK客户端失败: %w", err)
	}

	var instances []types.KafkaInstance
	var nextToken *string

	for {
		input := &kafka.ListClustersV2Input{
			MaxResults: aws.Int32(100),
			NextToken:  nextToken,
		}

		output, err := client.ListClustersV2(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("获取MSK集群列表失败: %w", err)
		}

		for _, cluster := range output.ClusterInfoList {
			instance := a.convertToKafkaInstance(&cluster, region)
			instances = append(instances, instance)
		}

		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	return instances, nil
}

// GetInstance 获取单个 MSK 集群详情
func (a *KafkaAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.KafkaInstance, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("创建MSK客户端失败: %w", err)
	}

	input := &kafka.DescribeClusterV2Input{
		ClusterArn: aws.String(instanceID),
	}

	output, err := client.DescribeClusterV2(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("获取MSK集群详情失败: %w", err)
	}

	if output.ClusterInfo == nil {
		return nil, fmt.Errorf("MSK集群不存在: %s", instanceID)
	}

	instance := a.convertDetailToKafkaInstance(output.ClusterInfo, region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取 MSK 集群
func (a *KafkaAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.KafkaInstance, error) {
	var instances []types.KafkaInstance
	for _, id := range instanceIDs {
		instance, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取MSK集群失败", elog.String("cluster_arn", id), elog.FieldErr(err))
			continue
		}
		instances = append(instances, *instance)
	}
	return instances, nil
}

// GetInstanceStatus 获取集群状态
func (a *KafkaAdapter) GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, instanceID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取集群列表
func (a *KafkaAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.KafkaInstanceFilter) ([]types.KafkaInstance, error) {
	instances, err := a.ListInstances(ctx, region)
	if err != nil {
		return nil, err
	}

	if filter == nil {
		return instances, nil
	}

	var filtered []types.KafkaInstance
	for _, inst := range instances {
		if len(filter.Status) > 0 && !slices.Contains(filter.Status, inst.Status) {
			continue
		}
		if filter.InstanceName != "" && inst.InstanceName != filter.InstanceName {
			continue
		}
		if filter.VPCID != "" && inst.VPCID != filter.VPCID {
			continue
		}
		filtered = append(filtered, inst)
	}

	return filtered, nil
}

// convertToKafkaInstance 转换为统一的 Kafka 实例结构
func (a *KafkaAdapter) convertToKafkaInstance(cluster *mskTypes.Cluster, region string) types.KafkaInstance {
	instance := types.KafkaInstance{
		Region:   region,
		Provider: "aws",
	}

	if cluster.ClusterArn != nil {
		instance.InstanceID = *cluster.ClusterArn
	}
	if cluster.ClusterName != nil {
		instance.InstanceName = *cluster.ClusterName
	}
	instance.Status = types.KafkaStatus("aws", string(cluster.State))
	if cluster.ClusterType == mskTypes.ClusterTypeProvisioned {
		instance.SpecType = "provisioned"
		if cluster.Provisioned != nil {
			if cluster.Provisioned.BrokerNodeGroupInfo != nil {
				if cluster.Provisioned.BrokerNodeGroupInfo.InstanceType != nil {
					instance.SpecType = *cluster.Provisioned.BrokerNodeGroupInfo.InstanceType
				}
				if cluster.Provisioned.BrokerNodeGroupInfo.StorageInfo != nil &&
					cluster.Provisioned.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo != nil &&
					cluster.Provisioned.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo.VolumeSize != nil {
					instance.DiskSize = int64(*cluster.Provisioned.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo.VolumeSize)
				}
				if cluster.Provisioned.BrokerNodeGroupInfo.ClientSubnets != nil && len(cluster.Provisioned.BrokerNodeGroupInfo.ClientSubnets) > 0 {
					instance.VSwitchID = cluster.Provisioned.BrokerNodeGroupInfo.ClientSubnets[0]
				}
				if cluster.Provisioned.BrokerNodeGroupInfo.SecurityGroups != nil && len(cluster.Provisioned.BrokerNodeGroupInfo.SecurityGroups) > 0 {
					instance.SecurityGroupID = cluster.Provisioned.BrokerNodeGroupInfo.SecurityGroups[0]
				}
			}
			if cluster.Provisioned.CurrentBrokerSoftwareInfo != nil && cluster.Provisioned.CurrentBrokerSoftwareInfo.KafkaVersion != nil {
				instance.Version = *cluster.Provisioned.CurrentBrokerSoftwareInfo.KafkaVersion
			}
			if cluster.Provisioned.NumberOfBrokerNodes != nil {
				instance.BrokerCount = int(*cluster.Provisioned.NumberOfBrokerNodes)
			}
		}
	} else if cluster.ClusterType == mskTypes.ClusterTypeServerless {
		instance.SpecType = "serverless"
		if cluster.Serverless != nil && cluster.Serverless.VpcConfigs != nil && len(cluster.Serverless.VpcConfigs) > 0 {
			if cluster.Serverless.VpcConfigs[0].SubnetIds != nil && len(cluster.Serverless.VpcConfigs[0].SubnetIds) > 0 {
				instance.VSwitchID = cluster.Serverless.VpcConfigs[0].SubnetIds[0]
			}
			if cluster.Serverless.VpcConfigs[0].SecurityGroupIds != nil && len(cluster.Serverless.VpcConfigs[0].SecurityGroupIds) > 0 {
				instance.SecurityGroupID = cluster.Serverless.VpcConfigs[0].SecurityGroupIds[0]
			}
		}
	}
	if cluster.CreationTime != nil {
		instance.CreationTime = *cluster.CreationTime
	}

	// 解析标签
	if cluster.Tags != nil {
		instance.Tags = cluster.Tags
	}

	return instance
}

// convertDetailToKafkaInstance 从详情转换
func (a *KafkaAdapter) convertDetailToKafkaInstance(cluster *mskTypes.Cluster, region string) types.KafkaInstance {
	instance := a.convertToKafkaInstance(cluster, region)

	// 获取 Bootstrap Servers (需要额外 API 调用)
	if cluster.ClusterArn != nil {
		ctx := context.Background()
		client, err := a.createClient(ctx, region)
		if err == nil {
			bootstrapInput := &kafka.GetBootstrapBrokersInput{
				ClusterArn: cluster.ClusterArn,
			}
			bootstrapOutput, err := client.GetBootstrapBrokers(ctx, bootstrapInput)
			if err == nil {
				if bootstrapOutput.BootstrapBrokerString != nil {
					instance.BootstrapServers = *bootstrapOutput.BootstrapBrokerString
				}
				if bootstrapOutput.BootstrapBrokerStringTls != nil {
					instance.SSLEndpoint = *bootstrapOutput.BootstrapBrokerStringTls
					instance.SSLEnabled = true
				}
				if bootstrapOutput.BootstrapBrokerStringSaslScram != nil {
					instance.SASLEndpoint = *bootstrapOutput.BootstrapBrokerStringSaslScram
				}
			}
		}
	}

	// 从 ARN 提取 VPC ID (如果可用)
	if cluster.ClusterArn != nil {
		parts := strings.Split(*cluster.ClusterArn, "/")
		if len(parts) > 1 {
			instance.ResourceGroupID = parts[0]
		}
	}

	return instance
}
