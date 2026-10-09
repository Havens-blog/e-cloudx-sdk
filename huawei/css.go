package huawei

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	css "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/css/v1"
	cssmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/css/v1/model"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/css/v1/region"
)

// CSSAdapter 华为云 CSS (Cloud Search Service) Elasticsearch 适配器
type CSSAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewCSSAdapter 创建 CSS 适配器
func NewCSSAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *CSSAdapter {
	return &CSSAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建 CSS 客户端
func (a *CSSAdapter) createClient(regionID string) (*css.CssClient, error) {
	if regionID == "" {
		regionID = a.defaultRegion
	}

	auth, err := basic.NewCredentialsBuilder().
		WithAk(a.accessKeyID).
		WithSk(a.accessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建认证凭证失败: %w", err)
	}

	reg, err := region.SafeValueOf(regionID)
	if err != nil {
		return nil, fmt.Errorf("无效的地域: %s, %w", regionID, err)
	}

	hcClient, err := css.CssClientBuilder().
		WithRegion(reg).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建CSS客户端失败: %w", err)
	}

	return css.NewCssClient(hcClient), nil
}

// ListInstances 获取 Elasticsearch 实例列表
func (a *CSSAdapter) ListInstances(ctx context.Context, regionID string) ([]types.ElasticsearchInstance, error) {
	client, err := a.createClient(regionID)
	if err != nil {
		return nil, fmt.Errorf("创建CSS客户端失败: %w", err)
	}

	var instances []types.ElasticsearchInstance

	request := &cssmodel.ListClustersDetailsRequest{}

	response, err := client.ListClustersDetails(request)
	if err != nil {
		return nil, fmt.Errorf("获取CSS集群列表失败: %w", err)
	}

	if response.Clusters == nil || len(*response.Clusters) == 0 {
		return instances, nil
	}

	for _, cluster := range *response.Clusters {
		instance := a.convertToElasticsearchInstance(&cluster, regionID)
		instances = append(instances, instance)
	}

	return instances, nil
}

// GetInstance 获取单个 Elasticsearch 实例详情
func (a *CSSAdapter) GetInstance(ctx context.Context, regionID, instanceID string) (*types.ElasticsearchInstance, error) {
	client, err := a.createClient(regionID)
	if err != nil {
		return nil, fmt.Errorf("创建CSS客户端失败: %w", err)
	}

	request := &cssmodel.ShowClusterDetailRequest{
		ClusterId: instanceID,
	}

	response, err := client.ShowClusterDetail(request)
	if err != nil {
		return nil, fmt.Errorf("获取CSS集群详情失败: %w", err)
	}

	instance := a.convertDetailToElasticsearchInstance(response, regionID)
	return &instance, nil
}

// ListInstancesByIDs 批量获取 Elasticsearch 实例
func (a *CSSAdapter) ListInstancesByIDs(ctx context.Context, regionID string, instanceIDs []string) ([]types.ElasticsearchInstance, error) {
	var instances []types.ElasticsearchInstance
	for _, id := range instanceIDs {
		instance, err := a.GetInstance(ctx, regionID, id)
		if err != nil {
			a.logger.Warn("获取CSS集群失败", elog.String("cluster_id", id), elog.FieldErr(err))
			continue
		}
		instances = append(instances, *instance)
	}
	return instances, nil
}

