package aliyun

import (
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ram"
)

// ConvertRAMUserToCloudUser 转换器RAM用户组为CloudUser领域模型型
func ConvertRAMUserToCloudUser(ramUser ram.User, account *domain.CloudAccount) *domain.CloudUser {
	now := time.Now()

	// 解析创建时间
	createTime := now
	if ramUser.CreateDate != "" {
		if t, err := time.Parse(time.RFC3339, ramUser.CreateDate); err == nil {
			createTime = t
		}
	}

	// 解析最后登录时�?
	var lastLoginTime *time.Time
	if ramUser.LastLoginDate != "" {
		if t, err := time.Parse(time.RFC3339, ramUser.LastLoginDate); err == nil {
			lastLoginTime = &t
		}
	}

	user := &domain.CloudUser{
		Username:       ramUser.UserName,
		UserType:       domain.CloudUserTypeRAMUser,
		CloudAccountID: account.ID,
		Provider:       domain.CloudProviderAliyun,
		CloudUserID:    ramUser.UserId,
		DisplayName:    ramUser.DisplayName,
		Email:          ramUser.Email,
		Status:         domain.CloudUserStatusActive,
		TenantID:       account.TenantID,
		CreateTime:     createTime,
		UpdateTime:     now,
		CTime:          createTime.Unix(),
		UTime:          now.Unix(),
		Metadata: domain.CloudUserMetadata{
			LastLoginTime: lastLoginTime,
			LastSyncTime:  &now,
			Tags:          make(map[string]string),
		},
	}

	return user
}

// ConvertPolicyType 转换器策略类型
func ConvertPolicyType(ramPolicyType string) domain.PolicyType {
	switch ramPolicyType {
	case "System":
		return domain.PolicyTypeSystem
	case "Custom":
		return domain.PolicyTypeCustom
	default:
		return domain.PolicyTypeCustom
	}
}

// ConvertRAMGroupToUserGroup 转换器RAM用户组组为PermissionGroup领域模型型
func ConvertRAMGroupToUserGroup(ramGroup ram.Group, account *domain.CloudAccount) *domain.UserGroup {
	now := time.Now()

	// 解析创建时间
	createTime := now
	if ramGroup.CreateDate != "" {
		if t, err := time.Parse(time.RFC3339, ramGroup.CreateDate); err == nil {
			createTime = t
		}
	}

	group := &domain.UserGroup{
		GroupName:      ramGroup.GroupName,
		DisplayName:    ramGroup.GroupName, // 阿里云RAM没有单独的DisplayName字段
		Description:    ramGroup.Comments,
		CloudAccountID: account.ID,
		Provider:       domain.CloudProviderAliyun,
		CloudGroupID:   ramGroup.GroupName, // 阿里云使用GroupName作为唯一标识
		TenantID:       account.TenantID,
		CreateTime:     createTime,
		UpdateTime:     now,
		CTime:          createTime.Unix(),
		UTime:          now.Unix(),
		Policies:       []domain.PermissionPolicy{}, // 需要单独查�?
		MemberCount:    0,                           // 需要单独查�?
	}

	return group
}
