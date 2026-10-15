package volcano

import (
	"context"
	"strings"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/domain"

	cloudxiam "github.com/Havens-blog/e-cloudx-sdk/iam"
	cloudxtypes "github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	iamSDK "github.com/volcengine/volcengine-go-sdk/service/iam"
	"github.com/volcengine/volcengine-go-sdk/volcengine/request"
	volcengineerr "github.com/volcengine/volcengine-go-sdk/volcengine/volcengineerr"
)

// fakeIAMClient mock 火山云 IAM SDK 客户端:仅注册测试关注的方法,
// 未注册方法(经嵌入的 nil iamAPI)调用会 panic,以此暴露意外调用(AC-4)。
type fakeIAMClient struct {
	iamAPI

	listUsersFn                 func(context.Context, *iamSDK.ListUsersInput) (*iamSDK.ListUsersOutput, error)
	getUserFn                   func(context.Context, *iamSDK.GetUserInput) (*iamSDK.GetUserOutput, error)
	createUserFn                func(context.Context, *iamSDK.CreateUserInput) (*iamSDK.CreateUserOutput, error)
	deleteUserFn                func(context.Context, *iamSDK.DeleteUserInput) (*iamSDK.DeleteUserOutput, error)
	listAccessKeysFn            func(context.Context, *iamSDK.ListAccessKeysInput) (*iamSDK.ListAccessKeysOutput, error)
	deleteAccessKeyFn           func(context.Context, *iamSDK.DeleteAccessKeyInput) (*iamSDK.DeleteAccessKeyOutput, error)
	listAttachedUserPoliciesFn  func(context.Context, *iamSDK.ListAttachedUserPoliciesInput) (*iamSDK.ListAttachedUserPoliciesOutput, error)
	attachUserPolicyFn          func(context.Context, *iamSDK.AttachUserPolicyInput) (*iamSDK.AttachUserPolicyOutput, error)
	detachUserPolicyFn          func(context.Context, *iamSDK.DetachUserPolicyInput) (*iamSDK.DetachUserPolicyOutput, error)
	listPoliciesFn              func(context.Context, *iamSDK.ListPoliciesInput) (*iamSDK.ListPoliciesOutput, error)
	listGroupsFn                func(context.Context, *iamSDK.ListGroupsInput) (*iamSDK.ListGroupsOutput, error)
	getGroupFn                  func(context.Context, *iamSDK.GetGroupInput) (*iamSDK.GetGroupOutput, error)
	createGroupFn               func(context.Context, *iamSDK.CreateGroupInput) (*iamSDK.CreateGroupOutput, error)
	deleteGroupFn               func(context.Context, *iamSDK.DeleteGroupInput) (*iamSDK.DeleteGroupOutput, error)
	listUsersForGroupFn         func(context.Context, *iamSDK.ListUsersForGroupInput) (*iamSDK.ListUsersForGroupOutput, error)
	listAttachedGroupPoliciesFn func(context.Context, *iamSDK.ListAttachedUserGroupPoliciesInput) (*iamSDK.ListAttachedUserGroupPoliciesOutput, error)
	attachUserGroupPolicyFn     func(context.Context, *iamSDK.AttachUserGroupPolicyInput) (*iamSDK.AttachUserGroupPolicyOutput, error)
	detachUserGroupPolicyFn     func(context.Context, *iamSDK.DetachUserGroupPolicyInput) (*iamSDK.DetachUserGroupPolicyOutput, error)
	addUserToGroupFn            func(context.Context, *iamSDK.AddUserToGroupInput) (*iamSDK.AddUserToGroupOutput, error)
	removeUserFromGroupFn       func(context.Context, *iamSDK.RemoveUserFromGroupInput) (*iamSDK.RemoveUserFromGroupOutput, error)
	getPolicyFn                 func(context.Context, *iamSDK.GetPolicyInput) (*iamSDK.GetPolicyOutput, error)
}

func (f *fakeIAMClient) ListUsersWithContext(ctx context.Context, input *iamSDK.ListUsersInput, opts ...request.Option) (*iamSDK.ListUsersOutput, error) {
	return f.listUsersFn(ctx, input)
}

func (f *fakeIAMClient) GetUserWithContext(ctx context.Context, input *iamSDK.GetUserInput, opts ...request.Option) (*iamSDK.GetUserOutput, error) {
	return f.getUserFn(ctx, input)
}

func (f *fakeIAMClient) CreateUserWithContext(ctx context.Context, input *iamSDK.CreateUserInput, opts ...request.Option) (*iamSDK.CreateUserOutput, error) {
	return f.createUserFn(ctx, input)
}

func (f *fakeIAMClient) DeleteUserWithContext(ctx context.Context, input *iamSDK.DeleteUserInput, opts ...request.Option) (*iamSDK.DeleteUserOutput, error) {
	return f.deleteUserFn(ctx, input)
}

