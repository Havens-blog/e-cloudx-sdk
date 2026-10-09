package aliyun

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/common/retry"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ram"
	"github.com/gotomicro/ego/core/elog"
)

// IAMAdapter 阿里云IAM适配器
type IAMAdapter struct {
	account     *domain.CloudAccount
	logger      *elog.Component
	rateLimiter *RateLimiter
}

// NewIAMAdapter 创建阿里云IAM适配器
func NewIAMAdapter(account *domain.CloudAccount, logger *elog.Component) *IAMAdapter {
	return &IAMAdapter{
		account:     account,
		logger:      logger,
		rateLimiter: NewRateLimiter(20),
	}
}

// getClient 获取RAM客户端
func (a *IAMAdapter) getClient() (*ram.Client, error) {
	return CreateRAMClientFromAccount(a.account)
}

// retryWithBackoff 使用指数退避策略重试
func (a *IAMAdapter) retryWithBackoff(ctx context.Context, operation func() error) error {
	return retry.WithBackoff(ctx, 3, operation, func(err error) bool {
		if IsThrottlingError(err) {
			a.logger.Warn("阿里云API限流，正在重试", elog.FieldErr(err))
			return true
		}
		return false
	})
}

// ========== 用户管理 ==========

// ListUsers 获取用户列表
func (a *IAMAdapter) ListUsers(ctx context.Context) ([]*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	var allUsers []*domain.CloudUser
	marker := ""

	for {
		request := ram.CreateListUsersRequest()
		request.Scheme = "https"
		request.MaxItems = "100"
		if marker != "" {
			request.Marker = marker
		}

		var response *ram.ListUsersResponse
		err := a.retryWithBackoff(ctx, func() error {
			var e error
			response, e = client.ListUsers(request)
			return e
		})

		if err != nil {
			return nil, fmt.Errorf("获取用户列表失败: %w", err)
		}

		for _, ramUser := range response.Users.User {
			user := convertListUserToCloudUser(ramUser, a.account)
			allUsers = append(allUsers, user)
		}

		if !response.IsTruncated {
			break
		}
		marker = response.Marker
	}

	a.logger.Info("获取阿里云RAM用户列表成功",
		elog.Int64("account_id", a.account.ID),
		elog.Int("count", len(allUsers)))

	return allUsers, nil
}

// GetUser 获取用户详情
func (a *IAMAdapter) GetUser(ctx context.Context, userID string) (*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	request := ram.CreateGetUserRequest()
	request.Scheme = "https"
	request.UserName = userID

	var response *ram.GetUserResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.GetUser(request)
		return e
	})

	if err != nil {
		return nil, fmt.Errorf("获取用户详情失败: %w", err)
	}

	user := convertGetUserToCloudUser(response.User, a.account)
	return user, nil
}

// GetUserPolicies 获取用户的个人权限策略
func (a *IAMAdapter) GetUserPolicies(ctx context.Context, userID string) ([]domain.PermissionPolicy, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	request := ram.CreateListPoliciesForUserRequest()
	request.Scheme = "https"
	request.UserName = userID

	var response *ram.ListPoliciesForUserResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.ListPoliciesForUser(request)
		return e
	})

	if err != nil {
		return nil, fmt.Errorf("获取用户策略失败: %w", err)
	}

	policies := make([]domain.PermissionPolicy, 0, len(response.Policies.Policy))
	for _, policy := range response.Policies.Policy {
		policies = append(policies, domain.PermissionPolicy{
			PolicyID:   policy.PolicyName,
			PolicyName: policy.PolicyName,
			Provider:   domain.CloudProviderAliyun,
			PolicyType: convertPolicyType(policy.PolicyType),
		})
	}

	return policies, nil
}

// CreateUser 创建用户
func (a *IAMAdapter) CreateUser(ctx context.Context, req *types.CreateUserRequest) (*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	request := ram.CreateCreateUserRequest()
	request.Scheme = "https"
	request.UserName = req.Username
	if req.DisplayName != "" {
		request.DisplayName = req.DisplayName
	}
	if req.Email != "" {
		request.Email = req.Email
	}

	var response *ram.CreateUserResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.CreateUser(request)
		return e
	})

	if err != nil {
		return nil, fmt.Errorf("创建用户失败: %w", err)
	}

	user := &domain.CloudUser{
		Provider:       domain.CloudProviderAliyun,
		CloudAccountID: a.account.ID,
		TenantID:       a.account.TenantID,
		CloudUserID:    response.User.UserId,
		Username:       response.User.UserName,
		DisplayName:    response.User.DisplayName,
		Email:          response.User.Email,
		Status:         domain.CloudUserStatusActive,
		UserType:       domain.CloudUserTypeRAMUser,
		CreateTime:     parseTime(response.User.CreateDate),
	}
	return user, nil
}