// GetInstanceStatus 获取实例状态
func (a *CSSAdapter) GetInstanceStatus(ctx context.Context, regionID, instanceID string) (string, error) {
	instance, err := a.GetInstance(ctx, regionID, instanceID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取实例列表
func (a *CSSAdapter) ListInstancesWithFilter(ctx context.Context, regionID string, filter *types.ElasticsearchInstanceFilter) ([]types.ElasticsearchInstance, error) {
	instances, err := a.ListInstances(ctx, regionID)
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
func (a *CSSAdapter) convertToElasticsearchInstance(cluster *cssmodel.ClusterList, regionID string) types.ElasticsearchInstance {
	instance := types.ElasticsearchInstance{
		Region:   regionID,
		Provider: "huawei",
	}

	if cluster.Id != nil {
		instance.InstanceID = *cluster.Id
	}
	if cluster.Name != nil {
		instance.InstanceName = *cluster.Name
	}
	if cluster.Status != nil {
		instance.Status = types.ElasticsearchStatus("huawei", *cluster.Status)
	}
	if cluster.Datastore != nil {
		if cluster.Datastore.Version != nil {
			instance.Version = *cluster.Datastore.Version
		}
		if cluster.Datastore.Type != nil {
			instance.EngineType = *cluster.Datastore.Type
		}
	}
	if cluster.Instances != nil {
		instance.NodeCount = len(*cluster.Instances)
		// 获取第一个节点的规格信息
		if len(*cluster.Instances) > 0 {
			firstNode := (*cluster.Instances)[0]
			if firstNode.SpecCode != nil {
				instance.NodeSpec = *firstNode.SpecCode
			}
		}
	}
	if cluster.Endpoint != nil {
		instance.PrivateEndpoint = *cluster.Endpoint
	}
	if cluster.VpcId != nil {
		instance.VPCID = *cluster.VpcId
	}
	if cluster.SubnetId != nil {
		instance.VSwitchID = *cluster.SubnetId
	}
	if cluster.SecurityGroupId != nil {
		instance.SecurityGroupID = *cluster.SecurityGroupId
	}
	if cluster.Period != nil && *cluster.Period {
		instance.ChargeType = "PrePaid"
	} else {
		instance.ChargeType = "PostPaid"
	}
	if cluster.Created != nil {
		if t, err := time.Parse("2006-01-02T15:04:05", *cluster.Created); err == nil {
			instance.CreationTime = t
		}
	}
	if cluster.Updated != nil {
		if t, err := time.Parse("2006-01-02T15:04:05", *cluster.Updated); err == nil {
			instance.UpdateTime = t
		}
	}

	// 解析标签
	if cluster.Tags != nil {
		instance.Tags = make(map[string]string)
		for _, tag := range *cluster.Tags {
			if tag.Key != nil && tag.Value != nil {
				instance.Tags[*tag.Key] = *tag.Value
			}
		}
	}

	return instance
}

// convertDetailToElasticsearchInstance 从详情转换
func (a *CSSAdapter) convertDetailToElasticsearchInstance(resp *cssmodel.ShowClusterDetailResponse, regionID string) types.ElasticsearchInstance {
	instance := types.ElasticsearchInstance{
		Region:   regionID,
		Provider: "huawei",
	}

	if resp.Id != nil {
		instance.InstanceID = *resp.Id
	}
	if resp.Name != nil {
		instance.InstanceName = *resp.Name
	}
	if resp.Status != nil {
		instance.Status = types.ElasticsearchStatus("huawei", *resp.Status)
	}
	if resp.Datastore != nil {
		if resp.Datastore.Version != nil {
			instance.Version = *resp.Datastore.Version
		}
		if resp.Datastore.Type != nil {
			instance.EngineType = *resp.Datastore.Type
		}
	}
	if resp.Instances != nil {
		instance.NodeCount = len(*resp.Instances)
		if len(*resp.Instances) > 0 {
			firstNode := (*resp.Instances)[0]
			if firstNode.SpecCode != nil {
				instance.NodeSpec = *firstNode.SpecCode
			}
			if firstNode.Volume != nil && firstNode.Volume.Size != nil {
				instance.NodeDiskSize = int(*firstNode.Volume.Size)
			}
			if firstNode.Volume != nil && firstNode.Volume.Type != nil {
				instance.NodeDiskType = *firstNode.Volume.Type
			}
			if firstNode.AzCode != nil {
				instance.Zone = *firstNode.AzCode
			}
		}
	}
	if resp.Endpoint != nil {
		instance.PrivateEndpoint = *resp.Endpoint
	}
	if resp.VpcId != nil {
		instance.VPCID = *resp.VpcId
	}
	if resp.SubnetId != nil {
		instance.VSwitchID = *resp.SubnetId
	}
	if resp.SecurityGroupId != nil {
		instance.SecurityGroupID = *resp.SecurityGroupId
	}
	if resp.Period != nil && *resp.Period {
		instance.ChargeType = "PrePaid"
	} else {
		instance.ChargeType = "PostPaid"
	}
	if resp.Created != nil {
		if t, err := time.Parse("2006-01-02T15:04:05", *resp.Created); err == nil {
			instance.CreationTime = t
		}
	}
	if resp.Updated != nil {
		if t, err := time.Parse("2006-01-02T15:04:05", *resp.Updated); err == nil {
			instance.UpdateTime = t
		}
	}
	if resp.HttpsEnable != nil {
		instance.SSLEnabled = *resp.HttpsEnable
	}
	if resp.AuthorityEnable != nil {
		instance.AuthEnabled = *resp.AuthorityEnable
	}

	if resp.Tags != nil {
		instance.Tags = make(map[string]string)
		for _, tag := range *resp.Tags {
			if tag.Key != nil && tag.Value != nil {
				instance.Tags[*tag.Key] = *tag.Value
			}
		}
	}

	return instance
}
