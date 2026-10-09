package aliyun

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/dds"
	"github.com/gotomicro/ego/core/elog"
)

// MongoDBAdapter 阿里云MongoDB适配器
type MongoDBAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewMongoDBAdapter 创建阿里云MongoDB适配器
func NewMongoDBAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *MongoDBAdapter {
	return &MongoDBAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// getClient 获取MongoDB客户端
func (a *MongoDBAdapter) getClient(region string) (*dds.Client, error) {
	client, err := dds.NewClientWithAccessKey(region, a.accessKeyID, a.accessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("创建阿里云MongoDB客户端失败: %w", err)
	}
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

	request := dds.CreateDescribeDBInstanceAttributeRequest()
	request.Scheme = "https"
	request.DBInstanceId = instanceID

	response, err := client.DescribeDBInstanceAttribute(request)
	if err != nil {
		return nil, fmt.Errorf("获取MongoDB实例详情失败: %w", err)
	}

	if len(response.DBInstances.DBInstance) == 0 {
		return nil, fmt.Errorf("MongoDB实例不存在: %s", instanceID)
	}

	inst := response.DBInstances.DBInstance[0]
	instance := convertAliyunMongoDBInstance(inst, region)
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
	pageNumber := 1
	pageSize := 30 // MongoDB API 默认最大30

	if filter != nil && filter.PageSize > 0 {
		pageSize = filter.PageSize
		if pageSize > 30 {
			pageSize = 30
		}
	}
	if filter != nil && filter.PageNumber > 0 {
		pageNumber = filter.PageNumber
	}

	for {
		request := dds.CreateDescribeDBInstancesRequest()
		request.Scheme = "https"
		request.RegionId = region
		request.PageNumber = requests.NewInteger(pageNumber)
		request.PageSize = requests.NewInteger(pageSize)

		if filter != nil {
			if filter.DBInstanceType != "" {
				request.DBInstanceType = filter.DBInstanceType
			}
		}

		response, err := client.DescribeDBInstances(request)
		if err != nil {
			return nil, fmt.Errorf("获取MongoDB实例列表失败: %w", err)
		}

		for _, inst := range response.DBInstances.DBInstance {
			instance := convertAliyunMongoDBListItem(inst, region)
			allInstances = append(allInstances, instance)
		}

		if filter != nil && filter.PageNumber > 0 {
			break
		}

		if len(response.DBInstances.DBInstance) < pageSize {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取阿里云MongoDB实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertAliyunMongoDBInstance 转换阿里云MongoDB实例详情为通用格式
func convertAliyunMongoDBInstance(inst dds.DBInstance, region string) types.MongoDBInstance {
	return types.MongoDBInstance{
		InstanceID:     inst.DBInstanceId,
		InstanceName:   inst.DBInstanceDescription,
		Status:         types.NormalizeMongoDBStatus(inst.DBInstanceStatus),
		Region:         region,
		Zone:           inst.ZoneId,
		EngineVersion:  inst.EngineVersion,
		InstanceClass:  inst.DBInstanceClass,
		DBInstanceType: inst.DBInstanceType,
		Storage:        inst.DBInstanceStorage,
		StorageType:    inst.StorageType,
		VPCID:          inst.VPCId,
		VSwitchID:      inst.VSwitchId,
		ReplicaSetName: inst.ReplicaSetName,
		ChargeType:     inst.ChargeType,
		CreationTime:   inst.CreationTime,
		ExpiredTime:    inst.ExpireTime,
		ProjectID:      inst.ResourceGroupId,
		Description:    inst.DBInstanceDescription,
		Provider:       string(types.ProviderAliyun),
	}
}

// convertAliyunMongoDBListItem 转换阿里云MongoDB列表项为通用格式
func convertAliyunMongoDBListItem(inst dds.DBInstance, region string) types.MongoDBInstance {
	return types.MongoDBInstance{
		InstanceID:     inst.DBInstanceId,
		InstanceName:   inst.DBInstanceDescription,
		Status:         types.NormalizeMongoDBStatus(inst.DBInstanceStatus),
		Region:         region,
		Zone:           inst.ZoneId,
		EngineVersion:  inst.EngineVersion,
		InstanceClass:  inst.DBInstanceClass,
		DBInstanceType: inst.DBInstanceType,
		Storage:        inst.DBInstanceStorage,
		StorageType:    inst.StorageType,
		VPCID:          inst.VPCId,
		VSwitchID:      inst.VSwitchId,
		ReplicaSetName: inst.ReplicaSetName,
		ChargeType:     inst.ChargeType,
		CreationTime:   inst.CreationTime,
		ExpiredTime:    inst.ExpireTime,
		ProjectID:      inst.ResourceGroupId,
		Description:    inst.DBInstanceDescription,
		Provider:       string(types.ProviderAliyun),
	}
}
