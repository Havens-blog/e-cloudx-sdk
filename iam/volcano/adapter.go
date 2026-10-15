package volcano

import (
	cloudxiam "github.com/Havens-blog/e-cloudx-sdk/iam"

	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/common/ratelimit"
	"github.com/Havens-blog/e-cloudx-sdk/common/retry"
	volcanocommon "github.com/Havens-blog/e-cloudx-sdk/common/volcano"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	cloudxtypes "github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	iamSDK "github.com/volcengine/volcengine-go-sdk/service/iam"
	"github.com/volcengine/volcengine-go-sdk/volcengine/request"
)

// iamPageLimit 火山云 IAM 列表接口单页条数上限
const iamPageLimit = 100

// iamAPI 抽象火山云 IAM SDK 客户端方法集(仅本适配器使用的操作)。
// *iamSDK.IAM 为天然实现;测试经 newClient 注入 mock 响应。
type iamAPI interface {
	ListUsersWithContext(ctx context.Context, input *iamSDK.ListUsersInput, opts ...request.Option) (*iamSDK.ListUsersOutput, error)
	GetUserWithContext(ctx context.Context, input *iamSDK.GetUserInput, opts ...request.Option) (*iamSDK.GetUserOutput, error)
	CreateUserWithContext(ctx context.Context, input *iamSDK.CreateUserInput, opts ...request.Option) (*iamSDK.CreateUserOutput, error)
	DeleteUserWithContext(ctx context.Context, input *iamSDK.DeleteUserInput, opts ...request.Option) (*iamSDK.DeleteUserOutput, error)
	ListAccessKeysWithContext(ctx context.Context, input *iamSDK.ListAccessKeysInput, opts ...request.Option) (*iamSDK.ListAccessKeysOutput, error)
	DeleteAccessKeyWithContext(ctx context.Context, input *iamSDK.DeleteAccessKeyInput, opts ...request.Option) (*iamSDK.DeleteAccessKeyOutput, error)
	ListAttachedUserPoliciesWithContext(ctx context.Context, input *iamSDK.ListAttachedUserPoliciesInput, opts ...request.Option) (*iamSDK.ListAttachedUserPoliciesOutput, error)
	AttachUserPolicyWithContext(ctx context.Context, input *iamSDK.AttachUserPolicyInput, opts ...request.Option) (*iamSDK.AttachUserPolicyOutput, error)
	DetachUserPolicyWithContext(ctx context.Context, input *iamSDK.DetachUserPolicyInput, opts ...request.Option) (*iamSDK.DetachUserPolicyOutput, error)
	ListPoliciesWithContext(ctx context.Context, input *iamSDK.ListPoliciesInput, opts ...request.Option) (*iamSDK.ListPoliciesOutput, error)
	ListGroupsWithContext(ctx context.Context, input *iamSDK.ListGroupsInput, opts ...request.Option) (*iamSDK.ListGroupsOutput, error)
	GetGroupWithContext(ctx context.Context, input *iamSDK.GetGroupInput, opts ...request.Option) (*iamSDK.GetGroupOutput, error)
	CreateGroupWithContext(ctx context.Context, input *iamSDK.CreateGroupInput, opts ...request.Option) (*iamSDK.CreateGroupOutput, error)
	DeleteGroupWithContext(ctx context.Context, input *iamSDK.DeleteGroupInput, opts ...request.Option) (*iamSDK.DeleteGroupOutput, error)
	ListUsersForGroupWithContext(ctx context.Context, input *iamSDK.ListUsersForGroupInput, opts ...request.Option) (*iamSDK.ListUsersForGroupOutput, error)
	ListAttachedUserGroupPoliciesWithContext(ctx context.Context, input *iamSDK.ListAttachedUserGroupPoliciesInput, opts ...request.Option) (*iamSDK.ListAttachedUserGroupPoliciesOutput, error)
	AttachUserGroupPolicyWithContext(ctx context.Context, input *iamSDK.AttachUserGroupPolicyInput, opts ...request.Option) (*iamSDK.AttachUserGroupPolicyOutput, error)
	DetachUserGroupPolicyWithContext(ctx context.Context, input *iamSDK.DetachUserGroupPolicyInput, opts ...request.Option) (*iamSDK.DetachUserGroupPolicyOutput, error)
	AddUserToGroupWithContext(ctx context.Context, input *iamSDK.AddUserToGroupInput, opts ...request.Option) (*iamSDK.AddUserToGroupOutput, error)
	RemoveUserFromGroupWithContext(ctx context.Context, input *iamSDK.RemoveUserFromGroupInput, opts ...request.Option) (*iamSDK.RemoveUserFromGroupOutput, error)
	GetPolicyWithContext(ctx context.Context, input *iamSDK.GetPolicyInput, opts ...request.Option) (*iamSDK.GetPolicyOutput, error)
}