func (f *fakeIAMClient) ListAccessKeysWithContext(ctx context.Context, input *iamSDK.ListAccessKeysInput, opts ...request.Option) (*iamSDK.ListAccessKeysOutput, error) {
	return f.listAccessKeysFn(ctx, input)
}

func (f *fakeIAMClient) DeleteAccessKeyWithContext(ctx context.Context, input *iamSDK.DeleteAccessKeyInput, opts ...request.Option) (*iamSDK.DeleteAccessKeyOutput, error) {
	return f.deleteAccessKeyFn(ctx, input)
}

func (f *fakeIAMClient) ListAttachedUserPoliciesWithContext(ctx context.Context, input *iamSDK.ListAttachedUserPoliciesInput, opts ...request.Option) (*iamSDK.ListAttachedUserPoliciesOutput, error) {
	return f.listAttachedUserPoliciesFn(ctx, input)
}

func (f *fakeIAMClient) AttachUserPolicyWithContext(ctx context.Context, input *iamSDK.AttachUserPolicyInput, opts ...request.Option) (*iamSDK.AttachUserPolicyOutput, error) {
	return f.attachUserPolicyFn(ctx, input)
}

func (f *fakeIAMClient) DetachUserPolicyWithContext(ctx context.Context, input *iamSDK.DetachUserPolicyInput, opts ...request.Option) (*iamSDK.DetachUserPolicyOutput, error) {
	return f.detachUserPolicyFn(ctx, input)
}

func (f *fakeIAMClient) ListPoliciesWithContext(ctx context.Context, input *iamSDK.ListPoliciesInput, opts ...request.Option) (*iamSDK.ListPoliciesOutput, error) {
	return f.listPoliciesFn(ctx, input)
}

func (f *fakeIAMClient) ListGroupsWithContext(ctx context.Context, input *iamSDK.ListGroupsInput, opts ...request.Option) (*iamSDK.ListGroupsOutput, error) {
	return f.listGroupsFn(ctx, input)
}

func (f *fakeIAMClient) GetGroupWithContext(ctx context.Context, input *iamSDK.GetGroupInput, opts ...request.Option) (*iamSDK.GetGroupOutput, error) {
	return f.getGroupFn(ctx, input)
}

func (f *fakeIAMClient) CreateGroupWithContext(ctx context.Context, input *iamSDK.CreateGroupInput, opts ...request.Option) (*iamSDK.CreateGroupOutput, error) {
	return f.createGroupFn(ctx, input)
}

func (f *fakeIAMClient) DeleteGroupWithContext(ctx context.Context, input *iamSDK.DeleteGroupInput, opts ...request.Option) (*iamSDK.DeleteGroupOutput, error) {
	return f.deleteGroupFn(ctx, input)
}

func (f *fakeIAMClient) ListUsersForGroupWithContext(ctx context.Context, input *iamSDK.ListUsersForGroupInput, opts ...request.Option) (*iamSDK.ListUsersForGroupOutput, error) {
	return f.listUsersForGroupFn(ctx, input)
}

func (f *fakeIAMClient) ListAttachedUserGroupPoliciesWithContext(ctx context.Context, input *iamSDK.ListAttachedUserGroupPoliciesInput, opts ...request.Option) (*iamSDK.ListAttachedUserGroupPoliciesOutput, error) {
	return f.listAttachedGroupPoliciesFn(ctx, input)
}

func (f *fakeIAMClient) AttachUserGroupPolicyWithContext(ctx context.Context, input *iamSDK.AttachUserGroupPolicyInput, opts ...request.Option) (*iamSDK.AttachUserGroupPolicyOutput, error) {
	return f.attachUserGroupPolicyFn(ctx, input)
}

func (f *fakeIAMClient) DetachUserGroupPolicyWithContext(ctx context.Context, input *iamSDK.DetachUserGroupPolicyInput, opts ...request.Option) (*iamSDK.DetachUserGroupPolicyOutput, error) {
	return f.detachUserGroupPolicyFn(ctx, input)
}

func (f *fakeIAMClient) AddUserToGroupWithContext(ctx context.Context, input *iamSDK.AddUserToGroupInput, opts ...request.Option) (*iamSDK.AddUserToGroupOutput, error) {
	return f.addUserToGroupFn(ctx, input)
}

func (f *fakeIAMClient) RemoveUserFromGroupWithContext(ctx context.Context, input *iamSDK.RemoveUserFromGroupInput, opts ...request.Option) (*iamSDK.RemoveUserFromGroupOutput, error) {
	return f.removeUserFromGroupFn(ctx, input)
}

func (f *fakeIAMClient) GetPolicyWithContext(ctx context.Context, input *iamSDK.GetPolicyInput, opts ...request.Option) (*iamSDK.GetPolicyOutput, error) {
	return f.getPolicyFn(ctx, input)
}

