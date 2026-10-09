package aliyun

import (
	"context"
	"fmt"

	aliyuncommon "github.com/Havens-blog/e-cloudx-sdk/common/aliyun"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ram"
	"github.com/gotomicro/ego/core/elog"
)

// ListGroups 获取用户组组列�?
func (a *Adapter) ListGroups(ctx context.Context, account *domain.CloudAccount) ([]*domain.UserGroup, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := aliyuncommon.CreateRAMClient(account)
	if err != nil {
		return nil, err
	}

	var allGroups []*domain.UserGroup
	marker := ""

	// 分页获取所有用户组组
	for {
		request := ram.CreateListGroupsRequest()
		request.Scheme = "https"
		request.MaxItems = "100" // 每页最�?00�?
		if marker != "" {
			request.Marker = marker
		}

		var response *ram.ListGroupsResponse
		err := a.retryWithBackoff(ctx, func() error {
			var e error
			response, e = client.ListGroups(request)
			return e
		})

		if err != nil {
			a.logger.Error("list aliyun ram groups failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.FieldErr(err))
			return nil, fmt.Errorf("failed to list RAM groups: %w", err)
		}

		// 转换器用户组组数�?
		for _, ramGroup := range response.Groups.Group {
			group := ConvertRAMGroupToUserGroup(ramGroup, account)

			// 获取用户组组的策略列表
			policies, err := a.listGroupPolicies(ctx, client, ramGroup.GroupName)
			if err != nil {
				a.logger.Warn("failed to list group policies",
					elog.String("group_name", ramGroup.GroupName),
					elog.FieldErr(err))
			} else {
				group.Policies = policies
			}

			allGroups = append(allGroups, group)
		}

		// 检查是否还有更多数�?
		if !response.IsTruncated {
			break
		}
		marker = response.Marker
	}

	a.logger.Info("list aliyun ram groups success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.Int("count", len(allGroups)))

	return allGroups, nil
}

// GetGroup 获取用户组组详�?
func (a *Adapter) GetGroup(ctx context.Context, account *domain.CloudAccount, groupID string) (*domain.UserGroup, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := aliyuncommon.CreateRAMClient(account)
	if err != nil {
		return nil, err
	}

	request := ram.CreateGetGroupRequest()
	request.Scheme = "https"
	request.GroupName = groupID

	var response *ram.GetGroupResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.GetGroup(request)
		return e
	})

	if err != nil {
		a.logger.Error("get aliyun ram group failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("group_id", groupID),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to get RAM group: %w", err)
	}

	group := ConvertRAMGroupToUserGroup(response.Group, account)

	// 获取用户组组的策略列表
	policies, err := a.listGroupPolicies(ctx, client, groupID)
	if err != nil {
		a.logger.Warn("failed to list group policies",
			elog.String("group_id", groupID),
			elog.FieldErr(err))
	} else {
		group.Policies = policies
	}

	// 获取用户组组成功员数�?
	usersRequest := ram.CreateListUsersForGroupRequest()
	usersRequest.Scheme = "https"
	usersRequest.GroupName = groupID

	var usersResponse *ram.ListUsersForGroupResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		usersResponse, e = client.ListUsersForGroup(usersRequest)
		return e
	})

	if err == nil && usersResponse != nil {
		group.MemberCount = len(usersResponse.Users.User)
	}

	a.logger.Info("get aliyun ram group success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID))

	return group, nil
}

// CreateGroup 创建用户组�?
func (a *Adapter) CreateGroup(ctx context.Context, account *domain.CloudAccount, req *types.CreateGroupRequest) (*domain.UserGroup, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := aliyuncommon.CreateRAMClient(account)
	if err != nil {
		return nil, err
	}

	request := ram.CreateCreateGroupRequest()
	request.Scheme = "https"
	request.GroupName = req.GroupName
	if req.Description != "" {
		request.Comments = req.Description
	}

	var response *ram.CreateGroupResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.CreateGroup(request)
		return e
	})

	if err != nil {
		a.logger.Error("create aliyun ram group failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("group_name", req.GroupName),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to create RAM group: %w", err)
	}

	group := ConvertRAMGroupToUserGroup(response.Group, account)

	a.logger.Info("create aliyun ram group success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_name", req.GroupName))

	return group, nil
}

