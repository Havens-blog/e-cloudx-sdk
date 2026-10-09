package aws

import (
	"context"
	"fmt"

	awscommon "github.com/Havens-blog/e-cloudx-sdk/common/aws"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/gotomicro/ego/core/elog"
)

// ListGroups 获取用户组组列�?
func (a *Adapter) ListGroups(ctx context.Context, account *domain.CloudAccount) ([]*domain.UserGroup, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := awscommon.CreateIAMClient(account)
	if err != nil {
		return nil, err
	}

	var allGroups []*domain.UserGroup

	// 使用分页器获取所有用户组组
	paginator := iam.NewListGroupsPaginator(client, &iam.ListGroupsInput{
		MaxItems: aws.Int32(100),
	})

	for paginator.HasMorePages() {
		var page *iam.ListGroupsOutput
		err := a.retryWithBackoff(ctx, func() error {
			var e error
			page, e = paginator.NextPage(ctx)
			return e
		})

		if err != nil {
			a.logger.Error("list aws iam groups failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.FieldErr(err))
			return nil, fmt.Errorf("failed to list IAM groups: %w", err)
		}

		// 转换器用户组组数�?
		for _, iamGroup := range page.Groups {
			group := ConvertIAMGroupToUserGroup(iamGroup, account)

			// 获取用户组组的策略列表
			policies, err := a.listGroupPolicies(ctx, client, *iamGroup.GroupName)
			if err != nil {
				a.logger.Warn("failed to list group policies",
					elog.String("group_name", *iamGroup.GroupName),
					elog.FieldErr(err))
			} else {
				group.Policies = policies
			}

			allGroups = append(allGroups, group)
		}
	}

	a.logger.Info("list aws iam groups success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.Int("count", len(allGroups)))

	return allGroups, nil
}

// GetGroup 获取用户组组详�?
func (a *Adapter) GetGroup(ctx context.Context, account *domain.CloudAccount, groupID string) (*domain.UserGroup, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := awscommon.CreateIAMClient(account)
	if err != nil {
		return nil, err
	}

	var response *iam.GetGroupOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.GetGroup(ctx, &iam.GetGroupInput{
			GroupName: aws.String(groupID),
		})
		return e
	})

	if err != nil {
		a.logger.Error("get aws iam group failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("group_id", groupID),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to get IAM group: %w", err)
	}

	group := ConvertIAMGroupToUserGroup(*response.Group, account)

	// 获取用户组组的策略列表
	policies, err := a.listGroupPolicies(ctx, client, groupID)
	if err != nil {
		a.logger.Warn("failed to list group policies",
			elog.String("group_id", groupID),
			elog.FieldErr(err))
	} else {
		group.Policies = policies
	}

	// 设置成功员数量
	group.MemberCount = len(response.Users)

	a.logger.Info("get aws iam group success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID))

	return group, nil
}

// CreateGroup 创建用户组�?
func (a *Adapter) CreateGroup(ctx context.Context, account *domain.CloudAccount, req *types.CreateGroupRequest) (*domain.UserGroup, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := awscommon.CreateIAMClient(account)
	if err != nil {
		return nil, err
	}

	input := &iam.CreateGroupInput{
		GroupName: aws.String(req.GroupName),
		Path:      aws.String("/"), // AWS IAM 默认路径
	}

	var response *iam.CreateGroupOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.CreateGroup(ctx, input)
		return e
	})

	if err != nil {
		a.logger.Error("create aws iam group failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("group_name", req.GroupName),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to create IAM group: %w", err)
	}

	group := ConvertIAMGroupToUserGroup(*response.Group, account)

	a.logger.Info("create aws iam group success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_name", req.GroupName))

	return group, nil
}