// newTestAdapter 构造注入 mock 客户端的适配器。
func newTestAdapter(t *testing.T, client *fakeIAMClient) *Adapter {
	t.Helper()
	a := NewAdapter(elog.DefaultLogger)
	a.newClient = func(account *domain.CloudAccount) (iamAPI, error) {
		return client, nil
	}
	return a
}

// sdkError 构造火山云 SDK 错误(非限流类,避免触发重试退避)。
func sdkError(code, msg string) error {
	return volcengineerr.New(code, msg, nil)
}

var adapterTestAccount = &domain.CloudAccount{
	ID:              11,
	TenantID:        5,
	Provider:        domain.CloudProviderVolcano,
	AccessKeyID:     "test-ak",
	AccessKeySecret: "test-sk",
}

// init() 注册(AC-1 工厂路径):volcano/volcengine 标识均可用,创建的适配器实现 CloudIAMAdapter。
func TestIAMAdapterRegistration(t *testing.T) {
	for _, provider := range []domain.CloudProvider{domain.CloudProviderVolcano, domain.CloudProviderVolcengine} {
		creator, err := cloudxiam.GetIAMAdapterCreator(provider)
		require.NoError(t, err, "provider: %s", provider)
		require.NotNil(t, creator)

		ad, err := creator(elog.DefaultLogger)
		require.NoError(t, err)
		require.NotNil(t, ad)
	}
}

// AC-3: 空凭证经由客户端工厂被识别,返回明确错误而非 not implemented。
func TestValidateCredentialsEmptyAccessKey(t *testing.T) {
	a := NewAdapter(elog.DefaultLogger)
	// newClient 为 nil → 走真实 CreateIAMClient;空 AK/SK 直接报错,不发起网络请求
	account := &domain.CloudAccount{ID: 1, TenantID: 1}

	err := a.ValidateCredentials(context.Background(), account)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
	assert.NotContains(t, err.Error(), "not implemented")
}

// AC-3: SDK 凭证错误(SignatureDoesNotMatch)被识别并包装为明确错误。
func TestValidateCredentialsSDKError(t *testing.T) {
	client := &fakeIAMClient{
		listUsersFn: func(ctx context.Context, input *iamSDK.ListUsersInput) (*iamSDK.ListUsersOutput, error) {
			return nil, sdkError("SignatureDoesNotMatch", "signature mismatch")
		},
	}
	a := newTestAdapter(t, client)

	err := a.ValidateCredentials(context.Background(), adapterTestAccount)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid volcano credentials")
	assert.Contains(t, err.Error(), "SignatureDoesNotMatch")
}

func TestValidateCredentialsSuccess(t *testing.T) {
	var gotLimit *int32
	client := &fakeIAMClient{
		listUsersFn: func(ctx context.Context, input *iamSDK.ListUsersInput) (*iamSDK.ListUsersOutput, error) {
			gotLimit = input.Limit
			return &iamSDK.ListUsersOutput{}, nil
		},
	}
	a := newTestAdapter(t, client)

	err := a.ValidateCredentials(context.Background(), adapterTestAccount)

	require.NoError(t, err)
	require.NotNil(t, gotLimit)
	assert.Equal(t, int32(1), *gotLimit, "凭证校验应使用最轻量的 ListUsers(Limit=1)")
}

func TestListUsersPagination(t *testing.T) {
	var offsets []int32
	client := &fakeIAMClient{
		listUsersFn: func(ctx context.Context, input *iamSDK.ListUsersInput) (*iamSDK.ListUsersOutput, error) {
			offsets = append(offsets, *input.Offset)
			if *input.Offset == 0 {
				total := int32(3)
				return &iamSDK.ListUsersOutput{
					Total: &total,
					UserMetadata: []*iamSDK.UserMetadataForListUsersOutput{
						{Id: i32Ptr(1), UserName: strPtr("u1")},
						{Id: i32Ptr(2), UserName: strPtr("u2")},
					},
				}, nil
			}
			total := int32(3)
			return &iamSDK.ListUsersOutput{
				Total: &total,
				UserMetadata: []*iamSDK.UserMetadataForListUsersOutput{
					{Id: i32Ptr(3), UserName: strPtr("u3")},
				},
			}, nil
		},
	}
	a := newTestAdapter(t, client)

	users, err := a.ListUsers(context.Background(), adapterTestAccount)

	require.NoError(t, err)
	require.Len(t, users, 3)
	assert.Equal(t, "u1", users[0].Username)
	assert.Equal(t, "1", users[0].CloudUserID)
	assert.Equal(t, "u3", users[2].Username)
	assert.Equal(t, []int32{0, 2}, offsets, "分页应按 Limit 前进 offset")
}

