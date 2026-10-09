package volcano

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
)

// ListGroups 获取用户组组列�?
func (a *Adapter) ListGroups(ctx context.Context, account *domain.CloudAccount) ([]*domain.UserGroup, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	// TODO: 实例现火山云用户组组列表获取
	a.logger.Warn("volcano cloud list groups not fully implemented yet",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)))

	return []*domain.UserGroup{}, nil
}

// GetGroup 获取用户组组详�?
func (a *Adapter) GetGroup(ctx context.Context, account *domain.CloudAccount, groupID string) (*domain.UserGroup, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	// TODO: 实例现火山云用户组组详情获取
	a.logger.Warn("volcano cloud get group not fully implemented yet",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID))

	return nil, fmt.Errorf("volcano cloud get group not fully implemented yet")
}

// CreateGroup 创建用户组�?
func (a *Adapter) CreateGroup(ctx context.Context, account *domain.CloudAccount, req *types.CreateGroupRequest) (*domain.UserGroup, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	// TODO: 实例现火山云用户组组创建
	a.logger.Warn("volcano cloud create group not fully implemented yet",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_name", req.GroupName))

	return nil, fmt.Errorf("volcano cloud create group not fully implemented yet")
}

// UpdateGroupPolicies 更新用户组组权限策�?
func (a *Adapter) UpdateGroupPolicies(ctx context.Context, account *domain.CloudAccount, groupID string, policies []domain.PermissionPolicy) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	// TODO: 实例现火山云用户组组权限策略更新
	a.logger.Warn("volcano cloud update group policies not fully implemented yet",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID),
		elog.Int("policy_count", len(policies)))

	return fmt.Errorf("volcano cloud update group policies not fully implemented yet")
}

// DeleteGroup 删除用户组�?
func (a *Adapter) DeleteGroup(ctx context.Context, account *domain.CloudAccount, groupID string) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	// TODO: 实例现火山云用户组组删除
	a.logger.Warn("volcano cloud delete group not fully implemented yet",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID))

	return fmt.Errorf("volcano cloud delete group not fully implemented yet")
}

// ListGroupUsers 获取用户组组成功员列�?
func (a *Adapter) ListGroupUsers(ctx context.Context, account *domain.CloudAccount, groupID string) ([]*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	// TODO: 实例现火山云用户组组成功员列表获取
	a.logger.Warn("volcano cloud list group users not fully implemented yet",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID))

	return []*domain.CloudUser{}, nil
}

// AddUserToGroup 将用户组添加到用户组�?
func (a *Adapter) AddUserToGroup(ctx context.Context, account *domain.CloudAccount, groupID string, userID string) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	// TODO: 实例现火山云添加用户组到用户组�?
	a.logger.Warn("volcano cloud add user to group not fully implemented yet",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID),
		elog.String("user_id", userID))

	return fmt.Errorf("volcano cloud add user to group not fully implemented yet")
}

// RemoveUserFromGroup 将用户组从用户组组移�?
func (a *Adapter) RemoveUserFromGroup(ctx context.Context, account *domain.CloudAccount, groupID string, userID string) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	// TODO: 实例现火山云从用户组组移除用�?
	a.logger.Warn("volcano cloud remove user from group not fully implemented yet",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("group_id", groupID),
		elog.String("user_id", userID))

	return fmt.Errorf("volcano cloud remove user from group not fully implemented yet")
}

// GetPolicy 获取策略详情
func (a *Adapter) GetPolicy(ctx context.Context, account *domain.CloudAccount, policyID string) (*domain.PermissionPolicy, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	// TODO: 实例现火山云策略详情获�?
	a.logger.Warn("volcano cloud get policy not fully implemented yet",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("policy_id", policyID))

	return nil, fmt.Errorf("volcano cloud get policy not fully implemented yet")
}
