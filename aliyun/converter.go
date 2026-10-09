package aliyun

import (
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ram"
)

// convertListUserToCloudUser 转换 ListUsers 返回的用户为 CloudUser
func convertListUserToCloudUser(u ram.User, account *domain.CloudAccount) *domain.CloudUser {
	return &domain.CloudUser{
		Provider:       domain.CloudProviderAliyun,
		CloudAccountID: account.ID,
		TenantID:       account.TenantID,
		CloudUserID:    u.UserId,
		Username:       u.UserName,
		DisplayName:    u.DisplayName,
		Email:          u.Email,
		Status:         domain.CloudUserStatusActive,
		UserType:       domain.CloudUserTypeRAMUser,
		CreateTime:     parseTime(u.CreateDate),
		UpdateTime:     parseTime(u.UpdateDate),
	}
}

// convertGetUserToCloudUser 转换 GetUser 返回的用户为 CloudUser
func convertGetUserToCloudUser(u ram.User, account *domain.CloudAccount) *domain.CloudUser {
	return &domain.CloudUser{
		Provider:       domain.CloudProviderAliyun,
		CloudAccountID: account.ID,
		TenantID:       account.TenantID,
		CloudUserID:    u.UserId,
		Username:       u.UserName,
		DisplayName:    u.DisplayName,
		Email:          u.Email,
		Status:         domain.CloudUserStatusActive,
		UserType:       domain.CloudUserTypeRAMUser,
		CreateTime:     parseTime(u.CreateDate),
		UpdateTime:     parseTime(u.UpdateDate),
	}
}

// convertGroupUserToCloudUser 转换用户组成员为 CloudUser
func convertGroupUserToCloudUser(u ram.User, account *domain.CloudAccount) *domain.CloudUser {
	return &domain.CloudUser{
		Provider:       domain.CloudProviderAliyun,
		CloudAccountID: account.ID,
		TenantID:       account.TenantID,
		CloudUserID:    u.UserId,
		Username:       u.UserName,
		DisplayName:    u.DisplayName,
		Status:         domain.CloudUserStatusActive,
		UserType:       domain.CloudUserTypeRAMUser,
	}
}

// convertGroupToUserGroup 转换 RAM 用户组为 UserGroup
func convertGroupToUserGroup(g ram.Group, account *domain.CloudAccount) *domain.UserGroup {
	return &domain.UserGroup{
		CloudGroupID:   g.GroupId,
		GroupName:      g.GroupName,
		Name:           g.GroupName,
		DisplayName:    g.GroupName,
		Description:    g.Comments,
		Provider:       domain.CloudProviderAliyun,
		CloudAccountID: account.ID,
		TenantID:       account.TenantID,
		CreateTime:     parseTime(g.CreateDate),
		UpdateTime:     parseTime(g.UpdateDate),
	}
}

// convertPolicyType 转换策略类型
func convertPolicyType(policyType string) domain.PolicyType {
	switch policyType {
	case "System":
		return domain.PolicyTypeSystem
	case "Custom":
		return domain.PolicyTypeCustom
	default:
		return domain.PolicyTypeCustom
	}
}

// parseTime 解析时间字符串
func parseTime(timeStr string) time.Time {
	if timeStr == "" {
		return time.Time{}
	}

	// 尝试多种时间格式
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, timeStr); err == nil {
			return t
		}
	}

	return time.Time{}
}
