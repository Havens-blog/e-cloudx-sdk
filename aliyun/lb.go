package aliyun

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/slb"
	"github.com/gotomicro/ego/core/elog"
)

// LBAdapter 阿里云负载均衡适配器
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

// createClient 创建SLB客户端
func (a *LBAdapter) createClient(region string) (*slb.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}
	return slb.NewClientWithAccessKey(region, a.accessKeyID, a.accessKeySecret)
}

// ListInstances 获取负载均衡实例列表
func (a *LBAdapter) ListInstances(ctx context.Context, region string) ([]types.LBInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个负载均衡实例详情
func (a *LBAdapter) GetInstance(ctx context.Context, region, lbID string) (*types.LBInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建SLB客户端失败: %w", err)
	}

	request := slb.CreateDescribeLoadBalancerAttributeRequest()
	request.RegionId = region
	request.LoadBalancerId = lbID

	response, err := client.DescribeLoadBalancerAttribute(request)
	if err != nil {
		return nil, fmt.Errorf("获取负载均衡详情失败: %w", err)
	}

	instance := a.convertDetailToLBInstance(response, region)

	// 获取虚拟服务器组（VServer Group）的后端服务器
	vsgServers := a.fetchVServerGroupBackendServers(client, region, lbID)
	if len(vsgServers) > 0 {
		// 用默认服务器组已有的 ServerID:Port 去重
		seen := make(map[string]bool)
		for _, bs := range instance.BackendServers {
			seen[fmt.Sprintf("%s:%d", bs.ServerID, bs.Port)] = true
		}
		for _, bs := range vsgServers {
			key := fmt.Sprintf("%s:%d", bs.ServerID, bs.Port)
			if !seen[key] {
				instance.BackendServers = append(instance.BackendServers, bs)
				seen[key] = true
			}
		}
		instance.BackendServerCount = len(instance.BackendServers)
	}

	return &instance, nil
}

