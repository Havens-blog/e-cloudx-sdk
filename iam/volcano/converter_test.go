package volcano

import (
	"testing"
	"time"

	iamSDK "github.com/volcengine/volcengine-go-sdk/service/iam"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/stretchr/testify/assert"
)

// mock SDK 响应测试:断言 volcengine IAM SDK 类型 → domain 模型的映射(AC-2)。

var convTestAccount = &domain.CloudAccount{
	ID:              7,
	TenantID:        3,
	Provider:        domain.CloudProviderVolcano,
	AccessKeyID:     "test-ak",
	AccessKeySecret: "test-sk",
}

func strPtr(s string) *string { return &s }
func i32Ptr(i int32) *int32   { return &i }

func TestConvertVolcanoUserToCloudUser(t *testing.T) {
	u := &iamSDK.UserForGetUserOutput{
		Id:          i32Ptr(42),
		UserName:    strPtr("alice"),
		DisplayName: strPtr("Alice"),
		Email:       strPtr("alice@example.com"),
		Trn:         strPtr("trn:iam::account:user/alice"),
		CreateDate:  strPtr("2024-01-02T03:04:05Z"),
		UpdateDate:  strPtr("2024-06-07T08:09:10Z"),
	}

	user := ConvertVolcanoUserToCloudUser(u, convTestAccount)

	assert.NotNil(t, user)
	assert.Equal(t, domain.CloudProviderVolcano, user.Provider)
	assert.Equal(t, int64(7), user.CloudAccountID)
	assert.Equal(t, int64(3), user.TenantID)
	assert.Equal(t, "42", user.CloudUserID)
	assert.Equal(t, "alice", user.Username)
	assert.Equal(t, "Alice", user.DisplayName)
	assert.Equal(t, "alice@example.com", user.Email)
	assert.Equal(t, domain.CloudUserTypeVolcUser, user.UserType)
	assert.Equal(t, domain.CloudUserStatusActive, user.Status)
	assert.Equal(t, "2024-01-02T03:04:05Z", user.CreateTime.Format(time.RFC3339))
	assert.Equal(t, "2024-06-07T08:09:10Z", user.UpdateTime.Format(time.RFC3339))
	assert.Equal(t, user.CreateTime.Unix(), user.CTime)
	assert.NotNil(t, user.Metadata.LastSyncTime)
	assert.NotNil(t, user.Metadata.Tags)
}

func TestConvertVolcanoUserToCloudUserNilFields(t *testing.T) {
	// 全空字段不 panic:CloudUserID 回退为空,CreateDate 缺失回退为当前时间(与阿里云模板一致)
	user := ConvertVolcanoUserToCloudUser(&iamSDK.UserForGetUserOutput{}, convTestAccount)

	assert.Equal(t, "", user.Username)
	assert.Equal(t, "", user.CloudUserID)
	assert.False(t, user.CreateTime.IsZero(), "空 CreateDate 应回退为当前时间")
	assert.False(t, user.UpdateTime.IsZero())
}

func TestConvertVolcanoListUserToCloudUser(t *testing.T) {
	u := &iamSDK.UserMetadataForListUsersOutput{
		Id:          i32Ptr(100),
		UserName:    strPtr("bob"),
		DisplayName: strPtr("Bob"),
		Email:       strPtr("bob@example.com"),
		Tags: []*iamSDK.TagForListUsersOutput{
			{Key: strPtr("env"), Value: strPtr("prod")},
		},
	}

	user := ConvertVolcanoListUserToCloudUser(u, convTestAccount)

	assert.Equal(t, "100", user.CloudUserID)
	assert.Equal(t, "bob", user.Username)
	assert.Equal(t, "Bob", user.DisplayName)
	assert.Equal(t, "bob@example.com", user.Email)
	assert.Equal(t, domain.CloudUserTypeVolcUser, user.UserType)
	assert.Equal(t, domain.CloudProviderVolcano, user.Provider)
	assert.Equal(t, "prod", user.Metadata.Tags["env"])
}

