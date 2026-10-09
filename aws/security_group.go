package aws

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/gotomicro/ego/core/elog"
)

// SecurityGroupAdapter AWS安全组适配器
type SecurityGroupAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewSecurityGroupAdapter 创建AWS安全组适配器
func NewSecurityGroupAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *SecurityGroupAdapter {
	return &SecurityGroupAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *SecurityGroupAdapter) getClient(ctx context.Context, region string) (*ec2.Client, error) {
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
	return ec2.NewFromConfig(cfg), nil
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
	return &instances[0], nil
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
	client, err := a.getClient(ctx, region)
	if err != nil {
		return nil, err
	}

	input := &ec2.DescribeSecurityGroupsInput{}

	if filter != nil {
		if len(filter.SecurityGroupIDs) > 0 {
			input.GroupIds = filter.SecurityGroupIDs
		}
		var filters []ec2types.Filter
		if filter.VPCID != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("vpc-id"),
				Values: []string{filter.VPCID},
			})
		}
		if filter.SecurityGroupName != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("group-name"),
				Values: []string{filter.SecurityGroupName},
			})
		}
		if len(filters) > 0 {
			input.Filters = filters
		}
	}

	var allInstances []types.SecurityGroupInstance
	paginator := ec2.NewDescribeSecurityGroupsPaginator(client, input)

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("获取安全组列表失败: %w", err)
		}
		for _, sg := range output.SecurityGroups {
			instance := convertAWSSecurityGroup(sg, region)
			allInstances = append(allInstances, instance)
		}
	}

	a.logger.Info("获取AWS安全组列表成功",
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
	client, err := a.getClient(ctx, region)
	if err != nil {
		return nil, err
	}

	// 获取实例信息
	descInput := &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	}
	output, err := client.DescribeInstances(ctx, descInput)
	if err != nil {
		return nil, fmt.Errorf("获取实例信息失败: %w", err)
	}

	var sgIDs []string
	for _, res := range output.Reservations {
		for _, inst := range res.Instances {
			for _, sg := range inst.SecurityGroups {
				if sg.GroupId != nil {
					sgIDs = append(sgIDs, *sg.GroupId)
				}
			}
		}
	}

	if len(sgIDs) == 0 {
		return nil, nil
	}

	return a.ListInstancesByIDs(ctx, region, sgIDs)
}

func convertAWSSecurityGroup(sg ec2types.SecurityGroup, region string) types.SecurityGroupInstance {
	tags := make(map[string]string)
	for _, tag := range sg.Tags {
		if tag.Key != nil && tag.Value != nil {
			tags[*tag.Key] = *tag.Value
		}
	}

	var ingressRules []types.SecurityGroupRule
	for _, perm := range sg.IpPermissions {
		rules := convertAWSPermission(perm, "ingress")
		ingressRules = append(ingressRules, rules...)
	}

	var egressRules []types.SecurityGroupRule
	for _, perm := range sg.IpPermissionsEgress {
		rules := convertAWSPermission(perm, "egress")
		egressRules = append(egressRules, rules...)
	}

	return types.SecurityGroupInstance{
		SecurityGroupID:   aws.ToString(sg.GroupId),
		SecurityGroupName: aws.ToString(sg.GroupName),
		Description:       aws.ToString(sg.Description),
		VPCID:             aws.ToString(sg.VpcId),
		Region:            region,
		IngressRules:      ingressRules,
		EgressRules:       egressRules,
		IngressRuleCount:  len(ingressRules),
		EgressRuleCount:   len(egressRules),
		Tags:              tags,
		Provider:          "aws",
	}
}

func convertAWSPermission(perm ec2types.IpPermission, direction string) []types.SecurityGroupRule {
	var rules []types.SecurityGroupRule

	protocol := aws.ToString(perm.IpProtocol)
	if protocol == "-1" {
		protocol = "all"
	}

	portRange := "-1/-1"
	if perm.FromPort != nil && perm.ToPort != nil {
		portRange = strconv.Itoa(int(*perm.FromPort)) + "/" + strconv.Itoa(int(*perm.ToPort))
	}

	// IP范围规则
	for _, ipRange := range perm.IpRanges {
		rule := types.SecurityGroupRule{
			Direction:   direction,
			Protocol:    protocol,
			PortRange:   portRange,
			Policy:      "accept",
			Description: aws.ToString(ipRange.Description),
		}
		if direction == "ingress" {
			rule.SourceCIDR = aws.ToString(ipRange.CidrIp)
		} else {
			rule.DestCIDR = aws.ToString(ipRange.CidrIp)
		}
		rules = append(rules, rule)
	}

	// IPv6范围规则
	for _, ipv6Range := range perm.Ipv6Ranges {
		rule := types.SecurityGroupRule{
			Direction:   direction,
			Protocol:    protocol,
			PortRange:   portRange,
			Policy:      "accept",
			Description: aws.ToString(ipv6Range.Description),
		}
		if direction == "ingress" {
			rule.SourceCIDR = aws.ToString(ipv6Range.CidrIpv6)
		} else {
			rule.DestCIDR = aws.ToString(ipv6Range.CidrIpv6)
		}
		rules = append(rules, rule)
	}

	// 安全组引用规则
	for _, sgRef := range perm.UserIdGroupPairs {
		rule := types.SecurityGroupRule{
			Direction:   direction,
			Protocol:    protocol,
			PortRange:   portRange,
			Policy:      "accept",
			Description: aws.ToString(sgRef.Description),
		}
		if direction == "ingress" {
			rule.SourceGroupID = aws.ToString(sgRef.GroupId)
		} else {
			rule.DestGroupID = aws.ToString(sgRef.GroupId)
		}
		rules = append(rules, rule)
	}

	return rules
}
