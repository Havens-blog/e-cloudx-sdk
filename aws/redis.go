package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	elasticachetypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	"github.com/gotomicro/ego/core/elog"
)

// RedisAdapter AWS ElastiCache Redis适配器
type RedisAdapter struct {
	account       *domain.CloudAccount
	defaultRegion string
	logger        *elog.Component
	clients       map[string]*elasticache.Client
	mu            sync.RWMutex
}

// NewRedisAdapter 创建AWS Redis适配器
func NewRedisAdapter(account *domain.CloudAccount, defaultRegion string, logger *elog.Component) *RedisAdapter {
	return &RedisAdapter{
		account:       account,
		defaultRegion: defaultRegion,
		logger:        logger,
		clients:       make(map[string]*elasticache.Client),
	}
}

// getClient 获取或创建指定地域的ElastiCache客户端
func (a *RedisAdapter) getClient(region string) *elasticache.Client {
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

	client := elasticache.NewFromConfig(cfg)
	a.clients[region] = client
	return client
}

// ListInstances 获取Redis实例列表
func (a *RedisAdapter) ListInstances(ctx context.Context, region string) ([]types.RedisInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个Redis实例详情
func (a *RedisAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.RedisInstance, error) {
	client := a.getClient(region)

	input := &elasticache.DescribeReplicationGroupsInput{
		ReplicationGroupId: aws.String(instanceID),
	}

	result, err := client.DescribeReplicationGroups(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("获取Redis实例详情失败: %w", err)
	}

	if len(result.ReplicationGroups) == 0 {
		return nil, fmt.Errorf("Redis实例不存在: %s", instanceID)
	}

	instance := convertAWSRedisInstance(result.ReplicationGroups[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取Redis实例
func (a *RedisAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.RedisInstance, error) {
	if len(instanceIDs) == 0 {
		return nil, nil
	}

	var instances []types.RedisInstance
	for _, id := range instanceIDs {
		inst, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取Redis实例失败", elog.String("instance_id", id), elog.FieldErr(err))
			continue
		}
		instances = append(instances, *inst)
	}
	return instances, nil
}

// GetInstanceStatus 获取实例状态
func (a *RedisAdapter) GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, instanceID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取实例列表
func (a *RedisAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.RedisInstanceFilter) ([]types.RedisInstance, error) {
	client := a.getClient(region)

	var allInstances []types.RedisInstance
	var marker *string

	for {
		input := &elasticache.DescribeReplicationGroupsInput{
			MaxRecords: aws.Int32(100),
			Marker:     marker,
		}

		result, err := client.DescribeReplicationGroups(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("获取Redis实例列表失败: %w", err)
		}

		for _, rg := range result.ReplicationGroups {
			instance := convertAWSRedisInstance(rg, region)
			allInstances = append(allInstances, instance)
		}

		if result.Marker == nil {
			break
		}
		marker = result.Marker
	}

	a.logger.Info("获取AWS Redis实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertAWSRedisInstance 转换AWS ElastiCache Redis实例为通用格式
func convertAWSRedisInstance(rg elasticachetypes.ReplicationGroup, region string) types.RedisInstance {
	instanceID := aws.ToString(rg.ReplicationGroupId)
	instanceName := aws.ToString(rg.Description)
	status := types.NormalizeRedisStatus(aws.ToString(rg.Status))

	engineVersion := ""
	if rg.CacheNodeType != nil {
		engineVersion = aws.ToString(rg.CacheNodeType)
	}

	architecture := "standard"
	if rg.ClusterEnabled != nil && *rg.ClusterEnabled {
		architecture = "cluster"
	}

	shardCount := 0
	if rg.NodeGroups != nil {
		shardCount = len(rg.NodeGroups)
	}

	replicaCount := 0
	if len(rg.MemberClusters) > 0 {
		replicaCount = len(rg.MemberClusters) - 1
	}

	connectionDomain := ""
	port := 0
	if rg.ConfigurationEndpoint != nil {
		connectionDomain = aws.ToString(rg.ConfigurationEndpoint.Address)
		port = int(aws.ToInt32(rg.ConfigurationEndpoint.Port))
	} else if len(rg.NodeGroups) > 0 && rg.NodeGroups[0].PrimaryEndpoint != nil {
		connectionDomain = aws.ToString(rg.NodeGroups[0].PrimaryEndpoint.Address)
		port = int(aws.ToInt32(rg.NodeGroups[0].PrimaryEndpoint.Port))
	}

	sslEnabled := false
	if rg.TransitEncryptionEnabled != nil {
		sslEnabled = *rg.TransitEncryptionEnabled
	}

	password := false
	if rg.AuthTokenEnabled != nil {
		password = *rg.AuthTokenEnabled
	}

	return types.RedisInstance{
		InstanceID:       instanceID,
		InstanceName:     instanceName,
		Status:           status,
		Region:           region,
		EngineVersion:    engineVersion,
		Architecture:     architecture,
		ShardCount:       shardCount,
		ReplicaCount:     replicaCount,
		ConnectionDomain: connectionDomain,
		Port:             port,
		SSLEnabled:       sslEnabled,
		Password:         password,
		ChargeType:       "PostPaid",
		Provider:         string(types.ProviderAWS),
	}
}