// ListInstancesByIDs 批量获取负载均衡实例
func (a *LBAdapter) ListInstancesByIDs(ctx context.Context, region string, lbIDs []string) ([]types.LBInstance, error) {
	var result []types.LBInstance
	for _, lbID := range lbIDs {
		instance, err := a.GetInstance(ctx, region, lbID)
		if err != nil {
			a.logger.Warn("获取负载均衡失败", elog.String("lb_id", lbID), elog.FieldErr(err))
			continue
		}
		result = append(result, *instance)
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
		return nil, fmt.Errorf("创建SLB客户端失败: %w", err)
	}

	var allInstances []types.LBInstance
	pageNumber := 1
	pageSize := 50

	if filter != nil {
		if filter.PageSize > 0 {
			pageSize = filter.PageSize
		}
	}

	for {
		request := slb.CreateDescribeLoadBalancersRequest()
		request.RegionId = region
		request.PageNumber = requests.NewInteger(pageNumber)
		request.PageSize = requests.NewInteger(pageSize)

		if filter != nil {
			if filter.LoadBalancerName != "" {
				request.LoadBalancerName = filter.LoadBalancerName
			}
			if filter.AddressType != "" {
				request.AddressType = filter.AddressType
			}
			if filter.VPCID != "" {
				request.VpcId = filter.VPCID
			}
			if len(filter.LoadBalancerIDs) == 1 {
				request.LoadBalancerId = filter.LoadBalancerIDs[0]
			}
		}

		response, err := client.DescribeLoadBalancers(request)
		if err != nil {
			return nil, fmt.Errorf("获取负载均衡列表失败: %w", err)
		}

		for _, lb := range response.LoadBalancers.LoadBalancer {
			allInstances = append(allInstances, a.convertToLBInstance(lb, region))
		}

		if len(response.LoadBalancers.LoadBalancer) < pageSize {
			break
		}
		pageNumber++
	}

	// CLB 列表接口不返回监听器和后端服务器，需要逐个调用详情接口补充
	for i, inst := range allInstances {
		detail, err := a.GetInstance(ctx, region, inst.LoadBalancerID)
		if err != nil {
			a.logger.Warn("获取CLB详情失败", elog.String("lb_id", inst.LoadBalancerID), elog.FieldErr(err))
			continue
		}
		allInstances[i].Listeners = detail.Listeners
		allInstances[i].ListenerCount = detail.ListenerCount
		allInstances[i].BackendServers = detail.BackendServers
		allInstances[i].BackendServerCount = detail.BackendServerCount
	}

	// 查询 ALB 实例
	albInstances, err := a.listALBInstances(region)
	if err != nil {
		a.logger.Warn("获取ALB实例列表失败", elog.String("region", region), elog.FieldErr(err))
	} else {
		allInstances = append(allInstances, albInstances...)
	}

	// 查询 NLB 实例
	nlbInstances, err := a.listNLBInstances(region)
	if err != nil {
		a.logger.Warn("获取NLB实例列表失败", elog.String("region", region), elog.FieldErr(err))
	} else {
		allInstances = append(allInstances, nlbInstances...)
	}

	a.logger.Info("获取阿里云负载均衡列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertToLBInstance 转换为通用LB实例
func (a *LBAdapter) convertToLBInstance(lb slb.LoadBalancer, region string) types.LBInstance {
	tags := make(map[string]string)

	// 判断LB类型
	lbType := "clb"
	if lb.LoadBalancerSpec != "" {
		lbType = "clb"
	}

	return types.LBInstance{
		LoadBalancerID:     lb.LoadBalancerId,
		LoadBalancerName:   lb.LoadBalancerName,
		LoadBalancerType:   lbType,
		Status:             types.NormalizeLBStatus("aliyun", lb.LoadBalancerStatus),
		Region:             region,
		Zone:               lb.MasterZoneId,
		SlaveZone:          lb.SlaveZoneId,
		Address:            lb.Address,
		AddressType:        lb.AddressType,
		AddressIPVersion:   lb.AddressIPVersion,
		VPCID:              lb.VpcId,
		VSwitchID:          lb.VSwitchId,
		NetworkType:        lb.NetworkType,
		LoadBalancerSpec:   lb.LoadBalancerSpec,
		Bandwidth:          lb.Bandwidth,
		InternetChargeType: lb.InternetChargeType,
		ChargeType:         lb.PayType,
		CreationTime:       lb.CreateTime,
		ResourceGroupID:    lb.ResourceGroupId,
		Tags:               tags,
		Provider:           "aliyun",
	}
}

// convertDetailToLBInstance 从详情接口转换为通用LB实例
func (a *LBAdapter) convertDetailToLBInstance(resp *slb.DescribeLoadBalancerAttributeResponse, region string) types.LBInstance {
	tags := make(map[string]string)

	lbType := "clb"

	// 提取监听器详情
	var listeners []types.LBListener
	for _, l := range resp.ListenerPortsAndProtocol.ListenerPortAndProtocol {
		listeners = append(listeners, types.LBListener{
			ListenerPort:     l.ListenerPort,
			ListenerProtocol: l.ListenerProtocol,
			Description:      l.ListenerForward,
		})
	}

	// 提取后端服务器详情
	var backendServers []types.LBBackendServer
	for _, bs := range resp.BackendServers.BackendServer {
		backendServers = append(backendServers, types.LBBackendServer{
			ServerID:    bs.ServerId,
			IP:          bs.ServerIp,
			Weight:      bs.Weight,
			Type:        bs.Type,
			Description: bs.Description,
		})
	}

	return types.LBInstance{
		LoadBalancerID:     resp.LoadBalancerId,
		LoadBalancerName:   resp.LoadBalancerName,
		LoadBalancerType:   lbType,
		Status:             resp.LoadBalancerStatus,
		Region:             region,
		Zone:               resp.MasterZoneId,
		SlaveZone:          resp.SlaveZoneId,
		Address:            resp.Address,
		AddressType:        resp.AddressType,
		AddressIPVersion:   resp.AddressIPVersion,
		VPCID:              resp.VpcId,
		VSwitchID:          resp.VSwitchId,
		NetworkType:        resp.NetworkType,
		LoadBalancerSpec:   resp.LoadBalancerSpec,
		Bandwidth:          resp.Bandwidth,
		InternetChargeType: resp.InternetChargeType,
		ChargeType:         resp.PayType,
		CreationTime:       resp.CreateTime,
		ResourceGroupID:    resp.ResourceGroupId,
		ListenerCount:      len(resp.ListenerPortsAndProtocol.ListenerPortAndProtocol),
		BackendServerCount: len(resp.BackendServers.BackendServer),
		Listeners:          listeners,
		BackendServers:     backendServers,
		Tags:               tags,
		Description:        resp.LoadBalancerName,
		Provider:           "aliyun",
	}
}

// fetchVServerGroupBackendServers 获取 CLB 虚拟服务器组的后端服务器
// 阿里云 CLB 的后端服务器分两种：默认服务器组（DescribeLoadBalancerAttribute 返回）和虚拟服务器组（需要额外查询）
func (a *LBAdapter) fetchVServerGroupBackendServers(client *slb.Client, region, lbID string) []types.LBBackendServer {
	// 1. 获取虚拟服务器组列表
	vsgRequest := slb.CreateDescribeVServerGroupsRequest()
	vsgRequest.RegionId = region
	vsgRequest.LoadBalancerId = lbID

	vsgResponse, err := client.DescribeVServerGroups(vsgRequest)
	if err != nil {
		a.logger.Debug("获取虚拟服务器组列表失败",
			elog.String("lb_id", lbID),
			elog.FieldErr(err))
		return nil
	}

	if len(vsgResponse.VServerGroups.VServerGroup) == 0 {
		return nil
	}

	// 2. 逐个获取虚拟服务器组的后端服务器
	var allServers []types.LBBackendServer
	seen := make(map[string]bool) // 去重：同一个 ServerID+Port 只保留一次

	for _, vsg := range vsgResponse.VServerGroups.VServerGroup {
		attrRequest := slb.CreateDescribeVServerGroupAttributeRequest()
		attrRequest.RegionId = region
		attrRequest.VServerGroupId = vsg.VServerGroupId

		attrResponse, err := client.DescribeVServerGroupAttribute(attrRequest)
		if err != nil {
			a.logger.Debug("获取虚拟服务器组详情失败",
				elog.String("vsg_id", vsg.VServerGroupId),
				elog.FieldErr(err))
			continue
		}

		for _, bs := range attrResponse.BackendServers.BackendServer {
			key := fmt.Sprintf("%s:%d", bs.ServerId, bs.Port)
			if seen[key] {
				continue
			}
			seen[key] = true

			allServers = append(allServers, types.LBBackendServer{
				ServerID:    bs.ServerId,
				IP:          bs.ServerIp,
				Port:        bs.Port,
				Weight:      bs.Weight,
				Type:        bs.Type,
				Description: bs.Description,
			})
		}
	}

	if len(allServers) > 0 {
		a.logger.Debug("获取虚拟服务器组后端服务器",
			elog.String("lb_id", lbID),
			elog.Int("vsg_count", len(vsgResponse.VServerGroups.VServerGroup)),
			elog.Int("server_count", len(allServers)))
	}

	return allServers
}

// ==================== ALB (Application Load Balancer) ====================

// albListResponse ALB ListLoadBalancers 响应
type albListResponse struct {
	RequestID     string        `json:"RequestId"`
	LoadBalancers []albInstance `json:"LoadBalancers"`
	TotalCount    int           `json:"TotalCount"`
	NextToken     string        `json:"NextToken"`
}

// albInstance ALB 实例
type albInstance struct {
	LoadBalancerID       string `json:"LoadBalancerId"`
	LoadBalancerName     string `json:"LoadBalancerName"`
	LoadBalancerStatus   string `json:"LoadBalancerStatus"`
	AddressType          string `json:"AddressType"`
	AddressAllocatedMode string `json:"AddressAllocatedMode"`
	VpcID                string `json:"VpcId"`
	CreateTime           string `json:"CreateTime"`
	LoadBalancerEdition  string `json:"LoadBalancerEdition"`
	DNSName              string `json:"DNSName"`
	ResourceGroupID      string `json:"ResourceGroupId"`
	AddressIPVersion     string `json:"AddressIpVersion"`
}

// albListListenersResponse ALB ListListeners 响应
type albListListenersResponse struct {
	RequestID  string        `json:"RequestId"`
	Listeners  []albListener `json:"Listeners"`
	TotalCount int           `json:"TotalCount"`
	NextToken  string        `json:"NextToken"`
}

// albListener ALB 监听器
type albListener struct {
	ListenerID       string             `json:"ListenerId"`
	ListenerPort     int                `json:"ListenerPort"`
	ListenerProtocol string             `json:"ListenerProtocol"`
	ListenerStatus   string             `json:"ListenerStatus"`
	DefaultActions   []albDefaultAction `json:"DefaultActions"`
}

// albDefaultAction ALB 监听器默认动作
type albDefaultAction struct {
	ForwardGroupConfig *albForwardGroupConfig `json:"ForwardGroupConfig"`
	Type               string                 `json:"Type"`
}

type albForwardGroupConfig struct {
	ServerGroupTuples []albServerGroupTuple `json:"ServerGroupTuples"`
}

type albServerGroupTuple struct {
	ServerGroupID string `json:"ServerGroupId"`
}

// albListServerGroupsResponse ALB ListServerGroups 响应
type albListServerGroupsResponse struct {
	RequestID    string           `json:"RequestId"`
	ServerGroups []albServerGroup `json:"ServerGroups"`
	TotalCount   int              `json:"TotalCount"`
	NextToken    string           `json:"NextToken"`
}

// albServerGroup ALB 服务器组
type albServerGroup struct {
	ServerGroupID   string `json:"ServerGroupId"`
	ServerGroupName string `json:"ServerGroupName"`
	ServerCount     int    `json:"ServerCount"`
}

// nlbListListenersResponse NLB ListListeners 响应
type nlbListListenersResponse struct {
	RequestID  string        `json:"RequestId"`
	Listeners  []nlbListener `json:"Listeners"`
	TotalCount int           `json:"TotalCount"`
	NextToken  string        `json:"NextToken"`
}

// nlbListener NLB 监听器
type nlbListener struct {
	ListenerID       string `json:"ListenerId"`
	ListenerPort     int    `json:"ListenerPort"`
	ListenerProtocol string `json:"ListenerProtocol"`
	ListenerStatus   string `json:"ListenerStatus"`
	ServerGroupID    string `json:"ServerGroupId"`
}

// nlbListServerGroupsResponse NLB ListServerGroups 响应
type nlbListServerGroupsResponse struct {
	RequestID    string           `json:"RequestId"`
	ServerGroups []nlbServerGroup `json:"ServerGroups"`
	TotalCount   int              `json:"TotalCount"`
	NextToken    string           `json:"NextToken"`
}

// nlbServerGroup NLB 服务器组
type nlbServerGroup struct {
	ServerGroupID   string `json:"ServerGroupId"`
	ServerGroupName string `json:"ServerGroupName"`
	ServerCount     int    `json:"ServerCount"`
}

// createCommonClient 创建通用SDK客户端
func (a *LBAdapter) createCommonClient(region string) (*sdk.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}
	return sdk.NewClientWithAccessKey(region, a.accessKeyID, a.accessKeySecret)
}

// listALBInstances 获取ALB实例列表
func (a *LBAdapter) listALBInstances(region string) ([]types.LBInstance, error) {
	client, err := a.createCommonClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建ALB客户端失败: %w", err)
	}

	var allInstances []types.LBInstance
	nextToken := ""
	pageSize := 50

	for {
		request := requests.NewCommonRequest()
		request.Method = "POST"
		request.Scheme = "https"
		request.Domain = fmt.Sprintf("alb.%s.aliyuncs.com", region)
		request.Version = "2020-06-16"
		request.ApiName = "ListLoadBalancers"
		request.QueryParams["RegionId"] = region
		request.QueryParams["MaxResults"] = strconv.Itoa(pageSize)

		if nextToken != "" {
			request.QueryParams["NextToken"] = nextToken
		}

		response, err := client.ProcessCommonRequest(request)
		if err != nil {
			a.logger.Warn("获取ALB实例列表失败", elog.String("region", region), elog.FieldErr(err))
			return nil, nil
		}

		rawBody := string(response.GetHttpContentBytes())
		var resp albListResponse
		if err := json.Unmarshal(response.GetHttpContentBytes(), &resp); err != nil {
			truncated := rawBody
			if len(truncated) > 500 {
				truncated = truncated[:500]
			}
			a.logger.Warn("解析ALB响应失败", elog.String("region", region), elog.String("raw", truncated))
			return nil, fmt.Errorf("解析ALB响应失败: %w", err)
		}

		a.logger.Info("ALB查询结果", elog.String("region", region), elog.Int("count", len(resp.LoadBalancers)), elog.Int("total", resp.TotalCount))

		for _, lb := range resp.LoadBalancers {
			allInstances = append(allInstances, a.convertALBToLBInstance(lb, region))
		}

		if resp.NextToken == "" || len(resp.LoadBalancers) == 0 {
			break
		}
		nextToken = resp.NextToken
	}

	// 为每个ALB实例获取监听器和后端服务器组信息
	for i := range allInstances {
		listeners, sgIDs, listenerCount := a.fetchALBListeners(client, region, allInstances[i].LoadBalancerID)
		allInstances[i].Listeners = listeners
		allInstances[i].ListenerCount = listenerCount

		// 只查当前 ALB 监听器关联的服务器组，不查全量
		backendServers := a.fetchALBBackendServersByGroups(client, region, sgIDs)
		allInstances[i].BackendServers = backendServers
		allInstances[i].BackendServerCount = len(backendServers)
	}

	return allInstances, nil
}

