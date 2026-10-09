package aliyun

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/gotomicro/ego/core/elog"
)

// SecurityGroupAdapter 阿里云安全组适配器
type SecurityGroupAdapter struct {
	client *Client
	logger *elog.Component
}

// NewSecurityGroupAdapter 创建阿里云安全组适配器
func NewSecurityGroupAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *SecurityGroupAdapter {
	return &SecurityGroupAdapter{
		client: NewClient(accessKeyID, accessKeySecret, defaultRegion, logger),
		logger: logger,
	}
}

// ListInstances 获取安全组列表
func (a *SecurityGroupAdapter) ListInstances(ctx context.Context, region string) ([]types.SecurityGroupInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个安全组详情 (包含规则)
func (a *SecurityGroupAdapter) GetInstance(ctx context.Context, region, securityGroupID string) (*types.SecurityGroupInstance, error) {
	instances, err := a.ListInstancesByIDs(ctx, region, []string{securityGroupID})
	if err != nil {
		return nil, err
	}
	if len(instances) == 0 {
		return nil, fmt.Errorf("安全组不存在: %s", securityGroupID)
	}

	// 获取规则详情
	sg := &instances[0]
	rules, err := a.GetSecurityGroupRules(ctx, region, securityGroupID)
	if err != nil {
		a.logger.Warn("获取安全组规则失败", elog.String("sg_id", securityGroupID), elog.FieldErr(err))
	} else {
		for _, rule := range rules {
			if rule.Direction == "ingress" {
				sg.IngressRules = append(sg.IngressRules, rule)
			} else {
				sg.EgressRules = append(sg.EgressRules, rule)
			}
		}
	}

	return sg, nil
}

// ListInstancesByIDs 批量获取安全组
func (a *SecurityGroupAdapter) ListInstancesByIDs(ctx context.Context, region string, securityGroupIDs []string) ([]types.SecurityGroupInstance, error) {
	if len(securityGroupIDs) == 0 {
		return nil, nil
	}

	filter := &types.SecurityGroupFilter{SecurityGroupIDs: securityGroupIDs}
	return a.ListInstancesWithFilter(ctx, region, filter)
}

// ListInstancesWithFilter 带过滤条件获取安全组列表
func (a *SecurityGroupAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.SecurityGroupFilter) ([]types.SecurityGroupInstance, error) {
	if err := a.client.WaitRateLimit(ctx); err != nil {
		return nil, err
	}

	ecsClient, err := a.client.GetECSClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.SecurityGroupInstance
	pageNumber := 1
	pageSize := 50

	if filter != nil && filter.PageSize > 0 {
		pageSize = filter.PageSize
	}
	if filter != nil && filter.PageNumber > 0 {
		pageNumber = filter.PageNumber
	}

	for {
		request := ecs.CreateDescribeSecurityGroupsRequest()
		request.Scheme = "https"
		request.RegionId = region
		request.PageNumber = requests.NewInteger(pageNumber)
		request.PageSize = requests.NewInteger(pageSize)

		// 应用过滤条件
		if filter != nil {
			if len(filter.SecurityGroupIDs) > 0 && len(filter.SecurityGroupIDs) == 1 {
				request.SecurityGroupId = filter.SecurityGroupIDs[0]
			}
			if filter.SecurityGroupName != "" {
				request.SecurityGroupName = filter.SecurityGroupName
			}
			if filter.VPCID != "" {
				request.VpcId = filter.VPCID
			}
			if filter.SecurityGroupType != "" {
				request.SecurityGroupType = filter.SecurityGroupType
			}
			if filter.ResourceGroupID != "" {
				request.ResourceGroupId = filter.ResourceGroupID
			}
			if len(filter.Tags) > 0 {
				var tags []ecs.DescribeSecurityGroupsTag
				for k, v := range filter.Tags {
					tags = append(tags, ecs.DescribeSecurityGroupsTag{Key: k, Value: v})
				}
				request.Tag = &tags
			}
		}

		var response *ecs.DescribeSecurityGroupsResponse
		err = a.client.RetryWithBackoff(ctx, func() error {
			var e error
			response, e = ecsClient.DescribeSecurityGroups(request)
			return e
		})

		if err != nil {
			return nil, fmt.Errorf("获取安全组列表失败: %w", err)
		}

		for _, sg := range response.SecurityGroups.SecurityGroup {
			instance := convertAliyunSecurityGroup(sg, region)
			allInstances = append(allInstances, instance)
		}

		// 如果指定了分页，只返回一页
		if filter != nil && filter.PageNumber > 0 {
			break
		}

		if len(response.SecurityGroups.SecurityGroup) < pageSize {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取阿里云安全组列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// GetSecurityGroupRules 获取安全组规则
func (a *SecurityGroupAdapter) GetSecurityGroupRules(ctx context.Context, region, securityGroupID string) ([]types.SecurityGroupRule, error) {
	if err := a.client.WaitRateLimit(ctx); err != nil {
		return nil, err
	}

	ecsClient, err := a.client.GetECSClient(region)
	if err != nil {
		return nil, err
	}

	request := ecs.CreateDescribeSecurityGroupAttributeRequest()
	request.Scheme = "https"
	request.RegionId = region
	request.SecurityGroupId = securityGroupID

	a.logger.Info("调用阿里云获取安全组规则",
		elog.String("region", region),
		elog.String("sg_id", securityGroupID))

	var response *ecs.DescribeSecurityGroupAttributeResponse
	err = a.client.RetryWithBackoff(ctx, func() error {
		var e error
		response, e = ecsClient.DescribeSecurityGroupAttribute(request)
		return e
	})

	if err != nil {
		return nil, fmt.Errorf("获取安全组规则失败: %w", err)
	}

	a.logger.Info("阿里云安全组规则响应",
		elog.String("sg_id", securityGroupID),
		elog.Int("permissions_count", len(response.Permissions.Permission)))

	var rules []types.SecurityGroupRule
	for _, perm := range response.Permissions.Permission {
		priority := 0
		if perm.Priority != "" {
			priority, _ = strconv.Atoi(perm.Priority)
		}
		rule := types.SecurityGroupRule{
			Direction:     perm.Direction,
			Protocol:      perm.IpProtocol,
			PortRange:     perm.PortRange,
			SourceCIDR:    perm.SourceCidrIp,
			DestCIDR:      perm.DestCidrIp,
			SourceGroupID: perm.SourceGroupId,
			DestGroupID:   perm.DestGroupId,
			Priority:      priority,
			Policy:        perm.Policy,
			Description:   perm.Description,
			CreationTime:  perm.CreateTime,
		}

		a.logger.Debug("解析安全组规则",
			elog.String("sg_id", securityGroupID),
			elog.String("direction", rule.Direction),
			elog.String("protocol", rule.Protocol),
			elog.String("port_range", rule.PortRange),
			elog.String("source_cidr", rule.SourceCIDR),
			elog.String("dest_cidr", rule.DestCIDR))

		rules = append(rules, rule)
	}

	a.logger.Info("阿里云安全组规则解析完成",
		elog.String("sg_id", securityGroupID),
		elog.Int("rules_count", len(rules)))

	return rules, nil
}

// ListByInstanceID 获取实例关联的安全组
func (a *SecurityGroupAdapter) ListByInstanceID(ctx context.Context, region, instanceID string) ([]types.SecurityGroupInstance, error) {
	if err := a.client.WaitRateLimit(ctx); err != nil {
		return nil, err
	}

	ecsClient, err := a.client.GetECSClient(region)
	if err != nil {
		return nil, err
	}

	// 先获取实例信息，拿到安全组ID列表
	request := ecs.CreateDescribeInstanceAttributeRequest()
	request.Scheme = "https"
	request.InstanceId = instanceID

	var response *ecs.DescribeInstanceAttributeResponse
	err = a.client.RetryWithBackoff(ctx, func() error {
		var e error
		response, e = ecsClient.DescribeInstanceAttribute(request)
		return e
	})

	if err != nil {
		return nil, fmt.Errorf("获取实例信息失败: %w", err)
	}

	if len(response.SecurityGroupIds.SecurityGroupId) == 0 {
		return nil, nil
	}

	// 批量获取安全组详情
	return a.ListInstancesByIDs(ctx, region, response.SecurityGroupIds.SecurityGroupId)
}

// convertAliyunSecurityGroup 转换阿里云安全组为通用格式
func convertAliyunSecurityGroup(sg ecs.SecurityGroup, region string) types.SecurityGroupInstance {
	tags := make(map[string]string)
	for _, tag := range sg.Tags.Tag {
		tags[tag.TagKey] = tag.TagValue
	}

	return types.SecurityGroupInstance{
		SecurityGroupID:   sg.SecurityGroupId,
		SecurityGroupName: sg.SecurityGroupName,
		Description:       sg.Description,
		SecurityGroupType: sg.SecurityGroupType,
		VPCID:             sg.VpcId,
		ResourceGroupID:   sg.ResourceGroupId,
		CreationTime:      sg.CreationTime,
		Region:            region,
		Tags:              tags,
		Provider:          string(types.ProviderAliyun),
	}
}
