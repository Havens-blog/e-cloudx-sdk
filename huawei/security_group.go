package huawei

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	vpc "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/vpc/v2"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/vpc/v2/model"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/vpc/v2/region"
)

// SecurityGroupAdapter 华为云安全组适配器
type SecurityGroupAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewSecurityGroupAdapter 创建华为云安全组适配器
func NewSecurityGroupAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *SecurityGroupAdapter {
	return &SecurityGroupAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *SecurityGroupAdapter) getClient(regionID string) (*vpc.VpcClient, error) {
	if regionID == "" {
		regionID = a.defaultRegion
	}
	auth, err := basic.NewCredentialsBuilder().
		WithAk(a.accessKeyID).
		WithSk(a.accessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}

	reg, err := region.SafeValueOf(regionID)
	if err != nil {
		return nil, fmt.Errorf("无效的华为云地域: %s", regionID)
	}

	client, err := vpc.VpcClientBuilder().
		WithRegion(reg).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云VPC客户端失败: %w", err)
	}
	return vpc.NewVpcClient(client), nil
}

// ListInstances 获取安全组列表
func (a *SecurityGroupAdapter) ListInstances(ctx context.Context, region string) ([]types.SecurityGroupInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个安全组详情
func (a *SecurityGroupAdapter) GetInstance(ctx context.Context, region, securityGroupID string) (*types.SecurityGroupInstance, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	request := &model.ShowSecurityGroupRequest{SecurityGroupId: securityGroupID}
	response, err := client.ShowSecurityGroup(request)
	if err != nil {
		return nil, fmt.Errorf("获取安全组详情失败: %w", err)
	}

	instance := convertHuaweiSecurityGroupShow(response.SecurityGroup, region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取安全组
func (a *SecurityGroupAdapter) ListInstancesByIDs(ctx context.Context, region string, securityGroupIDs []string) ([]types.SecurityGroupInstance, error) {
	var instances []types.SecurityGroupInstance
	for _, id := range securityGroupIDs {
		inst, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取安全组失败", elog.String("sg_id", id), elog.FieldErr(err))
			continue
		}
		instances = append(instances, *inst)
	}
	return instances, nil
}

// ListInstancesWithFilter 带过滤条件获取安全组列表
func (a *SecurityGroupAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.SecurityGroupFilter) ([]types.SecurityGroupInstance, error) {
	client, err := a.getClient(region)
	if err != nil {
		return nil, err
	}

	var allInstances []types.SecurityGroupInstance
	marker := ""
	limit := int32(100)

	for {
		request := &model.ListSecurityGroupsRequest{
			Limit: &limit,
		}
		if marker != "" {
			request.Marker = &marker
		}
		if filter != nil && filter.VPCID != "" {
			request.VpcId = &filter.VPCID
		}

		response, err := client.ListSecurityGroups(request)
		if err != nil {
			return nil, fmt.Errorf("获取安全组列表失败: %w", err)
		}

		if response.SecurityGroups == nil || len(*response.SecurityGroups) == 0 {
			break
		}

		for _, sg := range *response.SecurityGroups {
			instance := convertHuaweiSecurityGroupList(sg, region)
			allInstances = append(allInstances, instance)
		}

		if len(*response.SecurityGroups) < int(limit) {
			break
		}
		// 获取最后一个安全组的ID作为marker
		sgs := *response.SecurityGroups
		marker = sgs[len(sgs)-1].Id
	}

	a.logger.Info("获取华为云安全组列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))

	return allInstances, nil
}

// GetSecurityGroupRules 获取安全组规则
func (a *SecurityGroupAdapter) GetSecurityGroupRules(ctx context.Context, region, securityGroupID string) ([]types.SecurityGroupRule, error) {
	sg, err := a.GetInstance(ctx, region, securityGroupID)
	if err != nil {
		return nil, err
	}
	var rules []types.SecurityGroupRule
	rules = append(rules, sg.IngressRules...)
	rules = append(rules, sg.EgressRules...)
	return rules, nil
}

// ListByInstanceID 获取实例关联的安全组
func (a *SecurityGroupAdapter) ListByInstanceID(ctx context.Context, region, instanceID string) ([]types.SecurityGroupInstance, error) {
	// 华为云需要通过ECS API获取实例的安全组，这里简化处理返回空
	return nil, nil
}

func convertHuaweiSecurityGroupShow(sg *model.SecurityGroup, region string) types.SecurityGroupInstance {
	if sg == nil {
		return types.SecurityGroupInstance{}
	}

	var ingressRules, egressRules []types.SecurityGroupRule
	if sg.SecurityGroupRules != nil {
		for _, rule := range sg.SecurityGroupRules {
			sgRule := convertHuaweiSecurityGroupRule(rule)
			if sgRule.Direction == "ingress" {
				ingressRules = append(ingressRules, sgRule)
			} else {
				egressRules = append(egressRules, sgRule)
			}
		}
	}

	desc := ""
	if sg.Description != nil {
		desc = *sg.Description
	}
	vpcId := ""
	if sg.VpcId != nil {
		vpcId = *sg.VpcId
	}

	return types.SecurityGroupInstance{
		SecurityGroupID:   sg.Id,
		SecurityGroupName: sg.Name,
		Description:       desc,
		VPCID:             vpcId,
		Region:            region,
		IngressRules:      ingressRules,
		EgressRules:       egressRules,
		IngressRuleCount:  len(ingressRules),
		EgressRuleCount:   len(egressRules),
		Tags:              make(map[string]string),
		Provider:          "huawei",
	}
}

func convertHuaweiSecurityGroupList(sg model.SecurityGroup, region string) types.SecurityGroupInstance {
	var ingressRules, egressRules []types.SecurityGroupRule
	if sg.SecurityGroupRules != nil {
		for _, rule := range sg.SecurityGroupRules {
			sgRule := convertHuaweiSecurityGroupRule(rule)
			if sgRule.Direction == "ingress" {
				ingressRules = append(ingressRules, sgRule)
			} else {
				egressRules = append(egressRules, sgRule)
			}
		}
	}

	desc := ""
	if sg.Description != nil {
		desc = *sg.Description
	}
	vpcId := ""
	if sg.VpcId != nil {
		vpcId = *sg.VpcId
	}

	return types.SecurityGroupInstance{
		SecurityGroupID:   sg.Id,
		SecurityGroupName: sg.Name,
		Description:       desc,
		VPCID:             vpcId,
		Region:            region,
		IngressRules:      ingressRules,
		EgressRules:       egressRules,
		IngressRuleCount:  len(ingressRules),
		EgressRuleCount:   len(egressRules),
		Tags:              make(map[string]string),
		Provider:          "huawei",
	}
}

func convertHuaweiSecurityGroupRule(rule model.SecurityGroupRule) types.SecurityGroupRule {
	portRange := "-1/-1"
	if rule.PortRangeMin != 0 || rule.PortRangeMax != 0 {
		portRange = fmt.Sprintf("%d/%d", rule.PortRangeMin, rule.PortRangeMax)
	}

	protocol := "all"
	if rule.Protocol != "" {
		protocol = rule.Protocol
	}

	return types.SecurityGroupRule{
		RuleID:        rule.Id,
		Direction:     rule.Direction,
		Protocol:      protocol,
		PortRange:     portRange,
		SourceCIDR:    rule.RemoteIpPrefix,
		SourceGroupID: rule.RemoteGroupId,
		Policy:        "accept",
		Description:   rule.Description,
	}
}
