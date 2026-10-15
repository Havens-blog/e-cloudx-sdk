package volcano

import (
	"strconv"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	iamSDK "github.com/volcengine/volcengine-go-sdk/service/iam"
)

// volcanoUserFields 各 SDK 用户输出形态的公共字段投影
// (火山云 SDK 为 List/Get/Create 定义了独立但字段相同的输出类型)。
type volcanoUserFields struct {
	id          *int32
	userName    string
	displayName string
	email       string
	createDate  string
	updateDate  string
}

// ConvertVolcanoUserToCloudUser 转换火山云 GetUser 返回的用户为 CloudUser 领域模型。
func ConvertVolcanoUserToCloudUser(u *iamSDK.UserForGetUserOutput, account *domain.CloudAccount) *domain.CloudUser {
	if u == nil {
		return nil
	}

	return buildCloudUser(volcanoUserFields{
		id:          u.Id,
		userName:    deref(u.UserName),
		displayName: deref(u.DisplayName),
		email:       deref(u.Email),
		createDate:  deref(u.CreateDate),
		updateDate:  deref(u.UpdateDate),
	}, account)
}

// ConvertVolcanoCreatedUserToCloudUser 转换火山云 CreateUser 返回的用户为 CloudUser 领域模型。
func ConvertVolcanoCreatedUserToCloudUser(u *iamSDK.UserForCreateUserOutput, account *domain.CloudAccount) *domain.CloudUser {
	if u == nil {
		return nil
	}

	return buildCloudUser(volcanoUserFields{
		id:          u.Id,
		userName:    deref(u.UserName),
		displayName: deref(u.DisplayName),
		email:       deref(u.Email),
		createDate:  deref(u.CreateDate),
		updateDate:  deref(u.UpdateDate),
	}, account)
}

// ConvertVolcanoListUserToCloudUser 转换火山云 ListUsers 返回的用户为 CloudUser 领域模型。
func ConvertVolcanoListUserToCloudUser(u *iamSDK.UserMetadataForListUsersOutput, account *domain.CloudAccount) *domain.CloudUser {
	if u == nil {
		return nil
	}

	user := buildCloudUser(volcanoUserFields{
		id:          u.Id,
		userName:    deref(u.UserName),
		displayName: deref(u.DisplayName),
		email:       deref(u.Email),
		createDate:  deref(u.CreateDate),
		updateDate:  deref(u.UpdateDate),
	}, account)

	if len(u.Tags) > 0 {
		user.Metadata.Tags = make(map[string]string, len(u.Tags))
		for _, tag := range u.Tags {
			user.Metadata.Tags[deref(tag.Key)] = deref(tag.Value)
		}
	}

	return user
}

// ConvertVolcanoGroupUserToCloudUser 转换火山云用户组成员(ListUsersForGroup)为 CloudUser。
func ConvertVolcanoGroupUserToCloudUser(u *iamSDK.UserForListUsersForGroupOutput, account *domain.CloudAccount) *domain.CloudUser {
	if u == nil {
		return nil
	}

	return buildCloudUser(volcanoUserFields{
		id:          u.Id,
		userName:    deref(u.UserName),
		displayName: deref(u.DisplayName),
		createDate:  deref(u.JoinDate),
	}, account)
}

// buildCloudUser 按 SDK 用户字段投影构建 CloudUser。
func buildCloudUser(f volcanoUserFields, account *domain.CloudAccount) *domain.CloudUser {
	now := time.Now()
	createTime := ParseVolcanoTime(f.createDate)
	if createTime.IsZero() {
		createTime = now
	}
	updateTime := ParseVolcanoTime(f.updateDate)
	if updateTime.IsZero() {
		updateTime = now
	}

	user := buildCloudUserBase(account, now, createTime)
	user.CloudUserID = volcanoID(f.id, f.userName)
	user.Username = f.userName
	user.DisplayName = f.displayName
	user.Email = f.email
	user.UpdateTime = updateTime

	return user
}

