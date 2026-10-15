package volcano

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	cloudxtypes "github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	iamSDK "github.com/volcengine/volcengine-go-sdk/service/iam"
)

// ListGroups 获取用户组列表(分页聚合,附带每个用户组的策略)。
func (a *Adapter) ListGroups(ctx context.Context, account *domain.CloudAccount) ([]*domain.UserGroup, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := a.getClient(account)
	if err != nil {
		return nil, err
	}

	var allGroups []*domain.UserGroup
	offset := int32(0)

	for {
		input := &iamSDK.ListGroupsInput{}
		input.SetLimit(iamPageLimit)
		input.SetOffset(offset)

		var output *iamSDK.ListGroupsOutput
		err := a.retryWithBackoff(ctx, func() error {
			var e error
			output, e = client.ListGroupsWithContext(ctx, input)
			return e
		})

		if err != nil {
			a.logger.Error("list volcano IAM groups failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.FieldErr(err))
			return nil, fmt.Errorf("failed to list volcano IAM groups: %w", err)
		}

		for _, g := range output.UserGroups {
			group := ConvertVolcanoListGroupToUserGroup(g, account)

			// 获取用户组挂载的策略列表
			policies, err := a.listGroupPolicies(ctx, client, deref(g.UserGroupName))
			if err != nil {
				a.logger.Warn("failed to list volcano IAM group policies",
					elog.String("group_name", deref(g.UserGroupName)),
					elog.FieldErr(err))
			} else {
				group.Policies = policies
			}

			allGroups = append(allGroups, group)
		}

		if !hasMorePages(output.Total, len(output.UserGroups), len(allGroups)) {
			break
		}
		offset += int32(len(output.UserGroups))
	}

	a.logger.Info("list volcano IAM groups success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.Int("count", len(allGroups)))

	return allGroups, nil
}

// GetGroup 获取用户组详情(附带策略与成员数)。
func (a *Adapter) GetGroup(ctx context.Context, account *domain.CloudAccount, groupID string) (*domain.UserGroup, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := a.getClient(account)
	if err != nil {
		return nil, err
	}

	input := &iamSDK.GetGroupInput{}
	input.SetUserGroupName(groupID)

	var output *iamSDK.GetGroupOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		output, e = client.GetGroupWithContext(ctx, input)
		return e
	})

	if err != nil {
		a.logger.Error("get volcano IAM group failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("group_id", groupID),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to get volcano IAM group %s: %w", groupID, err)
	}

	group := ConvertVolcanoGroupToUserGroup(output.UserGroup, account)

	// 获取用户组挂载的策略列表
	policies, err := a.listGroupPolicies(ctx, client, groupID)
	if err != nil {
		a.logger.Warn("failed to list volcano IAM group policies",
			elog.String("group_id", groupID),
			elog.FieldErr(err))
	} else {
		group.Policies = policies
	}

	// 获取用户组成员数
	memberInput := &iamSDK.ListUsersForGroupInput{}
	memberInput.SetUserGroupName(groupID)

	var memberOutput *iamSDK.ListUsersForGroupOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		memberOutput, e = client.ListUsersForGroupWithContext(ctx, memberInput)
		return e
	})

	if err == nil && memberOutput != nil {
		group.MemberCount = len(memberOutput.Users)
	}

	a.logger.Info("get volcano IAM group success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID))

	return group, nil
}

// CreateGroup 创建用户组。
func (a *Adapter) CreateGroup(ctx context.Context, account *domain.CloudAccount, req *cloudxtypes.CreateGroupRequest) (*domain.UserGroup, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := a.getClient(account)
	if err != nil {
		return nil, err
	}

	input := &iamSDK.CreateGroupInput{}
	input.SetUserGroupName(req.GroupName)
	if req.DisplayName != "" {
		input.SetDisplayName(req.DisplayName)
	}
	if req.Description != "" {
		input.SetDescription(req.Description)
	}

	var output *iamSDK.CreateGroupOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		output, e = client.CreateGroupWithContext(ctx, input)
		return e
	})

	if err != nil {
		a.logger.Error("create volcano IAM group failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("group_name", req.GroupName),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to create volcano IAM group %s: %w", req.GroupName, err)
	}

	group := ConvertVolcanoCreatedGroupToUserGroup(output.UserGroup, account)

	a.logger.Info("create volcano IAM group success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_name", req.GroupName))

	return group, nil
}

