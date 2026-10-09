package volcano

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/vpc"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// SecurityGroupAdapter 火山引擎安全组适配器
type SecurityGroupAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewSecurityGroupAdapter 创建火山引擎安全组适配器
func NewSecurityGroupAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *SecurityGroupAdapter {
	return &SecurityGroupAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *SecurityGroupAdapter) getClient(region string) (*vpc.VPC, error) {
	if region == "" {
		region = a.defaultRegion
	}
	config := volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(
			a.accessKeyID,
			a.accessKeySecret,
			"",
		)).
		WithRegion(region)

	sess, err := session.NewSession(config)
	if err != nil {
		return nil, fmt.Errorf("创建火山引擎会话失败: %w", err)
	}

	return vpc.New(sess), nil
}

// ListInstances 获取安全组列表
func (a *SecurityGroupAdapter) ListInstances(ctx context.Context, region string) ([]types.SecurityGroupInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个安全组详情
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
		sg.IngressRuleCount = len(sg.IngressRules)
		sg.EgressRuleCount = len(sg.EgressRules)
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
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.SecurityGroupInstance
	pageNumber := int64(1)
	pageSize := int64(100)

	for {
		input := &vpc.DescribeSecurityGroupsInput{
			PageNumber: &pageNumber,
			PageSize:   &pageSize,
		}

		if filter != nil {
			if len(filter.SecurityGroupIDs) > 0 {
				input.SecurityGroupIds = volcengine.StringSlice(filter.SecurityGroupIDs)
			}
			if filter.VPCID != "" {
				input.VpcId = &filter.VPCID
			}
		}

		result, err := client.DescribeSecurityGroups(input)
		if err != nil {
			return nil, fmt.Errorf("获取安全组列表失败: %w", err)
		}

		if result.SecurityGroups == nil || len(result.SecurityGroups) == 0 {
			break
		}

		for _, sg := range result.SecurityGroups {
			instance := convertVolcanoSecurityGroup(sg, region)
			allInstances = append(allInstances, instance)
		}

		if len(result.SecurityGroups) < int(pageSize) {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取火山引擎安全组列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// GetSecurityGroupRules 获取安全组规则
func (a *SecurityGroupAdapter) GetSecurityGroupRules(ctx context.Context, region, securityGroupID string) ([]types.SecurityGroupRule, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	input := &vpc.DescribeSecurityGroupAttributesInput{
		SecurityGroupId: &securityGroupID,
	}

	result, err := client.DescribeSecurityGroupAttributes(input)
	if err != nil {
		return nil, fmt.Errorf("获取安全组规则失败: %w", err)
	}

	var rules []types.SecurityGroupRule
	if result.Permissions != nil {
		for _, perm := range result.Permissions {
			rule := convertVolcanoSecurityGroupRule(perm)
			rules = append(rules, rule)
		}
	}

	return rules, nil
}

// ListByInstanceID 获取实例关联的安全组
func (a *SecurityGroupAdapter) ListByInstanceID(ctx context.Context, region, instanceID string) ([]types.SecurityGroupInstance, error) {
	// 火山引擎需要通过ECS API获取实例的安全组，这里简化处理返回空
	return nil, nil
}

func convertVolcanoSecurityGroup(sg *vpc.SecurityGroupForDescribeSecurityGroupsOutput, region string) types.SecurityGroupInstance {
	tags := make(map[string]string)
	if sg.Tags != nil {
		for _, tag := range sg.Tags {
			if tag.Key != nil && tag.Value != nil {
				tags[*tag.Key] = *tag.Value
			}
		}
	}

	return types.SecurityGroupInstance{
		SecurityGroupID:   volcengine.StringValue(sg.SecurityGroupId),
		SecurityGroupName: volcengine.StringValue(sg.SecurityGroupName),
		Description:       volcengine.StringValue(sg.Description),
		VPCID:             volcengine.StringValue(sg.VpcId),
		Region:            region,
		CreationTime:      volcengine.StringValue(sg.CreationTime),
		Tags:              tags,
		Provider:          "volcano",
	}
}

func convertVolcanoSecurityGroupRule(perm *vpc.PermissionForDescribeSecurityGroupAttributesOutput) types.SecurityGroupRule {
	protocol := "all"
	if perm.Protocol != nil {
		protocol = *perm.Protocol
	}

	portRange := "-1/-1"
	if perm.PortStart != nil && perm.PortEnd != nil {
		portRange = fmt.Sprintf("%d/%d", *perm.PortStart, *perm.PortEnd)
	}

	direction := "ingress"
	if perm.Direction != nil {
		direction = *perm.Direction
	}

	policy := "accept"
	if perm.Policy != nil {
		policy = *perm.Policy
	}

	priority := 0
	if perm.Priority != nil {
		priority = int(*perm.Priority)
	}

	rule := types.SecurityGroupRule{
		Direction:   direction,
		Protocol:    protocol,
		PortRange:   portRange,
		Policy:      policy,
		Description: volcengine.StringValue(perm.Description),
		Priority:    priority,
	}

	if direction == "ingress" {
		if perm.CidrIp != nil {
			rule.SourceCIDR = *perm.CidrIp
		}
		if perm.SourceGroupId != nil {
			rule.SourceGroupID = *perm.SourceGroupId
		}
	} else {
		if perm.CidrIp != nil {
			rule.DestCIDR = *perm.CidrIp
		}
		if perm.SourceGroupId != nil {
			rule.DestGroupID = *perm.SourceGroupId
		}
	}

	return rule
}
