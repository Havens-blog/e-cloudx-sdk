package aws

import (
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// ConvertIAMUserToCloudUser 转换器 IAM 用户组�?CloudUser 领域模型型
func ConvertIAMUserToCloudUser(iamUser types.User, account *domain.CloudAccount) *domain.CloudUser {
	now := time.Now()

	// 解析创建时间
	createTime := now
	if iamUser.CreateDate != nil {
		createTime = *iamUser.CreateDate
	}

	// 解析最后密码使用时�?
	var passwordLastSet *time.Time
	if iamUser.PasswordLastUsed != nil {
		passwordLastSet = iamUser.PasswordLastUsed
	}

	user := &domain.CloudUser{
		Username:       *iamUser.UserName,
		UserType:       domain.CloudUserTypeIAMUser,
		CloudAccountID: account.ID,
		Provider:       domain.CloudProviderAWS,
		CloudUserID:    *iamUser.UserId,
		Status:         domain.CloudUserStatusActive,
		TenantID:       account.TenantID,
		CreateTime:     createTime,
		UpdateTime:     now,
		CTime:          createTime.Unix(),
		UTime:          now.Unix(),
		Metadata: domain.CloudUserMetadata{
			PasswordLastSet: passwordLastSet,
			LastSyncTime:    &now,
			Tags:            convertTags(iamUser.Tags),
		},
	}

	return user
}

// ConvertPolicyScope 转换器策略范围为策略类�?
func ConvertPolicyScope(policyArn *string) domain.PolicyType {
	if policyArn == nil {
		return domain.PolicyTypeCustom
	}

	// AWS 托管策略�?ARN 格式: arn:aws:iam::aws:policy/...
	// 客户托管策略�?ARN 格式: arn:aws:iam::123456789012:policy/...
	if strings.Contains(*policyArn, ":iam::aws:policy/") {
		return domain.PolicyTypeSystem
	}

	return domain.PolicyTypeCustom
}

// convertTags 转换器 AWS 标签�?map
func convertTags(tags []types.Tag) map[string]string {
	result := make(map[string]string)
	for _, tag := range tags {
		if tag.Key != nil && tag.Value != nil {
			result[*tag.Key] = *tag.Value
		}
	}
	return result
}

// ConvertIAMGroupToUserGroup 转换器 IAM 用户组组为 PermissionGroup 领域模型型
func ConvertIAMGroupToUserGroup(iamGroup types.Group, account *domain.CloudAccount) *domain.UserGroup {
	now := time.Now()

	// 解析创建时间
	createTime := now
	if iamGroup.CreateDate != nil {
		createTime = *iamGroup.CreateDate
	}

	group := &domain.UserGroup{
		GroupName:      *iamGroup.GroupName,
		DisplayName:    *iamGroup.GroupName, // AWS IAM 没有单独�?DisplayName 字段
		Description:    "",                  // AWS IAM Group 没有描述字段
		CloudAccountID: account.ID,
		Provider:       domain.CloudProviderAWS,
		CloudGroupID:   *iamGroup.GroupName, // AWS 使用 GroupName 作为唯一标识
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