// UpdateGroupPolicies 更新用户组组权限策�?
func (a *Adapter) UpdateGroupPolicies(ctx context.Context, account *domain.CloudAccount, groupID string, policies []domain.PermissionPolicy) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := awscommon.CreateIAMClient(account)
	if err != nil {
		return err
	}

	// 获取用户组组当前的策略列表
	currentPolicies, err := a.listGroupPolicies(ctx, client, groupID)
	if err != nil {
		a.logger.Error("list group policies failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("group_id", groupID),
			elog.FieldErr(err))
		return fmt.Errorf("failed to list group policies: %w", err)
	}

	// 构建当前策略映射
	currentPolicyMap := make(map[string]domain.PermissionPolicy)
	for _, policy := range currentPolicies {
		currentPolicyMap[policy.PolicyID] = policy
	}

	// 构建目标策略映射
	targetPolicyMap := make(map[string]domain.PermissionPolicy)
	for _, policy := range policies {
		if policy.Provider == domain.CloudProviderAWS {
			targetPolicyMap[policy.PolicyID] = policy
		}
	}

	// 分离需要附加和分离的策�?
	var toAttach []domain.PermissionPolicy
	var toDetach []domain.PermissionPolicy

	// 找出需要附加的策略
	for policyArn, policy := range targetPolicyMap {
		if _, exists := currentPolicyMap[policyArn]; !exists {
			toAttach = append(toAttach, policy)
		}
	}

	// 找出需要分离的策略
	for policyArn, policy := range currentPolicyMap {
		if _, exists := targetPolicyMap[policyArn]; !exists {
			toDetach = append(toDetach, policy)
		}
	}

	// 分离不需要的策略
	for _, policy := range toDetach {
		err = a.retryWithBackoff(ctx, func() error {
			_, e := client.DetachGroupPolicy(ctx, &iam.DetachGroupPolicyInput{
				GroupName: aws.String(groupID),
				PolicyArn: aws.String(policy.PolicyID),
			})
			return e
		})

		if err != nil {
			a.logger.Error("detach policy from group failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.String("group_id", groupID),
				elog.String("policy_arn", policy.PolicyID),
				elog.FieldErr(err))
			// 继续处理其他策略
		}
	}

	// 附加新策�?
	for _, policy := range toAttach {
		err = a.retryWithBackoff(ctx, func() error {
			_, e := client.AttachGroupPolicy(ctx, &iam.AttachGroupPolicyInput{
				GroupName: aws.String(groupID),
				PolicyArn: aws.String(policy.PolicyID),
			})
			return e
		})

		if err != nil {
			a.logger.Error("attach policy to group failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.String("group_id", groupID),
				elog.String("policy_arn", policy.PolicyID),
				elog.FieldErr(err))
			return fmt.Errorf("failed to attach policy %s: %w", policy.PolicyName, err)
		}
	}

	a.logger.Info("update group policies success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID),
		elog.Int("attached", len(toAttach)),
		elog.Int("detached", len(toDetach)))

	return nil
}

// DeleteGroup 删除用户组�?
func (a *Adapter) DeleteGroup(ctx context.Context, account *domain.CloudAccount, groupID string) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := awscommon.CreateIAMClient(account)
	if err != nil {
		return err
	}

	// 先获取用户组组成功员并移�?
	var getGroupResponse *iam.GetGroupOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		getGroupResponse, e = client.GetGroup(ctx, &iam.GetGroupInput{
			GroupName: aws.String(groupID),
		})
		return e
	})

	if err == nil && getGroupResponse != nil {
		for _, user := range getGroupResponse.Users {
			_ = a.retryWithBackoff(ctx, func() error {
				_, e := client.RemoveUserFromGroup(ctx, &iam.RemoveUserFromGroupInput{
					GroupName: aws.String(groupID),
					UserName:  user.UserName,
				})
				return e
			})
		}
	}

	// 分离所有策�?
	policies, err := a.listGroupPolicies(ctx, client, groupID)
	if err == nil {
		for _, policy := range policies {
			_ = a.retryWithBackoff(ctx, func() error {
				_, e := client.DetachGroupPolicy(ctx, &iam.DetachGroupPolicyInput{
					GroupName: aws.String(groupID),
					PolicyArn: aws.String(policy.PolicyID),
				})
				return e
			})
		}
	}

	// 删除用户组�?
	err = a.retryWithBackoff(ctx, func() error {
		_, e := client.DeleteGroup(ctx, &iam.DeleteGroupInput{
			GroupName: aws.String(groupID),
		})
		return e
	})

	if err != nil {
		a.logger.Error("delete aws iam group failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("group_id", groupID),
			elog.FieldErr(err))
		return fmt.Errorf("failed to delete IAM group: %w", err)
	}

	a.logger.Info("delete aws iam group success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID))

	return nil
}

// ListGroupUsers 获取用户组组成功员列�?
func (a *Adapter) ListGroupUsers(ctx context.Context, account *domain.CloudAccount, groupID string) ([]*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := awscommon.CreateIAMClient(account)
	if err != nil {
		return nil, err
	}

	var response *iam.GetGroupOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.GetGroup(ctx, &iam.GetGroupInput{
			GroupName: aws.String(groupID),
		})
		return e
	})

	if err != nil {
		a.logger.Error("list group users failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("group_id", groupID),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to list group users: %w", err)
	}

	var users []*domain.CloudUser
	for _, iamUser := range response.Users {
		user := ConvertIAMUserToCloudUser(iamUser, account)
		users = append(users, user)
	}

	a.logger.Info("list group users success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID),
		elog.Int("count", len(users)))

	return users, nil
}