// buildCloudUserBase 填充火山云用户的公共字段。
func buildCloudUserBase(account *domain.CloudAccount, now, createTime time.Time) *domain.CloudUser {
	return &domain.CloudUser{
		Provider:       domain.CloudProviderVolcano,
		CloudAccountID: account.ID,
		TenantID:       account.TenantID,
		UserType:       domain.CloudUserTypeVolcUser,
		Status:         domain.CloudUserStatusActive,
		CreateTime:     createTime,
		UpdateTime:     now,
		CTime:          createTime.Unix(),
		UTime:          now.Unix(),
		Metadata: domain.CloudUserMetadata{
			LastSyncTime: &now,
			Tags:         make(map[string]string),
		},
	}
}

// volcanoGroupFields 各 SDK 用户组输出形态的公共字段投影。
type volcanoGroupFields struct {
	id          *int32
	groupName   string
	displayName string
	description string
	createDate  string
	updateDate  string
}

// ConvertVolcanoGroupToUserGroup 转换火山云 GetGroup 返回的用户组为 UserGroup 领域模型。
func ConvertVolcanoGroupToUserGroup(g *iamSDK.UserGroupForGetGroupOutput, account *domain.CloudAccount) *domain.UserGroup {
	if g == nil {
		return nil
	}

	return buildUserGroup(volcanoGroupFields{
		id:          g.UserGroupID,
		groupName:   deref(g.UserGroupName),
		displayName: deref(g.DisplayName),
		description: deref(g.Description),
		createDate:  deref(g.CreateDate),
		updateDate:  deref(g.UpdateDate),
	}, account)
}

// ConvertVolcanoCreatedGroupToUserGroup 转换火山云 CreateGroup 返回的用户组为 UserGroup 领域模型。
func ConvertVolcanoCreatedGroupToUserGroup(g *iamSDK.UserGroupForCreateGroupOutput, account *domain.CloudAccount) *domain.UserGroup {
	if g == nil {
		return nil
	}

	return buildUserGroup(volcanoGroupFields{
		id:          g.UserGroupID,
		groupName:   deref(g.UserGroupName),
		displayName: deref(g.DisplayName),
		description: deref(g.Description),
		createDate:  deref(g.CreateDate),
		updateDate:  deref(g.UpdateDate),
	}, account)
}

// ConvertVolcanoListGroupToUserGroup 转换火山云 ListGroups 返回的用户组为 UserGroup 领域模型。
func ConvertVolcanoListGroupToUserGroup(g *iamSDK.UserGroupForListGroupsOutput, account *domain.CloudAccount) *domain.UserGroup {
	if g == nil {
		return nil
	}

	return buildUserGroup(volcanoGroupFields{
		id:          g.UserGroupID,
		groupName:   deref(g.UserGroupName),
		displayName: deref(g.DisplayName),
		description: deref(g.Description),
		createDate:  deref(g.CreateDate),
		updateDate:  deref(g.UpdateDate),
	}, account)
}

// buildUserGroup 按 SDK 用户组字段投影构建 UserGroup。
func buildUserGroup(f volcanoGroupFields, account *domain.CloudAccount) *domain.UserGroup {
	group := buildUserGroupBase(account)
	group.CloudGroupID = volcanoID(f.id, f.groupName)
	group.GroupName = f.groupName
	group.Name = f.groupName
	group.DisplayName = f.displayName
	if group.DisplayName == "" {
		group.DisplayName = group.GroupName
	}
	group.Description = f.description
	group.CreateTime = ParseVolcanoTime(f.createDate)
	group.UpdateTime = ParseVolcanoTime(f.updateDate)
	group.CTime = group.CreateTime.Unix()
	group.UTime = group.UpdateTime.Unix()

	return group
}

// buildUserGroupBase 填充火山云用户组的公共字段。
func buildUserGroupBase(account *domain.CloudAccount) *domain.UserGroup {
	return &domain.UserGroup{
		Provider:       domain.CloudProviderVolcano,
		CloudAccountID: account.ID,
		TenantID:       account.TenantID,
		Policies:       []domain.PermissionPolicy{},
	}
}

