package volcano

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/rdsmysqlv2"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// RDSAdapter 火山引擎RDS适配器
type RDSAdapter struct {
	account       *domain.CloudAccount
	defaultRegion string
	logger        *elog.Component
}

// NewRDSAdapter 创建火山引擎RDS适配器
func NewRDSAdapter(account *domain.CloudAccount, defaultRegion string, logger *elog.Component) *RDSAdapter {
	return &RDSAdapter{
		account:       account,
		defaultRegion: defaultRegion,
		logger:        logger,
	}
}

// getClient 获取RDS MySQL客户端
func (a *RDSAdapter) getClient(region string) (*rdsmysqlv2.RDSMYSQLV2, error) {
	config := volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(a.account.AccessKeyID, a.account.AccessKeySecret, "")).
		WithRegion(region)

	sess, err := session.NewSession(config)
	if err != nil {
		return nil, fmt.Errorf("创建火山引擎会话失败: %w", err)
	}

	client := rdsmysqlv2.New(sess)
	return client, nil
}

// ListInstances 获取RDS实例列表
func (a *RDSAdapter) ListInstances(ctx context.Context, region string) ([]types.RDSInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个RDS实例详情
func (a *RDSAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.RDSInstance, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	input := &rdsmysqlv2.DescribeDBInstanceDetailInput{
		InstanceId: volcengine.String(instanceID),
	}

	output, err := client.DescribeDBInstanceDetail(input)
	if err != nil {
		return nil, fmt.Errorf("获取RDS实例详情失败: %w", err)
	}

	if output.BasicInfo == nil {
		return nil, fmt.Errorf("RDS实例不存在: %s", instanceID)
	}

	instance := convertVolcanoRDSInstance(output, region)
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
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.RDSInstance
	pageNumber := int32(1)
	pageSize := int32(100)

	if filter != nil && filter.PageSize > 0 {
		pageSize = int32(filter.PageSize)
	}
	if filter != nil && filter.PageNumber > 0 {
		pageNumber = int32(filter.PageNumber)
	}

	for {
		input := &rdsmysqlv2.DescribeDBInstancesInput{
			PageNumber: volcengine.Int32(pageNumber),
			PageSize:   volcengine.Int32(pageSize),
		}

		output, err := client.DescribeDBInstances(input)
		if err != nil {
			return nil, fmt.Errorf("获取RDS实例列表失败: %w", err)
		}

		if output.Instances == nil {
			break
		}

		for _, inst := range output.Instances {
			instance := convertVolcanoRDSListItem(inst, region)
			allInstances = append(allInstances, instance)
		}

		if filter != nil && filter.PageNumber > 0 {
			break
		}

		if len(output.Instances) < int(pageSize) {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取火山引擎RDS实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertVolcanoRDSInstance 转换火山引擎RDS实例详情为通用格式
func convertVolcanoRDSInstance(output *rdsmysqlv2.DescribeDBInstanceDetailOutput, region string) types.RDSInstance {
	basic := output.BasicInfo

	instanceID := ""
	instanceName := ""
	status := ""
	zone := ""
	engine := "mysql"
	engineVersion := ""
	instanceClass := ""
	storage := 0
	storageType := ""
	vpcID := ""
	vswitchID := ""
	creationTime := ""
	projectName := ""

	if basic != nil {
		if basic.InstanceId != nil {
			instanceID = *basic.InstanceId
		}
		if basic.InstanceName != nil {
			instanceName = *basic.InstanceName
		}
		if basic.InstanceStatus != nil {
			status = *basic.InstanceStatus
		}
		if basic.ZoneId != nil {
			zone = *basic.ZoneId
		}
		if basic.DBEngineVersion != nil {
			engineVersion = *basic.DBEngineVersion
		}
		if basic.NodeSpec != nil {
			instanceClass = *basic.NodeSpec
		}
		if basic.StorageSpace != nil {
			storage = int(*basic.StorageSpace)
		}
		if basic.StorageType != nil {
			storageType = *basic.StorageType
		}
		if basic.VpcId != nil {
			vpcID = *basic.VpcId
		}
		if basic.SubnetId != nil {
			vswitchID = *basic.SubnetId
		}
		if basic.CreateTime != nil {
			creationTime = *basic.CreateTime
		}
		if basic.ProjectName != nil {
			projectName = *basic.ProjectName
		}
	}

	return types.RDSInstance{
		InstanceID:      instanceID,
		InstanceName:    instanceName,
		Status:          types.NormalizeRDSStatus(status),
		Region:          region,
		Zone:            zone,
		Engine:          engine,
		EngineVersion:   engineVersion,
		DBInstanceClass: instanceClass,
		Storage:         storage,
		StorageType:     storageType,
		VPCID:           vpcID,
		VSwitchID:       vswitchID,
		CreationTime:    creationTime,
		ProjectName:     projectName,
		Provider:        string(types.ProviderVolcano),
	}
}

// convertVolcanoRDSListItem 转换火山引擎RDS列表项为通用格式
func convertVolcanoRDSListItem(inst *rdsmysqlv2.InstanceForDescribeDBInstancesOutput, region string) types.RDSInstance {
	instanceID := ""
	instanceName := ""
	status := ""
	zone := ""
	engine := "mysql"
	engineVersion := ""
	instanceClass := ""
	storage := 0
	storageType := ""
	vpcID := ""
	vswitchID := ""
	creationTime := ""
	projectName := ""

	if inst.InstanceId != nil {
		instanceID = *inst.InstanceId
	}
	if inst.InstanceName != nil {
		instanceName = *inst.InstanceName
	}
	if inst.InstanceStatus != nil {
		status = *inst.InstanceStatus
	}
	if inst.ZoneId != nil {
		zone = *inst.ZoneId
	}
	if inst.DBEngineVersion != nil {
		engineVersion = *inst.DBEngineVersion
	}
	if inst.NodeSpec != nil {
		instanceClass = *inst.NodeSpec
	}
	if inst.StorageSpace != nil {
		storage = int(*inst.StorageSpace)
	}
	if inst.StorageType != nil {
		storageType = *inst.StorageType
	}
	if inst.VpcId != nil {
		vpcID = *inst.VpcId
	}
	if inst.SubnetId != nil {
		vswitchID = *inst.SubnetId
	}
	if inst.CreateTime != nil {
		creationTime = *inst.CreateTime
	}
	if inst.ProjectName != nil {
		projectName = *inst.ProjectName
	}

	return types.RDSInstance{
		InstanceID:      instanceID,
		InstanceName:    instanceName,
		Status:          types.NormalizeRDSStatus(status),
		Region:          region,
		Zone:            zone,
		Engine:          engine,
		EngineVersion:   engineVersion,
		DBInstanceClass: instanceClass,
		Storage:         storage,
		StorageType:     storageType,
		VPCID:           vpcID,
		VSwitchID:       vswitchID,
		CreationTime:    creationTime,
		ProjectName:     projectName,
		Provider:        string(types.ProviderVolcano),
	}
}
