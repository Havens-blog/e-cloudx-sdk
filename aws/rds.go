package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/gotomicro/ego/core/elog"
)

// RDSAdapter AWS RDS适配器
type RDSAdapter struct {
	account       *domain.CloudAccount
	defaultRegion string
	logger        *elog.Component
	clients       map[string]*rds.Client
	mu            sync.RWMutex
}

// NewRDSAdapter 创建AWS RDS适配器
func NewRDSAdapter(account *domain.CloudAccount, defaultRegion string, logger *elog.Component) *RDSAdapter {
	return &RDSAdapter{
		account:       account,
		defaultRegion: defaultRegion,
		logger:        logger,
		clients:       make(map[string]*rds.Client),
	}
}

// getClient 获取或创建指定地域的RDS客户端
func (a *RDSAdapter) getClient(region string) *rds.Client {
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

	client := rds.NewFromConfig(cfg)
	a.clients[region] = client
	return client
}

// ListInstances 获取RDS实例列表
func (a *RDSAdapter) ListInstances(ctx context.Context, region string) ([]types.RDSInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个RDS实例详情
func (a *RDSAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.RDSInstance, error) {
	client := a.getClient(region)

	input := &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(instanceID),
	}

	result, err := client.DescribeDBInstances(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("获取RDS实例详情失败: %w", err)
	}

	if len(result.DBInstances) == 0 {
		return nil, fmt.Errorf("RDS实例不存在: %s", instanceID)
	}

	instance := convertAWSRDSInstance(result.DBInstances[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取RDS实例
func (a *RDSAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.RDSInstance, error) {
	if len(instanceIDs) == 0 {
		return nil, nil
	}

	var instances []types.RDSInstance
	for _, id := range instanceIDs {
		inst, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取RDS实例失败", elog.String("instance_id", id), elog.FieldErr(err))
			continue
		}
		instances = append(instances, *inst)
	}
	return instances, nil
}

// GetInstanceStatus 获取实例状态
func (a *RDSAdapter) GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, instanceID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取实例列表
func (a *RDSAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.RDSInstanceFilter) ([]types.RDSInstance, error) {
	client := a.getClient(region)

	var allInstances []types.RDSInstance
	var marker *string

	for {
		input := &rds.DescribeDBInstancesInput{
			MaxRecords: aws.Int32(100),
			Marker:     marker,
		}

		// 应用过滤条件
		if filter != nil {
			var filters []rdstypes.Filter
			if filter.Engine != "" {
				filters = append(filters, rdstypes.Filter{
					Name:   aws.String("engine"),
					Values: []string{filter.Engine},
				})
			}
			if len(filters) > 0 {
				input.Filters = filters
			}
		}

		result, err := client.DescribeDBInstances(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("获取RDS实例列表失败: %w", err)
		}

		for _, inst := range result.DBInstances {
			instance := convertAWSRDSInstance(inst, region)
			allInstances = append(allInstances, instance)
		}

		if result.Marker == nil {
			break
		}
		marker = result.Marker
	}

	a.logger.Info("获取AWS RDS实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertAWSRDSInstance 转换AWS RDS实例为通用格式
func convertAWSRDSInstance(inst rdstypes.DBInstance, region string) types.RDSInstance {
	instanceID := aws.ToString(inst.DBInstanceIdentifier)
	instanceName := aws.ToString(inst.DBInstanceIdentifier)
	status := types.NormalizeRDSStatus(aws.ToString(inst.DBInstanceStatus))
	zone := aws.ToString(inst.AvailabilityZone)
	engine := aws.ToString(inst.Engine)
	engineVersion := aws.ToString(inst.EngineVersion)
	instanceClass := aws.ToString(inst.DBInstanceClass)
	storage := int(aws.ToInt32(inst.AllocatedStorage))
	storageType := aws.ToString(inst.StorageType)
	maxIOPS := int(aws.ToInt32(inst.Iops))

	connectionString := ""
	port := 0
	if inst.Endpoint != nil {
		connectionString = aws.ToString(inst.Endpoint.Address)
		port = int(aws.ToInt32(inst.Endpoint.Port))
	}

	vpcID := ""
	if inst.DBSubnetGroup != nil {
		vpcID = aws.ToString(inst.DBSubnetGroup.VpcId)
	}

	chargeType := "PostPaid" // AWS RDS 默认按需付费

	creationTime := ""
	if inst.InstanceCreateTime != nil {
		creationTime = inst.InstanceCreateTime.Format("2006-01-02T15:04:05Z")
	}

	category := "Basic"
	if inst.MultiAZ != nil && *inst.MultiAZ {
		category = "HighAvailability"
	}

	sslEnabled := false
	if inst.CACertificateIdentifier != nil && *inst.CACertificateIdentifier != "" {
		sslEnabled = true
	}

	backupRetention := 0
	if inst.BackupRetentionPeriod != nil {
		backupRetention = int(*inst.BackupRetentionPeriod)
	}

	backupWindow := aws.ToString(inst.PreferredBackupWindow)

	return types.RDSInstance{
		InstanceID:            instanceID,
		InstanceName:          instanceName,
		Status:                status,
		Region:                region,
		Zone:                  zone,
		Engine:                engine,
		EngineVersion:         engineVersion,
		DBInstanceClass:       instanceClass,
		Storage:               storage,
		StorageType:           storageType,
		MaxIOPS:               maxIOPS,
		ConnectionString:      connectionString,
		Port:                  port,
		VPCID:                 vpcID,
		Category:              category,
		ChargeType:            chargeType,
		CreationTime:          creationTime,
		SSLEnabled:            sslEnabled,
		BackupRetentionPeriod: backupRetention,
		PreferredBackupTime:   backupWindow,
		Provider:              string(types.ProviderAWS),
	}
}
