package huawei

import (
	"github.com/Havens-blog/e-cloudx-sdk/domain"
)

// ConvertHuaweiUserToCloudUser 转换器华为�?IAM 用户组�?CloudUser 领域模型型
// TODO: 实例现具体的转换器逻辑，需要根据华为云 SDK 的实例际类型定�?
func ConvertHuaweiUserToCloudUser(huaweiUser interface{}, account *domain.CloudAccount) *domain.CloudUser {
	// 占位符实例�?
	return &domain.CloudUser{
		Provider:       domain.CloudProviderHuawei,
		CloudAccountID: account.ID,
		TenantID:       account.TenantID,
		UserType:       domain.CloudUserTypeIAMUser,
		Status:         domain.CloudUserStatusActive,
	}
}

// ConvertHuaweiGroupToUserGroup 转换器华为云用户组组�?PermissionGroup 领域模型型
// TODO: 实例现具体的转换器逻辑，需要根据华为云 SDK 的实例际类型定�?
func ConvertHuaweiGroupToUserGroup(huaweiGroup interface{}, account *domain.CloudAccount) *domain.UserGroup {
	// 占位符实例�?
	return &domain.UserGroup{
		Provider:       domain.CloudProviderHuawei,
		CloudAccountID: account.ID,
		TenantID:       account.TenantID,
	}
}

// ConvertPolicyType 转换器策略类型
// TODO: 实例现具体的转换器逻辑
// 华为云角色类型：
// "AX" - 系统角色
// "XA" - 自定义角�?
func ConvertPolicyType(huaweiPolicyType string) domain.PolicyType {
	switch huaweiPolicyType {
	case "AX":
		return domain.PolicyTypeSystem
	case "XA":
		return domain.PolicyTypeCustom
	default:
		return domain.PolicyTypeCustom
	}
}