func TestListUsersSDKError(t *testing.T) {
	client := &fakeIAMClient{
		listUsersFn: func(ctx context.Context, input *iamSDK.ListUsersInput) (*iamSDK.ListUsersOutput, error) {
			return nil, sdkError("InternalError", "boom")
		},
	}
	a := newTestAdapter(t, client)

	users, err := a.ListUsers(context.Background(), adapterTestAccount)

	require.Error(t, err)
	assert.Nil(t, users)
	assert.Contains(t, err.Error(), "failed to list volcano IAM users")
	assert.Contains(t, err.Error(), "InternalError")
	assert.NotContains(t, err.Error(), "not implemented")
}

func TestGetUser(t *testing.T) {
	client := &fakeIAMClient{
		getUserFn: func(ctx context.Context, input *iamSDK.GetUserInput) (*iamSDK.GetUserOutput, error) {
			assert.Equal(t, "alice", *input.UserName)
			return &iamSDK.GetUserOutput{
				User: &iamSDK.UserForGetUserOutput{Id: i32Ptr(42), UserName: strPtr("alice"), DisplayName: strPtr("Alice")},
			}, nil
		},
	}
	a := newTestAdapter(t, client)

	user, err := a.GetUser(context.Background(), adapterTestAccount, "alice")

	require.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, "alice", user.Username)
	assert.Equal(t, "42", user.CloudUserID)
	assert.Equal(t, domain.CloudProviderVolcano, user.Provider)
}

func TestGetUserSDKError(t *testing.T) {
	client := &fakeIAMClient{
		getUserFn: func(ctx context.Context, input *iamSDK.GetUserInput) (*iamSDK.GetUserOutput, error) {
			return nil, sdkError("UserNotFound", "no such user")
		},
	}
	a := newTestAdapter(t, client)

	user, err := a.GetUser(context.Background(), adapterTestAccount, "ghost")

	require.Error(t, err)
	assert.Nil(t, user)
	assert.Contains(t, err.Error(), "failed to get volcano IAM user")
}

func TestCreateUser(t *testing.T) {
	client := &fakeIAMClient{
		createUserFn: func(ctx context.Context, input *iamSDK.CreateUserInput) (*iamSDK.CreateUserOutput, error) {
			assert.Equal(t, "dave", *input.UserName)
			assert.Equal(t, "Dave", *input.DisplayName)
			assert.Equal(t, "dave@example.com", *input.Email)
			return &iamSDK.CreateUserOutput{
				User: &iamSDK.UserForCreateUserOutput{Id: i32Ptr(77), UserName: strPtr("dave"), DisplayName: strPtr("Dave")},
			}, nil
		},
	}
	a := newTestAdapter(t, client)

	user, err := a.CreateUser(context.Background(), adapterTestAccount, &CreateUserParams{
		Username:    "dave",
		DisplayName: "Dave",
		Email:       "dave@example.com",
	})

	require.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, "dave", user.Username)
	assert.Equal(t, "77", user.CloudUserID)
}

func TestCreateUserSDKError(t *testing.T) {
	client := &fakeIAMClient{
		createUserFn: func(ctx context.Context, input *iamSDK.CreateUserInput) (*iamSDK.CreateUserOutput, error) {
			return nil, sdkError("UserAlreadyExists", "duplicate")
		},
	}
	a := newTestAdapter(t, client)

	user, err := a.CreateUser(context.Background(), adapterTestAccount, &CreateUserParams{Username: "dave"})

	require.Error(t, err)
	assert.Nil(t, user)
	assert.Contains(t, err.Error(), "failed to create volcano IAM user")
}

// DeleteUser 应先清理 AccessKey(尽力而为)再删除用户。
func TestDeleteUserRemovesAccessKeysFirst(t *testing.T) {
	var deletedAKs []string
	deletedUser := ""
	client := &fakeIAMClient{
		listAccessKeysFn: func(ctx context.Context, input *iamSDK.ListAccessKeysInput) (*iamSDK.ListAccessKeysOutput, error) {
			assert.Equal(t, "dave", *input.UserName)
			return &iamSDK.ListAccessKeysOutput{
				AccessKeyMetadata: []*iamSDK.AccessKeyMetadataForListAccessKeysOutput{
					{AccessKeyId: strPtr("AK1")},
					{AccessKeyId: strPtr("AK2")},
				},
			}, nil
		},
		deleteAccessKeyFn: func(ctx context.Context, input *iamSDK.DeleteAccessKeyInput) (*iamSDK.DeleteAccessKeyOutput, error) {
			deletedAKs = append(deletedAKs, *input.AccessKeyId)
			return &iamSDK.DeleteAccessKeyOutput{}, nil
		},
		deleteUserFn: func(ctx context.Context, input *iamSDK.DeleteUserInput) (*iamSDK.DeleteUserOutput, error) {
			deletedUser = *input.UserName
			return &iamSDK.DeleteUserOutput{}, nil
		},
	}
	a := newTestAdapter(t, client)

	err := a.DeleteUser(context.Background(), adapterTestAccount, "dave")

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"AK1", "AK2"}, deletedAKs)
	assert.Equal(t, "dave", deletedUser)
}

