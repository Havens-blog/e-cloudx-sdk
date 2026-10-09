package tencent

import (
	"fmt"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	cam "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cam/v20190116"
)

// ConvertTencentUserToCloudUser 转换器腾讯�?CAM 用户组�?CloudUser 领域模型型
func ConvertTencentUserToCloudUser(tencentUser *cam.SubAccountInfo, account *domain.CloudAccount) *domain.CloudUser {
	now := time.Now()

	// 解析创建时间
	createTime := now
	if tencentUser.CreateTime != nil && *tencentUser.CreateTime != "" {
		// 腾讯云时间格�? "2006-01-02 15:04:05"
		if t, err := time.Parse("2006-01-02 15:04:05", *tencentUser.CreateTime); err == nil {
			createTime = t
		}
	}

	// 确定用户组状�?
	status := domain.CloudUserStatusActive
	// 腾讯云没有明确的用户组状态字段，默认为活�?

	user := &domain.CloudUser{
		Username:       getStringValue(tencentUser.Name),
		UserType:       domain.CloudUserTypeCAMUser,
		CloudAccountID: account.ID,
		Provider:       domain.CloudProviderTencent,
		CloudUserID:    uint64ToString(tencentUser.Uin),
		DisplayName:    getStringValue(tencentUser.Remark),
		Status:         status,
		TenantID:       account.TenantID,
		CreateTime:     createTime,
		UpdateTime:     now,
		CTime:          createTime.Unix(),
		UTime:          now.Unix(),
		Metadata: domain.CloudUserMetadata{
			LastSyncTime: &now,
			Tags:         make(map[string]string),
		},
	}

	// 添加控制台登录信息到元数�?
	if tencentUser.ConsoleLogin != nil {
		if *tencentUser.ConsoleLogin == 1 {
			user.Metadata.Tags["console_login"] = "enabled"
		} else {
			user.Metadata.Tags["console_login"] = "disabled"
		}
	}

	return user
}

// ConvertTencentGroupToUserGroup 转换器腾讯云用户组组�?PermissionGroup 领域模型型
func ConvertTencentGroupToUserGroup(tencentGroup *cam.GroupInfo, account *domain.CloudAccount) *domain.UserGroup {
	now := time.Now()

	// 解析创建时间
	createTime := now
	if tencentGroup.CreateTime != nil && *tencentGroup.CreateTime != "" {
		if t, err := time.Parse("2006-01-02 15:04:05", *tencentGroup.CreateTime); err == nil {
			createTime = t
		}
	}

	group := &domain.UserGroup{
		GroupName:      getStringValue(tencentGroup.GroupName),
		DisplayName:    getStringValue(tencentGroup.GroupName),
		Description:    getStringValue(tencentGroup.Remark),
		CloudAccountID: account.ID,
		Provider:       domain.CloudProviderTencent,
		CloudGroupID:   uint64ToString(tencentGroup.GroupId),
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

// ConvertPolicyType 转换器策略类型
func ConvertPolicyType(policyType uint64) domain.PolicyType {
	// 腾讯云策略类型：
	// 1: 自定义策�?
	// 2: 预设策略
	switch policyType {
	case 2:
		return domain.PolicyTypeSystem
	case 1:
		return domain.PolicyTypeCustom
	default:
		return domain.PolicyTypeCustom
	}
}

// getStringValue 安全获取字符串指针的�?
func getStringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// getUint64Value 安全获取 uint64 指针的�?
func getUint64Value(u *uint64) uint64 {
	if u == nil {
		return 0
	}
	return *u
}

// uint64ToString �?uint64 指针转换器为字符串
func uint64ToString(u *uint64) string {
	if u == nil {
		return ""
	}
	return fmt.Sprintf("%d", *u)
}

// stringToUint64 将字符串转换器�?uint64
func stringToUint64(s string) uint64 {
	var result uint64
	fmt.Sscanf(s, "%d", &result)
	return result
}

// ConvertGroupMemberToCloudUser 转换器腾讯云用户组组成功员�?CloudUser 领域模型型
func ConvertGroupMemberToCloudUser(member *cam.GroupMemberInfo, account *domain.CloudAccount) *domain.CloudUser {
	now := time.Now()

	user := &domain.CloudUser{
		Username:       getStringValue(member.Name),
		UserType:       domain.CloudUserTypeCAMUser,
		CloudAccountID: account.ID,
		Provider:       domain.CloudProviderTencent,
		CloudUserID:    uint64ToString(member.Uid),
		Status:         domain.CloudUserStatusActive,
		TenantID:       account.TenantID,
		CreateTime:     now,
		UpdateTime:     now,
		CTime:          now.Unix(),
		UTime:          now.Unix(),
		Metadata: domain.CloudUserMetadata{
			LastSyncTime: &now,
			Tags:         make(map[string]string),
		},
	}

	return user
}
