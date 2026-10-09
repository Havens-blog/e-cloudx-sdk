package domain

import (
	"fmt"
	"time"
)

// PolicyType 策略类型
type PolicyType string

const (
	PolicyTypeSystem PolicyType = "system"
	PolicyTypeCustom PolicyType = "custom"
)

// PermissionPolicy 权限策略
type PermissionPolicy struct {
	PolicyID       string        `json:"policy_id" bson:"policy_id"`
	PolicyName     string        `json:"policy_name" bson:"policy_name"`
	PolicyDocument string        `json:"policy_document" bson:"policy_document"`
	Provider       CloudProvider `json:"provider" bson:"provider"`
	PolicyType     PolicyType    `json:"policy_type" bson:"policy_type"`
}

// UserGroup 用户组领域模型
type UserGroup struct {
	ID             int64              `json:"id" bson:"id"`
	Name           string             `json:"name" bson:"name"`
	GroupName      string             `json:"group_name" bson:"group_name"`     // 云端用户组名称
	DisplayName    string             `json:"display_name" bson:"display_name"` // 显示名称
	Description    string             `json:"description" bson:"description"`
	Policies       []PermissionPolicy `json:"policies" bson:"policies"`
	CloudPlatforms []CloudProvider    `json:"cloud_platforms" bson:"cloud_platforms"`
	CloudAccountID int64              `json:"cloud_account_id" bson:"cloud_account_id"` // 云账号ID
	Provider       CloudProvider      `json:"provider" bson:"provider"`                 // 云厂商
	CloudGroupID   string             `json:"cloud_group_id" bson:"cloud_group_id"`     // 云端用户组ID
	UserCount      int                `json:"user_count" bson:"user_count"`
	MemberCount    int                `json:"member_count" bson:"member_count"` // 成员数量（同步时使用）
	TenantID       int64              `json:"tenant_id" bson:"tenant_id"`
	CreateTime     time.Time          `json:"create_time" bson:"create_time"`
	UpdateTime     time.Time          `json:"update_time" bson:"update_time"`
	CTime          int64              `json:"ctime" bson:"ctime"`
	UTime          int64              `json:"utime" bson:"utime"`
}

// UserGroupFilter 用户组查询过滤器
type UserGroupFilter struct {
	TenantID int64  `json:"tenant_id"`
	Keyword  string `json:"keyword"`
	Offset   int64  `json:"offset"`
	Limit    int64  `json:"limit"`
}

// CreateUserGroupRequest 创建用户组请求
type CreateUserGroupRequest struct {
	Name           string             `json:"name" binding:"required,min=1,max=100"`
	Description    string             `json:"description" binding:"max=500"`
	Policies       []PermissionPolicy `json:"policies"`
	CloudPlatforms []CloudProvider    `json:"cloud_platforms" binding:"required,min=1"`
	TenantID       int64              `json:"tenant_id"`
}

// UpdateUserGroupRequest 更新用户组请求
type UpdateUserGroupRequest struct {
	Name           *string         `json:"name,omitempty"`
	Description    *string         `json:"description,omitempty"`
	CloudPlatforms []CloudProvider `json:"cloud_platforms,omitempty"`
}

// 领域方法

// Validate 验证用户组数据
func (g *UserGroup) Validate() error {
	if g.Name == "" {
		return fmt.Errorf("group name cannot be empty")
	}
	if len(g.CloudPlatforms) == 0 {
		return fmt.Errorf("cloud platforms cannot be empty")
	}
	if g.TenantID == 0 {
		return fmt.Errorf("tenant id cannot be empty")
	}
	return nil
}

// HasPolicy 检查是否包含指定策略
func (g *UserGroup) HasPolicy(policyID string, provider CloudProvider) bool {
	for _, policy := range g.Policies {
		if policy.PolicyID == policyID && policy.Provider == provider {
			return true
		}
	}
	return false
}

// AddPolicy 添加权限策略
func (g *UserGroup) AddPolicy(policy PermissionPolicy) {
	if !g.HasPolicy(policy.PolicyID, policy.Provider) {
		g.Policies = append(g.Policies, policy)
		g.UpdateTime = time.Now()
		g.UTime = g.UpdateTime.Unix()
	}
}

// RemovePolicy 移除权限策略
func (g *UserGroup) RemovePolicy(policyID string, provider CloudProvider) {
	newPolicies := make([]PermissionPolicy, 0)
	for _, policy := range g.Policies {
		if policy.PolicyID != policyID || policy.Provider != provider {
			newPolicies = append(newPolicies, policy)
		}
	}
	g.Policies = newPolicies
	g.UpdateTime = time.Now()
	g.UTime = g.UpdateTime.Unix()
}