// 编译期校验:SDK 客户端天然满足适配器所需方法集
var _ iamAPI = (*iamSDK.IAM)(nil)

// Adapter 火山云 IAM 适配器
type Adapter struct {
	logger      *elog.Component
	rateLimiter *ratelimit.RateLimiter

	// newClient 客户端工厂;nil 时使用 volcanocommon.CreateIAMClient,测试可注入 mock。
	newClient func(account *domain.CloudAccount) (iamAPI, error)
}

// NewAdapter 创建火山云 IAM 适配器实例
func NewAdapter(logger *elog.Component) *Adapter {
	return &Adapter{
		logger:      logger,
		rateLimiter: ratelimit.NewRateLimiter(15), // 15 QPS
	}
}

// getClient 获取 IAM SDK 客户端。
func (a *Adapter) getClient(account *domain.CloudAccount) (iamAPI, error) {
	if a.newClient != nil {
		return a.newClient(account)
	}
	return volcanocommon.CreateIAMClient(account)
}

// ValidateCredentials 验证凭证。
// 使用最轻量的 ListUsers(Limit=1) 探测:凭证无效时 SDK 返回签名/授权类错误,
// 经由该 SDK 错误被识别并包装为明确错误(而非 not implemented)。
func (a *Adapter) ValidateCredentials(ctx context.Context, account *domain.CloudAccount) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := a.getClient(account)
	if err != nil {
		return fmt.Errorf("invalid volcano credentials: %w", err)
	}

	input := &iamSDK.ListUsersInput{}
	input.SetLimit(1)

	if _, err = client.ListUsersWithContext(ctx, input); err != nil {
		a.logger.Error("validate volcano credentials failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.FieldErr(err))
		return fmt.Errorf("invalid volcano credentials: %w", err)
	}

	a.logger.Info("validate volcano credentials success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)))

	return nil
}

// ListUsers 获取 IAM 用户列表(分页聚合)。
func (a *Adapter) ListUsers(ctx context.Context, account *domain.CloudAccount) ([]*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := a.getClient(account)
	if err != nil {
		return nil, err
	}

	var allUsers []*domain.CloudUser
	offset := int32(0)

	for {
		input := &iamSDK.ListUsersInput{}
		input.SetLimit(iamPageLimit)
		input.SetOffset(offset)

		var output *iamSDK.ListUsersOutput
		err := a.retryWithBackoff(ctx, func() error {
			var e error
			output, e = client.ListUsersWithContext(ctx, input)
			return e
		})

		if err != nil {
			a.logger.Error("list volcano IAM users failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.FieldErr(err))
			return nil, fmt.Errorf("failed to list volcano IAM users: %w", err)
		}

		for _, u := range output.UserMetadata {
			if user := ConvertVolcanoListUserToCloudUser(u, account); user != nil {
				allUsers = append(allUsers, user)
			}
		}

		if !hasMorePages(output.Total, len(output.UserMetadata), len(allUsers)) {
			break
		}
		offset += int32(len(output.UserMetadata))
	}

	a.logger.Info("list volcano IAM users success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.Int("count", len(allUsers)))

	return allUsers, nil
}

// GetUser 获取用户详情。
func (a *Adapter) GetUser(ctx context.Context, account *domain.CloudAccount, userID string) (*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := a.getClient(account)
	if err != nil {
		return nil, err
	}

	// 火山云 IAM 用户以 UserName 为主标识
	input := &iamSDK.GetUserInput{}
	input.SetUserName(userID)

	var output *iamSDK.GetUserOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		output, e = client.GetUserWithContext(ctx, input)
		return e
	})

	if err != nil {
		a.logger.Error("get volcano IAM user failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("user_id", userID),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to get volcano IAM user %s: %w", userID, err)
	}

	a.logger.Info("get volcano IAM user success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("user_id", userID))

	return ConvertVolcanoUserToCloudUser(output.User, account), nil
}

// GetUserPolicies 获取用户的个人权限策略(直接挂载)。
func (a *Adapter) GetUserPolicies(ctx context.Context, account *domain.CloudAccount, userID string) ([]domain.PermissionPolicy, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := a.getClient(account)
	if err != nil {
		return nil, err
	}

	input := &iamSDK.ListAttachedUserPoliciesInput{}
	input.SetUserName(userID)

	var output *iamSDK.ListAttachedUserPoliciesOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		output, e = client.ListAttachedUserPoliciesWithContext(ctx, input)
		return e
	})

	if err != nil {
		a.logger.Error("get volcano IAM user policies failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("user_id", userID),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to get volcano IAM user policies: %w", err)
	}

	policies := make([]domain.PermissionPolicy, 0, len(output.AttachedPolicyMetadata))
	for _, p := range output.AttachedPolicyMetadata {
		policies = append(policies, domain.PermissionPolicy{
			PolicyID:   deref(p.PolicyName),
			PolicyName: deref(p.PolicyName),
			Provider:   domain.CloudProviderVolcano,
			PolicyType: ConvertPolicyType(deref(p.PolicyType)),
		})
	}

	a.logger.Info("get volcano IAM user policies success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("user_id", userID),
		elog.Int("count", len(policies)))

	return policies, nil
}

