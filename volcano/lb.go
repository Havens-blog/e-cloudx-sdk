package volcano

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/alb"
	"github.com/volcengine/volcengine-go-sdk/service/clb"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// LBAdapter 火山引擎负载均衡适配器 (CLB)
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
func (a *LBAdapter) createClient(region string) (*clb.CLB, error) {
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

	return clb.New(sess), nil
}

// createALBClient 创建ALB客户端
func (a *LBAdapter) createALBClient(region string) (*alb.ALB, error) {
	if region == "" {
		region = a.defaultRegion
	}

	config := volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(a.accessKeyID, a.accessKeySecret, "")).
		WithRegion(region)

	sess, err := session.NewSession(config)
	if err != nil {
		return nil, fmt.Errorf("创建ALB会话失败: %w", err)
	}

	return alb.New(sess), nil
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

	input := &clb.DescribeLoadBalancerAttributesInput{
		LoadBalancerId: volcengine.String(lbID),
	}

	output, err := client.DescribeLoadBalancerAttributes(input)
	if err != nil {
		return nil, fmt.Errorf("获取负载均衡详情失败: %w", err)
	}

	instance := a.convertDetailToLBInstance(output, region)
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

	idPtrs := make([]*string, len(lbIDs))
	for i, id := range lbIDs {
		idPtrs[i] = volcengine.String(id)
	}

	input := &clb.DescribeLoadBalancersInput{
		LoadBalancerIds: idPtrs,
	}

	output, err := client.DescribeLoadBalancers(input)
	if err != nil {
		return nil, fmt.Errorf("批量获取负载均衡失败: %w", err)
	}

	var result []types.LBInstance
	if output.LoadBalancers != nil {
		for _, lb := range output.LoadBalancers {
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
	pageNumber := int64(1)
	pageSize := int64(100)

	if filter != nil && filter.PageSize > 0 {
		pageSize = int64(filter.PageSize)
	}

	for {
		input := &clb.DescribeLoadBalancersInput{
			PageNumber: &pageNumber,
			PageSize:   &pageSize,
		}

		if filter != nil {
			if len(filter.LoadBalancerIDs) > 0 {
				idPtrs := make([]*string, len(filter.LoadBalancerIDs))
				for i, id := range filter.LoadBalancerIDs {
					idPtrs[i] = volcengine.String(id)
				}
				input.LoadBalancerIds = idPtrs
			}
			if filter.LoadBalancerName != "" {
				input.LoadBalancerName = volcengine.String(filter.LoadBalancerName)
			}
			if filter.VPCID != "" {
				input.VpcId = volcengine.String(filter.VPCID)
			}
		}

		output, err := client.DescribeLoadBalancers(input)
		if err != nil {
			return nil, fmt.Errorf("获取负载均衡列表失败: %w", err)
		}

		if output.LoadBalancers == nil || len(output.LoadBalancers) == 0 {
			break
		}

		for _, lb := range output.LoadBalancers {
			instance := a.convertToLBInstance(lb, region)

			// 客户端过滤: AddressType
			if filter != nil && filter.AddressType != "" {
				if filter.AddressType != instance.AddressType {
					continue
				}
			}

			allInstances = append(allInstances, instance)
		}

		if len(output.LoadBalancers) < int(pageSize) {
			break
		}
		pageNumber++
	}

	// CLB实例数量较少时，补充监听器和后端服务器详情
	if len(allInstances) > 0 && len(allInstances) <= 50 {
		a.enrichCLBDetails(client, allInstances)
	}

	// 查询ALB实例
	albInstances, err := a.listALBInstances(ctx, region, filter)
	if err != nil {
		a.logger.Warn("获取ALB实例列表失败，跳过ALB",
			elog.String("region", region),
			elog.FieldErr(err))
	} else {
		allInstances = append(allInstances, albInstances...)
	}

	a.logger.Info("获取火山引擎负载均衡列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// listALBInstances 查询ALB实例列表
func (a *LBAdapter) listALBInstances(ctx context.Context, region string, filter *types.LBInstanceFilter) ([]types.LBInstance, error) {
	albClient, err := a.createALBClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建ALB客户端失败: %w", err)
	}

	var albInstances []types.LBInstance
	pageNumber := int64(1)
	pageSize := int64(100)

	if filter != nil && filter.PageSize > 0 {
		pageSize = int64(filter.PageSize)
	}

	for {
		input := &alb.DescribeLoadBalancersInput{
			PageNumber: &pageNumber,
			PageSize:   &pageSize,
		}

		if filter != nil {
			if len(filter.LoadBalancerIDs) > 0 {
				idPtrs := make([]*string, len(filter.LoadBalancerIDs))
				for i, id := range filter.LoadBalancerIDs {
					idPtrs[i] = volcengine.String(id)
				}
				input.LoadBalancerIds = idPtrs
			}
			if filter.LoadBalancerName != "" {
				input.LoadBalancerName = volcengine.String(filter.LoadBalancerName)
			}
			if filter.VPCID != "" {
				input.VpcId = volcengine.String(filter.VPCID)
			}
		}

		output, err := albClient.DescribeLoadBalancers(input)
		if err != nil {
			return nil, fmt.Errorf("获取ALB列表失败: %w", err)
		}

		if len(output.LoadBalancers) == 0 {
			break
		}

		for _, lb := range output.LoadBalancers {
			instance := a.convertALBToLBInstance(lb, region)

			// 客户端过滤: AddressType
			if filter != nil && filter.AddressType != "" {
				if filter.AddressType != instance.AddressType {
					continue
				}
			}

			albInstances = append(albInstances, instance)
		}

		if len(output.LoadBalancers) < int(pageSize) {
			break
		}
		pageNumber++
	}

	// 补充每个 ALB 的监听器详情和后端服务器
	for i := range albInstances {
		listeners, serverGroupIDs, listenerCount := a.getALBListenerDetailsAndServerGroups(albClient, albInstances[i].LoadBalancerID)
		albInstances[i].ListenerCount = listenerCount
		albInstances[i].Listeners = listeners

		// 通过 ServerGroupId 获取后端服务器
		backendServers := a.getALBBackendServers(albClient, serverGroupIDs)
		albInstances[i].BackendServers = backendServers
		albInstances[i].BackendServerCount = len(backendServers)
	}

	return albInstances, nil
}

// getALBListenerDetailsAndServerGroups 获取 ALB 监听器详情和关联的 ServerGroupID
func (a *LBAdapter) getALBListenerDetailsAndServerGroups(client *alb.ALB, lbID string) ([]types.LBListener, []string, int) {
	var allListeners []types.LBListener
	serverGroupIDSet := make(map[string]bool)
	pageNumber := int64(1)
	pageSize := int64(100)
	totalCount := 0

	for {
		input := &alb.DescribeListenersInput{
			LoadBalancerId: volcengine.String(lbID),
			PageNumber:     &pageNumber,
			PageSize:       &pageSize,
		}

		output, err := client.DescribeListeners(input)
		if err != nil {
			a.logger.Warn("获取ALB监听器列表失败",
				elog.String("lb_id", lbID),
				elog.FieldErr(err))
			return nil, nil, 0
		}

		if output.TotalCount != nil {
			totalCount = int(*output.TotalCount)
		}

		if len(output.Listeners) == 0 {
			break
		}

		for _, l := range output.Listeners {
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
			if l.Status != nil {
				listener.Status = *l.Status
			}
			if l.Description != nil {
				listener.Description = *l.Description
			}
			allListeners = append(allListeners, listener)

			// 收集 ServerGroupId
			if l.ServerGroupId != nil && *l.ServerGroupId != "" {
				serverGroupIDSet[*l.ServerGroupId] = true
			}
		}

		if len(output.Listeners) < int(pageSize) {
			break
		}
		pageNumber++
	}

	var serverGroupIDs []string
	for sgID := range serverGroupIDSet {
		serverGroupIDs = append(serverGroupIDs, sgID)
	}

	return allListeners, serverGroupIDs, totalCount
}

// getALBBackendServers 通过 ServerGroupId 列表获取 ALB 后端服务器
func (a *LBAdapter) getALBBackendServers(client *alb.ALB, serverGroupIDs []string) []types.LBBackendServer {
	var allServers []types.LBBackendServer
	seen := make(map[string]bool)

	for _, sgID := range serverGroupIDs {
		input := &alb.DescribeServerGroupBackendServersInput{
			ServerGroupId: volcengine.String(sgID),
		}

		output, err := client.DescribeServerGroupBackendServers(input)
		if err != nil {
			a.logger.Warn("获取ALB后端服务器失败",
				elog.String("server_group_id", sgID),
				elog.FieldErr(err))
			continue
		}

		for _, s := range output.Servers {
			serverID := ""
			if s.ServerId != nil {
				serverID = *s.ServerId
			}
			if seen[serverID] {
				continue
			}
			seen[serverID] = true

			server := types.LBBackendServer{
				ServerID: serverID,
			}
			if s.InstanceId != nil {
				server.InstanceID = *s.InstanceId
			}
			if s.Ip != nil {
				server.IP = *s.Ip
			}
			if s.Port != nil {
				server.Port = int(*s.Port)
			}
			if s.Weight != nil {
				server.Weight = int(*s.Weight)
			}
			if s.Type != nil {
				server.Type = *s.Type
			}
			if s.Description != nil {
				server.Description = *s.Description
			}
			allServers = append(allServers, server)
		}
	}

	return allServers
}

// enrichCLBDetails 为CLB实例补充监听器和后端服务器详情
func (a *LBAdapter) enrichCLBDetails(client *clb.CLB, instances []types.LBInstance) {
	for i := range instances {
		if instances[i].LoadBalancerType != "clb" {
			continue
		}
		lbID := instances[i].LoadBalancerID

		// 获取监听器详情
		listeners := a.getCLBListenerDetails(client, lbID)
		instances[i].Listeners = listeners
		instances[i].ListenerCount = len(listeners)

		// 通过 DescribeLoadBalancerAttributes 获取 ServerGroup 列表，再查询后端服务器
		backendServers := a.getCLBBackendServers(client, lbID)
		instances[i].BackendServers = backendServers
		instances[i].BackendServerCount = len(backendServers)
	}
}

// getCLBListenerDetails 获取 CLB 监听器详情列表
func (a *LBAdapter) getCLBListenerDetails(client *clb.CLB, lbID string) []types.LBListener {
	var allListeners []types.LBListener
	pageNumber := int64(1)
	pageSize := int64(100)

	for {
		input := &clb.DescribeListenersInput{
			LoadBalancerId: volcengine.String(lbID),
			PageNumber:     &pageNumber,
			PageSize:       &pageSize,
		}

		output, err := client.DescribeListeners(input)
		if err != nil {
			a.logger.Warn("获取CLB监听器列表失败",
				elog.String("lb_id", lbID),
				elog.FieldErr(err))
			return nil
		}

		if len(output.Listeners) == 0 {
			break
		}

		for _, l := range output.Listeners {
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
			if l.Bandwidth != nil {
				listener.Bandwidth = int(*l.Bandwidth)
			}
			if l.Status != nil {
				listener.Status = *l.Status
			}
			if l.Description != nil {
				listener.Description = *l.Description
			}
			allListeners = append(allListeners, listener)
		}

		if len(output.Listeners) < int(pageSize) {
			break
		}
		pageNumber++
	}

	return allListeners
}

// getCLBBackendServers 获取 CLB 后端服务器列表 (通过 DescribeLoadBalancerAttributes 获取 ServerGroup，再查询后端)
func (a *LBAdapter) getCLBBackendServers(client *clb.CLB, lbID string) []types.LBBackendServer {
	// 先获取 LB 的 ServerGroup 列表
	attrInput := &clb.DescribeLoadBalancerAttributesInput{
		LoadBalancerId: volcengine.String(lbID),
	}
	attrOutput, err := client.DescribeLoadBalancerAttributes(attrInput)
	if err != nil {
		a.logger.Warn("获取CLB属性失败，跳过后端服务器查询",
			elog.String("lb_id", lbID),
			elog.FieldErr(err))
		return nil
	}

	if len(attrOutput.ServerGroups) == 0 {
		return nil
	}

	var allServers []types.LBBackendServer
	seen := make(map[string]struct{})

	for _, sg := range attrOutput.ServerGroups {
		if sg.ServerGroupId == nil {
			continue
		}
		sgID := *sg.ServerGroupId

		sgInput := &clb.DescribeServerGroupAttributesInput{
			ServerGroupId: volcengine.String(sgID),
		}
		sgOutput, err := client.DescribeServerGroupAttributes(sgInput)
		if err != nil {
			a.logger.Warn("获取CLB服务器组详情失败",
				elog.String("lb_id", lbID),
				elog.String("server_group_id", sgID),
				elog.FieldErr(err))
			continue
		}

		for _, s := range sgOutput.Servers {
			serverID := ""
			if s.ServerId != nil {
				serverID = *s.ServerId
			}
			// 去重: 同一后端可能出现在多个 ServerGroup 中
			if _, exists := seen[serverID]; exists {
				continue
			}
			seen[serverID] = struct{}{}

			server := types.LBBackendServer{
				ServerID: serverID,
			}
			if s.InstanceId != nil {
				server.InstanceID = *s.InstanceId
			}
			if s.Ip != nil {
				server.IP = *s.Ip
			}
			if s.Port != nil {
				server.Port = int(*s.Port)
			}
			if s.Weight != nil {
				server.Weight = int(*s.Weight)
			}
			if s.Type != nil {
				server.Type = *s.Type
			}
			if s.Description != nil {
				server.Description = *s.Description
			}
			allServers = append(allServers, server)
		}
	}

	return allServers
}

// convertToLBInstance 转换为通用LB实例
func (a *LBAdapter) convertToLBInstance(lb *clb.LoadBalancerForDescribeLoadBalancersOutput, region string) types.LBInstance {
	lbID := ""
	if lb.LoadBalancerId != nil {
		lbID = *lb.LoadBalancerId
	}

	lbName := ""
	if lb.LoadBalancerName != nil {
		lbName = *lb.LoadBalancerName
	}

	lbType := "clb"

	status := ""
	if lb.Status != nil {
		status = *lb.Status
	}

	address := ""
	if lb.EniAddress != nil {
		address = *lb.EniAddress
	}

	addressType := "intranet"
	if lb.Type != nil && *lb.Type == "public" {
		addressType = "internet"
	}

	vpcID := ""
	if lb.VpcId != nil {
		vpcID = *lb.VpcId
	}

	subnetID := ""
	if lb.SubnetId != nil {
		subnetID = *lb.SubnetId
	}

	createTime := ""
	if lb.CreateTime != nil {
		createTime = *lb.CreateTime
	}

	description := ""
	if lb.Description != nil {
		description = *lb.Description
	}

	// 提取标签
	tags := make(map[string]string)
	if lb.Tags != nil {
		for _, tag := range lb.Tags {
			if tag.Key != nil && tag.Value != nil {
				tags[*tag.Key] = *tag.Value
			}
		}
	}

	return types.LBInstance{
		LoadBalancerID:   lbID,
		LoadBalancerName: lbName,
		LoadBalancerType: lbType,
		Status:           types.NormalizeLBStatus("volcano", status),
		Region:           region,
		Address:          address,
		AddressType:      addressType,
		VPCID:            vpcID,
		VSwitchID:        subnetID,
		CreationTime:     createTime,
		Description:      description,
		Tags:             tags,
		Provider:         "volcano",
	}
}

// convertDetailToLBInstance 从详情接口转换为通用LB实例
func (a *LBAdapter) convertDetailToLBInstance(output *clb.DescribeLoadBalancerAttributesOutput, region string) types.LBInstance {
	lbID := ""
	if output.LoadBalancerId != nil {
		lbID = *output.LoadBalancerId
	}

	lbName := ""
	if output.LoadBalancerName != nil {
		lbName = *output.LoadBalancerName
	}

	lbType := "clb"

	status := ""
	if output.Status != nil {
		status = *output.Status
	}

	address := ""
	if output.EniAddress != nil {
		address = *output.EniAddress
	}

	addressType := "intranet"
	if output.Type != nil && *output.Type == "public" {
		addressType = "internet"
	}

	vpcID := ""
	if output.VpcId != nil {
		vpcID = *output.VpcId
	}

	subnetID := ""
	if output.SubnetId != nil {
		subnetID = *output.SubnetId
	}

	createTime := ""
	if output.CreateTime != nil {
		createTime = *output.CreateTime
	}

	description := ""
	if output.Description != nil {
		description = *output.Description
	}

	listenerCount := 0
	if output.Listeners != nil {
		listenerCount = len(output.Listeners)
	}

	// 提取监听器详情 (DescribeLoadBalancerAttributes 只返回 ListenerId 和 ListenerName)
	var listeners []types.LBListener
	if output.Listeners != nil {
		for _, l := range output.Listeners {
			listener := types.LBListener{}
			if l.ListenerId != nil {
				listener.ListenerID = *l.ListenerId
			}
			if l.ListenerName != nil {
				listener.Description = *l.ListenerName
			}
			listeners = append(listeners, listener)
		}
	}

	// 提取标签
	tags := make(map[string]string)
	if output.Tags != nil {
		for _, tag := range output.Tags {
			if tag.Key != nil && tag.Value != nil {
				tags[*tag.Key] = *tag.Value
			}
		}
	}

	return types.LBInstance{
		LoadBalancerID:   lbID,
		LoadBalancerName: lbName,
		LoadBalancerType: lbType,
		Status:           types.NormalizeLBStatus("volcano", status),
		Region:           region,
		Address:          address,
		AddressType:      addressType,
		VPCID:            vpcID,
		VSwitchID:        subnetID,
		ListenerCount:    listenerCount,
		Listeners:        listeners,
		CreationTime:     createTime,
		Description:      description,
		Tags:             tags,
		Provider:         "volcano",
	}
}

// convertALBToLBInstance 将ALB实例转换为通用LB实例
func (a *LBAdapter) convertALBToLBInstance(lb *alb.LoadBalancerForDescribeLoadBalancersOutput, region string) types.LBInstance {
	lbID := ""
	if lb.LoadBalancerId != nil {
		lbID = *lb.LoadBalancerId
	}

	lbName := ""
	if lb.LoadBalancerName != nil {
		lbName = *lb.LoadBalancerName
	}

	lbType := "alb"

	status := ""
	if lb.Status != nil {
		status = *lb.Status
	}

	address := ""
	if lb.EniAddress != nil {
		address = *lb.EniAddress
	}

	addressType := "intranet"
	if lb.Type != nil && *lb.Type == "public" {
		addressType = "internet"
	}

	addressIPVersion := ""
	if lb.AddressIpVersion != nil {
		addressIPVersion = *lb.AddressIpVersion
	}

	vpcID := ""
	if lb.VpcId != nil {
		vpcID = *lb.VpcId
	}

	subnetID := ""
	if lb.SubnetId != nil {
		subnetID = *lb.SubnetId
	}

	createTime := ""
	if lb.CreateTime != nil {
		createTime = *lb.CreateTime
	}

	description := ""
	if lb.Description != nil {
		description = *lb.Description
	}

	lbEdition := ""
	if lb.LoadBalancerEdition != nil {
		lbEdition = *lb.LoadBalancerEdition
	}

	// 提取标签
	tags := make(map[string]string)
	if lb.Tags != nil {
		for _, tag := range lb.Tags {
			if tag.Key != nil && tag.Value != nil {
				tags[*tag.Key] = *tag.Value
			}
		}
	}

	return types.LBInstance{
		LoadBalancerID:      lbID,
		LoadBalancerName:    lbName,
		LoadBalancerType:    lbType,
		LoadBalancerEdition: lbEdition,
		Status:              types.NormalizeLBStatus("volcano", status),
		Region:              region,
		Address:             address,
		AddressType:         addressType,
		AddressIPVersion:    addressIPVersion,
		VPCID:               vpcID,
		VSwitchID:           subnetID,
		CreationTime:        createTime,
		Description:         description,
		Tags:                tags,
		Provider:            "volcano",
	}
}