// UpdateUserPermissions 更新用户权限
func (a *IAMAdapter) UpdateUserPermissions(ctx context.Context, userID string, policies []domain.PermissionPolicy) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return err
	}

	client, err := a.getClient()
	if err != nil {
		return err
	}

	// 获取当前策略
	listRequest := ram.CreateListPoliciesForUserRequest()
	listRequest.Scheme = "https"
	listRequest.UserName = userID

	var listResponse *ram.ListPoliciesForUserResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		listResponse, e = client.ListPoliciesForUser(listRequest)
		return e
	})

	if err != nil {
		return fmt.Errorf("获取用户当前策略失败: %w", err)
	}

	// 构建当前策略映射
	currentPolicies := make(map[string]string)
	for _, policy := range listResponse.Policies.Policy {
		currentPolicies[policy.PolicyName] = policy.PolicyType
	}

	// 构建目标策略映射
	targetPolicies := make(map[string]domain.PermissionPolicy)
	for _, policy := range policies {
		if policy.Provider == domain.CloudProviderAliyun {
			targetPolicies[policy.PolicyName] = policy
		}
	}

	// 分离需要附加和分离的策略
	for policyName, policyType := range currentPolicies {
		if _, exists := targetPolicies[policyName]; !exists {
			// 分离策略
			detachRequest := ram.CreateDetachPolicyFromUserRequest()
			detachRequest.Scheme = "https"
			detachRequest.PolicyName = policyName
			detachRequest.PolicyType = policyType
			detachRequest.UserName = userID

			_ = a.retryWithBackoff(ctx, func() error {
				_, e := client.DetachPolicyFromUser(detachRequest)
				return e
			})
		}
	}

	for policyName, policy := range targetPolicies {
		if _, exists := currentPolicies[policyName]; !exists {
			// 附加策略
			attachRequest := ram.CreateAttachPolicyToUserRequest()
			attachRequest.Scheme = "https"
			attachRequest.PolicyName = policy.PolicyName
			attachRequest.PolicyType = string(policy.PolicyType)
			attachRequest.UserName = userID

			err = a.retryWithBackoff(ctx, func() error {
				_, e := client.AttachPolicyToUser(attachRequest)
				return e
			})

			if err != nil {
				return fmt.Errorf("附加策略 %s 失败: %w", policyName, err)
			}
		}
	}

	return nil
}

// DeleteUser 删除用户
func (a *IAMAdapter) DeleteUser(ctx context.Context, userID string) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return err
	}

	client, err := a.getClient()
	if err != nil {
		return err
	}

	// 先删除用户的所有AccessKey
	akRequest := ram.CreateListAccessKeysRequest()
	akRequest.Scheme = "https"
	akRequest.UserName = userID

	var akResponse *ram.ListAccessKeysResponse
	_ = a.retryWithBackoff(ctx, func() error {
		var e error
		akResponse, e = client.ListAccessKeys(akRequest)
		return e
	})

	if akResponse != nil {
		for _, ak := range akResponse.AccessKeys.AccessKey {
			deleteAKRequest := ram.CreateDeleteAccessKeyRequest()
			deleteAKRequest.Scheme = "https"
			deleteAKRequest.UserAccessKeyId = ak.AccessKeyId
			deleteAKRequest.UserName = userID

			_ = a.retryWithBackoff(ctx, func() error {
				_, e := client.DeleteAccessKey(deleteAKRequest)
				return e
			})
		}
	}

	// 删除用户
	request := ram.CreateDeleteUserRequest()
	request.Scheme = "https"
	request.UserName = userID

	err = a.retryWithBackoff(ctx, func() error {
		_, e := client.DeleteUser(request)
		return e
	})

	if err != nil {
		return fmt.Errorf("删除用户失败: %w", err)
	}

	return nil
}

// ========== 用户组管理 ==========

// ListGroups 获取用户组列表
func (a *IAMAdapter) ListGroups(ctx context.Context) ([]*domain.UserGroup, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	var allGroups []*domain.UserGroup
	marker := ""

	for {
		request := ram.CreateListGroupsRequest()
		request.Scheme = "https"
		request.MaxItems = "100"
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
			return nil, fmt.Errorf("获取用户组列表失败: %w", err)
		}

		for _, ramGroup := range response.Groups.Group {
			group := convertGroupToUserGroup(ramGroup, a.account)
			allGroups = append(allGroups, group)
		}

		if !response.IsTruncated {
			break
		}
		marker = response.Marker
	}

	return allGroups, nil
}