func TestDeleteUserSDKError(t *testing.T) {
	client := &fakeIAMClient{
		listAccessKeysFn: func(ctx context.Context, input *iamSDK.ListAccessKeysInput) (*iamSDK.ListAccessKeysOutput, error) {
			return &iamSDK.ListAccessKeysOutput{}, nil
		},
		deleteUserFn: func(ctx context.Context, input *iamSDK.DeleteUserInput) (*iamSDK.DeleteUserOutput, error) {
			return nil, sdkError("InternalError", "boom")
		},
	}
	a := newTestAdapter(t, client)

	err := a.DeleteUser(context.Background(), adapterTestAccount, "dave")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete volcano IAM user")
}

// UpdateUserPermissions 应按差集附加/分离,并过滤非火山云策略(AC-1 权限同步)。
func TestUpdateUserPermissionsDiff(t *testing.T) {
	var attached, detached []string
	client := &fakeIAMClient{
		listAttachedUserPoliciesFn: func(ctx context.Context, input *iamSDK.ListAttachedUserPoliciesInput) (*iamSDK.ListAttachedUserPoliciesOutput, error) {
			assert.Equal(t, "dave", *input.UserName)
			return &iamSDK.ListAttachedUserPoliciesOutput{
				AttachedPolicyMetadata: []*iamSDK.AttachedPolicyMetadataForListAttachedUserPoliciesOutput{
					{PolicyName: strPtr("OldA"), PolicyType: strPtr("System")},
					{PolicyName: strPtr("KeepB"), PolicyType: strPtr("Custom")},
				},
			}, nil
		},
		attachUserPolicyFn: func(ctx context.Context, input *iamSDK.AttachUserPolicyInput) (*iamSDK.AttachUserPolicyOutput, error) {
			attached = append(attached, *input.PolicyName)
			assert.Equal(t, "dave", *input.UserName)
			return &iamSDK.AttachUserPolicyOutput{}, nil
		},
		detachUserPolicyFn: func(ctx context.Context, input *iamSDK.DetachUserPolicyInput) (*iamSDK.DetachUserPolicyOutput, error) {
			detached = append(detached, *input.PolicyName)
			return &iamSDK.DetachUserPolicyOutput{}, nil
		},
	}
	a := newTestAdapter(t, client)

	err := a.UpdateUserPermissions(context.Background(), adapterTestAccount, "dave", []domain.PermissionPolicy{
		{PolicyID: "KeepB", PolicyName: "KeepB", Provider: domain.CloudProviderVolcano, PolicyType: domain.PolicyTypeCustom},
		{PolicyID: "NewC", PolicyName: "NewC", Provider: domain.CloudProviderVolcano, PolicyType: domain.PolicyTypeSystem},
		{PolicyID: "ALY", PolicyName: "ALY", Provider: domain.CloudProviderAliyun, PolicyType: domain.PolicyTypeSystem},
	})

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"NewC"}, attached)
	assert.ElementsMatch(t, []string{"OldA"}, detached)
}