// fetchALBListeners 获取ALB监听器列表，同时提取关联的 ServerGroupId
func (a *LBAdapter) fetchALBListeners(client *sdk.Client, region, lbID string) ([]types.LBListener, []string, int) {
	request := requests.NewCommonRequest()
	request.Method = "POST"
	request.Scheme = "https"
	request.Domain = fmt.Sprintf("alb.%s.aliyuncs.com", region)
	request.Version = "2020-06-16"
	request.ApiName = "ListListeners"
	request.QueryParams["LoadBalancerIds.1"] = lbID

	response, err := client.ProcessCommonRequest(request)
	if err != nil {
		a.logger.Warn("获取ALB监听器列表失败", elog.String("lb_id", lbID), elog.FieldErr(err))
		return nil, nil, 0
	}

	var resp albListListenersResponse
	if err := json.Unmarshal(response.GetHttpContentBytes(), &resp); err != nil {
		a.logger.Warn("解析ALB监听器响应失败", elog.String("lb_id", lbID), elog.FieldErr(err))
		return nil, nil, 0
	}

	var listeners []types.LBListener
	sgIDSet := make(map[string]bool)

	for _, l := range resp.Listeners {
		listeners = append(listeners, types.LBListener{
			ListenerID:       l.ListenerID,
			ListenerPort:     l.ListenerPort,
			ListenerProtocol: l.ListenerProtocol,
			Status:           l.ListenerStatus,
		})
		// 从 DefaultActions 提取关联的 ServerGroupId
		for _, action := range l.DefaultActions {
			if action.ForwardGroupConfig != nil {
				for _, tuple := range action.ForwardGroupConfig.ServerGroupTuples {
					if tuple.ServerGroupID != "" {
						sgIDSet[tuple.ServerGroupID] = true
					}
				}
			}
		}
		// 从转发规则(Rules)中提取 ServerGroupId（ACK Ingress 场景）
		if l.ListenerID != "" {
			ruleSGIDs := a.fetchALBRuleServerGroupIDs(client, region, l.ListenerID)
			for _, id := range ruleSGIDs {
				sgIDSet[id] = true
			}
		}
	}

	var sgIDs []string
	for id := range sgIDSet {
		sgIDs = append(sgIDs, id)
	}

	a.logger.Info("ALB 提取到的 ServerGroupId",
		elog.String("lb_id", lbID),
		elog.Int("sg_count", len(sgIDs)))

	return listeners, sgIDs, resp.TotalCount
}

