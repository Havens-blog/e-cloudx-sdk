// 文件：wrapper.go
//
// 作用：CloudIAMAdapter 的通用适配器包装器（IAM 收敛：五厂商 wrapper.go 逐字节相同
// 复制 → 父包唯一实现）。厂商 *Adapter 的方法签名与 CloudIAMAdapter 一致，仅
// CreateUser 接收自家 CreateUserParams，故包装器把"CreateUser 请求转换"抽为
// 构造时注入的转换器，其余方法统一委托 IAMAdapterCore。
package iam

import (
	"context"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
)

// IAMAdapterCore 厂商 Adapter 已实现、且签名与 CloudIAMAdapter 一致的方法集
// （除 CreateUser）。由 AdapterWrapper 委托实现 CloudIAMAdapter。
type IAMAdapterCore interface {
	// ===== 用户管理 =====
	ListUsers(ctx context.Context, account *domain.CloudAccount) ([]*domain.CloudUser, error)
	GetUser(ctx context.Context, account *domain.CloudAccount, userID string) (*domain.CloudUser, error)
	GetUserPolicies(ctx context.Context, account *domain.CloudAccount, userID string) ([]domain.PermissionPolicy, error)
	UpdateUserPermissions(ctx context.Context, account *domain.CloudAccount, userID string, policies []domain.PermissionPolicy) error
	DeleteUser(ctx context.Context, account *domain.CloudAccount, userID string) error

	// ===== 用户组管理 =====
	ListGroups(ctx context.Context, account *domain.CloudAccount) ([]*domain.UserGroup, error)
	GetGroup(ctx context.Context, account *domain.CloudAccount, groupID string) (*domain.UserGroup, error)
	CreateGroup(ctx context.Context, account *domain.CloudAccount, req *types.CreateGroupRequest) (*domain.UserGroup, error)
	UpdateGroupPolicies(ctx context.Context, account *domain.CloudAccount, groupID string, policies []domain.PermissionPolicy) error
	DeleteGroup(ctx context.Context, account *domain.CloudAccount, groupID string) error
	ListGroupUsers(ctx context.Context, account *domain.CloudAccount, groupID string) ([]*domain.CloudUser, error)
	AddUserToGroup(ctx context.Context, account *domain.CloudAccount, groupID string, userID string) error
	RemoveUserFromGroup(ctx context.Context, account *domain.CloudAccount, groupID string, userID string) error

	// ===== 策略管理 =====
	ListPolicies(ctx context.Context, account *domain.CloudAccount) ([]domain.PermissionPolicy, error)
	GetPolicy(ctx context.Context, account *domain.CloudAccount, policyID string) (*domain.PermissionPolicy, error)

	// ===== 凭证验证 =====
	ValidateCredentials(ctx context.Context, account *domain.CloudAccount) error
}

// CreateUserConverter 通用 CreateUserRequest → 厂商 CreateUserParams 的桥接转换器
// （各厂商在 init() 注册处构造闭包，捕获自家 *Adapter）。
type CreateUserConverter func(ctx context.Context, account *domain.CloudAccount, req *types.CreateUserRequest) (*domain.CloudUser, error)

// AdapterWrapper 通用适配器包装器：委托 core 实现 CloudIAMAdapter，
// CreateUser 经 createUser 转换器桥接厂商私有参数类型（避免循环导入）。
type AdapterWrapper struct {
	core       IAMAdapterCore
	createUser CreateUserConverter
}

// NewAdapterWrapper 创建通用适配器包装器。
func NewAdapterWrapper(core IAMAdapterCore, createUser CreateUserConverter) *AdapterWrapper {
	return &AdapterWrapper{core: core, createUser: createUser}
}

var _ CloudIAMAdapter = (*AdapterWrapper)(nil)

// ===== 用户管理 =====

func (w *AdapterWrapper) ListUsers(ctx context.Context, account *domain.CloudAccount) ([]*domain.CloudUser, error) {
	return w.core.ListUsers(ctx, account)
}

func (w *AdapterWrapper) GetUser(ctx context.Context, account *domain.CloudAccount, userID string) (*domain.CloudUser, error) {
	return w.core.GetUser(ctx, account, userID)
}

func (w *AdapterWrapper) GetUserPolicies(ctx context.Context, account *domain.CloudAccount, userID string) ([]domain.PermissionPolicy, error) {
	return w.core.GetUserPolicies(ctx, account, userID)
}

func (w *AdapterWrapper) CreateUser(ctx context.Context, account *domain.CloudAccount, req *types.CreateUserRequest) (*domain.CloudUser, error) {
	return w.createUser(ctx, account, req)
}

func (w *AdapterWrapper) UpdateUserPermissions(ctx context.Context, account *domain.CloudAccount, userID string, policies []domain.PermissionPolicy) error {
	return w.core.UpdateUserPermissions(ctx, account, userID, policies)
}

func (w *AdapterWrapper) DeleteUser(ctx context.Context, account *domain.CloudAccount, userID string) error {
	return w.core.DeleteUser(ctx, account, userID)
}

// ===== 用户组管理 =====

func (w *AdapterWrapper) ListGroups(ctx context.Context, account *domain.CloudAccount) ([]*domain.UserGroup, error) {
	return w.core.ListGroups(ctx, account)
}

func (w *AdapterWrapper) GetGroup(ctx context.Context, account *domain.CloudAccount, groupID string) (*domain.UserGroup, error) {
	return w.core.GetGroup(ctx, account, groupID)
}

func (w *AdapterWrapper) CreateGroup(ctx context.Context, account *domain.CloudAccount, req *types.CreateGroupRequest) (*domain.UserGroup, error) {
	return w.core.CreateGroup(ctx, account, req)
}

func (w *AdapterWrapper) UpdateGroupPolicies(ctx context.Context, account *domain.CloudAccount, groupID string, policies []domain.PermissionPolicy) error {
	return w.core.UpdateGroupPolicies(ctx, account, groupID, policies)
}

func (w *AdapterWrapper) DeleteGroup(ctx context.Context, account *domain.CloudAccount, groupID string) error {
	return w.core.DeleteGroup(ctx, account, groupID)
}

func (w *AdapterWrapper) ListGroupUsers(ctx context.Context, account *domain.CloudAccount, groupID string) ([]*domain.CloudUser, error) {
	return w.core.ListGroupUsers(ctx, account, groupID)
}

func (w *AdapterWrapper) AddUserToGroup(ctx context.Context, account *domain.CloudAccount, groupID string, userID string) error {
	return w.core.AddUserToGroup(ctx, account, groupID, userID)
}

func (w *AdapterWrapper) RemoveUserFromGroup(ctx context.Context, account *domain.CloudAccount, groupID string, userID string) error {
	return w.core.RemoveUserFromGroup(ctx, account, groupID, userID)
}

// ===== 策略管理 =====

func (w *AdapterWrapper) ListPolicies(ctx context.Context, account *domain.CloudAccount) ([]domain.PermissionPolicy, error) {
	return w.core.ListPolicies(ctx, account)
}

func (w *AdapterWrapper) GetPolicy(ctx context.Context, account *domain.CloudAccount, policyID string) (*domain.PermissionPolicy, error) {
	return w.core.GetPolicy(ctx, account, policyID)
}

// ===== 凭证验证 =====

func (w *AdapterWrapper) ValidateCredentials(ctx context.Context, account *domain.CloudAccount) error {
	return w.core.ValidateCredentials(ctx, account)
}