func TestUpdateUserPermissionsAttachError(t *testing.T) {
	client := &fakeIAMClient{
		listAttachedUserPoliciesFn: func(ctx context.Context, input *iamSDK.ListAttachedUserPoliciesInput) (*iamSDK.ListAttachedUserPoliciesOutput, error) {
			return &iamSDK.ListAttachedUserPoliciesOutput{}, nil
		},
		attachUserPolicyFn: func(ctx context.Context, input *iamSDK.AttachUserPolicyInput) (*iamSDK.AttachUserPolicyOutput, error) {
			return nil, sdkError("NoSuchPolicy", "policy missing")
		},
	}
	a := newTestAdapter(t, client)

	err := a.UpdateUserPermissions(context.Background(), adapterTestAccount, "dave", []domain.PermissionPolicy{
		{PolicyID: "NewC", PolicyName: "NewC", Provider: domain.CloudProviderVolcano, PolicyType: domain.PolicyTypeSystem},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to attach policy NewC")
}

func TestGetUserPolicies(t *testing.T) {
	client := &fakeIAMClient{
		listAttachedUserPoliciesFn: func(ctx context.Context, input *iamSDK.ListAttachedUserPoliciesInput) (*iamSDK.ListAttachedUserPoliciesOutput, error) {
			assert.Equal(t, "dave", *input.UserName)
			return &iamSDK.ListAttachedUserPoliciesOutput{
				AttachedPolicyMetadata: []*iamSDK.AttachedPolicyMetadataForListAttachedUserPoliciesOutput{
					{PolicyName: strPtr("ReadOnly"), PolicyType: strPtr("System")},
					{PolicyName: strPtr("CustomOps"), PolicyType: strPtr("Custom")},
				},
			}, nil
		},
	}
	a := newTestAdapter(t, client)

	policies, err := a.GetUserPolicies(context.Background(), adapterTestAccount, "dave")

	require.NoError(t, err)
	require.Len(t, policies, 2)
	assert.Equal(t, "ReadOnly", policies[0].PolicyName)
	assert.Equal(t, domain.PolicyTypeSystem, policies[0].PolicyType)
	assert.Equal(t, domain.PolicyTypeCustom, policies[1].PolicyType)
	assert.Equal(t, domain.CloudProviderVolcano, policies[0].Provider)
}

func TestListPolicies(t *testing.T) {
	client := &fakeIAMClient{
		listPoliciesFn: func(ctx context.Context, input *iamSDK.ListPoliciesInput) (*iamSDK.ListPoliciesOutput, error) {
			assert.Equal(t, int32(0), *input.Offset)
			return &iamSDK.ListPoliciesOutput{
				PolicyMetadata: []*iamSDK.PolicyMetadataForListPoliciesOutput{
					{PolicyName: strPtr("ReadOnlyAccess"), PolicyType: strPtr("System")},
					{PolicyName: strPtr("CustomOps"), PolicyType: strPtr("Custom")},
				},
			}, nil
		},
	}
	a := newTestAdapter(t, client)

	policies, err := a.ListPolicies(context.Background(), adapterTestAccount)

	require.NoError(t, err)
	require.Len(t, policies, 2)
	assert.Equal(t, "ReadOnlyAccess", policies[0].PolicyID)
	assert.Equal(t, domain.PolicyTypeSystem, policies[0].PolicyType)
}

func TestListPoliciesSDKError(t *testing.T) {
	client := &fakeIAMClient{
		listPoliciesFn: func(ctx context.Context, input *iamSDK.ListPoliciesInput) (*iamSDK.ListPoliciesOutput, error) {
			return nil, sdkError("InternalError", "boom")
		},
	}
	a := newTestAdapter(t, client)

	policies, err := a.ListPolicies(context.Background(), adapterTestAccount)

	require.Error(t, err)
	assert.Nil(t, policies)
	assert.Contains(t, err.Error(), "failed to list volcano IAM policies")
}

func TestListGroups(t *testing.T) {
	client := &fakeIAMClient{
		listGroupsFn: func(ctx context.Context, input *iamSDK.ListGroupsInput) (*iamSDK.ListGroupsOutput, error) {
			return &iamSDK.ListGroupsOutput{
				UserGroups: []*iamSDK.UserGroupForListGroupsOutput{
					{UserGroupID: i32Ptr(9), UserGroupName: strPtr("admins")},
				},
			}, nil
		},
		listAttachedGroupPoliciesFn: func(ctx context.Context, input *iamSDK.ListAttachedUserGroupPoliciesInput) (*iamSDK.ListAttachedUserGroupPoliciesOutput, error) {
			assert.Equal(t, "admins", *input.UserGroupName)
			return &iamSDK.ListAttachedUserGroupPoliciesOutput{
				AttachedPolicyMetadata: []*iamSDK.AttachedPolicyMetadataForListAttachedUserGroupPoliciesOutput{
					{PolicyName: strPtr("ReadOnly"), PolicyType: strPtr("System")},
				},
			}, nil
		},
	}
	a := newTestAdapter(t, client)

	groups, err := a.ListGroups(context.Background(), adapterTestAccount)

	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, "9", groups[0].CloudGroupID)
	assert.Equal(t, "admins", groups[0].GroupName)
	require.Len(t, groups[0].Policies, 1)
	assert.Equal(t, "ReadOnly", groups[0].Policies[0].PolicyName)
}

func TestGetGroup(t *testing.T) {
	client := &fakeIAMClient{
		getGroupFn: func(ctx context.Context, input *iamSDK.GetGroupInput) (*iamSDK.GetGroupOutput, error) {
			assert.Equal(t, "admins", *input.UserGroupName)
			return &iamSDK.GetGroupOutput{
				UserGroup: &iamSDK.UserGroupForGetGroupOutput{UserGroupID: i32Ptr(9), UserGroupName: strPtr("admins")},
			}, nil
		},
		listAttachedGroupPoliciesFn: func(ctx context.Context, input *iamSDK.ListAttachedUserGroupPoliciesInput) (*iamSDK.ListAttachedUserGroupPoliciesOutput, error) {
			return &iamSDK.ListAttachedUserGroupPoliciesOutput{
				AttachedPolicyMetadata: []*iamSDK.AttachedPolicyMetadataForListAttachedUserGroupPoliciesOutput{
					{PolicyName: strPtr("ReadOnly"), PolicyType: strPtr("System")},
				},
			}, nil
		},
		listUsersForGroupFn: func(ctx context.Context, input *iamSDK.ListUsersForGroupInput) (*iamSDK.ListUsersForGroupOutput, error) {
			return &iamSDK.ListUsersForGroupOutput{
				Users: []*iamSDK.UserForListUsersForGroupOutput{
					{Id: i32Ptr(1), UserName: strPtr("u1")},
					{Id: i32Ptr(2), UserName: strPtr("u2")},
				},
			}, nil
		},
	}
	a := newTestAdapter(t, client)

	group, err := a.GetGroup(context.Background(), adapterTestAccount, "admins")

	require.NoError(t, err)
	require.NotNil(t, group)
	assert.Equal(t, "admins", group.GroupName)
	require.Len(t, group.Policies, 1)
	assert.Equal(t, 2, group.MemberCount)
}

func TestCreateGroup(t *testing.T) {
	client := &fakeIAMClient{
		createGroupFn: func(ctx context.Context, input *iamSDK.CreateGroupInput) (*iamSDK.CreateGroupOutput, error) {
			assert.Equal(t, "devs", *input.UserGroupName)
			assert.Equal(t, "Developers", *input.DisplayName)
			assert.Equal(t, "dev group", *input.Description)
			return &iamSDK.CreateGroupOutput{
				UserGroup: &iamSDK.UserGroupForCreateGroupOutput{UserGroupID: i32Ptr(20), UserGroupName: strPtr("devs")},
			}, nil
		},
	}
	a := newTestAdapter(t, client)

	group, err := a.CreateGroup(context.Background(), adapterTestAccount, &cloudxtypes.CreateGroupRequest{
		GroupName:   "devs",
		DisplayName: "Developers",
		Description: "dev group",
	})

	require.NoError(t, err)
	require.NotNil(t, group)
	assert.Equal(t, "devs", group.GroupName)
	assert.Equal(t, "20", group.CloudGroupID)
}

func TestUpdateGroupPoliciesDiff(t *testing.T) {
	var attached, detached []string
	client := &fakeIAMClient{
		listAttachedGroupPoliciesFn: func(ctx context.Context, input *iamSDK.ListAttachedUserGroupPoliciesInput) (*iamSDK.ListAttachedUserGroupPoliciesOutput, error) {
			return &iamSDK.ListAttachedUserGroupPoliciesOutput{}, nil
		},
		attachUserGroupPolicyFn: func(ctx context.Context, input *iamSDK.AttachUserGroupPolicyInput) (*iamSDK.AttachUserGroupPolicyOutput, error) {
			attached = append(attached, *input.PolicyName)
			assert.Equal(t, "admins", *input.UserGroupName)
			return &iamSDK.AttachUserGroupPolicyOutput{}, nil
		},
		detachUserGroupPolicyFn: func(ctx context.Context, input *iamSDK.DetachUserGroupPolicyInput) (*iamSDK.DetachUserGroupPolicyOutput, error) {
			detached = append(detached, *input.PolicyName)
			return &iamSDK.DetachUserGroupPolicyOutput{}, nil
		},
	}
	a := newTestAdapter(t, client)

	err := a.UpdateGroupPolicies(context.Background(), adapterTestAccount, "admins", []domain.PermissionPolicy{
		{PolicyID: "NewP", PolicyName: "NewP", Provider: domain.CloudProviderVolcano, PolicyType: domain.PolicyTypeCustom},
	})

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"NewP"}, attached)
	assert.Empty(t, detached)
}