// ==================== ALB 转发规则 ====================

// albListRulesResponse ALB ListRules 响应
type albListRulesResponse struct {
	RequestID  string    `json:"RequestId"`
	Rules      []albRule `json:"Rules"`
	TotalCount int       `json:"TotalCount"`
	NextToken  string    `json:"NextToken"`
}

// albRule ALB 转发规则
type albRule struct {
	RuleID      string          `json:"RuleId"`
	RuleName    string          `json:"RuleName"`
	RuleActions []albRuleAction `json:"RuleActions"`
}

// albRuleAction ALB 规则动作
type albRuleAction struct {
	Type               string                 `json:"Type"`
	ForwardGroupConfig *albForwardGroupConfig `json:"ForwardGroupConfig"`
}

// fetchALBRuleServerGroupIDs 从 ALB 监听器的转发规则中提取 ServerGroupId
// ACK Ingress 场景下，后端服务器组配置在转发规则里而不是 DefaultActions
func (a *LBAdapter) fetchALBRuleServerGroupIDs(client *sdk.Client, region, listenerID string) []string {
	sgIDSet := make(map[string]bool)
	nextToken := ""

	for {
		request := requests.NewCommonRequest()
		request.Method = "POST"
		request.Scheme = "https"
		request.Domain = fmt.Sprintf("alb.%s.aliyuncs.com", region)
		request.Version = "2020-06-16"
		request.ApiName = "ListRules"
		request.QueryParams["ListenerId"] = listenerID
		request.QueryParams["MaxResults"] = "100"

		if nextToken != "" {
			request.QueryParams["NextToken"] = nextToken
		}

		response, err := client.ProcessCommonRequest(request)
		if err != nil {
			// 某些监听器可能没有规则，不需要报警
			return nil
		}

		var resp albListRulesResponse
		if err := json.Unmarshal(response.GetHttpContentBytes(), &resp); err != nil {
			return nil
		}

		for _, rule := range resp.Rules {
			for _, action := range rule.RuleActions {
				if action.ForwardGroupConfig != nil {
					for _, tuple := range action.ForwardGroupConfig.ServerGroupTuples {
						if tuple.ServerGroupID != "" {
							sgIDSet[tuple.ServerGroupID] = true
						}
					}
				}
			}
		}

		if resp.NextToken == "" || len(resp.Rules) == 0 {
			break
		}
		nextToken = resp.NextToken
	}

	var sgIDs []string
	for id := range sgIDSet {
		sgIDs = append(sgIDs, id)
	}
	return sgIDs
}