// UpdateGroupPolicies 更新用户组权限策略(差集附加/分离)。
func (a *Adapter) UpdateGroupPolicies(ctx context.Context, account *domain.CloudAccount, groupID string, policies []domain.PermissionPolicy) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := a.getClient(account)
	if err != nil {
		return err
	}

	current, err := a.listGroupPolicies(ctx, client, groupID)
	if err != nil {
		a.logger.Error("list volcano IAM group policies failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("group_id", groupID),
			elog.FieldErr(err))
		return fmt.Errorf("failed to list volcano IAM group policies: %w", err)
	}

	toAttach, toDetach := DiffPolicies(current, policies, domain.CloudProviderVolcano)

	// 分离不需要的策略(尽力而为,继续处理其他策略)
	for _, policy := range toDetach {
		detachInput := &iamSDK.DetachUserGroupPolicyInput{}
		detachInput.SetPolicyName(policy.PolicyName)
		detachInput.SetPolicyType(PolicyTypeToVolcano(policy.PolicyType))
		detachInput.SetUserGroupName(groupID)

		err = a.retryWithBackoff(ctx, func() error {
			_, e := client.DetachUserGroupPolicyWithContext(ctx, detachInput)
			return e
		})

		if err != nil {
			a.logger.Error("detach policy from volcano IAM group failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.String("group_id", groupID),
				elog.String("policy_name", policy.PolicyName),
				elog.FieldErr(err))
		}
	}

	// 附加新策略
	for _, policy := range toAttach {
		attachInput := &iamSDK.AttachUserGroupPolicyInput{}
		attachInput.SetPolicyName(policy.PolicyName)
		attachInput.SetPolicyType(PolicyTypeToVolcano(policy.PolicyType))
		attachInput.SetUserGroupName(groupID)

		err = a.retryWithBackoff(ctx, func() error {
			_, e := client.AttachUserGroupPolicyWithContext(ctx, attachInput)
			return e
		})

		if err != nil {
			a.logger.Error("attach policy to volcano IAM group failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.String("group_id", groupID),
				elog.String("policy_name", policy.PolicyName),
				elog.FieldErr(err))
			return fmt.Errorf("failed to attach policy %s: %w", policy.PolicyName, err)
		}
	}

	a.logger.Info("update volcano IAM group policies success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID),
		elog.Int("attached", len(toAttach)),
		elog.Int("detached", len(toDetach)))

	return nil
}

// DeleteGroup 删除用户组。
// 先尽力移除所有成员、分离所有策略,再删除用户组。
func (a *Adapter) DeleteGroup(ctx context.Context, account *domain.CloudAccount, groupID string) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := a.getClient(account)
	if err != nil {
		return err
	}

	// 移除所有成员
	memberInput := &iamSDK.ListUsersForGroupInput{}
	memberInput.SetUserGroupName(groupID)

	var memberOutput *iamSDK.ListUsersForGroupOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		memberOutput, e = client.ListUsersForGroupWithContext(ctx, memberInput)
		return e
	})

	if err == nil && memberOutput != nil {
		for _, u := range memberOutput.Users {
			removeInput := &iamSDK.RemoveUserFromGroupInput{}
			removeInput.SetUserGroupName(groupID)
			removeInput.SetUserName(deref(u.UserName))

			_ = a.retryWithBackoff(ctx, func() error {
				_, e := client.RemoveUserFromGroupWithContext(ctx, removeInput)
				return e
			})
		}
	}

	// 分离所有策略
	policies, err := a.listGroupPolicies(ctx, client, groupID)
	if err == nil {
		for _, policy := range policies {
			detachInput := &iamSDK.DetachUserGroupPolicyInput{}
			detachInput.SetPolicyName(policy.PolicyName)
			detachInput.SetPolicyType(PolicyTypeToVolcano(policy.PolicyType))
			detachInput.SetUserGroupName(groupID)

			_ = a.retryWithBackoff(ctx, func() error {
				_, e := client.DetachUserGroupPolicyWithContext(ctx, detachInput)
				return e
			})
		}
	}

	input := &iamSDK.DeleteGroupInput{}
	input.SetUserGroupName(groupID)

	err = a.retryWithBackoff(ctx, func() error {
		_, e := client.DeleteGroupWithContext(ctx, input)
		return e
	})

	if err != nil {
		a.logger.Error("delete volcano IAM group failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("group_id", groupID),
			elog.FieldErr(err))
		return fmt.Errorf("failed to delete volcano IAM group %s: %w", groupID, err)
	}

	a.logger.Info("delete volcano IAM group success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID))

	return nil
}

