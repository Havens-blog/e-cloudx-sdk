package tencent

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	clb "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/clb/v20180317"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

// LBAdapter 腾讯云负载均衡适配器 (CLB)
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

// createClient 创建CLB客户端
func (a *LBAdapter) createClient(region string) (*clb.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}

	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "clb.tencentcloudapi.com"

	return clb.NewClient(credential, region, cpf)
}

// ListInstances 获取负载均衡实例列表
func (a *LBAdapter) ListInstances(ctx context.Context, region string) ([]types.LBInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个负载均衡实例详情
func (a *LBAdapter) GetInstance(ctx context.Context, region, lbID string) (*types.LBInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	request := clb.NewDescribeLoadBalancersRequest()
	request.LoadBalancerIds = common.StringPtrs([]string{lbID})

	response, err := client.DescribeLoadBalancers(request)
	if err != nil {
		return nil, fmt.Errorf("获取负载均衡详情失败: %w", err)
	}

	if response.Response.LoadBalancerSet == nil || len(response.Response.LoadBalancerSet) == 0 {
		return nil, fmt.Errorf("负载均衡不存在: %s", lbID)
	}

	instance := a.convertToLBInstance(response.Response.LoadBalancerSet[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取负载均衡实例
func (a *LBAdapter) ListInstancesByIDs(ctx context.Context, region string, lbIDs []string) ([]types.LBInstance, error) {
	if len(lbIDs) == 0 {
		return nil, nil
	}

	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	request := clb.NewDescribeLoadBalancersRequest()
	request.LoadBalancerIds = common.StringPtrs(lbIDs)

	response, err := client.DescribeLoadBalancers(request)
	if err != nil {
		return nil, fmt.Errorf("批量获取负载均衡失败: %w", err)
	}

	var result []types.LBInstance
	if response.Response.LoadBalancerSet != nil {
		for _, lb := range response.Response.LoadBalancerSet {
			result = append(result, a.convertToLBInstance(lb, region))
		}
	}

	return result, nil
}

// GetInstanceStatus 获取实例状态
func (a *LBAdapter) GetInstanceStatus(ctx context.Context, region, lbID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, lbID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取实例列表
func (a *LBAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.LBInstanceFilter) ([]types.LBInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建CLB客户端失败: %w", err)
	}

	var allInstances []types.LBInstance
	offset := int64(0)
	limit := int64(100)

	if filter != nil && filter.PageSize > 0 {
		limit = int64(filter.PageSize)
	}

	for {
		request := clb.NewDescribeLoadBalancersRequest()
		request.Offset = &offset
		request.Limit = &limit

		if filter != nil {
			if len(filter.LoadBalancerIDs) > 0 {
				request.LoadBalancerIds = common.StringPtrs(filter.LoadBalancerIDs)
			}
			if filter.LoadBalancerName != "" {
				request.LoadBalancerName = &filter.LoadBalancerName
			}
			if filter.VPCID != "" {
				request.VpcId = &filter.VPCID
			}
			if filter.AddressType != "" {
				loadBalancerType := "INTERNAL"
				if filter.AddressType == "internet" {
					loadBalancerType = "OPEN"
				}
				request.LoadBalancerType = &loadBalancerType
			}
		}

		response, err := client.DescribeLoadBalancers(request)
		if err != nil {
			return nil, fmt.Errorf("获取负载均衡列表失败: %w", err)
		}

		if response.Response.LoadBalancerSet == nil || len(response.Response.LoadBalancerSet) == 0 {
			break
		}

		for _, lb := range response.Response.LoadBalancerSet {
			allInstances = append(allInstances, a.convertToLBInstance(lb, region))
		}

		if len(response.Response.LoadBalancerSet) < int(limit) {
			break
		}
		offset += limit
	}

	// 为每个CLB实例获取监听器和后端服务器详情（仅在实例数量较少时，避免过多API调用）
	if len(allInstances) > 0 && len(allInstances) < 50 {
		for i := range allInstances {
			listeners, listenerCount := a.fetchListenerDetails(client, allInstances[i].LoadBalancerID)
			allInstances[i].Listeners = listeners
			allInstances[i].ListenerCount = listenerCount

			backendServers, backendServerCount := a.fetchBackendServers(client, allInstances[i].LoadBalancerID)
			allInstances[i].BackendServers = backendServers
			allInstances[i].BackendServerCount = backendServerCount
		}
	}

	a.logger.Info("获取腾讯云负载均衡列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertToLBInstance 转换为通用LB实例
func (a *LBAdapter) convertToLBInstance(lb *clb.LoadBalancer, region string) types.LBInstance {
	lbID := ""
	if lb.LoadBalancerId != nil {
		lbID = *lb.LoadBalancerId
	}

	lbName := ""
	if lb.LoadBalancerName != nil {
		lbName = *lb.LoadBalancerName
	}

	lbType := "slb"

	status := ""
	if lb.Status != nil {
		switch *lb.Status {
		case 0:
			status = "Creating"
		case 1:
			status = "Active"
		default:
			status = fmt.Sprintf("%d", *lb.Status)
		}
	}

	addressType := "intranet"
	if lb.LoadBalancerType != nil && *lb.LoadBalancerType == "OPEN" {
		addressType = "internet"
	}

	address := ""
	if lb.LoadBalancerVips != nil && len(lb.LoadBalancerVips) > 0 {
		address = *lb.LoadBalancerVips[0]
	}

	vpcID := ""
	if lb.VpcId != nil {
		vpcID = *lb.VpcId
	}

	subnetID := ""
	if lb.SubnetId != nil {
		subnetID = *lb.SubnetId
	}

	zone := ""
	if lb.MasterZone != nil && lb.MasterZone.Zone != nil {
		zone = *lb.MasterZone.Zone
	}

	slaveZone := ""
	if lb.Zones != nil && len(lb.Zones) > 1 && lb.Zones[1] != nil {
		slaveZone = *lb.Zones[1]
	}

	createTime := ""
	if lb.CreateTime != nil {
		createTime = *lb.CreateTime
	}

	expiredTime := ""
	if lb.ExpireTime != nil {
		expiredTime = *lb.ExpireTime
	}

	chargeType := ""
	if lb.ChargeType != nil {
		chargeType = *lb.ChargeType
	}

	projectID := ""
	if lb.ProjectId != nil {
		projectID = fmt.Sprintf("%d", *lb.ProjectId)
	}

	tags := make(map[string]string)
	if lb.Tags != nil {
		for _, tag := range lb.Tags {
			if tag.TagKey != nil && tag.TagValue != nil {
				tags[*tag.TagKey] = *tag.TagValue
			}
		}
	}

	addressIPVersion := "ipv4"
	if lb.AddressIPVersion != nil {
		addressIPVersion = *lb.AddressIPVersion
	}

	return types.LBInstance{
		LoadBalancerID:   lbID,
		LoadBalancerName: lbName,
		LoadBalancerType: lbType,
		Status:           types.NormalizeLBStatus("tencent", status),
		Region:           region,
		Zone:             zone,
		SlaveZone:        slaveZone,
		Address:          address,
		AddressType:      addressType,
		AddressIPVersion: addressIPVersion,
		VPCID:            vpcID,
		VSwitchID:        subnetID,
		ChargeType:       chargeType,
		CreationTime:     createTime,
		ExpiredTime:      expiredTime,
		ProjectID:        projectID,
		Tags:             tags,
		Provider:         "tencent",
	}
}

// fetchListenerDetails 获取CLB监听器详情
func (a *LBAdapter) fetchListenerDetails(client *clb.Client, lbID string) ([]types.LBListener, int) {
	request := clb.NewDescribeListenersRequest()
	request.LoadBalancerId = common.StringPtr(lbID)

	response, err := client.DescribeListeners(request)
	if err != nil {
		a.logger.Warn("获取CLB监听器列表失败",
			elog.String("lb_id", lbID),
			elog.FieldErr(err))
		return nil, 0
	}

	if response.Response.Listeners == nil {
		return nil, 0
	}

	var listeners []types.LBListener
	for _, l := range response.Response.Listeners {
		listener := types.LBListener{}

		if l.ListenerId != nil {
			listener.ListenerID = *l.ListenerId
		}
		if l.Port != nil {
			listener.ListenerPort = int(*l.Port)
		}
		if l.Protocol != nil {
			listener.ListenerProtocol = *l.Protocol
		}
		if l.ListenerName != nil {
			listener.Description = *l.ListenerName
		}

		listeners = append(listeners, listener)
	}

	return listeners, len(listeners)
}

// fetchBackendServers 获取CLB后端服务器详情
func (a *LBAdapter) fetchBackendServers(client *clb.Client, lbID string) ([]types.LBBackendServer, int) {
	request := clb.NewDescribeTargetsRequest()
	request.LoadBalancerId = common.StringPtr(lbID)

	response, err := client.DescribeTargets(request)
	if err != nil {
		a.logger.Warn("获取CLB后端服务器列表失败",
			elog.String("lb_id", lbID),
			elog.FieldErr(err))
		return nil, 0
	}

	if response.Response.Listeners == nil {
		return nil, 0
	}

	// 使用 map 去重，同一个后端服务器可能绑定到多个监听器
	serverMap := make(map[string]types.LBBackendServer)
	for _, listener := range response.Response.Listeners {
		if listener.Targets == nil {
			continue
		}
		for _, target := range listener.Targets {
			serverID := ""
			if target.InstanceId != nil {
				serverID = *target.InstanceId
			}

			// 用 instanceID + port 作为唯一键
			port := 0
			if target.Port != nil {
				port = int(*target.Port)
			}
			key := fmt.Sprintf("%s:%d", serverID, port)

			if _, exists := serverMap[key]; !exists {
				server := types.LBBackendServer{
					ServerID:   serverID,
					InstanceID: serverID,
					Port:       port,
				}
				if target.Weight != nil {
					server.Weight = int(*target.Weight)
				}
				if target.Type != nil {
					server.Type = *target.Type
				}
				if target.PrivateIpAddresses != nil && len(target.PrivateIpAddresses) > 0 && target.PrivateIpAddresses[0] != nil {
					server.IP = *target.PrivateIpAddresses[0]
				}
				if target.InstanceName != nil {
					server.ServerName = *target.InstanceName
				}
				serverMap[key] = server
			}
		}
	}

	var backendServers []types.LBBackendServer
	for _, server := range serverMap {
		backendServers = append(backendServers, server)
	}

	return backendServers, len(backendServers)
}
