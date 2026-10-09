package tencent

import (
	"context"
	"fmt"
	"time"

	tencentcommon "github.com/Havens-blog/e-cloudx-sdk/common/tencent"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	cam "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cam/v20190116"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
)

// ListGroups 获取用户组组列�?
func (a *Adapter) ListGroups(ctx context.Context, account *domain.CloudAccount) ([]*domain.UserGroup, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := tencentcommon.CreateCAMClient(account)
	if err != nil {
		return nil, err
	}

	var allGroups []*domain.UserGroup
	page := uint64(1)
	pageSize := uint64(100)

	for {
		request := cam.NewListGroupsRequest()
		request.Page = common.Uint64Ptr(page)
		request.Rp = common.Uint64Ptr(pageSize)

		var response *cam.ListGroupsResponse
		err := a.retryWithBackoff(ctx, func() error {
			var e error
			response, e = client.ListGroups(request)
			return e
		})

		if err != nil {
			a.logger.Error("list tencent cloud cam groups failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.FieldErr(err))
			return nil, fmt.Errorf("failed to list CAM groups: %w", err)
		}

		// 转换器用户组组数�?
		if response.Response.GroupInfo != nil {
			for _, tencentGroup := range response.Response.GroupInfo {
				group := ConvertTencentGroupToUserGroup(tencentGroup, account)

				// 获取用户组组的策略列表
				policies, err := a.listGroupPolicies(ctx, client, getUint64Value(tencentGroup.GroupId))
				if err != nil {
					a.logger.Warn("failed to list group policies",
						elog.String("group_id", uint64ToString(tencentGroup.GroupId)),
						elog.FieldErr(err))
				} else {
					group.Policies = policies
				}

				allGroups = append(allGroups, group)
			}
		}

		// 检查是否还有更多数�?
		if response.Response.GroupInfo == nil || len(response.Response.GroupInfo) < int(pageSize) {
			break
		}
		page++
	}

	a.logger.Info("list tencent cloud cam groups success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.Int("count", len(allGroups)))

	return allGroups, nil
}

// GetGroup 获取用户组组详�?
func (a *Adapter) GetGroup(ctx context.Context, account *domain.CloudAccount, groupID string) (*domain.UserGroup, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := tencentcommon.CreateCAMClient(account)
	if err != nil {
		return nil, err
	}

	request := cam.NewGetGroupRequest()
	request.GroupId = common.Uint64Ptr(stringToUint64(groupID))

	var response *cam.GetGroupResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.GetGroup(request)
		return e
	})

	if err != nil {
		a.logger.Error("get tencent cloud cam group failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("group_id", groupID),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to get CAM group: %w", err)
	}

	// 构建 GroupInfo
	groupInfo := &cam.GroupInfo{
		GroupId:    response.Response.GroupId,
		GroupName:  response.Response.GroupName,
		Remark:     response.Response.Remark,
		CreateTime: response.Response.CreateTime,
	}

	group := ConvertTencentGroupToUserGroup(groupInfo, account)

	// 获取用户组组的策略列表
	policies, err := a.listGroupPolicies(ctx, client, stringToUint64(groupID))
	if err != nil {
		a.logger.Warn("failed to list group policies",
			elog.String("group_id", groupID),
			elog.FieldErr(err))
	} else {
		group.Policies = policies
	}

	// 获取用户组组成功员数�?
	users, err := a.ListGroupUsers(ctx, account, groupID)
	if err == nil {
		group.MemberCount = len(users)
	}

	a.logger.Info("get tencent cloud cam group success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID))

	return group, nil
}

// CreateGroup 创建用户组�?
func (a *Adapter) CreateGroup(ctx context.Context, account *domain.CloudAccount, req *types.CreateGroupRequest) (*domain.UserGroup, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := tencentcommon.CreateCAMClient(account)
	if err != nil {
		return nil, err
	}

	request := cam.NewCreateGroupRequest()
	request.GroupName = common.StringPtr(req.GroupName)
	if req.Description != "" {
		request.Remark = common.StringPtr(req.Description)
	}

	var response *cam.CreateGroupResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.CreateGroup(request)
		return e
	})

	if err != nil {
		a.logger.Error("create tencent cloud cam group failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("group_name", req.GroupName),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to create CAM group: %w", err)
	}

	// 获取创建的用户组组详情
	groupID := uint64ToString(response.Response.GroupId)
	group, err := a.GetGroup(ctx, account, groupID)
	if err != nil {
		a.logger.Warn("failed to get created group details",
			elog.String("group_id", groupID),
			elog.FieldErr(err))
		// 返回基本信息
		now := time.Now()
		group = &domain.UserGroup{
			GroupName:      req.GroupName,
			DisplayName:    req.GroupName,
			Description:    req.Description,
			CloudAccountID: account.ID,
			Provider:       domain.CloudProviderTencent,
			CloudGroupID:   groupID,
			TenantID:       account.TenantID,
			CreateTime:     now,
			UpdateTime:     now,
			CTime:          now.Unix(),
			UTime:          now.Unix(),
		}
	}

	a.logger.Info("create tencent cloud cam group success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_name", req.GroupName))

	return group, nil
}