// ListGroupUsers 获取用户组成员列表。
func (a *Adapter) ListGroupUsers(ctx context.Context, account *domain.CloudAccount, groupID string) ([]*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := a.getClient(account)
	if err != nil {
		return nil, err
	}

	input := &iamSDK.ListUsersForGroupInput{}
	input.SetUserGroupName(groupID)

	var output *iamSDK.ListUsersForGroupOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		output, e = client.ListUsersForGroupWithContext(ctx, input)
		return e
	})

	if err != nil {
		a.logger.Error("list volcano IAM group users failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("group_id", groupID),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to list volcano IAM group users: %w", err)
	}

	users := make([]*domain.CloudUser, 0, len(output.Users))
	for _, u := range output.Users {
		if user := ConvertVolcanoGroupUserToCloudUser(u, account); user != nil {
			users = append(users, user)
		}
	}

	a.logger.Info("list volcano IAM group users success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID),
		elog.Int("count", len(users)))

	return users, nil
}

// AddUserToGroup 将用户添加到用户组。
func (a *Adapter) AddUserToGroup(ctx context.Context, account *domain.CloudAccount, groupID string, userID string) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := a.getClient(account)
	if err != nil {
		return err
	}

	input := &iamSDK.AddUserToGroupInput{}
	input.SetUserGroupName(groupID)
	input.SetUserName(userID)

	err = a.retryWithBackoff(ctx, func() error {
		_, e := client.AddUserToGroupWithContext(ctx, input)
		return e
	})

	if err != nil {
		a.logger.Error("add user to volcano IAM group failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("group_id", groupID),
			elog.String("user_id", userID),
			elog.FieldErr(err))
		return fmt.Errorf("failed to add user to volcano IAM group: %w", err)
	}

	a.logger.Info("add user to volcano IAM group success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID),
		elog.String("user_id", userID))

	return nil
}

// RemoveUserFromGroup 将用户从用户组移除。
func (a *Adapter) RemoveUserFromGroup(ctx context.Context, account *domain.CloudAccount, groupID string, userID string) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := a.getClient(account)
	if err != nil {
		return err
	}

	input := &iamSDK.RemoveUserFromGroupInput{}
	input.SetUserGroupName(groupID)
	input.SetUserName(userID)

	err = a.retryWithBackoff(ctx, func() error {
		_, e := client.RemoveUserFromGroupWithContext(ctx, input)
		return e
	})

	if err != nil {
		a.logger.Error("remove user from volcano IAM group failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("group_id", groupID),
			elog.String("user_id", userID),
			elog.FieldErr(err))
		return fmt.Errorf("failed to remove user from volcano IAM group: %w", err)
	}

	a.logger.Info("remove user from volcano IAM group success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID),
		elog.String("user_id", userID))

	return nil
}

// GetPolicy 获取策略详情:先查 System,不存在再回退 Custom。
func (a *Adapter) GetPolicy(ctx context.Context, account *domain.CloudAccount, policyID string) (*domain.PermissionPolicy, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := a.getClient(account)
	if err != nil {
		return nil, err
	}

	// 先尝试系统策略
	input := &iamSDK.GetPolicyInput{}
	input.SetPolicyName(policyID)
	input.SetPolicyType("System")

	var output *iamSDK.GetPolicyOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		output, e = client.GetPolicyWithContext(ctx, input)
		return e
	})

	if err != nil {
		// 系统策略不存在,尝试自定义策略
		input.SetPolicyType("Custom")
		err = a.retryWithBackoff(ctx, func() error {
			var e error
			output, e = client.GetPolicyWithContext(ctx, input)
			return e
		})

		if err != nil {
			a.logger.Error("get volcano IAM policy failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.String("policy_id", policyID),
				elog.FieldErr(err))
			return nil, fmt.Errorf("failed to get volcano IAM policy %s: %w", policyID, err)
		}
	}

	policy := ConvertVolcanoPolicyToPermissionPolicy(output.Policy)

	a.logger.Info("get volcano IAM policy success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("policy_id", policyID))

	return &policy, nil
}

// listGroupPolicies 获取用户组挂载的策略列表(内部方法)。
func (a *Adapter) listGroupPolicies(ctx context.Context, client iamAPI, groupName string) ([]domain.PermissionPolicy, error) {
	input := &iamSDK.ListAttachedUserGroupPoliciesInput{}
	input.SetUserGroupName(groupName)

	var output *iamSDK.ListAttachedUserGroupPoliciesOutput
	err := a.retryWithBackoff(ctx, func() error {
		var e error
		output, e = client.ListAttachedUserGroupPoliciesWithContext(ctx, input)
		return e
	})

	if err != nil {
		return nil, err
	}

	policies := make([]domain.PermissionPolicy, 0, len(output.AttachedPolicyMetadata))
	for _, p := range output.AttachedPolicyMetadata {
		policies = append(policies, domain.PermissionPolicy{
			PolicyID:   deref(p.PolicyName),
			PolicyName: deref(p.PolicyName),
			Provider:   domain.CloudProviderVolcano,
			PolicyType: ConvertPolicyType(deref(p.PolicyType)),
		})
	}

	return policies, nil
}
