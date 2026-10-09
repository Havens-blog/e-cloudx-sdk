package volcano

import (
	"context"
	"fmt"
	"slices"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/escloud"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// ElasticsearchAdapter 火山引擎 ESCloud 适配器
type ElasticsearchAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewElasticsearchAdapter 创建 ESCloud 适配器
func NewElasticsearchAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *ElasticsearchAdapter {
	return &ElasticsearchAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建 ESCloud 客户端
func (a *ElasticsearchAdapter) createClient(region string) (*escloud.ESCLOUD, error) {
	if region == "" {
		region = a.defaultRegion
	}

	config := volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(a.accessKeyID, a.accessKeySecret, "")).
		WithRegion(region)

	sess, err := session.NewSession(config)
	if err != nil {
		return nil, fmt.Errorf("创建会话失败: %w", err)
	}

	return escloud.New(sess), nil
}

// ListInstances 获取 Elasticsearch 实例列表
func (a *ElasticsearchAdapter) ListInstances(ctx context.Context, region string) ([]types.ElasticsearchInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建ESCloud客户端失败: %w", err)
	}

	var instances []types.ElasticsearchInstance
	pageNumber := int32(1)
	pageSize := int32(100)

	for {
		input := &escloud.DescribeInstancesInput{
			PageNumber: volcengine.Int32(pageNumber),
			PageSize:   volcengine.Int32(pageSize),
		}

		output, err := client.DescribeInstances(input)
		if err != nil {
			return nil, fmt.Errorf("获取ESCloud实例列表失败: %w", err)
		}

		if output.Instances == nil || len(output.Instances) == 0 {
			break
		}

		for _, inst := range output.Instances {
			instance := a.convertToElasticsearchInstance(inst, region)
			instances = append(instances, instance)
		}

		if len(output.Instances) < int(pageSize) {
			break
		}
		pageNumber++
	}

	return instances, nil
}

// GetInstance 获取单个 Elasticsearch 实例详情
func (a *ElasticsearchAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.ElasticsearchInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建ESCloud客户端失败: %w", err)
	}

	input := &escloud.DescribeInstanceInput{
		InstanceId: volcengine.String(instanceID),
	}

	output, err := client.DescribeInstance(input)
	if err != nil {
		return nil, fmt.Errorf("获取ESCloud实例详情失败: %w", err)
	}

	instance := a.convertDetailToElasticsearchInstance(output, region, instanceID)
	return &instance, nil
}

// ListInstancesByIDs 批量获取 Elasticsearch 实例
func (a *ElasticsearchAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.ElasticsearchInstance, error) {
	var instances []types.ElasticsearchInstance
	for _, id := range instanceIDs {
		instance, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取ESCloud实例失败", elog.String("instance_id", id), elog.FieldErr(err))
			continue
		}
		instances = append(instances, *instance)
	}
	return instances, nil
}

// GetInstanceStatus 获取实例状态
func (a *ElasticsearchAdapter) GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, instanceID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取实例列表
func (a *ElasticsearchAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.ElasticsearchInstanceFilter) ([]types.ElasticsearchInstance, error) {
	instances, err := a.ListInstances(ctx, region)
	if err != nil {
		return nil, err
	}

	if filter == nil {
		return instances, nil
	}

	var filtered []types.ElasticsearchInstance
	for _, inst := range instances {
		if len(filter.Status) > 0 && !slices.Contains(filter.Status, inst.Status) {
			continue
		}
		if filter.InstanceName != "" && inst.InstanceName != filter.InstanceName {
			continue
		}
		if filter.Version != "" && inst.Version != filter.Version {
			continue
		}
		if filter.VPCID != "" && inst.VPCID != filter.VPCID {
			continue
		}
		filtered = append(filtered, inst)
	}

	return filtered, nil
}

// convertToElasticsearchInstance 转换为统一的 Elasticsearch 实例结构
func (a *ElasticsearchAdapter) convertToElasticsearchInstance(inst *escloud.InstanceForDescribeInstancesOutput, region string) types.ElasticsearchInstance {
	instance := types.ElasticsearchInstance{
		Region:   region,
		Provider: "volcano",
	}

	if inst.InstanceId != nil {
		instance.InstanceID = *inst.InstanceId
	}
	if inst.Status != nil {
		instance.Status = types.ElasticsearchStatus("volcano", *inst.Status)
	}

	return instance
}

// convertDetailToElasticsearchInstance 从详情转换
func (a *ElasticsearchAdapter) convertDetailToElasticsearchInstance(output *escloud.DescribeInstanceOutput, region, instanceID string) types.ElasticsearchInstance {
	instance := types.ElasticsearchInstance{
		InstanceID: instanceID,
		Region:     region,
		Provider:   "volcano",
		Status:     "running",
	}

	return instance
}