// UpdateGroupPolicies 更新用户组组权限策�?
func (a *Adapter) UpdateGroupPolicies(ctx context.Context, account *domain.CloudAccount, groupID string, policies []domain.PermissionPolicy) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := tencentcommon.CreateCAMClient(account)
	if err != nil {
		return err
	}

	// 获取用户组组当前的策略列表
	currentPolicies, err := a.listGroupPolicies(ctx, client, stringToUint64(groupID))
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
		if policy.Provider == domain.CloudProviderTencent {
			targetPolicyMap[policy.PolicyID] = policy
		}
	}

	// 分离需要附加和分离的策�?
	var toAttach []domain.PermissionPolicy
	var toDetach []domain.PermissionPolicy

	// 找出需要附加的策略
	for policyID, policy := range targetPolicyMap {
		if _, exists := currentPolicyMap[policyID]; !exists {
			toAttach = append(toAttach, policy)
		}
	}

	// 找出需要分离的策略
	for policyID, policy := range currentPolicyMap {
		if _, exists := targetPolicyMap[policyID]; !exists {
			toDetach = append(toDetach, policy)
		}
	}

	// 分离不需要的策略
	for _, policy := range toDetach {
		detachRequest := cam.NewDetachGroupPolicyRequest()
		detachRequest.DetachGroupId = common.Uint64Ptr(stringToUint64(groupID))
		detachRequest.PolicyId = common.Uint64Ptr(stringToUint64(policy.PolicyID))

		err = a.retryWithBackoff(ctx, func() error {
			_, e := client.DetachGroupPolicy(detachRequest)
			return e
		})

		if err != nil {
			a.logger.Error("detach policy from group failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.String("group_id", groupID),
				elog.String("policy_id", policy.PolicyID),
				elog.FieldErr(err))
			// 继续处理其他策略
		}
	}

	// 附加新策�?
	for _, policy := range toAttach {
		attachRequest := cam.NewAttachGroupPolicyRequest()
		attachRequest.AttachGroupId = common.Uint64Ptr(stringToUint64(groupID))
		attachRequest.PolicyId = common.Uint64Ptr(stringToUint64(policy.PolicyID))

		err = a.retryWithBackoff(ctx, func() error {
			_, e := client.AttachGroupPolicy(attachRequest)
			return e
		})

		if err != nil {
			a.logger.Error("attach policy to group failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.String("group_id", groupID),
				elog.String("policy_id", policy.PolicyID),
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

	client, err := tencentcommon.CreateCAMClient(account)
	if err != nil {
		return err
	}

	request := cam.NewDeleteGroupRequest()
	request.GroupId = common.Uint64Ptr(stringToUint64(groupID))

	err = a.retryWithBackoff(ctx, func() error {
		_, e := client.DeleteGroup(request)
		return e
	})

	if err != nil {
		a.logger.Error("delete tencent cloud cam group failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("group_id", groupID),
			elog.FieldErr(err))
		return fmt.Errorf("failed to delete CAM group: %w", err)
	}

	a.logger.Info("delete tencent cloud cam group success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID))

	return nil
}

// ListGroupUsers 获取用户组组成功员列�?
func (a *Adapter) ListGroupUsers(ctx context.Context, account *domain.CloudAccount, groupID string) ([]*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := tencentcommon.CreateCAMClient(account)
	if err != nil {
		return nil, err
	}

	var allUsers []*domain.CloudUser
	page := uint64(1)
	pageSize := uint64(100)

	for {
		request := cam.NewListUsersForGroupRequest()
		request.GroupId = common.Uint64Ptr(stringToUint64(groupID))
		request.Page = common.Uint64Ptr(page)
		request.Rp = common.Uint64Ptr(pageSize)

		var response *cam.ListUsersForGroupResponse
		err := a.retryWithBackoff(ctx, func() error {
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

		// 转换器用户组数据
		if response.Response.UserInfo != nil {
			for _, tencentUser := range response.Response.UserInfo {
				user := ConvertGroupMemberToCloudUser(tencentUser, account)
				allUsers = append(allUsers, user)
			}
		}

		// 检查是否还有更多数�?
		if response.Response.UserInfo == nil || len(response.Response.UserInfo) < int(pageSize) {
			break
		}
		page++
	}

	a.logger.Info("list group users success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID),
		elog.Int("count", len(allUsers)))

	return allUsers, nil
}

// AddUserToGroup 将用户组添加到用户组�?
func (a *Adapter) AddUserToGroup(ctx context.Context, account *domain.CloudAccount, groupID string, userID string) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := tencentcommon.CreateCAMClient(account)
	if err != nil {
		return err
	}

	request := cam.NewAddUserToGroupRequest()
	request.Info = []*cam.GroupIdOfUidInfo{
		{
			GroupId: common.Uint64Ptr(stringToUint64(groupID)),
			Uid:     common.Uint64Ptr(stringToUint64(userID)),
		},
	}

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

	client, err := tencentcommon.CreateCAMClient(account)
	if err != nil {
		return err
	}

	request := cam.NewRemoveUserFromGroupRequest()
	request.Info = []*cam.GroupIdOfUidInfo{
		{
			GroupId: common.Uint64Ptr(stringToUint64(groupID)),
			Uid:     common.Uint64Ptr(stringToUint64(userID)),
		},
	}

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

// GetPolicy 获取策略详情
func (a *Adapter) GetPolicy(ctx context.Context, account *domain.CloudAccount, policyID string) (*domain.PermissionPolicy, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := tencentcommon.CreateCAMClient(account)
	if err != nil {
		return nil, err
	}

	request := cam.NewGetPolicyRequest()
	request.PolicyId = common.Uint64Ptr(stringToUint64(policyID))

	var response *cam.GetPolicyResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.GetPolicy(request)
		return e
	})

	if err != nil {
		a.logger.Error("get tencent cloud policy failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("policy_id", policyID),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to get policy: %w", err)
	}

	policy := &domain.PermissionPolicy{
		PolicyID:       policyID,
		PolicyName:     getStringValue(response.Response.PolicyName),
		PolicyDocument: getStringValue(response.Response.PolicyDocument),
		Provider:       domain.CloudProviderTencent,
		PolicyType:     ConvertPolicyType(getUint64Value(response.Response.Type)),
	}

	a.logger.Info("get tencent cloud policy success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("policy_id", policyID))

	return policy, nil
}

// listGroupPolicies 获取用户组组的策略列表（内部方法）
func (a *Adapter) listGroupPolicies(ctx context.Context, client *cam.Client, groupID uint64) ([]domain.PermissionPolicy, error) {
	var allPolicies []domain.PermissionPolicy
	page := uint64(1)
	pageSize := uint64(100)

	for {
		request := cam.NewListAttachedGroupPoliciesRequest()
		request.TargetGroupId = common.Uint64Ptr(groupID)
		request.Page = common.Uint64Ptr(page)
		request.Rp = common.Uint64Ptr(pageSize)

		var response *cam.ListAttachedGroupPoliciesResponse
		err := a.retryWithBackoff(ctx, func() error {
			var e error
			response, e = client.ListAttachedGroupPolicies(request)
			return e
		})

		if err != nil {
			return nil, err
		}

		// 转换器策略数据
		if response.Response.List != nil {
			for _, tencentPolicy := range response.Response.List {
				// 腾讯云策略类型是字符串，需要转�?
				var policyType domain.PolicyType
				if tencentPolicy.PolicyType != nil {
					if *tencentPolicy.PolicyType == "QCS" {
						policyType = domain.PolicyTypeSystem
					} else {
						policyType = domain.PolicyTypeCustom
					}
				} else {
					policyType = domain.PolicyTypeCustom
				}

				policy := domain.PermissionPolicy{
					PolicyID:       uint64ToString(tencentPolicy.PolicyId),
					PolicyName:     getStringValue(tencentPolicy.PolicyName),
					PolicyDocument: "", // 需要单独调�?GetPolicy 获取
					Provider:       domain.CloudProviderTencent,
					PolicyType:     policyType,
				}
				allPolicies = append(allPolicies, policy)
			}
		}

		// 检查是否还有更多数�?
		if response.Response.List == nil || len(response.Response.List) < int(pageSize) {
			break
		}
		page++
	}

	return allPolicies, nil
}