// CreateUser 创建用户。
func (a *Adapter) CreateUser(ctx context.Context, account *domain.CloudAccount, params *CreateUserParams) (*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := a.getClient(account)
	if err != nil {
		return nil, err
	}

	input := &iamSDK.CreateUserInput{}
	input.SetUserName(params.Username)
	if params.DisplayName != "" {
		input.SetDisplayName(params.DisplayName)
	}
	if params.Email != "" {
		input.SetEmail(params.Email)
	}
	if params.PhoneNumber != "" {
		input.SetMobilePhone(params.PhoneNumber)
	}
	if params.Description != "" {
		input.SetDescription(params.Description)
	}

	var output *iamSDK.CreateUserOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		output, e = client.CreateUserWithContext(ctx, input)
		return e
	})

	if err != nil {
		a.logger.Error("create volcano IAM user failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("username", params.Username),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to create volcano IAM user %s: %w", params.Username, err)
	}

	user := ConvertVolcanoCreatedUserToCloudUser(output.User, account)

	a.logger.Info("create volcano IAM user success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("username", params.Username))

	return user, nil
}

// DeleteUser 删除用户。
// 先尽力清理 AccessKey(存在 AK 的用户在火山云无法直接删除),再删除用户。
func (a *Adapter) DeleteUser(ctx context.Context, account *domain.CloudAccount, userID string) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := a.getClient(account)
	if err != nil {
		return err
	}

	akInput := &iamSDK.ListAccessKeysInput{}
	akInput.SetUserName(userID)

	var akOutput *iamSDK.ListAccessKeysOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		akOutput, e = client.ListAccessKeysWithContext(ctx, akInput)
		return e
	})

	if err == nil && akOutput != nil {
		for _, ak := range akOutput.AccessKeyMetadata {
			delInput := &iamSDK.DeleteAccessKeyInput{}
			delInput.SetAccessKeyId(deref(ak.AccessKeyId))
			delInput.SetUserName(userID)

			_ = a.retryWithBackoff(ctx, func() error {
				_, e := client.DeleteAccessKeyWithContext(ctx, delInput)
				return e
			})
		}
	}

	input := &iamSDK.DeleteUserInput{}
	input.SetUserName(userID)

	err = a.retryWithBackoff(ctx, func() error {
		_, e := client.DeleteUserWithContext(ctx, input)
		return e
	})

	if err != nil {
		a.logger.Error("delete volcano IAM user failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("user_id", userID),
			elog.FieldErr(err))
		return fmt.Errorf("failed to delete volcano IAM user %s: %w", userID, err)
	}

	a.logger.Info("delete volcano IAM user success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("user_id", userID))

	return nil
}

