package iam

import (
	"context"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
)

// CloudIAMAdapter 云平台IAM适配器接�?
type CloudIAMAdapter interface {
	// ========== 用户组管理 ==========

	// ListUsers 获取用户组列表
	ListUsers(ctx context.Context, account *domain.CloudAccount) ([]*domain.CloudUser, error)

	// GetUser 获取用户详情
	GetUser(ctx context.Context, account *domain.CloudAccount, userID string) (*domain.CloudUser, error)

	// GetUserPolicies 获取用户的个人权限策略
	GetUserPolicies(ctx context.Context, account *domain.CloudAccount, userID string) ([]domain.PermissionPolicy, error)

	// CreateUser 创建用户
	CreateUser(ctx context.Context, account *domain.CloudAccount, req *types.CreateUserRequest) (*domain.CloudUser, error)

	// UpdateUserPermissions 更新用户组权限
	UpdateUserPermissions(ctx context.Context, account *domain.CloudAccount, userID string, policies []domain.PermissionPolicy) error

	// DeleteUser 删除用户组
	DeleteUser(ctx context.Context, account *domain.CloudAccount, userID string) error

	// ========== 用户组组管�?==========

	// ListGroups 获取用户组组列�?
	ListGroups(ctx context.Context, account *domain.CloudAccount) ([]*domain.UserGroup, error)

	// GetGroup 获取用户组组详�?
	GetGroup(ctx context.Context, account *domain.CloudAccount, groupID string) (*domain.UserGroup, error)

	// CreateGroup 创建用户组�?
	CreateGroup(ctx context.Context, account *domain.CloudAccount, req *types.CreateGroupRequest) (*domain.UserGroup, error)

	// UpdateGroupPolicies 更新用户组组权限策�?
	UpdateGroupPolicies(ctx context.Context, account *domain.CloudAccount, groupID string, policies []domain.PermissionPolicy) error

	// DeleteGroup 删除用户组�?
	DeleteGroup(ctx context.Context, account *domain.CloudAccount, groupID string) error

	// ListGroupUsers 获取用户组组成功员列�?
	ListGroupUsers(ctx context.Context, account *domain.CloudAccount, groupID string) ([]*domain.CloudUser, error)

	// AddUserToGroup 将用户组添加到用户组�?
	AddUserToGroup(ctx context.Context, account *domain.CloudAccount, groupID string, userID string) error

	// RemoveUserFromGroup 将用户组从用户组组移�?
	RemoveUserFromGroup(ctx context.Context, account *domain.CloudAccount, groupID string, userID string) error

	// ========== 策略管理 ==========

	// ListPolicies 获取权限策略列表
	ListPolicies(ctx context.Context, account *domain.CloudAccount) ([]domain.PermissionPolicy, error)

	// GetPolicy 获取策略详情
	GetPolicy(ctx context.Context, account *domain.CloudAccount, policyID string) (*domain.PermissionPolicy, error)

	// ========== 凭证验证 ==========

	// ValidateCredentials 验证凭证
	ValidateCredentials(ctx context.Context, account *domain.CloudAccount) error
}

// CloudIAMAdapterFactory 云平台IAM适配器工厂接�?
type CloudIAMAdapterFactory interface {
	// CreateAdapter 根据云厂商类型创建适配器�?
	CreateAdapter(provider domain.CloudProvider) (CloudIAMAdapter, error)
}

// CloudIAMAdapterCreator IAM 适配器创建器：各厂商包在 init() 中自注册，
// 工厂经注册表查找后调用。logger 由工厂持有，创建时注入。
type CloudIAMAdapterCreator func(logger *elog.Component) (CloudIAMAdapter, error)
