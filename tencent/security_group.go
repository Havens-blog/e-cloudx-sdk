package tencent

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	vpc "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/vpc/v20170312"
)

// SecurityGroupAdapter 腾讯云安全组适配器
type SecurityGroupAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewSecurityGroupAdapter 创建腾讯云安全组适配器
func NewSecurityGroupAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *SecurityGroupAdapter {
	return &SecurityGroupAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *SecurityGroupAdapter) getClient(region string) (*vpc.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}
	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "vpc.tencentcloudapi.com"

	client, err := vpc.NewClient(credential, region, cpf)
	if err != nil {
		return nil, fmt.Errorf("创建腾讯云VPC客户端失败: %w", err)
	}
	return client, nil
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
	offset := "0"
	limit := "100"

	for {
		request := vpc.NewDescribeSecurityGroupsRequest()
		request.Offset = &offset
		request.Limit = &limit

		if filter != nil {
			if len(filter.SecurityGroupIDs) > 0 {
				request.SecurityGroupIds = common.StringPtrs(filter.SecurityGroupIDs)
			}
			var filters []*vpc.Filter
			if filter.SecurityGroupName != "" {
				filters = append(filters, &vpc.Filter{
					Name:   common.StringPtr("security-group-name"),
					Values: common.StringPtrs([]string{filter.SecurityGroupName}),
				})
			}
			if len(filters) > 0 {
				request.Filters = filters
			}
		}

		response, err := client.DescribeSecurityGroups(request)
		if err != nil {
			return nil, fmt.Errorf("获取安全组列表失败: %w", err)
		}

		if response.Response.SecurityGroupSet == nil || len(response.Response.SecurityGroupSet) == 0 {
			break
		}

		for _, sg := range response.Response.SecurityGroupSet {
			instance := convertTencentSecurityGroup(sg, region)
			allInstances = append(allInstances, instance)
		}

		if len(response.Response.SecurityGroupSet) < 100 {
			break
		}
		offsetInt := 0
		fmt.Sscanf(offset, "%d", &offsetInt)
		offset = fmt.Sprintf("%d", offsetInt+100)
	}

	a.logger.Info("获取腾讯云安全组列表成功",
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

	request := vpc.NewDescribeSecurityGroupPoliciesRequest()
	request.SecurityGroupId = &securityGroupID

	response, err := client.DescribeSecurityGroupPolicies(request)
	if err != nil {
		return nil, fmt.Errorf("获取安全组规则失败: %w", err)
	}

	var rules []types.SecurityGroupRule

	if response.Response.SecurityGroupPolicySet != nil {
		// 入方向规则
		if response.Response.SecurityGroupPolicySet.Ingress != nil {
			for _, policy := range response.Response.SecurityGroupPolicySet.Ingress {
				rule := convertTencentSecurityGroupPolicy(policy, "ingress")
				rules = append(rules, rule)
			}
		}
		// 出方向规则
		if response.Response.SecurityGroupPolicySet.Egress != nil {
			for _, policy := range response.Response.SecurityGroupPolicySet.Egress {
				rule := convertTencentSecurityGroupPolicy(policy, "egress")
				rules = append(rules, rule)
			}
		}
	}

	return rules, nil
}

// ListByInstanceID 获取实例关联的安全组
func (a *SecurityGroupAdapter) ListByInstanceID(ctx context.Context, region, instanceID string) ([]types.SecurityGroupInstance, error) {
	// 腾讯云需要通过CVM API获取实例的安全组，这里简化处理返回空
	return nil, nil
}

func convertTencentSecurityGroup(sg *vpc.SecurityGroup, region string) types.SecurityGroupInstance {
	tags := make(map[string]string)
	if sg.TagSet != nil {
		for _, tag := range sg.TagSet {
			if tag.Key != nil && tag.Value != nil {
				tags[*tag.Key] = *tag.Value
			}
		}
	}

	return types.SecurityGroupInstance{
		SecurityGroupID:   safeStringPtr(sg.SecurityGroupId),
		SecurityGroupName: safeStringPtr(sg.SecurityGroupName),
		Description:       safeStringPtr(sg.SecurityGroupDesc),
		Region:            region,
		CreationTime:      safeStringPtr(sg.CreatedTime),
		Tags:              tags,
		Provider:          "tencent",
	}
}

func convertTencentSecurityGroupPolicy(policy *vpc.SecurityGroupPolicy, direction string) types.SecurityGroupRule {
	protocol := "all"
	if policy.Protocol != nil {
		protocol = *policy.Protocol
	}

	portRange := "-1/-1"
	if policy.Port != nil && *policy.Port != "" {
		portRange = *policy.Port
	}

	policy_action := "accept"
	if policy.Action != nil {
		if *policy.Action == "DROP" {
			policy_action = "drop"
		}
	}

	rule := types.SecurityGroupRule{
		Direction:   direction,
		Protocol:    protocol,
		PortRange:   portRange,
		Policy:      policy_action,
		Description: safeStringPtr(policy.PolicyDescription),
	}

	if direction == "ingress" {
		if policy.CidrBlock != nil {
			rule.SourceCIDR = *policy.CidrBlock
		}
		if policy.SecurityGroupId != nil {
			rule.SourceGroupID = *policy.SecurityGroupId
		}
	} else {
		if policy.CidrBlock != nil {
			rule.DestCIDR = *policy.CidrBlock
		}
		if policy.SecurityGroupId != nil {
			rule.DestGroupID = *policy.SecurityGroupId
		}
	}

	return rule
}