// UpdateGroupPolicies 更新用户组组权限策�?
func (a *Adapter) UpdateGroupPolicies(ctx context.Context, account *domain.CloudAccount, groupID string, policies []domain.PermissionPolicy) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := aliyuncommon.CreateRAMClient(account)
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
		currentPolicyMap[policy.PolicyName] = policy
	}

	// 构建目标策略映射
	targetPolicyMap := make(map[string]domain.PermissionPolicy)
	for _, policy := range policies {
		if policy.Provider == domain.CloudProviderAliyun {
			targetPolicyMap[policy.PolicyName] = policy
		}
	}

	// 分离需要附加和分离的策�?
	var toAttach []domain.PermissionPolicy
	var toDetach []domain.PermissionPolicy

	// 找出需要附加的策略
	for policyName, policy := range targetPolicyMap {
		if _, exists := currentPolicyMap[policyName]; !exists {
			toAttach = append(toAttach, policy)
		}
	}

	// 找出需要分离的策略
	for policyName, policy := range currentPolicyMap {
		if _, exists := targetPolicyMap[policyName]; !exists {
			toDetach = append(toDetach, policy)
		}
	}

	// 分离不需要的策略
	for _, policy := range toDetach {
		detachRequest := ram.CreateDetachPolicyFromGroupRequest()
		detachRequest.Scheme = "https"
		detachRequest.PolicyName = policy.PolicyName
		detachRequest.PolicyType = string(policy.PolicyType)
		detachRequest.GroupName = groupID

		err = a.retryWithBackoff(ctx, func() error {
			_, e := client.DetachPolicyFromGroup(detachRequest)
			return e
		})

		if err != nil {
			a.logger.Error("detach policy from group failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.String("group_id", groupID),
				elog.String("policy_name", policy.PolicyName),
				elog.FieldErr(err))
			// 继续处理其他策略
		}
	}

	// 附加新策�?
	for _, policy := range toAttach {
		attachRequest := ram.CreateAttachPolicyToGroupRequest()
		attachRequest.Scheme = "https"
		attachRequest.PolicyName = policy.PolicyName
		attachRequest.PolicyType = string(policy.PolicyType)
		attachRequest.GroupName = groupID

		err = a.retryWithBackoff(ctx, func() error {
			_, e := client.AttachPolicyToGroup(attachRequest)
			return e
		})

		if err != nil {
			a.logger.Error("attach policy to group failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.String("group_id", groupID),
				elog.String("policy_name", policy.PolicyName),
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

	client, err := aliyuncommon.CreateRAMClient(account)
	if err != nil {
		return err
	}

	// 先移除所有用户组组成功员
	usersRequest := ram.CreateListUsersForGroupRequest()
	usersRequest.Scheme = "https"
	usersRequest.GroupName = groupID

	var usersResponse *ram.ListUsersForGroupResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		usersResponse, e = client.ListUsersForGroup(usersRequest)
		return e
	})

	if err == nil && usersResponse != nil {
		for _, user := range usersResponse.Users.User {
			removeRequest := ram.CreateRemoveUserFromGroupRequest()
			removeRequest.Scheme = "https"
			removeRequest.UserName = user.UserName
			removeRequest.GroupName = groupID

			_ = a.retryWithBackoff(ctx, func() error {
				_, e := client.RemoveUserFromGroup(removeRequest)
				return e
			})
		}
	}

	// 分离所有策�?
	policies, err := a.listGroupPolicies(ctx, client, groupID)
	if err == nil {
		for _, policy := range policies {
			detachRequest := ram.CreateDetachPolicyFromGroupRequest()
			detachRequest.Scheme = "https"
			detachRequest.PolicyName = policy.PolicyName
			detachRequest.PolicyType = string(policy.PolicyType)
			detachRequest.GroupName = groupID

			_ = a.retryWithBackoff(ctx, func() error {
				_, e := client.DetachPolicyFromGroup(detachRequest)
				return e
			})
		}
	}

	// 删除用户组�?
	request := ram.CreateDeleteGroupRequest()
	request.Scheme = "https"
	request.GroupName = groupID

	err = a.retryWithBackoff(ctx, func() error {
		_, e := client.DeleteGroup(request)
		return e
	})

	if err != nil {
		a.logger.Error("delete aliyun ram group failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("group_id", groupID),
			elog.FieldErr(err))
		return fmt.Errorf("failed to delete RAM group: %w", err)
	}

	a.logger.Info("delete aliyun ram group success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID))

	return nil
}

// ListGroupUsers 获取用户组组成功员列�?
func (a *Adapter) ListGroupUsers(ctx context.Context, account *domain.CloudAccount, groupID string) ([]*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := aliyuncommon.CreateRAMClient(account)
	if err != nil {
		return nil, err
	}

	request := ram.CreateListUsersForGroupRequest()
	request.Scheme = "https"
	request.GroupName = groupID

	var response *ram.ListUsersForGroupResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.ListUsersForGroup(request)
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
	for _, ramUser := range response.Users.User {
		user := ConvertRAMUserToCloudUser(ramUser, account)
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

	client, err := aliyuncommon.CreateRAMClient(account)
	if err != nil {
		return err
	}

	request := ram.CreateAddUserToGroupRequest()
	request.Scheme = "https"
	request.UserName = userID
	request.GroupName = groupID

	err = a.retryWithBackoff(ctx, func() error {
		_, e := client.AddUserToGroup(request)
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

	client, err := aliyuncommon.CreateRAMClient(account)
	if err != nil {
		return err
	}

	request := ram.CreateRemoveUserFromGroupRequest()
	request.Scheme = "https"
	request.UserName = userID
	request.GroupName = groupID

	err = a.retryWithBackoff(ctx, func() error {
		_, e := client.RemoveUserFromGroup(request)
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

// listGroupPolicies 获取用户组组的策略列表（内部方法）
func (a *Adapter) listGroupPolicies(ctx context.Context, client *ram.Client, groupName string) ([]domain.PermissionPolicy, error) {
	request := ram.CreateListPoliciesForGroupRequest()
	request.Scheme = "https"
	request.GroupName = groupName

	var response *ram.ListPoliciesForGroupResponse
	err := a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.ListPoliciesForGroup(request)
		return e
	})

	if err != nil {
		return nil, err
	}

	var policies []domain.PermissionPolicy
	for _, ramPolicy := range response.Policies.Policy {
		policy := domain.PermissionPolicy{
			PolicyID:       ramPolicy.PolicyName,
			PolicyName:     ramPolicy.PolicyName,
			PolicyDocument: ramPolicy.Description,
			Provider:       domain.CloudProviderAliyun,
			PolicyType:     ConvertPolicyType(ramPolicy.PolicyType),
		}
		policies = append(policies, policy)
	}

	return policies, nil
}