// UpdateUserPermissions 更新用户直接挂载的权限策略(差集附加/分离)。
func (a *Adapter) UpdateUserPermissions(ctx context.Context, account *domain.CloudAccount, userID string, policies []domain.PermissionPolicy) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := a.getClient(account)
	if err != nil {
		return err
	}

	// 获取当前挂载的策略
	input := &iamSDK.ListAttachedUserPoliciesInput{}
	input.SetUserName(userID)

	var output *iamSDK.ListAttachedUserPoliciesOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		output, e = client.ListAttachedUserPoliciesWithContext(ctx, input)
		return e
	})

	if err != nil {
		a.logger.Error("list volcano IAM user policies failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("user_id", userID),
			elog.FieldErr(err))
		return fmt.Errorf("failed to get volcano IAM user policies: %w", err)
	}

	current := make([]domain.PermissionPolicy, 0, len(output.AttachedPolicyMetadata))
	for _, p := range output.AttachedPolicyMetadata {
		current = append(current, domain.PermissionPolicy{
			PolicyID:   deref(p.PolicyName),
			PolicyName: deref(p.PolicyName),
			Provider:   domain.CloudProviderVolcano,
			PolicyType: ConvertPolicyType(deref(p.PolicyType)),
		})
	}

	toAttach, toDetach := DiffPolicies(current, policies, domain.CloudProviderVolcano)

	// 分离不需要的策略(尽力而为,继续处理其他策略)
	for _, policy := range toDetach {
		detachInput := &iamSDK.DetachUserPolicyInput{}
		detachInput.SetPolicyName(policy.PolicyName)
		detachInput.SetPolicyType(PolicyTypeToVolcano(policy.PolicyType))
		detachInput.SetUserName(userID)

		err = a.retryWithBackoff(ctx, func() error {
			_, e := client.DetachUserPolicyWithContext(ctx, detachInput)
			return e
		})

		if err != nil {
			a.logger.Error("detach policy from volcano IAM user failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.String("user_id", userID),
				elog.String("policy_name", policy.PolicyName),
				elog.FieldErr(err))
		}
	}

	// 附加新策略
	for _, policy := range toAttach {
		attachInput := &iamSDK.AttachUserPolicyInput{}
		attachInput.SetPolicyName(policy.PolicyName)
		attachInput.SetPolicyType(PolicyTypeToVolcano(policy.PolicyType))
		attachInput.SetUserName(userID)

		err = a.retryWithBackoff(ctx, func() error {
			_, e := client.AttachUserPolicyWithContext(ctx, attachInput)
			return e
		})

		if err != nil {
			a.logger.Error("attach policy to volcano IAM user failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.String("user_id", userID),
				elog.String("policy_name", policy.PolicyName),
				elog.FieldErr(err))
			return fmt.Errorf("failed to attach policy %s: %w", policy.PolicyName, err)
		}
	}

	a.logger.Info("update volcano IAM user permissions success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("user_id", userID),
		elog.Int("attached", len(toAttach)),
		elog.Int("detached", len(toDetach)))

	return nil
}

// ListPolicies 获取权限策略列表(分页聚合)。
func (a *Adapter) ListPolicies(ctx context.Context, account *domain.CloudAccount) ([]domain.PermissionPolicy, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := a.getClient(account)
	if err != nil {
		return nil, err
	}

	var allPolicies []domain.PermissionPolicy
	offset := int32(0)

	for {
		input := &iamSDK.ListPoliciesInput{}
		input.SetLimit(iamPageLimit)
		input.SetOffset(offset)

		var output *iamSDK.ListPoliciesOutput
		err := a.retryWithBackoff(ctx, func() error {
			var e error
			output, e = client.ListPoliciesWithContext(ctx, input)
			return e
		})

		if err != nil {
			a.logger.Error("list volcano IAM policies failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.FieldErr(err))
			return nil, fmt.Errorf("failed to list volcano IAM policies: %w", err)
		}

		for _, p := range output.PolicyMetadata {
			allPolicies = append(allPolicies, ConvertVolcanoPolicyMetadataToPermissionPolicy(p))
		}

		if !hasMorePages(output.Total, len(output.PolicyMetadata), len(allPolicies)) {
			break
		}
		offset += int32(len(output.PolicyMetadata))
	}

	a.logger.Info("list volcano IAM policies success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.Int("count", len(allPolicies)))

	return allPolicies, nil
}

// hasMorePages 判断火山云 IAM 列表接口是否还有下一页。
// 火山云不返回截断标志,以响应回显的 Total 为准;Total 缺失时退化为未满页即终止。
func hasMorePages(total *int32, pageLen, collected int) bool {
	if pageLen == 0 {
		return false
	}
	if total != nil {
		return collected < int(*total)
	}
	return pageLen >= iamPageLimit
}

// retryWithBackoff 使用指数退避策略重试(仅限流类错误重试)。
func (a *Adapter) retryWithBackoff(ctx context.Context, operation func() error) error {
	return retry.WithBackoff(ctx, 3, operation, func(err error) bool {
		if volcanocommon.IsThrottlingError(err) {
			a.logger.Warn("volcano cloud api throttled, retrying", elog.FieldErr(err))
			return true
		}
		return false
	})
}

func init() {
	// 与 cloudx/billing 资产注册表保持一致：volcano 与 volcengine 为同一厂商的别名
	creator := func(logger *elog.Component) (cloudxiam.CloudIAMAdapter, error) {
		ad := NewAdapter(logger)
		return cloudxiam.NewAdapterWrapper(ad, func(ctx context.Context, account *domain.CloudAccount, req *cloudxtypes.CreateUserRequest) (*domain.CloudUser, error) {
			params := &CreateUserParams{
				Username:    req.Username,
				DisplayName: req.DisplayName,
				Email:       req.Email,
			}
			return ad.CreateUser(ctx, account, params)
		}), nil
	}
	cloudxiam.RegisterIAMAdapter(domain.CloudProviderVolcano, creator)
	cloudxiam.RegisterIAMAdapter(domain.CloudProviderVolcengine, creator)
}
