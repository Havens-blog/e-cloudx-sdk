package huawei

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	v3 "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/elb/v3"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/elb/v3/model"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/elb/v3/region"
)

// LBAdapter 华为云负载均衡适配器 (ELB)
type LBAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewLBAdapter 创建负载均衡适配器
func NewLBAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *LBAdapter {
	return &LBAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建ELB客户端
func (a *LBAdapter) createClient(reg string) (*v3.ElbClient, error) {
	if reg == "" {
		reg = a.defaultRegion
	}

	auth, err := basic.NewCredentialsBuilder().
		WithAk(a.accessKeyID).
		WithSk(a.accessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}

	r, err := region.SafeValueOf(reg)
	if err != nil {
		return nil, fmt.Errorf("无效的地域: %s", reg)
	}

	client, err := v3.ElbClientBuilder().
		WithRegion(r).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云ELB客户端失败: %w", err)
	}

	return v3.NewElbClient(client), nil
}

// ListInstances 获取负载均衡实例列表
func (a *LBAdapter) ListInstances(ctx context.Context, reg string) ([]types.LBInstance, error) {
	return a.ListInstancesWithFilter(ctx, reg, nil)
}

// GetInstance 获取单个负载均衡实例详情
func (a *LBAdapter) GetInstance(ctx context.Context, reg, lbID string) (*types.LBInstance, error) {
	client, err := a.createClient(reg)
	if err != nil {
		return nil, err
	}

	request := &model.ShowLoadBalancerRequest{
		LoadbalancerId: lbID,
	}

	response, err := client.ShowLoadBalancer(request)
	if err != nil {
		return nil, fmt.Errorf("获取负载均衡详情失败: %w", err)
	}

	if response.Loadbalancer == nil {
		return nil, fmt.Errorf("负载均衡不存在: %s", lbID)
	}

	instance := a.convertToLBInstance(*response.Loadbalancer, reg, client)
	return &instance, nil
}

// ListInstancesByIDs 批量获取负载均衡实例
func (a *LBAdapter) ListInstancesByIDs(ctx context.Context, reg string, lbIDs []string) ([]types.LBInstance, error) {
	var result []types.LBInstance
	for _, lbID := range lbIDs {
		instance, err := a.GetInstance(ctx, reg, lbID)
		if err != nil {
			a.logger.Warn("获取负载均衡失败", elog.String("lb_id", lbID), elog.FieldErr(err))
			continue
		}
		result = append(result, *instance)
	}
	return result, nil
}