// albListServerGroupServersResponse ALB ListServerGroupServers 响应
type albListServerGroupServersResponse struct {
	RequestID  string          `json:"RequestId"`
	Servers    []albServerItem `json:"Servers"`
	TotalCount int             `json:"TotalCount"`
	NextToken  string          `json:"NextToken"`
}

// albServerItem ALB 服务器组中的后端服务器
type albServerItem struct {
	ServerID    string `json:"ServerId"`    // 后端服务器 ID（ECS 实例 ID / ENI ID / Pod IP）
	ServerIP    string `json:"ServerIp"`    // 后端服务器 IP
	ServerType  string `json:"ServerType"`  // Ecs / Eni / Ip / Fc
	Port        int    `json:"Port"`        // 端口
	Weight      int    `json:"Weight"`      // 权重
	Status      string `json:"Status"`      // Available / Unavailable
	Description string `json:"Description"` // 描述
}

// fetchALBBackendServersByGroups 根据指定的 ServerGroupId 列表获取后端服务器
// 只查当前 ALB 监听器关联的服务器组，避免查全量导致数据错乱
func (a *LBAdapter) fetchALBBackendServersByGroups(client *sdk.Client, region string, serverGroupIDs []string) []types.LBBackendServer {
	if len(serverGroupIDs) == 0 {
		return nil
	}

	var allServers []types.LBBackendServer
	seen := make(map[string]bool)

	for _, sgID := range serverGroupIDs {
		servers := a.fetchServerGroupServers(client, region, sgID)
		for _, s := range servers {
			key := fmt.Sprintf("%s:%d", s.ServerID, s.Port)
			if seen[key] {
				continue
			}
			seen[key] = true
			allServers = append(allServers, s)
		}
	}

	return allServers
}