func TestConvertVolcanoGroupUserToCloudUser(t *testing.T) {
	u := &iamSDK.UserForListUsersForGroupOutput{
		Id:          i32Ptr(55),
		UserName:    strPtr("carol"),
		DisplayName: strPtr("Carol"),
		JoinDate:    strPtr("2024-03-04T05:06:07Z"),
	}

	user := ConvertVolcanoGroupUserToCloudUser(u, convTestAccount)

	assert.Equal(t, "55", user.CloudUserID)
	assert.Equal(t, "carol", user.Username)
	assert.Equal(t, "Carol", user.DisplayName)
	assert.Equal(t, "2024-03-04T05:06:07Z", user.CreateTime.Format(time.RFC3339))
	assert.Equal(t, domain.CloudUserTypeVolcUser, user.UserType)
	assert.Equal(t, domain.CloudProviderVolcano, user.Provider)
}

func TestConvertVolcanoGroupToUserGroup(t *testing.T) {
	g := &iamSDK.UserGroupForGetGroupOutput{
		UserGroupID:   i32Ptr(9),
		UserGroupName: strPtr("admins"),
		DisplayName:   strPtr("Administrators"),
		Description:   strPtr("admin group"),
		CreateDate:    strPtr("2024-02-03T04:05:06Z"),
		UpdateDate:    strPtr("2024-07-08T09:10:11Z"),
	}

	group := ConvertVolcanoGroupToUserGroup(g, convTestAccount)

	assert.Equal(t, "9", group.CloudGroupID)
	assert.Equal(t, "admins", group.GroupName)
	assert.Equal(t, "admins", group.Name)
	assert.Equal(t, "Administrators", group.DisplayName)
	assert.Equal(t, "admin group", group.Description)
	assert.Equal(t, domain.CloudProviderVolcano, group.Provider)
	assert.Equal(t, int64(7), group.CloudAccountID)
	assert.Equal(t, int64(3), group.TenantID)
	assert.Equal(t, "2024-02-03T04:05:06Z", group.CreateTime.Format(time.RFC3339))
	assert.Equal(t, "2024-07-08T09:10:11Z", group.UpdateTime.Format(time.RFC3339))
	assert.NotNil(t, group.Policies)
	assert.Equal(t, 0, group.MemberCount)
}

func TestConvertVolcanoListGroupToUserGroup(t *testing.T) {
	g := &iamSDK.UserGroupForListGroupsOutput{
		UserGroupID:   i32Ptr(12),
		UserGroupName: strPtr("devs"),
		DisplayName:   strPtr("Developers"),
	}

	group := ConvertVolcanoListGroupToUserGroup(g, convTestAccount)

	assert.Equal(t, "12", group.CloudGroupID)
	assert.Equal(t, "devs", group.GroupName)
	assert.Equal(t, "Developers", group.DisplayName)
	assert.Equal(t, domain.CloudProviderVolcano, group.Provider)
}

func TestConvertVolcanoPolicyMetadataToPermissionPolicy(t *testing.T) {
	p := &iamSDK.PolicyMetadataForListPoliciesOutput{
		PolicyName:     strPtr("ReadOnlyAccess"),
		PolicyType:     strPtr("System"),
		PolicyDocument: strPtr("{\"Version\":\"2018-01-01\"}"),
		Description:    strPtr("read only"),
	}

	policy := ConvertVolcanoPolicyMetadataToPermissionPolicy(p)

	assert.Equal(t, "ReadOnlyAccess", policy.PolicyID)
	assert.Equal(t, "ReadOnlyAccess", policy.PolicyName)
	assert.Equal(t, "{\"Version\":\"2018-01-01\"}", policy.PolicyDocument)
	assert.Equal(t, domain.CloudProviderVolcano, policy.Provider)
	assert.Equal(t, domain.PolicyTypeSystem, policy.PolicyType)
}