// GetGroup 获取用户组详情
func (a *IAMAdapter) GetGroup(ctx context.Context, groupID string) (*domain.UserGroup, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	client, err := a.getClient()
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
		return nil, fmt.Errorf("获取用户组详情失败: %w", err)
	}

	group := &domain.UserGroup{
		CloudGroupID:   response.Group.GroupId,
		GroupName:      response.Group.GroupName,
		Name:           response.Group.GroupName,
		DisplayName:    response.Group.GroupName,
		Description:    response.Group.Comments,
		Provider:       domain.CloudProviderAliyun,
		CloudAccountID: a.account.ID,
		TenantID:       a.account.TenantID,
		CreateTime:     parseTime(response.Group.CreateDate),
		UpdateTime:     parseTime(response.Group.UpdateDate),
	}

	return group, nil
}

// CreateGroup 创建用户组
func (a *IAMAdapter) CreateGroup(ctx context.Context, req *types.CreateGroupRequest) (*domain.UserGroup, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	client, err := a.getClient()
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
		return nil, fmt.Errorf("创建用户组失败: %w", err)
	}

	group := &domain.UserGroup{
		CloudGroupID:   response.Group.GroupId,
		GroupName:      response.Group.GroupName,
		Name:           response.Group.GroupName,
		DisplayName:    req.DisplayName,
		Description:    response.Group.Comments,
		Provider:       domain.CloudProviderAliyun,
		CloudAccountID: a.account.ID,
		TenantID:       a.account.TenantID,
		CreateTime:     parseTime(response.Group.CreateDate),
	}

	return group, nil
}

// UpdateGroupPolicies 更新用户组权限策略
func (a *IAMAdapter) UpdateGroupPolicies(ctx context.Context, groupID string, policies []domain.PermissionPolicy) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return err
	}

	client, err := a.getClient()
	if err != nil {
		return err
	}

	// 获取当前策略
	listRequest := ram.CreateListPoliciesForGroupRequest()
	listRequest.Scheme = "https"
	listRequest.GroupName = groupID

	var listResponse *ram.ListPoliciesForGroupResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		listResponse, e = client.ListPoliciesForGroup(listRequest)
		return e
	})

	if err != nil {
		return fmt.Errorf("获取用户组当前策略失败: %w", err)
	}

	// 构建当前策略映射
	currentPolicies := make(map[string]string)
	for _, policy := range listResponse.Policies.Policy {
		currentPolicies[policy.PolicyName] = policy.PolicyType
	}

	// 构建目标策略映射
	targetPolicies := make(map[string]domain.PermissionPolicy)
	for _, policy := range policies {
		if policy.Provider == domain.CloudProviderAliyun {
			targetPolicies[policy.PolicyName] = policy
		}
	}

	// 分离不需要的策略
	for policyName, policyType := range currentPolicies {
		if _, exists := targetPolicies[policyName]; !exists {
			detachRequest := ram.CreateDetachPolicyFromGroupRequest()
			detachRequest.Scheme = "https"
			detachRequest.PolicyName = policyName
			detachRequest.PolicyType = policyType
			detachRequest.GroupName = groupID

			_ = a.retryWithBackoff(ctx, func() error {
				_, e := client.DetachPolicyFromGroup(detachRequest)
				return e
			})
		}
	}

	// 附加新策略
	for policyName, policy := range targetPolicies {
		if _, exists := currentPolicies[policyName]; !exists {
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
				return fmt.Errorf("附加策略 %s 失败: %w", policyName, err)
			}
		}
	}

	return nil
}

// DeleteGroup 删除用户组
func (a *IAMAdapter) DeleteGroup(ctx context.Context, groupID string) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return err
	}

	client, err := a.getClient()
	if err != nil {
		return err
	}

	request := ram.CreateDeleteGroupRequest()
	request.Scheme = "https"
	request.GroupName = groupID

	err = a.retryWithBackoff(ctx, func() error {
		_, e := client.DeleteGroup(request)
		return e
	})

	if err != nil {
		return fmt.Errorf("删除用户组失败: %w", err)
	}

	return nil
}

