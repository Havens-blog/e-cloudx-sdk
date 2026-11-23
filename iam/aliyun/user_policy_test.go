package aliyun

import (
	"context"
	"errors"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	cloudxiam "github.com/Havens-blog/e-cloudx-sdk/iam"
	sdkerrors "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ram"
	"github.com/gotomicro/ego/core/elog"
)

// fakeRAMClient 只实现 ramUserClient 需要的方法，用于在不访问真实云平台的前提下
// 验证 UserId -> UserName 解析逻辑。
type fakeRAMClient struct {
	// listUserPages 按调用顺序返回，每页一个响应。
	listUserPages []*ram.ListUsersResponse
	listUsersErr  error
	listUsersCall int

	getUserResp  *ram.GetUserResponse
	getUserErr   error
	getUserNames []string

	policyResp    *ram.ListPoliciesForUserResponse
	policyErr     error
	policyUserIDs []string

	accessKeyCalls int
}

func (f *fakeRAMClient) ListUsers(_ *ram.ListUsersRequest) (*ram.ListUsersResponse, error) {
	f.listUsersCall++
	if f.listUsersErr != nil {
		return nil, f.listUsersErr
	}
	if len(f.listUserPages) == 0 {
		return &ram.ListUsersResponse{}, nil
	}
	index := f.listUsersCall - 1
	if index >= len(f.listUserPages) {
		index = len(f.listUserPages) - 1
	}
	return f.listUserPages[index], nil
}

func (f *fakeRAMClient) GetUser(request *ram.GetUserRequest) (*ram.GetUserResponse, error) {
	f.getUserNames = append(f.getUserNames, request.UserName)
	if f.getUserErr != nil {
		return nil, f.getUserErr
	}
	if f.getUserResp == nil {
		return &ram.GetUserResponse{}, nil
	}
	return f.getUserResp, nil
}

func (f *fakeRAMClient) ListAccessKeys(_ *ram.ListAccessKeysRequest) (*ram.ListAccessKeysResponse, error) {
	f.accessKeyCalls++
	return &ram.ListAccessKeysResponse{}, nil
}

func (f *fakeRAMClient) ListPoliciesForUser(request *ram.ListPoliciesForUserRequest) (*ram.ListPoliciesForUserResponse, error) {
	f.policyUserIDs = append(f.policyUserIDs, request.UserName)
	if f.policyErr != nil {
		return nil, f.policyErr
	}
	if f.policyResp == nil {
		return &ram.ListPoliciesForUserResponse{}, nil
	}
	return f.policyResp, nil
}

func newTestAdapter() *Adapter {
	return &Adapter{
		logger:      elog.DefaultLogger,
		rateLimiter: nil,
	}
}

func testAccount() *domain.CloudAccount {
	return &domain.CloudAccount{ID: 1, Provider: domain.CloudProviderAliyun, TenantID: 3}
}

func userNotFoundError() error {
	return sdkerrors.NewServerError(404, `{"Code":"EntityNotExist.User","Message":"The user does not exist."}`, "")
}

// TestGetUserPoliciesResolvesNumericUserID 覆盖本次缺陷：调用方传的是 ListUsers 下发的
// 数值型 UserId，而 ListPoliciesForUser 只接受 UserName。
func TestGetUserPoliciesResolvesNumericUserID(t *testing.T) {
	client := &fakeRAMClient{
		listUserPages: []*ram.ListUsersResponse{
			{
				IsTruncated: true,
				Marker:      "page-2",
				Users: ram.UsersInListUsers{User: []ram.User{
					{UserId: "261555089280165526", UserName: "aliyun_2019"},
				}},
			},
			{
				IsTruncated: false,
				Users: ram.UsersInListUsers{User: []ram.User{
					{UserId: "248996265577798116", UserName: "zhenglong"},
				}},
			},
		},
		policyResp: &ram.ListPoliciesForUserResponse{
			Policies: ram.PoliciesInListPoliciesForUser{Policy: []ram.Policy{
				{PolicyName: "AliyunOSSFullAccess", PolicyType: "System"},
				{PolicyName: "custom-readonly", PolicyType: "Custom"},
			}},
		},
	}

	adapter := newTestAdapter()
	policies, err := adapter.getUserPoliciesWith(context.Background(), client, testAccount(), "248996265577798116")
	if err != nil {
		t.Fatalf("解析 UserId 后不应失败, got: %v", err)
	}

	if got := client.listUsersCall; got != 2 {
		t.Errorf("应分页拉满 2 页用户列表, got %d", got)
	}
	if len(client.policyUserIDs) != 1 || client.policyUserIDs[0] != "zhenglong" {
		t.Errorf("ListPoliciesForUser 必须使用 UserName, got %v", client.policyUserIDs)
	}
	if len(policies) != 2 {
		t.Fatalf("应解析出 2 条策略, got %d", len(policies))
	}
	if policies[0].PolicyName != "AliyunOSSFullAccess" || policies[0].Provider != domain.CloudProviderAliyun {
		t.Errorf("策略解析异常: %+v", policies[0])
	}
}