// ConvertVolcanoPolicyMetadataToPermissionPolicy 转换 ListPolicies 返回的策略为 PermissionPolicy。
func ConvertVolcanoPolicyMetadataToPermissionPolicy(p *iamSDK.PolicyMetadataForListPoliciesOutput) domain.PermissionPolicy {
	policy := domain.PermissionPolicy{
		Provider: domain.CloudProviderVolcano,
	}
	if p == nil {
		return policy
	}

	policy.PolicyID = deref(p.PolicyName)
	policy.PolicyName = deref(p.PolicyName)
	policy.PolicyDocument = deref(p.PolicyDocument)
	policy.PolicyType = ConvertPolicyType(deref(p.PolicyType))

	return policy
}

// ConvertVolcanoPolicyToPermissionPolicy 转换 GetPolicy 返回的策略为 PermissionPolicy。
func ConvertVolcanoPolicyToPermissionPolicy(p *iamSDK.PolicyForGetPolicyOutput) domain.PermissionPolicy {
	policy := domain.PermissionPolicy{
		Provider: domain.CloudProviderVolcano,
	}
	if p == nil {
		return policy
	}

	policy.PolicyID = deref(p.PolicyName)
	policy.PolicyName = deref(p.PolicyName)
	policy.PolicyDocument = deref(p.PolicyDocument)
	policy.PolicyType = ConvertPolicyType(deref(p.PolicyType))

	return policy
}

// ConvertPolicyType 转换火山云策略类型为领域策略类型。
// 火山云 IAM PolicyType 枚举为 "System"/"Custom"。
func ConvertPolicyType(volcanoPolicyType string) domain.PolicyType {
	switch volcanoPolicyType {
	case "System":
		return domain.PolicyTypeSystem
	case "Custom":
		return domain.PolicyTypeCustom
	default:
		return domain.PolicyTypeCustom
	}
}

// PolicyTypeToVolcano 将领域策略类型转换为火山云 API 要求的大写枚举
// (AttachUserPolicy/DetachUserPolicy/GetPolicy 等要求 "System"/"Custom")。
func PolicyTypeToVolcano(policyType domain.PolicyType) string {
	switch policyType {
	case domain.PolicyTypeSystem:
		return "System"
	case domain.PolicyTypeCustom:
		return "Custom"
	default:
		return "Custom"
	}
}

// ParseVolcanoTime 解析火山云时间字符串 (RFC3339,如 "2024-01-02T03:04:05Z")。
func ParseVolcanoTime(timeStr string) time.Time {
	if timeStr == "" {
		return time.Time{}
	}

	if t, err := time.Parse(time.RFC3339, timeStr); err == nil {
		return t
	}

	return time.Time{}
}

// volcanoID 将 SDK 数值 ID 转为字符串;缺失时回退到名称。
func volcanoID(id *int32, fallback string) string {
	if id != nil {
		return strconv.Itoa(int(*id))
	}
	return fallback
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// DiffPolicies 计算需要附加/分离的策略集合(按策略名做差集)。
// target 中仅保留 provider 匹配的策略;调用方保证 current 来自云端当前挂载状态。
func DiffPolicies(current, target []domain.PermissionPolicy, provider domain.CloudProvider) (toAttach, toDetach []domain.PermissionPolicy) {
	currentMap := make(map[string]domain.PermissionPolicy, len(current))
	for _, policy := range current {
		currentMap[policy.PolicyName] = policy
	}

	targetMap := make(map[string]domain.PermissionPolicy, len(target))
	for _, policy := range target {
		if policy.Provider == provider {
			targetMap[policy.PolicyName] = policy
		}
	}

	for name, policy := range targetMap {
		if _, exists := currentMap[name]; !exists {
			toAttach = append(toAttach, policy)
		}
	}

	for name, policy := range currentMap {
		if _, exists := targetMap[name]; !exists {
			toDetach = append(toDetach, policy)
		}
	}

	return toAttach, toDetach
}