// ListGroupUsers 获取用户组成员列表
func (a *IAMAdapter) ListGroupUsers(ctx context.Context, groupID string) ([]*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	client, err := a.getClient()
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
		return nil, fmt.Errorf("获取用户组成员失败: %w", err)
	}

	users := make([]*domain.CloudUser, 0, len(response.Users.User))
	for _, ramUser := range response.Users.User {
		user := convertGroupUserToCloudUser(ramUser, a.account)
		users = append(users, user)
	}

	return users, nil
}

// AddUserToGroup 将用户添加到用户组
func (a *IAMAdapter) AddUserToGroup(ctx context.Context, groupID string, userID string) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return err
	}

	client, err := a.getClient()
	if err != nil {
		return err
	}

	request := ram.CreateAddUserToGroupRequest()
	request.Scheme = "https"
	request.GroupName = groupID
	request.UserName = userID

	err = a.retryWithBackoff(ctx, func() error {
		_, e := client.AddUserToGroup(request)
		return e
	})

	if err != nil {
		return fmt.Errorf("添加用户到用户组失败: %w", err)
	}

	return nil
}

// RemoveUserFromGroup 将用户从用户组移除
func (a *IAMAdapter) RemoveUserFromGroup(ctx context.Context, groupID string, userID string) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return err
	}

	client, err := a.getClient()
	if err != nil {
		return err
	}

	request := ram.CreateRemoveUserFromGroupRequest()
	request.Scheme = "https"
	request.GroupName = groupID
	request.UserName = userID

	err = a.retryWithBackoff(ctx, func() error {
		_, e := client.RemoveUserFromGroup(request)
		return e
	})

	if err != nil {
		return fmt.Errorf("从用户组移除用户失败: %w", err)
	}

	return nil
}

// ========== 策略管理 ==========

// ListPolicies 获取权限策略列表
func (a *IAMAdapter) ListPolicies(ctx context.Context) ([]domain.PermissionPolicy, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	var allPolicies []domain.PermissionPolicy
	marker := ""

	for {
		request := ram.CreateListPoliciesRequest()
		request.Scheme = "https"
		request.MaxItems = "100"
		if marker != "" {
			request.Marker = marker
		}

		var response *ram.ListPoliciesResponse
		err := a.retryWithBackoff(ctx, func() error {
			var e error
			response, e = client.ListPolicies(request)
			return e
		})

		if err != nil {
			return nil, fmt.Errorf("获取策略列表失败: %w", err)
		}

		for _, ramPolicy := range response.Policies.Policy {
			policy := domain.PermissionPolicy{
				PolicyID:       ramPolicy.PolicyName,
				PolicyName:     ramPolicy.PolicyName,
				PolicyDocument: ramPolicy.Description,
				Provider:       domain.CloudProviderAliyun,
				PolicyType:     convertPolicyType(ramPolicy.PolicyType),
			}
			allPolicies = append(allPolicies, policy)
		}

		if !response.IsTruncated {
			break
		}
		marker = response.Marker
	}

	return allPolicies, nil
}

// GetPolicy 获取策略详情
func (a *IAMAdapter) GetPolicy(ctx context.Context, policyID string) (*domain.PermissionPolicy, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	// 先尝试系统策略
	request := ram.CreateGetPolicyRequest()
	request.Scheme = "https"
	request.PolicyName = policyID
	request.PolicyType = "System"

	var response *ram.GetPolicyResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.GetPolicy(request)
		return e
	})

	if err != nil {
		// 尝试自定义策略
		request.PolicyType = "Custom"
		err = a.retryWithBackoff(ctx, func() error {
			var e error
			response, e = client.GetPolicy(request)
			return e
		})

		if err != nil {
			return nil, fmt.Errorf("获取策略详情失败: %w", err)
		}
	}

	policy := &domain.PermissionPolicy{
		PolicyID:       response.Policy.PolicyName,
		PolicyName:     response.Policy.PolicyName,
		PolicyDocument: response.Policy.Description,
		Provider:       domain.CloudProviderAliyun,
		PolicyType:     convertPolicyType(response.Policy.PolicyType),
	}

	// 获取策略版本详情
	versionRequest := ram.CreateGetPolicyVersionRequest()
	versionRequest.Scheme = "https"
	versionRequest.PolicyName = policyID
	versionRequest.PolicyType = response.Policy.PolicyType
	versionRequest.VersionId = response.Policy.DefaultVersion

	var versionResponse *ram.GetPolicyVersionResponse
	_ = a.retryWithBackoff(ctx, func() error {
		var e error
		versionResponse, e = client.GetPolicyVersion(versionRequest)
		return e
	})

	if versionResponse != nil {
		policy.PolicyDocument = versionResponse.PolicyVersion.PolicyDocument
	}

	return policy, nil
}
