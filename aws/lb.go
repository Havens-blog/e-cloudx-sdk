package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/gotomicro/ego/core/elog"
)

// LBAdapter AWS负载均衡适配器 (ELBv2)
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

// createClient 创建ELBv2客户端
func (a *LBAdapter) createClient(ctx context.Context, region string) (*elbv2.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			a.accessKeyID, a.accessKeySecret, "",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("加载AWS配置失败: %w", err)
	}
	return elbv2.NewFromConfig(cfg), nil
}

// ListInstances 获取负载均衡实例列表
func (a *LBAdapter) ListInstances(ctx context.Context, region string) ([]types.LBInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个负载均衡实例详情
func (a *LBAdapter) GetInstance(ctx context.Context, region, lbARN string) (*types.LBInstance, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &elbv2.DescribeLoadBalancersInput{
		LoadBalancerArns: []string{lbARN},
	}

	output, err := client.DescribeLoadBalancers(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("获取负载均衡详情失败: %w", err)
	}

	if len(output.LoadBalancers) == 0 {
		return nil, fmt.Errorf("负载均衡不存在: %s", lbARN)
	}

	instance := a.convertToLBInstance(output.LoadBalancers[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取负载均衡实例
func (a *LBAdapter) ListInstancesByIDs(ctx context.Context, region string, lbARNs []string) ([]types.LBInstance, error) {
	if len(lbARNs) == 0 {
		return nil, nil
	}

	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &elbv2.DescribeLoadBalancersInput{
		LoadBalancerArns: lbARNs,
	}

	output, err := client.DescribeLoadBalancers(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("批量获取负载均衡失败: %w", err)
	}

	var result []types.LBInstance
	for _, lb := range output.LoadBalancers {
		result = append(result, a.convertToLBInstance(lb, region))
	}

	return result, nil
}

// GetInstanceStatus 获取实例状态
func (a *LBAdapter) GetInstanceStatus(ctx context.Context, region, lbARN string) (string, error) {
	instance, err := a.GetInstance(ctx, region, lbARN)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取实例列表
func (a *LBAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.LBInstanceFilter) ([]types.LBInstance, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("创建ELBv2客户端失败: %w", err)
	}

	var allInstances []types.LBInstance
	var marker *string

	for {
		input := &elbv2.DescribeLoadBalancersInput{
			Marker: marker,
		}

		if filter != nil {
			if len(filter.LoadBalancerIDs) > 0 {
				input.LoadBalancerArns = filter.LoadBalancerIDs
			}
			if filter.LoadBalancerName != "" {
				input.Names = []string{filter.LoadBalancerName}
			}
			if filter.PageSize > 0 {
				pageSize := int32(filter.PageSize)
				input.PageSize = &pageSize
			}
		}

		output, err := client.DescribeLoadBalancers(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("获取负载均衡列表失败: %w", err)
		}

		for _, lb := range output.LoadBalancers {
			instance := a.convertToLBInstance(lb, region)

			// 客户端过滤: AddressType
			if filter != nil && filter.AddressType != "" {
				if filter.AddressType == "internet" && instance.AddressType != "internet" {
					continue
				}
				if filter.AddressType == "intranet" && instance.AddressType != "intranet" {
					continue
				}
			}

			// 客户端过滤: VPCID
			if filter != nil && filter.VPCID != "" && instance.VPCID != filter.VPCID {
				continue
			}

			allInstances = append(allInstances, instance)
		}

		if output.NextMarker == nil {
			break
		}
		marker = output.NextMarker
	}

	// 当LB数量不超过50时，获取监听器和后端服务器详情，避免过多API调用
	if len(allInstances) > 0 && len(allInstances) <= 50 {
		a.enrichLBDetails(ctx, client, allInstances)
	}

	a.logger.Info("获取AWS负载均衡列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// convertToLBInstance 转换为通用LB实例
func (a *LBAdapter) convertToLBInstance(lb elbv2types.LoadBalancer, region string) types.LBInstance {
	lbARN := aws.ToString(lb.LoadBalancerArn)
	lbName := aws.ToString(lb.LoadBalancerName)

	// AWS LB类型映射
	lbType := "slb"
	switch lb.Type {
	case elbv2types.LoadBalancerTypeEnumApplication:
		lbType = "alb"
	case elbv2types.LoadBalancerTypeEnumNetwork:
		lbType = "nlb"
	case elbv2types.LoadBalancerTypeEnumGateway:
		lbType = "slb"
	}

	// 状态映射
	status := ""
	if lb.State != nil {
		status = string(lb.State.Code)
	}

	// 地址类型
	addressType := "intranet"
	if lb.Scheme == elbv2types.LoadBalancerSchemeEnumInternetFacing {
		addressType = "internet"
	}

	// VPC ID
	vpcID := aws.ToString(lb.VpcId)

	// DNS名称作为地址
	address := aws.ToString(lb.DNSName)

	// IP版本
	addressIPVersion := "ipv4"
	if lb.IpAddressType == elbv2types.IpAddressTypeDualstack {
		addressIPVersion = "dualstack"
	}

	// 可用区
	zone := ""
	if len(lb.AvailabilityZones) > 0 {
		zone = aws.ToString(lb.AvailabilityZones[0].ZoneName)
	}

	// 创建时间
	createTime := ""
	if lb.CreatedTime != nil {
		createTime = lb.CreatedTime.Format("2006-01-02T15:04:05Z")
	}

	// 提取标签 (Name)
	tags := make(map[string]string)

	// 从ARN提取简短ID
	shortID := lbARN
	if parts := strings.Split(lbARN, "/"); len(parts) > 0 {
		shortID = parts[len(parts)-1]
	}
	_ = shortID

	return types.LBInstance{
		LoadBalancerID:   lbARN,
		LoadBalancerName: lbName,
		LoadBalancerType: lbType,
		Status:           types.NormalizeLBStatus("aws", status),
		Region:           region,
		Zone:             zone,
		Address:          address,
		AddressType:      addressType,
		AddressIPVersion: addressIPVersion,
		VPCID:            vpcID,
		CreationTime:     createTime,
		Tags:             tags,
		Provider:         "aws",
	}
}

// enrichLBDetails 为LB实例补充监听器和后端服务器详情
func (a *LBAdapter) enrichLBDetails(ctx context.Context, client *elbv2.Client, instances []types.LBInstance) {
	for i := range instances {
		lbARN := instances[i].LoadBalancerID

		// 获取监听器
		listeners, listenerCount := a.fetchListeners(ctx, client, lbARN)
		instances[i].Listeners = listeners
		instances[i].ListenerCount = listenerCount

		// 获取后端服务器详情 (通过目标组)
		backendServers, backendServerCount := a.fetchTargetGroupCount(ctx, client, lbARN)
		instances[i].BackendServers = backendServers
		instances[i].BackendServerCount = backendServerCount
	}
}

// fetchListeners 获取ELBv2监听器列表
func (a *LBAdapter) fetchListeners(ctx context.Context, client *elbv2.Client, lbARN string) ([]types.LBListener, int) {
	input := &elbv2.DescribeListenersInput{
		LoadBalancerArn: aws.String(lbARN),
	}

	output, err := client.DescribeListeners(ctx, input)
	if err != nil {
		a.logger.Warn("获取AWS监听器列表失败", elog.String("lb_arn", lbARN), elog.FieldErr(err))
		return nil, 0
	}

	var listeners []types.LBListener
	for _, l := range output.Listeners {
		listeners = append(listeners, types.LBListener{
			ListenerID:       aws.ToString(l.ListenerArn),
			ListenerPort:     int(aws.ToInt32(l.Port)),
			ListenerProtocol: string(l.Protocol),
		})
	}

	return listeners, len(listeners)
}

// fetchTargetGroupCount 获取LB关联的目标组，并查询后端目标详情
func (a *LBAdapter) fetchTargetGroupCount(ctx context.Context, client *elbv2.Client, lbARN string) ([]types.LBBackendServer, int) {
	input := &elbv2.DescribeTargetGroupsInput{
		LoadBalancerArn: aws.String(lbARN),
	}

	output, err := client.DescribeTargetGroups(ctx, input)
	if err != nil {
		a.logger.Warn("获取AWS目标组列表失败", elog.String("lb_arn", lbARN), elog.FieldErr(err))
		return nil, 0
	}

	var allServers []types.LBBackendServer
	seen := make(map[string]bool)

	for _, tg := range output.TargetGroups {
		if tg.TargetGroupArn == nil {
			continue
		}
		healthInput := &elbv2.DescribeTargetHealthInput{
			TargetGroupArn: tg.TargetGroupArn,
		}
		healthOutput, err := client.DescribeTargetHealth(ctx, healthInput)
		if err != nil {
			a.logger.Warn("获取AWS目标健康状态失败",
				elog.String("target_group", aws.ToString(tg.TargetGroupArn)),
				elog.FieldErr(err))
			continue
		}
		for _, thd := range healthOutput.TargetHealthDescriptions {
			if thd.Target == nil {
				continue
			}
			targetID := aws.ToString(thd.Target.Id)
			port := int(aws.ToInt32(thd.Target.Port))
			key := fmt.Sprintf("%s:%d", targetID, port)
			if seen[key] {
				continue
			}
			seen[key] = true

			status := ""
			if thd.TargetHealth != nil {
				status = string(thd.TargetHealth.State)
			}

			server := types.LBBackendServer{
				ServerID: targetID,
				IP:       targetID, // AWS target ID 通常就是 IP 或 instance-id
				Port:     port,
				Status:   status,
			}
			// 如果 target ID 以 "i-" 开头，是 EC2 实例 ID
			if strings.HasPrefix(targetID, "i-") {
				server.InstanceID = targetID
				server.IP = ""
				server.Type = "ecs"
			} else {
				server.Type = "ip"
			}
			allServers = append(allServers, server)
		}
	}

	return allServers, len(allServers)
}
