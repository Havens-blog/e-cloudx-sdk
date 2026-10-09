package volcano

import (
	"github.com/Havens-blog/e-cloudx-sdk/domain"
)

// ConvertVolcanoUserToCloudUser 转换器火山云用户组为 CloudUser 领域模型型
// TODO: 实例现具体的转换器逻辑，需要根据火山云 SDK 的实例际类型定�?
func ConvertVolcanoUserToCloudUser(volcanoUser interface{}, account *domain.CloudAccount) *domain.CloudUser {
	// 占位符实例�?
	return &domain.CloudUser{
		Provider:       domain.CloudProviderVolcano,
		CloudAccountID: account.ID,
		TenantID:       account.TenantID,
		UserType:       domain.CloudUserTypeVolcUser,
		Status:         domain.CloudUserStatusActive,
	}
}

// ConvertVolcanoGroupToUserGroup 转换器火山云用户组组�?PermissionGroup 领域模型型
// TODO: 实例现具体的转换器逻辑，需要根据火山云 SDK 的实例际类型定�?
func ConvertVolcanoGroupToUserGroup(volcanoGroup interface{}, account *domain.CloudAccount) *domain.UserGroup {
	// 占位符实例�?
	return &domain.UserGroup{
		Provider:       domain.CloudProviderVolcano,
		CloudAccountID: account.ID,
		TenantID:       account.TenantID,
	}
}

// ConvertPolicyType 转换器策略类型
// TODO: 实例现具体的转换器逻辑
func ConvertPolicyType(volcanoPolicyType string) domain.PolicyType {
	// 火山云策略类型待确认
	switch volcanoPolicyType {
	case "System":
		return domain.PolicyTypeSystem
	case "Custom":
		return domain.PolicyTypeCustom
	default:
		return domain.PolicyTypeCustom
	}
}