// AddUserToGroup 将用户组添加到用户组�?
func (a *Adapter) AddUserToGroup(ctx context.Context, account *domain.CloudAccount, groupID string, userID string) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := awscommon.CreateIAMClient(account)
	if err != nil {
		return err
	}

	err = a.retryWithBackoff(ctx, func() error {
		_, e := client.AddUserToGroup(ctx, &iam.AddUserToGroupInput{
			GroupName: aws.String(groupID),
			UserName:  aws.String(userID),
		})
		return e
	})

	if err != nil {
		a.logger.Error("add user to group failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("group_id", groupID),
			elog.String("user_id", userID),
			elog.FieldErr(err))
		return fmt.Errorf("failed to add user to group: %w", err)
	}

	a.logger.Info("add user to group success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID),
		elog.String("user_id", userID))

	return nil
}

// RemoveUserFromGroup 将用户组从用户组组移�?
func (a *Adapter) RemoveUserFromGroup(ctx context.Context, account *domain.CloudAccount, groupID string, userID string) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := awscommon.CreateIAMClient(account)
	if err != nil {
		return err
	}

	err = a.retryWithBackoff(ctx, func() error {
		_, e := client.RemoveUserFromGroup(ctx, &iam.RemoveUserFromGroupInput{
			GroupName: aws.String(groupID),
			UserName:  aws.String(userID),
		})
		return e
	})

	if err != nil {
		a.logger.Error("remove user from group failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("group_id", groupID),
			elog.String("user_id", userID),
			elog.FieldErr(err))
		return fmt.Errorf("failed to remove user from group: %w", err)
	}

	a.logger.Info("remove user from group success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID),
		elog.String("user_id", userID))

	return nil
}

// GetPolicy 获取策略详情
func (a *Adapter) GetPolicy(ctx context.Context, account *domain.CloudAccount, policyID string) (*domain.PermissionPolicy, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := awscommon.CreateIAMClient(account)
	if err != nil {
		return nil, err
	}

	// 获取策略基本信息
	var getPolicyResponse *iam.GetPolicyOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		getPolicyResponse, e = client.GetPolicy(ctx, &iam.GetPolicyInput{
			PolicyArn: aws.String(policyID),
		})
		return e
	})

	if err != nil {
		a.logger.Error("get aws policy failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("policy_id", policyID),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to get policy: %w", err)
	}

	policy := &domain.PermissionPolicy{
		PolicyID:   *getPolicyResponse.Policy.Arn,
		PolicyName: *getPolicyResponse.Policy.PolicyName,
		Provider:   domain.CloudProviderAWS,
		PolicyType: ConvertPolicyScope(getPolicyResponse.Policy.Arn),
	}

	// 获取策略文档
	if getPolicyResponse.Policy.DefaultVersionId != nil {
		var getPolicyVersionResponse *iam.GetPolicyVersionOutput
		err = a.retryWithBackoff(ctx, func() error {
			var e error
			getPolicyVersionResponse, e = client.GetPolicyVersion(ctx, &iam.GetPolicyVersionInput{
				PolicyArn: aws.String(policyID),
				VersionId: getPolicyResponse.Policy.DefaultVersionId,
			})
			return e
		})

		if err == nil && getPolicyVersionResponse.PolicyVersion.Document != nil {
			policy.PolicyDocument = *getPolicyVersionResponse.PolicyVersion.Document
		}
	}

	a.logger.Info("get aws policy success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("policy_id", policyID))

	return policy, nil
}

// listGroupPolicies 获取用户组组的策略列表（内部方法）
func (a *Adapter) listGroupPolicies(ctx context.Context, client *iam.Client, groupName string) ([]domain.PermissionPolicy, error) {
	var allPolicies []domain.PermissionPolicy

	// 使用分页器获取所有附加的策略
	paginator := iam.NewListAttachedGroupPoliciesPaginator(client, &iam.ListAttachedGroupPoliciesInput{
		GroupName: aws.String(groupName),
		MaxItems:  aws.Int32(100),
	})

	for paginator.HasMorePages() {
		var page *iam.ListAttachedGroupPoliciesOutput
		err := a.retryWithBackoff(ctx, func() error {
			var e error
			page, e = paginator.NextPage(ctx)
			return e
		})

		if err != nil {
			return nil, err
		}

		for _, attachedPolicy := range page.AttachedPolicies {
			policy := domain.PermissionPolicy{
				PolicyID:       *attachedPolicy.PolicyArn,
				PolicyName:     *attachedPolicy.PolicyName,
				PolicyDocument: "", // 需要单独调�?GetPolicyVersion 获取
				Provider:       domain.CloudProviderAWS,
				PolicyType:     ConvertPolicyScope(attachedPolicy.PolicyArn),
			}
			allPolicies = append(allPolicies, policy)
		}
	}

	return allPolicies, nil
}
