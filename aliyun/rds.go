package aliyun

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/rds"
	"github.com/gotomicro/ego/core/elog"
)

// RDSAdapter 阿里云RDS适配器
type RDSAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewRDSAdapter 创建阿里云RDS适配器
func NewRDSAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *RDSAdapter {
	return &RDSAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// getClient 获取RDS客户端
func (a *RDSAdapter) getClient(region string) (*rds.Client, error) {
	client, err := rds.NewClientWithAccessKey(region, a.accessKeyID, a.accessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("创建阿里云RDS客户端失败: %w", err)
	}
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

	request := rds.CreateDescribeDBInstanceAttributeRequest()
	request.Scheme = "https"
	request.DBInstanceId = instanceID

	response, err := client.DescribeDBInstanceAttribute(request)
	if err != nil {
		return nil, fmt.Errorf("获取RDS实例详情失败: %w", err)
	}

	if len(response.Items.DBInstanceAttribute) == 0 {
		return nil, fmt.Errorf("RDS实例不存在: %s", instanceID)
	}

	inst := response.Items.DBInstanceAttribute[0]
	instance := convertAliyunRDSInstance(inst, region)
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
	pageNumber := 1
	pageSize := 100

	if filter != nil && filter.PageSize > 0 {
		pageSize = filter.PageSize
	}
	if filter != nil && filter.PageNumber > 0 {
		pageNumber = filter.PageNumber
	}

	for {
		request := rds.CreateDescribeDBInstancesRequest()
		request.Scheme = "https"
		request.RegionId = region
		request.PageNumber = requests.NewInteger(pageNumber)
		request.PageSize = requests.NewInteger(pageSize)

		if filter != nil {
			if filter.Engine != "" {
				request.Engine = filter.Engine
			}
			if len(filter.Status) > 0 {
				request.DBInstanceStatus = filter.Status[0]
			}
			if filter.VPCID != "" {
				request.VpcId = filter.VPCID
			}
		}

		response, err := client.DescribeDBInstances(request)
		if err != nil {
			return nil, fmt.Errorf("获取RDS实例列表失败: %w", err)
		}

		for _, inst := range response.Items.DBInstance {
			instance := convertAliyunRDSListItem(inst, region)
			allInstances = append(allInstances, instance)
		}

		if filter != nil && filter.PageNumber > 0 {
			break
		}

		if len(response.Items.DBInstance) < pageSize {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取阿里云RDS实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertAliyunRDSInstance 转换阿里云RDS实例详情为通用格式
func convertAliyunRDSInstance(inst rds.DBInstanceAttribute, region string) types.RDSInstance {
	cpu, _ := strconv.Atoi(inst.DBInstanceCPU)
	return types.RDSInstance{
		InstanceID:       inst.DBInstanceId,
		InstanceName:     inst.DBInstanceDescription,
		Status:           types.NormalizeRDSStatus(inst.DBInstanceStatus),
		Region:           region,
		Zone:             inst.ZoneId,
		Engine:           inst.Engine,
		EngineVersion:    inst.EngineVersion,
		DBInstanceClass:  inst.DBInstanceClass,
		CPU:              cpu,
		Memory:           parseMemoryMB(inst.DBInstanceMemory),
		Storage:          inst.DBInstanceStorage,
		StorageType:      inst.DBInstanceStorageType,
		MaxIOPS:          inst.MaxIOPS,
		ConnectionString: inst.ConnectionString,
		Port:             parsePort(inst.Port),
		VPCID:            inst.VpcId,
		VSwitchID:        inst.VSwitchId,
		Category:         inst.Category,
		ChargeType:       inst.PayType,
		CreationTime:     inst.CreationTime,
		ExpiredTime:      inst.ExpireTime,
		ProjectID:        inst.ResourceGroupId,
		Description:      inst.DBInstanceDescription,
		Provider:         string(types.ProviderAliyun),
	}
}

// convertAliyunRDSListItem 转换阿里云RDS列表项为通用格式
func convertAliyunRDSListItem(inst rds.DBInstance, region string) types.RDSInstance {
	return types.RDSInstance{
		InstanceID:      inst.DBInstanceId,
		InstanceName:    inst.DBInstanceDescription,
		Status:          types.NormalizeRDSStatus(inst.DBInstanceStatus),
		Region:          region,
		Zone:            inst.ZoneId,
		Engine:          inst.Engine,
		EngineVersion:   inst.EngineVersion,
		DBInstanceClass: inst.DBInstanceClass,
		StorageType:     inst.DBInstanceStorageType,
		VPCID:           inst.VpcId,
		VSwitchID:       inst.VSwitchId,
		Category:        inst.Category,
		ChargeType:      inst.PayType,
		CreationTime:    inst.CreateTime,
		ExpiredTime:     inst.ExpireTime,
		ProjectID:       inst.ResourceGroupId,
		Description:     inst.DBInstanceDescription,
		Provider:        string(types.ProviderAliyun),
	}
}

// parseMemoryMB 解析内存值（MB）
func parseMemoryMB(memory int64) int {
	return int(memory)
}

// parsePort 解析端口
func parsePort(port string) int {
	p, _ := strconv.Atoi(port)
	return p
}