// fetchServerGroupServers 获取单个服务器组中的后端服务器
func (a *LBAdapter) fetchServerGroupServers(client *sdk.Client, region, serverGroupID string) []types.LBBackendServer {
	var allServers []types.LBBackendServer
	nextToken := ""

	for {
		request := requests.NewCommonRequest()
		request.Method = "POST"
		request.Scheme = "https"
		request.Domain = fmt.Sprintf("alb.%s.aliyuncs.com", region)
		request.Version = "2020-06-16"
		request.ApiName = "ListServerGroupServers"
		request.QueryParams["ServerGroupId"] = serverGroupID
		request.QueryParams["MaxResults"] = "100"

		if nextToken != "" {
			request.QueryParams["NextToken"] = nextToken
		}

		response, err := client.ProcessCommonRequest(request)
		if err != nil {
			a.logger.Warn("获取ALB服务器组后端失败",
				elog.String("server_group_id", serverGroupID),
				elog.FieldErr(err))
			return allServers
		}

		var resp albListServerGroupServersResponse
		if err := json.Unmarshal(response.GetHttpContentBytes(), &resp); err != nil {
			a.logger.Warn("解析ALB服务器组后端响应失败",
				elog.String("server_group_id", serverGroupID),
				elog.FieldErr(err))
			return allServers
		}

		for _, s := range resp.Servers {
			server := types.LBBackendServer{
				ServerID:    s.ServerID,
				InstanceID:  s.ServerID,
				IP:          s.ServerIP,
				Port:        s.Port,
				Weight:      s.Weight,
				Type:        s.ServerType,
				Status:      s.Status,
				Description: s.Description,
			}
			allServers = append(allServers, server)
		}

		if resp.NextToken == "" || len(resp.Servers) == 0 {
			break
		}
		nextToken = resp.NextToken
	}

	return allServers
}

// convertALBToLBInstance 将ALB实例转换为通用LB实例
func (a *LBAdapter) convertALBToLBInstance(lb albInstance, region string) types.LBInstance {
	address := lb.DNSName

	return types.LBInstance{
		LoadBalancerID:      lb.LoadBalancerID,
		LoadBalancerName:    lb.LoadBalancerName,
		LoadBalancerType:    "alb",
		Status:              types.NormalizeLBStatus("aliyun", lb.LoadBalancerStatus),
		Region:              region,
		Address:             address,
		AddressType:         lb.AddressType,
		AddressIPVersion:    lb.AddressIPVersion,
		VPCID:               lb.VpcID,
		LoadBalancerEdition: lb.LoadBalancerEdition,
		CreationTime:        lb.CreateTime,
		ResourceGroupID:     lb.ResourceGroupID,
		Tags:                make(map[string]string),
		Provider:            "aliyun",
	}
}