// GetInstanceStatus 获取实例状态
func (a *LBAdapter) GetInstanceStatus(ctx context.Context, reg, lbID string) (string, error) {
	instance, err := a.GetInstance(ctx, reg, lbID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取实例列表
func (a *LBAdapter) ListInstancesWithFilter(ctx context.Context, reg string, filter *types.LBInstanceFilter) ([]types.LBInstance, error) {
	client, err := a.createClient(reg)
	if err != nil {
		return nil, fmt.Errorf("创建ELB客户端失败: %w", err)
	}

	var allInstances []types.LBInstance
	var marker *string
	limit := int32(100)

	if filter != nil && filter.PageSize > 0 {
		limit = int32(filter.PageSize)
	}

	for {
		request := &model.ListLoadBalancersRequest{
			Limit:  &limit,
			Marker: marker,
		}

		if filter != nil {
			if filter.LoadBalancerName != "" {
				name := []string{filter.LoadBalancerName}
				request.Name = &name
			}
			if filter.VPCID != "" {
				vpcID := []string{filter.VPCID}
				request.VpcId = &vpcID
			}
			if len(filter.LoadBalancerIDs) > 0 {
				request.Id = &filter.LoadBalancerIDs
			}
		}

		response, err := client.ListLoadBalancers(request)
		if err != nil {
			return nil, fmt.Errorf("获取负载均衡列表失败: %w", err)
		}

		if response.Loadbalancers == nil || len(*response.Loadbalancers) == 0 {
			break
		}

		for _, lb := range *response.Loadbalancers {
			allInstances = append(allInstances, a.convertToLBInstance(lb, reg, client))
		}

		if len(*response.Loadbalancers) < int(limit) {
			break
		}

		lastLB := (*response.Loadbalancers)[len(*response.Loadbalancers)-1]
		marker = &lastLB.Id
	}

	a.logger.Info("获取华为云负载均衡列表成功",
		elog.String("region", reg),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertToLBInstance 转换为通用LB实例
func (a *LBAdapter) convertToLBInstance(lb model.LoadBalancer, reg string, client *v3.ElbClient) types.LBInstance {
	tags := make(map[string]string)
	for _, tag := range lb.Tags {
		if tag.Key != nil && tag.Value != nil {
			tags[*tag.Key] = *tag.Value
		}
	}

	// 华为云ELB统一为slb类型
	lbType := "slb"

	// 判断地址类型: 有EIP则为公网
	addressType := "intranet"
	if len(lb.Eips) > 0 {
		addressType = "internet"
	}

	// 获取可用区
	zone := ""
	if len(lb.AvailabilityZoneList) > 0 {
		zone = lb.AvailabilityZoneList[0]
	}

	// 获取监听器详情
	listeners := a.fetchListenerDetails(client, lb.Id)

	// 获取后端服务器详情
	backendServers := a.fetchBackendServers(client, lb.Pools)

	return types.LBInstance{
		LoadBalancerID:     lb.Id,
		LoadBalancerName:   lb.Name,
		LoadBalancerType:   lbType,
		Status:             types.NormalizeLBStatus("huawei", lb.ProvisioningStatus),
		Region:             reg,
		Zone:               zone,
		Address:            lb.VipAddress,
		AddressType:        addressType,
		VPCID:              lb.VpcId,
		VSwitchID:          lb.VipSubnetCidrId,
		ListenerCount:      len(lb.Listeners),
		BackendServerCount: len(lb.Pools),
		Listeners:          listeners,
		BackendServers:     backendServers,
		CreationTime:       lb.CreatedAt,
		ProjectID:          lb.ProjectId,
		Description:        lb.Description,
		Tags:               tags,
		Provider:           "huawei",
	}
}

// fetchListenerDetails 获取负载均衡器的监听器详情
func (a *LBAdapter) fetchListenerDetails(client *v3.ElbClient, lbID string) []types.LBListener {
	if client == nil {
		return nil
	}

	request := &model.ListListenersRequest{
		LoadbalancerId: &[]string{lbID},
	}

	response, err := client.ListListeners(request)
	if err != nil {
		a.logger.Warn("获取监听器详情失败",
			elog.String("lb_id", lbID),
			elog.FieldErr(err))
		return nil
	}

	if response.Listeners == nil {
		return nil
	}

	var listeners []types.LBListener
	for _, l := range *response.Listeners {
		listeners = append(listeners, types.LBListener{
			ListenerID:       l.Id,
			ListenerPort:     int(l.ProtocolPort),
			ListenerProtocol: l.Protocol,
			Description:      l.Description,
		})
	}
	return listeners
}

// fetchBackendServers 获取后端服务器组中的成员详情
func (a *LBAdapter) fetchBackendServers(client *v3.ElbClient, pools []model.PoolRef) []types.LBBackendServer {
	if client == nil || len(pools) == 0 {
		return nil
	}

	var backendServers []types.LBBackendServer
	for _, pool := range pools {
		request := &model.ListMembersRequest{
			PoolId: pool.Id,
		}

		response, err := client.ListMembers(request)
		if err != nil {
			a.logger.Warn("获取后端服务器列表失败",
				elog.String("pool_id", pool.Id),
				elog.FieldErr(err))
			continue
		}

		if response.Members == nil {
			continue
		}

		for _, m := range *response.Members {
			instanceID := ""
			if m.InstanceId != nil {
				instanceID = *m.InstanceId
			}
			backendServers = append(backendServers, types.LBBackendServer{
				ServerID:   m.Id,
				ServerName: m.Name,
				InstanceID: instanceID,
				IP:         m.Address,
				Port:       int(m.ProtocolPort),
				Weight:     int(m.Weight),
				Status:     m.OperatingStatus,
			})
		}
	}
	return backendServers
}