func TestConvertVolcanoPolicyToPermissionPolicy(t *testing.T) {
	p := &iamSDK.PolicyForGetPolicyOutput{
		PolicyName:     strPtr("CustomFull"),
		PolicyType:     strPtr("Custom"),
		PolicyDocument: strPtr("{\"Statement\":[]}"),
	}

	policy := ConvertVolcanoPolicyToPermissionPolicy(p)

	assert.Equal(t, "CustomFull", policy.PolicyID)
	assert.Equal(t, "CustomFull", policy.PolicyName)
	assert.Equal(t, "{\"Statement\":[]}", policy.PolicyDocument)
	assert.Equal(t, domain.PolicyTypeCustom, policy.PolicyType)
	assert.Equal(t, domain.CloudProviderVolcano, policy.Provider)
}

func TestConvertPolicyType(t *testing.T) {
	cases := []struct {
		input    string
		expected domain.PolicyType
	}{
		{"System", domain.PolicyTypeSystem},
		{"Custom", domain.PolicyTypeCustom},
		{"", domain.PolicyTypeCustom},
		{"unknown", domain.PolicyTypeCustom},
	}
	for _, c := range cases {
		assert.Equal(t, c.expected, ConvertPolicyType(c.input), "input: %q", c.input)
	}
}

func TestPolicyTypeToVolcano(t *testing.T) {
	cases := []struct {
		input    domain.PolicyType
		expected string
	}{
		{domain.PolicyTypeSystem, "System"},
		{domain.PolicyTypeCustom, "Custom"},
		{"other", "Custom"},
		{"", "Custom"},
	}
	for _, c := range cases {
		assert.Equal(t, c.expected, PolicyTypeToVolcano(c.input), "input: %q", c.input)
	}
}

func TestParseVolcanoTime(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		expect time.Time
	}{
		{"RFC3339", "2024-01-02T03:04:05Z", time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)},
		{"空字符串", "", time.Time{}},
		{"非法格式", "not-a-time", time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseVolcanoTime(c.input)
			assert.True(t, got.Equal(c.expect), "got: %v, want: %v", got, c.expect)
		})
	}
}

func TestDiffPolicies(t *testing.T) {
	pA := domain.PermissionPolicy{PolicyID: "A", PolicyName: "A", Provider: domain.CloudProviderVolcano}
	pB := domain.PermissionPolicy{PolicyID: "B", PolicyName: "B", Provider: domain.CloudProviderVolcano}
	pC := domain.PermissionPolicy{PolicyID: "C", PolicyName: "C", Provider: domain.CloudProviderVolcano}
	pAliyun := domain.PermissionPolicy{PolicyID: "ALY", PolicyName: "ALY", Provider: domain.CloudProviderAliyun}

	t.Run("附加新增并分离移除", func(t *testing.T) {
		toAttach, toDetach := DiffPolicies(
			[]domain.PermissionPolicy{pA, pB},
			[]domain.PermissionPolicy{pB, pC},
			domain.CloudProviderVolcano,
		)
		assert.ElementsMatch(t, []domain.PermissionPolicy{pC}, toAttach)
		assert.ElementsMatch(t, []domain.PermissionPolicy{pA}, toDetach)
	})

	t.Run("目标中过滤非火山云策略", func(t *testing.T) {
		toAttach, toDetach := DiffPolicies(
			[]domain.PermissionPolicy{},
			[]domain.PermissionPolicy{pC, pAliyun},
			domain.CloudProviderVolcano,
		)
		assert.ElementsMatch(t, []domain.PermissionPolicy{pC}, toAttach)
		assert.Empty(t, toDetach)
	})

	t.Run("一致则无动作", func(t *testing.T) {
		toAttach, toDetach := DiffPolicies(
			[]domain.PermissionPolicy{pA, pB},
			[]domain.PermissionPolicy{pB, pA},
			domain.CloudProviderVolcano,
		)
		assert.Empty(t, toAttach)
		assert.Empty(t, toDetach)
	})

	t.Run("当前为空全部附加", func(t *testing.T) {
		toAttach, toDetach := DiffPolicies(nil, []domain.PermissionPolicy{pA, pC}, domain.CloudProviderVolcano)
		assert.ElementsMatch(t, []domain.PermissionPolicy{pA, pC}, toAttach)
		assert.Empty(t, toDetach)
	})
}