// ==================== NLB (Network Load Balancer) ====================

// nlbListResponse NLB ListLoadBalancers 响应
type nlbListResponse struct {
	RequestID     string        `json:"RequestId"`
	LoadBalancers []nlbInstance `json:"LoadBalancers"`
	TotalCount    int           `json:"TotalCount"`
	NextToken     string        `json:"NextToken"`
}

// nlbInstance NLB 实例
type nlbInstance struct {
	LoadBalancerID     string `json:"LoadBalancerId"`
	LoadBalancerName   string `json:"LoadBalancerName"`
	LoadBalancerStatus string `json:"LoadBalancerStatus"`
	AddressType        string `json:"AddressType"`
	VpcID              string `json:"VpcId"`
	CreateTime         string `json:"CreateTime"`
	DNSName            string `json:"DNSName"`
	ResourceGroupID    string `json:"ResourceGroupId"`
	AddressIPVersion   string `json:"AddressIpVersion"`
	BandwidthPackageID string `json:"BandwidthPackageId"`
}

// listNLBInstances 获取NLB实例列表
func (a *LBAdapter) listNLBInstances(region string) ([]types.LBInstance, error) {
	client, err := a.createCommonClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建NLB客户端失败: %w", err)
	}

	var allInstances []types.LBInstance
	nextToken := ""
	pageSize := 50

	for {
		request := requests.NewCommonRequest()
		request.Method = "POST"
		request.Scheme = "https"
		request.Domain = fmt.Sprintf("nlb.%s.aliyuncs.com", region)
		request.Version = "2022-04-30"
		request.ApiName = "ListLoadBalancers"
		request.QueryParams["RegionId"] = region
		request.QueryParams["MaxResults"] = strconv.Itoa(pageSize)

		if nextToken != "" {
			request.QueryParams["NextToken"] = nextToken
		}

		response, err := client.ProcessCommonRequest(request)
		if err != nil {
			a.logger.Warn("获取NLB实例列表失败", elog.String("region", region), elog.FieldErr(err))
			return nil, nil
		}

		var resp nlbListResponse
		if err := json.Unmarshal(response.GetHttpContentBytes(), &resp); err != nil {
			return nil, fmt.Errorf("解析NLB响应失败: %w", err)
		}

		for _, lb := range resp.LoadBalancers {
			allInstances = append(allInstances, a.convertNLBToLBInstance(lb, region))
		}

		if resp.NextToken == "" || len(resp.LoadBalancers) == 0 {
			break
		}
		nextToken = resp.NextToken
	}

	// 为每个NLB实例获取监听器和后端服务器组信息
	for i := range allInstances {
		listeners, sgIDs, listenerCount := a.fetchNLBListeners(client, region, allInstances[i].LoadBalancerID)
		allInstances[i].Listeners = listeners
		allInstances[i].ListenerCount = listenerCount

		backendServers := a.fetchNLBBackendServersByGroups(client, region, sgIDs)
		allInstances[i].BackendServers = backendServers
		allInstances[i].BackendServerCount = len(backendServers)
	}

	return allInstances, nil
}

// fetchNLBListeners 获取NLB监听器列表，同时提取关联的 ServerGroupId
func (a *LBAdapter) fetchNLBListeners(client *sdk.Client, region, lbID string) ([]types.LBListener, []string, int) {
	request := requests.NewCommonRequest()
	request.Method = "POST"
	request.Scheme = "https"
	request.Domain = fmt.Sprintf("nlb.%s.aliyuncs.com", region)
	request.Version = "2022-04-30"
	request.ApiName = "ListListeners"
	request.QueryParams["LoadBalancerIds.1"] = lbID

	response, err := client.ProcessCommonRequest(request)
	if err != nil {
		a.logger.Warn("获取NLB监听器列表失败", elog.String("lb_id", lbID), elog.FieldErr(err))
		return nil, nil, 0
	}

	var resp nlbListListenersResponse
	if err := json.Unmarshal(response.GetHttpContentBytes(), &resp); err != nil {
		a.logger.Warn("解析NLB监听器响应失败", elog.String("lb_id", lbID), elog.FieldErr(err))
		return nil, nil, 0
	}

	var listeners []types.LBListener
	sgIDSet := make(map[string]bool)

	for _, l := range resp.Listeners {
		listeners = append(listeners, types.LBListener{
			ListenerID:       l.ListenerID,
			ListenerPort:     l.ListenerPort,
			ListenerProtocol: l.ListenerProtocol,
			Status:           l.ListenerStatus,
		})
		if l.ServerGroupID != "" {
			sgIDSet[l.ServerGroupID] = true
		}
	}

	var sgIDs []string
	for id := range sgIDSet {
		sgIDs = append(sgIDs, id)
	}

	return listeners, sgIDs, resp.TotalCount
}