// DeleteGroup 应先移除成员、分离策略(尽力而为),再删除用户组。
func TestDeleteGroupCleansMembersAndPolicies(t *testing.T) {
	var removedUsers, detachedPolicies, deletedGroups []string
	client := &fakeIAMClient{
		listUsersForGroupFn: func(ctx context.Context, input *iamSDK.ListUsersForGroupInput) (*iamSDK.ListUsersForGroupOutput, error) {
			return &iamSDK.ListUsersForGroupOutput{
				Users: []*iamSDK.UserForListUsersForGroupOutput{
					{Id: i32Ptr(1), UserName: strPtr("u1")},
				},
			}, nil
		},
		removeUserFromGroupFn: func(ctx context.Context, input *iamSDK.RemoveUserFromGroupInput) (*iamSDK.RemoveUserFromGroupOutput, error) {
			removedUsers = append(removedUsers, *input.UserName)
			assert.Equal(t, "admins", *input.UserGroupName)
			return &iamSDK.RemoveUserFromGroupOutput{}, nil
		},
		listAttachedGroupPoliciesFn: func(ctx context.Context, input *iamSDK.ListAttachedUserGroupPoliciesInput) (*iamSDK.ListAttachedUserGroupPoliciesOutput, error) {
			return &iamSDK.ListAttachedUserGroupPoliciesOutput{
				AttachedPolicyMetadata: []*iamSDK.AttachedPolicyMetadataForListAttachedUserGroupPoliciesOutput{
					{PolicyName: strPtr("ReadOnly"), PolicyType: strPtr("System")},
				},
			}, nil
		},
		detachUserGroupPolicyFn: func(ctx context.Context, input *iamSDK.DetachUserGroupPolicyInput) (*iamSDK.DetachUserGroupPolicyOutput, error) {
			detachedPolicies = append(detachedPolicies, *input.PolicyName)
			return &iamSDK.DetachUserGroupPolicyOutput{}, nil
		},
		deleteGroupFn: func(ctx context.Context, input *iamSDK.DeleteGroupInput) (*iamSDK.DeleteGroupOutput, error) {
			deletedGroups = append(deletedGroups, *input.UserGroupName)
			return &iamSDK.DeleteGroupOutput{}, nil
		},
	}
	a := newTestAdapter(t, client)

	err := a.DeleteGroup(context.Background(), adapterTestAccount, "admins")

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"u1"}, removedUsers)
	assert.ElementsMatch(t, []string{"ReadOnly"}, detachedPolicies)
	assert.ElementsMatch(t, []string{"admins"}, deletedGroups)
}

