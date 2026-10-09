package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/docdb"
	docdbtypes "github.com/aws/aws-sdk-go-v2/service/docdb/types"
	"github.com/gotomicro/ego/core/elog"
)

// MongoDBAdapter AWS DocumentDB适配器
type MongoDBAdapter struct {
	account       *domain.CloudAccount
	defaultRegion string
	logger        *elog.Component
	clients       map[string]*docdb.Client
	mu            sync.RWMutex
}

// NewMongoDBAdapter 创建AWS MongoDB适配器
func NewMongoDBAdapter(account *domain.CloudAccount, defaultRegion string, logger *elog.Component) *MongoDBAdapter {
	return &MongoDBAdapter{
		account:       account,
		defaultRegion: defaultRegion,
		logger:        logger,
		clients:       make(map[string]*docdb.Client),
	}
}

// getClient 获取或创建指定地域的DocumentDB客户端
func (a *MongoDBAdapter) getClient(region string) *docdb.Client {
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

	client := docdb.NewFromConfig(cfg)
	a.clients[region] = client
	return client
}

// ListInstances 获取MongoDB实例列表
func (a *MongoDBAdapter) ListInstances(ctx context.Context, region string) ([]types.MongoDBInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个MongoDB实例详情
func (a *MongoDBAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.MongoDBInstance, error) {
	client := a.getClient(region)

	input := &docdb.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String(instanceID),
	}

	result, err := client.DescribeDBClusters(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("获取MongoDB实例详情失败: %w", err)
	}

	if len(result.DBClusters) == 0 {
		return nil, fmt.Errorf("MongoDB实例不存在: %s", instanceID)
	}

	instance := convertAWSMongoDBInstance(result.DBClusters[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取MongoDB实例
func (a *MongoDBAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.MongoDBInstance, error) {
	if len(instanceIDs) == 0 {
		return nil, nil
	}

	var instances []types.MongoDBInstance
	for _, id := range instanceIDs {
		inst, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取MongoDB实例失败", elog.String("instance_id", id), elog.FieldErr(err))
			continue
		}
		instances = append(instances, *inst)
	}
	return instances, nil
}

// GetInstanceStatus 获取实例状态
func (a *MongoDBAdapter) GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, instanceID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取实例列表
func (a *MongoDBAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.MongoDBInstanceFilter) ([]types.MongoDBInstance, error) {
	client := a.getClient(region)

	var allInstances []types.MongoDBInstance
	var marker *string

	for {
		input := &docdb.DescribeDBClustersInput{
			MaxRecords: aws.Int32(100),
			Marker:     marker,
		}

		// 应用过滤条件
		if filter != nil {
			var filters []docdbtypes.Filter
			if len(filters) > 0 {
				input.Filters = filters
			}
		}

		result, err := client.DescribeDBClusters(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("获取MongoDB实例列表失败: %w", err)
		}

		for _, cluster := range result.DBClusters {
			instance := convertAWSMongoDBInstance(cluster, region)
			allInstances = append(allInstances, instance)
		}

		if result.Marker == nil {
			break
		}
		marker = result.Marker
	}

	a.logger.Info("获取AWS DocumentDB实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertAWSMongoDBInstance 转换AWS DocumentDB实例为通用格式
func convertAWSMongoDBInstance(cluster docdbtypes.DBCluster, region string) types.MongoDBInstance {
	instanceID := aws.ToString(cluster.DBClusterIdentifier)
	instanceName := aws.ToString(cluster.DBClusterIdentifier)
	status := types.NormalizeMongoDBStatus(aws.ToString(cluster.Status))
	zone := ""
	if len(cluster.AvailabilityZones) > 0 {
		zone = cluster.AvailabilityZones[0]
	}
	engineVersion := aws.ToString(cluster.EngineVersion)

	connectionString := ""
	port := 0
	if cluster.Endpoint != nil {
		connectionString = *cluster.Endpoint
	}
	if cluster.Port != nil {
		port = int(*cluster.Port)
	}

	vpcID := ""
	if cluster.DBSubnetGroup != nil {
		vpcID = aws.ToString(cluster.DBSubnetGroup)
	}

	nodeCount := len(cluster.DBClusterMembers)

	creationTime := ""
	if cluster.ClusterCreateTime != nil {
		creationTime = cluster.ClusterCreateTime.Format("2006-01-02T15:04:05Z")
	}

	sslEnabled := false
	if cluster.StorageEncrypted != nil {
		sslEnabled = *cluster.StorageEncrypted
	}

	backupRetention := 0
	if cluster.BackupRetentionPeriod != nil {
		backupRetention = int(*cluster.BackupRetentionPeriod)
	}

	backupWindow := aws.ToString(cluster.PreferredBackupWindow)

	return types.MongoDBInstance{
		InstanceID:            instanceID,
		InstanceName:          instanceName,
		Status:                status,
		Region:                region,
		Zone:                  zone,
		EngineVersion:         engineVersion,
		DBInstanceType:        "replicate",
		ConnectionString:      connectionString,
		Port:                  port,
		VPCID:                 vpcID,
		NodeCount:             nodeCount,
		ChargeType:            "PostPaid",
		CreationTime:          creationTime,
		SSLEnabled:            sslEnabled,
		BackupRetentionPeriod: backupRetention,
		PreferredBackupTime:   backupWindow,
		Provider:              string(types.ProviderAWS),
	}
}