// TestGetUserPoliciesUsesCachedUserName 确认 ListUsers 之后的调用不再额外拉取用户列表。
func TestGetUserPoliciesUsesCachedUserName(t *testing.T) {
	client := &fakeRAMClient{policyResp: &ram.ListPoliciesForUserResponse{}}
	adapter := newTestAdapter()
	adapter.rememberUserName("261555089280165526", "aliyun_2019")

	if _, err := adapter.getUserPoliciesWith(context.Background(), client, testAccount(), "261555089280165526"); err != nil {
		t.Fatalf("命中缓存不应失败, got: %v", err)
	}
	if client.listUsersCall != 0 {
		t.Errorf("命中缓存时不应调用 ListUsers, got %d", client.listUsersCall)
	}
	if len(client.policyUserIDs) != 1 || client.policyUserIDs[0] != "aliyun_2019" {
		t.Errorf("应使用缓存中的 UserName, got %v", client.policyUserIDs)
	}
}

// TestGetUserPoliciesPassesThroughUserName 确认调用方直接传 UserName 时不做任何解析。
func TestGetUserPoliciesPassesThroughUserName(t *testing.T) {
	client := &fakeRAMClient{policyResp: &ram.ListPoliciesForUserResponse{}}
	adapter := newTestAdapter()

	if _, err := adapter.getUserPoliciesWith(context.Background(), client, testAccount(), "zhenglong"); err != nil {
		t.Fatalf("UserName 直通不应失败, got: %v", err)
	}
	if client.listUsersCall != 0 {
		t.Errorf("UserName 直通时不应调用 ListUsers, got %d", client.listUsersCall)
	}
	if len(client.policyUserIDs) != 1 || client.policyUserIDs[0] != "zhenglong" {
		t.Errorf("应原样使用 UserName, got %v", client.policyUserIDs)
	}
}

// TestGetUserPoliciesUnknownNumericUserID 确认无法解析的 UserId 返回可识别错误而不是打一次必失败的请求。
func TestGetUserPoliciesUnknownNumericUserID(t *testing.T) {
	client := &fakeRAMClient{
		listUserPages: []*ram.ListUsersResponse{{
			Users: ram.UsersInListUsers{User: []ram.User{{UserId: "1", UserName: "other"}}},
		}},
	}
	adapter := newTestAdapter()

	_, err := adapter.getUserPoliciesWith(context.Background(), client, testAccount(), "999")
	if !errors.Is(err, cloudxiam.ErrCloudUserNotFound) {
		t.Fatalf("应返回 ErrCloudUserNotFound, got: %v", err)
	}
	if len(client.policyUserIDs) != 0 {
		t.Errorf("解析失败时不应调用 ListPoliciesForUser, got %v", client.policyUserIDs)
	}
}