func TestListGroupUsers(t *testing.T) {
	client := &fakeIAMClient{
		listUsersForGroupFn: func(ctx context.Context, input *iamSDK.ListUsersForGroupInput) (*iamSDK.ListUsersForGroupOutput, error) {
			assert.Equal(t, "admins", *input.UserGroupName)
			return &iamSDK.ListUsersForGroupOutput{
				Users: []*iamSDK.UserForListUsersForGroupOutput{
					{Id: i32Ptr(1), UserName: strPtr("u1")},
					{Id: i32Ptr(2), UserName: strPtr("u2")},
				},
			}, nil
		},
	}
	a := newTestAdapter(t, client)

	users, err := a.ListGroupUsers(context.Background(), adapterTestAccount, "admins")

	require.NoError(t, err)
	require.Len(t, users, 2)
	assert.Equal(t, "u1", users[0].Username)
	assert.Equal(t, "1", users[0].CloudUserID)
}

func TestAddAndRemoveUserFromGroup(t *testing.T) {
	var added, removed [2]string
	client := &fakeIAMClient{
		addUserToGroupFn: func(ctx context.Context, input *iamSDK.AddUserToGroupInput) (*iamSDK.AddUserToGroupOutput, error) {
			added = [2]string{*input.UserGroupName, *input.UserName}
			return &iamSDK.AddUserToGroupOutput{}, nil
		},
		removeUserFromGroupFn: func(ctx context.Context, input *iamSDK.RemoveUserFromGroupInput) (*iamSDK.RemoveUserFromGroupOutput, error) {
			removed = [2]string{*input.UserGroupName, *input.UserName}
			return &iamSDK.RemoveUserFromGroupOutput{}, nil
		},
	}
	a := newTestAdapter(t, client)

	require.NoError(t, a.AddUserToGroup(context.Background(), adapterTestAccount, "admins", "dave"))
	assert.Equal(t, [2]string{"admins", "dave"}, added)

	require.NoError(t, a.RemoveUserFromGroup(context.Background(), adapterTestAccount, "admins", "dave"))
	assert.Equal(t, [2]string{"admins", "dave"}, removed)
}

// GetPolicy 应先查 System 再回退 Custom,类型枚举使用火山云大写形式。
func TestGetPolicySystemFallback(t *testing.T) {
	var queriedTypes []string
	client := &fakeIAMClient{
		getPolicyFn: func(ctx context.Context, input *iamSDK.GetPolicyInput) (*iamSDK.GetPolicyOutput, error) {
			queriedTypes = append(queriedTypes, *input.PolicyType)
			if *input.PolicyType == "Custom" {
				return &iamSDK.GetPolicyOutput{
					Policy: &iamSDK.PolicyForGetPolicyOutput{
						PolicyName:     strPtr("CustomOps"),
						PolicyType:     strPtr("Custom"),
						PolicyDocument: strPtr("{}"),
					},
				}, nil
			}
			return nil, sdkError("PolicyNotFound", "not found in system")
		},
	}
	a := newTestAdapter(t, client)

	policy, err := a.GetPolicy(context.Background(), adapterTestAccount, "CustomOps")

	require.NoError(t, err)
	require.NotNil(t, policy)
	assert.Equal(t, "CustomOps", policy.PolicyName)
	assert.Equal(t, domain.PolicyTypeCustom, policy.PolicyType)
	assert.Equal(t, []string{"System", "Custom"}, queriedTypes)
}

func TestGetPolicyNotFound(t *testing.T) {
	client := &fakeIAMClient{
		getPolicyFn: func(ctx context.Context, input *iamSDK.GetPolicyInput) (*iamSDK.GetPolicyOutput, error) {
			return nil, sdkError("PolicyNotFound", "not found")
		},
	}
	a := newTestAdapter(t, client)

	policy, err := a.GetPolicy(context.Background(), adapterTestAccount, "ghost")

	require.Error(t, err)
	assert.Nil(t, policy)
	assert.True(t, strings.Contains(err.Error(), "failed to get volcano IAM policy"), "err: %v", err)
}