// fetchNLBBackendServersByGroups 根据指定的 ServerGroupId 列表获取 NLB 后端服务器
func (a *LBAdapter) fetchNLBBackendServersByGroups(client *sdk.Client, region string, serverGroupIDs []string) []types.LBBackendServer {
	if len(serverGroupIDs) == 0 {
		return nil
	}

	var allServers []types.LBBackendServer
	seen := make(map[string]bool)

	for _, sgID := range serverGroupIDs {
		servers := a.fetchNLBServerGroupServers(client, region, sgID)
		for _, s := range servers {
			key := fmt.Sprintf("%s:%d", s.ServerID, s.Port)
			if seen[key] {
				continue
			}
			seen[key] = true
			allServers = append(allServers, s)
		}
	}

	return allServers
}

// nlbListServerGroupServersResponse NLB ListServerGroupServers 响应
type nlbListServerGroupServersResponse struct {
	RequestID  string          `json:"RequestId"`
	Servers    []nlbServerItem `json:"Servers"`
	TotalCount int             `json:"TotalCount"`
	NextToken  string          `json:"NextToken"`
}

type nlbServerItem struct {
	ServerID    string `json:"ServerId"`
	ServerIP    string `json:"ServerIp"`
	ServerType  string `json:"ServerType"` // Ecs / Eni / Ip
	Port        int    `json:"Port"`
	Weight      int    `json:"Weight"`
	Status      string `json:"Status"`
	Description string `json:"Description"`
}

// fetchNLBServerGroupServers 获取单个 NLB 服务器组中的后端服务器
func (a *LBAdapter) fetchNLBServerGroupServers(client *sdk.Client, region, serverGroupID string) []types.LBBackendServer {
	var allServers []types.LBBackendServer
	nextToken := ""

	for {
		request := requests.NewCommonRequest()
		request.Method = "POST"
		request.Scheme = "https"
		request.Domain = fmt.Sprintf("nlb.%s.aliyuncs.com", region)
		request.Version = "2022-04-30"
		request.ApiName = "ListServerGroupServers"
		request.QueryParams["ServerGroupId"] = serverGroupID
		request.QueryParams["MaxResults"] = "100"

		if nextToken != "" {
			request.QueryParams["NextToken"] = nextToken
		}

		response, err := client.ProcessCommonRequest(request)
		if err != nil {
			a.logger.Warn("获取NLB服务器组后端失败",
				elog.String("server_group_id", serverGroupID),
				elog.FieldErr(err))
			return allServers
		}

		var resp nlbListServerGroupServersResponse
		if err := json.Unmarshal(response.GetHttpContentBytes(), &resp); err != nil {
			a.logger.Warn("解析NLB服务器组后端响应失败",
				elog.String("server_group_id", serverGroupID),
				elog.FieldErr(err))
			return allServers
		}

		for _, s := range resp.Servers {
			server := types.LBBackendServer{
				ServerID:    s.ServerID,
				InstanceID:  s.ServerID,
				IP:          s.ServerIP,
				Port:        s.Port,
				Weight:      s.Weight,
				Type:        s.ServerType,
				Status:      s.Status,
				Description: s.Description,
			}
			allServers = append(allServers, server)
		}

		if resp.NextToken == "" || len(resp.Servers) == 0 {
			break
		}
		nextToken = resp.NextToken
	}

	return allServers
}

// convertNLBToLBInstance 将NLB实例转换为通用LB实例
func (a *LBAdapter) convertNLBToLBInstance(lb nlbInstance, region string) types.LBInstance {
	address := lb.DNSName

	return types.LBInstance{
		LoadBalancerID:     lb.LoadBalancerID,
		LoadBalancerName:   lb.LoadBalancerName,
		LoadBalancerType:   "nlb",
		Status:             types.NormalizeLBStatus("aliyun", lb.LoadBalancerStatus),
		Region:             region,
		Address:            address,
		AddressType:        lb.AddressType,
		AddressIPVersion:   lb.AddressIPVersion,
		VPCID:              lb.VpcID,
		BandwidthPackageID: lb.BandwidthPackageID,
		CreationTime:       lb.CreateTime,
		ResourceGroupID:    lb.ResourceGroupID,
		Tags:               make(map[string]string),
		Provider:           "aliyun",
	}
}