// TestGetUserPoliciesUserGoneInCloud 确认云端已删除用户时返回 sentinel 而不是普通故障。
func TestGetUserPoliciesUserGoneInCloud(t *testing.T) {
	client := &fakeRAMClient{policyErr: userNotFoundError()}
	adapter := newTestAdapter()

	_, err := adapter.getUserPoliciesWith(context.Background(), client, testAccount(), "zhenglong")
	if !errors.Is(err, cloudxiam.ErrCloudUserNotFound) {
		t.Fatalf("用户已删除应返回 ErrCloudUserNotFound, got: %v", err)
	}
}

// TestGetUserPoliciesRealFailureKeepsError 确认真实故障不被误判成「用户已删除」。
func TestGetUserPoliciesRealFailureKeepsError(t *testing.T) {
	client := &fakeRAMClient{policyErr: sdkerrors.NewServerError(500, `{"Code":"InternalError","Message":"boom"}`, "")}
	adapter := newTestAdapter()

	_, err := adapter.getUserPoliciesWith(context.Background(), client, testAccount(), "zhenglong")
	if err == nil {
		t.Fatal("真实故障必须返回错误")
	}
	if errors.Is(err, cloudxiam.ErrCloudUserNotFound) {
		t.Fatalf("真实故障不得被判为用户已删除: %v", err)
	}
}

// TestGetUserResolvesNumericUserID 确认 GetUser 同样把 UserId 解析成 UserName。
func TestGetUserResolvesNumericUserID(t *testing.T) {
	client := &fakeRAMClient{
		getUserResp: &ram.GetUserResponse{User: ram.User{
			UserId:      "261555089280165526",
			UserName:    "aliyun_2019",
			DisplayName: "运维监控",
		}},
	}
	adapter := newTestAdapter()
	adapter.rememberUserName("261555089280165526", "aliyun_2019")

	user, err := adapter.getUserWith(context.Background(), client, testAccount(), "261555089280165526")
	if err != nil {
		t.Fatalf("GetUser 不应失败, got: %v", err)
	}
	if len(client.getUserNames) != 1 || client.getUserNames[0] != "aliyun_2019" {
		t.Errorf("GetUser 必须使用 UserName, got %v", client.getUserNames)
	}
	if user == nil || user.Username != "aliyun_2019" {
		t.Fatalf("用户解析异常: %+v", user)
	}
	if client.accessKeyCalls != 1 {
		t.Errorf("应查询一次 AccessKey, got %d", client.accessKeyCalls)
	}
}

// TestGetUserNotFoundInCloud 确认 GetUser 对已删除用户返回 sentinel。
func TestGetUserNotFoundInCloud(t *testing.T) {
	client := &fakeRAMClient{getUserErr: userNotFoundError()}
	adapter := newTestAdapter()

	_, err := adapter.getUserWith(context.Background(), client, testAccount(), "zhenglong")
	if !errors.Is(err, cloudxiam.ErrCloudUserNotFound) {
		t.Fatalf("应返回 ErrCloudUserNotFound, got: %v", err)
	}
}

// TestLoadUserNamesOnlyOnce 确认全量用户列表只拉取一次。
func TestLoadUserNamesOnlyOnce(t *testing.T) {
	client := &fakeRAMClient{listUserPages: []*ram.ListUsersResponse{{
		Users: ram.UsersInListUsers{User: []ram.User{{UserId: "1", UserName: "alice"}}},
	}}}
	adapter := newTestAdapter()

	first, err := adapter.resolveUserName(context.Background(), client, "1")
	if err != nil || first != "alice" {
		t.Fatalf("首次解析失败: %q %v", first, err)
	}
	if _, err := adapter.resolveUserName(context.Background(), client, "1"); err != nil {
		t.Fatalf("二次解析不应失败: %v", err)
	}
	if client.listUsersCall != 1 {
		t.Errorf("用户列表只应拉取一次, got %d", client.listUsersCall)
	}
}

func TestIsRAMUserID(t *testing.T) {
	cases := map[string]bool{
		"":                   false,
		"261555089280165526": true,
		"aliyun_2019":        false,
		"123abc":             false,
		"-1":                 false,
	}
	for input, want := range cases {
		if got := isRAMUserID(input); got != want {
			t.Errorf("isRAMUserID(%q) = %v, want %v", input, got, want)
		}
	}
}
