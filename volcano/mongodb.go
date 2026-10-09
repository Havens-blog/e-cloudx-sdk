package volcano

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/mongodb"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// MongoDBAdapter 火山引擎MongoDB适配器
type MongoDBAdapter struct {
	account       *domain.CloudAccount
	defaultRegion string
	logger        *elog.Component
}

// NewMongoDBAdapter 创建火山引擎MongoDB适配器
func NewMongoDBAdapter(account *domain.CloudAccount, defaultRegion string, logger *elog.Component) *MongoDBAdapter {
	return &MongoDBAdapter{
		account:       account,
		defaultRegion: defaultRegion,
		logger:        logger,
	}
}

// getClient 获取MongoDB客户端
func (a *MongoDBAdapter) getClient(region string) (*mongodb.MONGODB, error) {
	config := volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(a.account.AccessKeyID, a.account.AccessKeySecret, "")).
		WithRegion(region)

	sess, err := session.NewSession(config)
	if err != nil {
		return nil, fmt.Errorf("创建火山引擎会话失败: %w", err)
	}

	client := mongodb.New(sess)
	return client, nil
}

// ListInstances 获取MongoDB实例列表
func (a *MongoDBAdapter) ListInstances(ctx context.Context, region string) ([]types.MongoDBInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个MongoDB实例详情
func (a *MongoDBAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.MongoDBInstance, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	input := &mongodb.DescribeDBInstanceDetailInput{
		InstanceId: volcengine.String(instanceID),
	}

	output, err := client.DescribeDBInstanceDetail(input)
	if err != nil {
		return nil, fmt.Errorf("获取MongoDB实例详情失败: %w", err)
	}

	instance := convertVolcanoMongoDBDetail(output, region)
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
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.MongoDBInstance
	pageNumber := int32(1)
	pageSize := int32(100)

	if filter != nil && filter.PageSize > 0 {
		pageSize = int32(filter.PageSize)
	}
	if filter != nil && filter.PageNumber > 0 {
		pageNumber = int32(filter.PageNumber)
	}

	for {
		input := &mongodb.DescribeDBInstancesInput{
			PageNumber: volcengine.Int32(pageNumber),
			PageSize:   volcengine.Int32(pageSize),
		}

		output, err := client.DescribeDBInstances(input)
		if err != nil {
			return nil, fmt.Errorf("获取MongoDB实例列表失败: %w", err)
		}

		if output.DBInstances == nil {
			break
		}

		for _, inst := range output.DBInstances {
			instance := convertVolcanoMongoDBListItem(inst, region)
			allInstances = append(allInstances, instance)
		}

		if filter != nil && filter.PageNumber > 0 {
			break
		}

		if len(output.DBInstances) < int(pageSize) {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取火山引擎MongoDB实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertVolcanoMongoDBDetail 转换火山引擎MongoDB实例详情为通用格式
func convertVolcanoMongoDBDetail(output *mongodb.DescribeDBInstanceDetailOutput, region string) types.MongoDBInstance {
	instanceID := ""
	instanceName := ""
	status := ""
	engineVersion := ""
	dbInstanceType := ""
	vpcID := ""
	vswitchID := ""
	chargeType := ""
	creationTime := ""
	nodeCount := 0

	if output.DBInstance != nil {
		inst := output.DBInstance
		if inst.InstanceId != nil {
			instanceID = *inst.InstanceId
		}
		if inst.InstanceName != nil {
			instanceName = *inst.InstanceName
		}
		if inst.InstanceStatus != nil {
			status = *inst.InstanceStatus
		}
		if inst.DBEngineVersion != nil {
			engineVersion = *inst.DBEngineVersion
		}
		if inst.InstanceType != nil {
			dbInstanceType = *inst.InstanceType
		}
		if inst.VpcId != nil {
			vpcID = *inst.VpcId
		}
		if inst.SubnetId != nil {
			vswitchID = *inst.SubnetId
		}
		if inst.ChargeType != nil {
			chargeType = *inst.ChargeType
		}
		if inst.CreateTime != nil {
			creationTime = *inst.CreateTime
		}
		if inst.Nodes != nil {
			nodeCount = len(inst.Nodes)
		}
	}

	return types.MongoDBInstance{
		InstanceID:     instanceID,
		InstanceName:   instanceName,
		Status:         types.NormalizeMongoDBStatus(status),
		Region:         region,
		EngineVersion:  engineVersion,
		DBInstanceType: dbInstanceType,
		VPCID:          vpcID,
		VSwitchID:      vswitchID,
		ChargeType:     chargeType,
		CreationTime:   creationTime,
		NodeCount:      nodeCount,
		Provider:       string(types.ProviderVolcano),
	}
}

// convertVolcanoMongoDBListItem 转换火山引擎MongoDB列表项为通用格式
func convertVolcanoMongoDBListItem(inst *mongodb.DBInstanceForDescribeDBInstancesOutput, region string) types.MongoDBInstance {
	instanceID := ""
	instanceName := ""
	status := ""
	engineVersion := ""
	dbInstanceType := ""
	vpcID := ""
	chargeType := ""
	creationTime := ""

	if inst.InstanceId != nil {
		instanceID = *inst.InstanceId
	}
	if inst.InstanceName != nil {
		instanceName = *inst.InstanceName
	}
	if inst.InstanceStatus != nil {
		status = *inst.InstanceStatus
	}
	if inst.DBEngineVersion != nil {
		engineVersion = *inst.DBEngineVersion
	}
	if inst.InstanceType != nil {
		dbInstanceType = *inst.InstanceType
	}
	if inst.VpcId != nil {
		vpcID = *inst.VpcId
	}
	if inst.ChargeType != nil {
		chargeType = *inst.ChargeType
	}
	if inst.CreateTime != nil {
		creationTime = *inst.CreateTime
	}

	return types.MongoDBInstance{
		InstanceID:     instanceID,
		InstanceName:   instanceName,
		Status:         types.NormalizeMongoDBStatus(status),
		Region:         region,
		EngineVersion:  engineVersion,
		DBInstanceType: dbInstanceType,
		VPCID:          vpcID,
		ChargeType:     chargeType,
		CreationTime:   creationTime,
		Provider:       string(types.ProviderVolcano),
	}
}
