package aliyun

import (
	cloudxiam "github.com/Havens-blog/e-cloudx-sdk/iam"

	"context"
	"fmt"

	aliyuncommon "github.com/Havens-blog/e-cloudx-sdk/common/aliyun"
	"github.com/Havens-blog/e-cloudx-sdk/common/ratelimit"
	"github.com/Havens-blog/e-cloudx-sdk/common/retry"
	cloudxtypes "github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ram"
	"github.com/gotomicro/ego/core/elog"
)

// Adapter 阿里云IAM适配器
type Adapter struct {
	logger      *elog.Component
	rateLimiter *ratelimit.RateLimiter
}

// NewAdapter 创建阿里云IAM适配器实例例
func NewAdapter(logger *elog.Component) *Adapter {
	return &Adapter{
		logger:      logger,
		rateLimiter: ratelimit.NewRateLimiter(20), // 20 QPS
	}
}

// ValidateCredentials 验证凭证
func (a *Adapter) ValidateCredentials(ctx context.Context, account *domain.CloudAccount) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := aliyuncommon.CreateRAMClient(account)
	if err != nil {
		return err
	}

	// 尝试列出用户组来验证凭证（限制返回1个）
	request := ram.CreateListUsersRequest()
	request.Scheme = "https"
	request.MaxItems = "1"

	_, err = client.ListUsers(request)
	if err != nil {
		a.logger.Error("validate aliyun credentials failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.FieldErr(err))
		return fmt.Errorf("invalid aliyun credentials: %w", err)
	}

	a.logger.Info("validate aliyun credentials success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)))

	return nil
}

// ListUsers 获取RAM用户组列表
func (a *Adapter) ListUsers(ctx context.Context, account *domain.CloudAccount) ([]*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := aliyuncommon.CreateRAMClient(account)
	if err != nil {
		return nil, err
	}

	var allUsers []*domain.CloudUser
	marker := ""

	// 分页获取所有用�?
	for {
		request := ram.CreateListUsersRequest()
		request.Scheme = "https"
		request.MaxItems = "100" // 每页最�?00�?
		if marker != "" {
			request.Marker = marker
		}

		var response *ram.ListUsersResponse
		err := a.retryWithBackoff(ctx, func() error {
			var e error
			response, e = client.ListUsers(request)
			return e
		})

		if err != nil {
			a.logger.Error("list aliyun ram users failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.FieldErr(err))
			return nil, fmt.Errorf("failed to list RAM users: %w", err)
		}

		// 转换器用户组数据
		for _, ramUser := range response.Users.User {
			user := ConvertRAMUserToCloudUser(ramUser, account)
			allUsers = append(allUsers, user)
		}

		// 检查是否还有更多数�?
		if !response.IsTruncated {
			break
		}
		marker = response.Marker
	}

	a.logger.Info("list aliyun ram users success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.Int("count", len(allUsers)))

	return allUsers, nil
}

// GetUser 获取用户组详情
func (a *Adapter) GetUser(ctx context.Context, account *domain.CloudAccount, userID string) (*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := aliyuncommon.CreateRAMClient(account)
	if err != nil {
		return nil, err
	}

	request := ram.CreateGetUserRequest()
	request.Scheme = "https"
	request.UserName = userID

	var response *ram.GetUserResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.GetUser(request)
		return e
	})

	if err != nil {
		a.logger.Error("get aliyun ram user failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("user_id", userID),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to get RAM user: %w", err)
	}

	user := ConvertRAMUserToCloudUser(response.User, account)

	// 获取用户组的AccessKey信息
	akRequest := ram.CreateListAccessKeysRequest()
	akRequest.Scheme = "https"
	akRequest.UserName = userID

	var akResponse *ram.ListAccessKeysResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		akResponse, e = client.ListAccessKeys(akRequest)
		return e
	})

	if err == nil && akResponse != nil {
		user.Metadata.AccessKeyCount = len(akResponse.AccessKeys.AccessKey)
	}

	a.logger.Info("get aliyun ram user success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("user_id", userID))

	return user, nil
}

// CreateUser 创建用户组
func (a *Adapter) CreateUser(ctx context.Context, account *domain.CloudAccount, req *CreateUserParams) (*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := aliyuncommon.CreateRAMClient(account)
	if err != nil {
		return nil, err
	}

	request := ram.CreateCreateUserRequest()
	request.Scheme = "https"
	request.UserName = req.Username
	if req.DisplayName != "" {
		request.DisplayName = req.DisplayName
	}
	if req.Email != "" {
		request.Email = req.Email
	}

	var response *ram.CreateUserResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.CreateUser(request)
		return e
	})

	if err != nil {
		a.logger.Error("create aliyun ram user failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("username", req.Username),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to create RAM user: %w", err)
	}

	user := ConvertRAMUserToCloudUser(response.User, account)

	a.logger.Info("create aliyun ram user success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("username", req.Username))

	return user, nil
}

// DeleteUser 删除用户组
func (a *Adapter) DeleteUser(ctx context.Context, account *domain.CloudAccount, userID string) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := aliyuncommon.CreateRAMClient(account)
	if err != nil {
		return err
	}

	// 先删除用户组的所有AccessKey
	akRequest := ram.CreateListAccessKeysRequest()
	akRequest.Scheme = "https"
	akRequest.UserName = userID

	var akResponse *ram.ListAccessKeysResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		akResponse, e = client.ListAccessKeys(akRequest)
		return e
	})

	if err == nil && akResponse != nil {
		for _, ak := range akResponse.AccessKeys.AccessKey {
			deleteAKRequest := ram.CreateDeleteAccessKeyRequest()
			deleteAKRequest.Scheme = "https"
			deleteAKRequest.UserAccessKeyId = ak.AccessKeyId
			deleteAKRequest.UserName = userID

			_ = a.retryWithBackoff(ctx, func() error {
				_, e := client.DeleteAccessKey(deleteAKRequest)
				return e
			})
		}
	}

	// 删除用户组
	request := ram.CreateDeleteUserRequest()
	request.Scheme = "https"
	request.UserName = userID

	err = a.retryWithBackoff(ctx, func() error {
		_, e := client.DeleteUser(request)
		return e
	})

	if err != nil {
		a.logger.Error("delete aliyun ram user failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("user_id", userID),
			elog.FieldErr(err))
		return fmt.Errorf("failed to delete RAM user: %w", err)
	}

	a.logger.Info("delete aliyun ram user success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("user_id", userID))

	return nil
}

// UpdateUserPermissions 更新用户组权限
func (a *Adapter) UpdateUserPermissions(ctx context.Context, account *domain.CloudAccount, userID string, policies []domain.PermissionPolicy) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := aliyuncommon.CreateRAMClient(account)
	if err != nil {
		return err
	}

	// 获取用户组当前的策略列�?
	listRequest := ram.CreateListPoliciesForUserRequest()
	listRequest.Scheme = "https"
	listRequest.UserName = userID

	var listResponse *ram.ListPoliciesForUserResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		listResponse, e = client.ListPoliciesForUser(listRequest)
		return e
	})

	if err != nil {
		a.logger.Error("list user policies failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("user_id", userID),
			elog.FieldErr(err))
		return fmt.Errorf("failed to list user policies: %w", err)
	}

	// 构建当前策略映射
	currentPolicies := make(map[string]string) // policyName -> policyType
	for _, policy := range listResponse.Policies.Policy {
		currentPolicies[policy.PolicyName] = policy.PolicyType
	}

	// 构建目标策略映射
	targetPolicies := make(map[string]domain.PermissionPolicy)
	for _, policy := range policies {
		if policy.Provider == domain.CloudProviderAliyun {
			targetPolicies[policy.PolicyName] = policy
		}
	}

	// 分离需要附加和分离的策�?
	var toAttach []domain.PermissionPolicy
	var toDetach []string

	// 找出需要附加的策略
	for policyName, policy := range targetPolicies {
		if _, exists := currentPolicies[policyName]; !exists {
			toAttach = append(toAttach, policy)
		}
	}

	// 找出需要分离的策略
	for policyName := range currentPolicies {
		if _, exists := targetPolicies[policyName]; !exists {
			toDetach = append(toDetach, policyName)
		}
	}

	// 分离不需要的策略
	for _, policyName := range toDetach {
		policyType := currentPolicies[policyName]

		detachRequest := ram.CreateDetachPolicyFromUserRequest()
		detachRequest.Scheme = "https"
		detachRequest.PolicyName = policyName
		detachRequest.PolicyType = policyType
		detachRequest.UserName = userID

		err = a.retryWithBackoff(ctx, func() error {
			_, e := client.DetachPolicyFromUser(detachRequest)
			return e
		})

		if err != nil {
			a.logger.Error("detach policy from user failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.String("user_id", userID),
				elog.String("policy_name", policyName),
				elog.FieldErr(err))
			// 继续处理其他策略
		}
	}

	// 附加新策�?
	for _, policy := range toAttach {
		attachRequest := ram.CreateAttachPolicyToUserRequest()
		attachRequest.Scheme = "https"
		attachRequest.PolicyName = policy.PolicyName
		attachRequest.PolicyType = string(policy.PolicyType)
		attachRequest.UserName = userID

		err = a.retryWithBackoff(ctx, func() error {
			_, e := client.AttachPolicyToUser(attachRequest)
			return e
		})

		if err != nil {
			a.logger.Error("attach policy to user failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.String("user_id", userID),
				elog.String("policy_name", policy.PolicyName),
				elog.FieldErr(err))
			return fmt.Errorf("failed to attach policy %s: %w", policy.PolicyName, err)
		}
	}

	a.logger.Info("update user permissions success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("user_id", userID),
		elog.Int("attached", len(toAttach)),
		elog.Int("detached", len(toDetach)))

	return nil
}

// ListPolicies 获取权限策略列表
func (a *Adapter) ListPolicies(ctx context.Context, account *domain.CloudAccount) ([]domain.PermissionPolicy, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := aliyuncommon.CreateRAMClient(account)
	if err != nil {
		return nil, err
	}

	var allPolicies []domain.PermissionPolicy
	marker := ""

	// 分页获取所有策�?
	for {
		request := ram.CreateListPoliciesRequest()
		request.Scheme = "https"
		request.MaxItems = "100" // 每页最�?00�?
		if marker != "" {
			request.Marker = marker
		}

		var response *ram.ListPoliciesResponse
		err := a.retryWithBackoff(ctx, func() error {
			var e error
			response, e = client.ListPolicies(request)
			return e
		})

		if err != nil {
			a.logger.Error("list aliyun policies failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.FieldErr(err))
			return nil, fmt.Errorf("failed to list policies: %w", err)
		}

		// 转换器策略数据
		for _, ramPolicy := range response.Policies.Policy {
			policy := domain.PermissionPolicy{
				PolicyID:       ramPolicy.PolicyName,
				PolicyName:     ramPolicy.PolicyName,
				PolicyDocument: ramPolicy.Description, // 使用描述作为文档
				Provider:       domain.CloudProviderAliyun,
				PolicyType:     ConvertPolicyType(ramPolicy.PolicyType),
			}
			allPolicies = append(allPolicies, policy)
		}

		// 检查是否还有更多数�?
		if !response.IsTruncated {
			break
		}
		marker = response.Marker
	}

	a.logger.Info("list aliyun policies success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.Int("count", len(allPolicies)))

	return allPolicies, nil
}

// retryWithBackoff 使用指数退避策略重�?
func (a *Adapter) retryWithBackoff(ctx context.Context, operation func() error) error {
	return retry.WithBackoff(ctx, 3, operation, func(err error) bool {
		if aliyuncommon.IsThrottlingError(err) {
			a.logger.Warn("aliyun api throttled, retrying", elog.FieldErr(err))
			return true
		}
		return false
	})
}

// GetUserPolicies 获取用户的个人权限策略
func (a *Adapter) GetUserPolicies(ctx context.Context, account *domain.CloudAccount, userID string) ([]domain.PermissionPolicy, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := aliyuncommon.CreateRAMClient(account)
	if err != nil {
		return nil, err
	}

	request := ram.CreateListPoliciesForUserRequest()
	request.Scheme = "https"
	request.UserName = userID

	var response *ram.ListPoliciesForUserResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.ListPoliciesForUser(request)
		return e
	})

	if err != nil {
		a.logger.Error("get user policies failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("user_id", userID),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to get user policies: %w", err)
	}

	policies := make([]domain.PermissionPolicy, 0, len(response.Policies.Policy))
	for _, policy := range response.Policies.Policy {
		policies = append(policies, domain.PermissionPolicy{
			PolicyID:       policy.PolicyName,
			PolicyName:     policy.PolicyName,
			PolicyDocument: policy.Description,
			Provider:       domain.CloudProviderAliyun,
			PolicyType:     ConvertPolicyType(policy.PolicyType),
		})
	}

	a.logger.Info("get user policies success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("user_id", userID),
		elog.Int("count", len(policies)))

	return policies, nil
}

// GetPolicy 获取策略详情
func (a *Adapter) GetPolicy(ctx context.Context, account *domain.CloudAccount, policyID string) (*domain.PermissionPolicy, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := aliyuncommon.CreateRAMClient(account)
	if err != nil {
		return nil, err
	}

	request := ram.CreateGetPolicyRequest()
	request.Scheme = "https"
	request.PolicyName = policyID
	request.PolicyType = "System" // 先尝试系统策�?

	var response *ram.GetPolicyResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.GetPolicy(request)
		return e
	})

	if err != nil {
		// 如果系统策略不存在，尝试自定义策�?
		request.PolicyType = "Custom"
		err = a.retryWithBackoff(ctx, func() error {
			var e error
			response, e = client.GetPolicy(request)
			return e
		})

		if err != nil {
			a.logger.Error("get aliyun policy failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.String("policy_id", policyID),
				elog.FieldErr(err))
			return nil, fmt.Errorf("failed to get policy: %w", err)
		}
	}

	policy := &domain.PermissionPolicy{
		PolicyID:       response.Policy.PolicyName,
		PolicyName:     response.Policy.PolicyName,
		PolicyDocument: response.Policy.Description,
		Provider:       domain.CloudProviderAliyun,
		PolicyType:     ConvertPolicyType(response.Policy.PolicyType),
	}

	// 获取策略版本详情（包含策略文档）
	versionRequest := ram.CreateGetPolicyVersionRequest()
	versionRequest.Scheme = "https"
	versionRequest.PolicyName = policyID
	versionRequest.PolicyType = response.Policy.PolicyType
	versionRequest.VersionId = response.Policy.DefaultVersion

	var versionResponse *ram.GetPolicyVersionResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		versionResponse, e = client.GetPolicyVersion(versionRequest)
		return e
	})

	if err == nil && versionResponse != nil {
		policy.PolicyDocument = versionResponse.PolicyVersion.PolicyDocument
	}

	a.logger.Info("get aliyun policy success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("policy_id", policyID))

	return policy, nil
}

func init() {
	cloudxiam.RegisterIAMAdapter(domain.CloudProviderAliyun, func(logger *elog.Component) (cloudxiam.CloudIAMAdapter, error) {
		ad := NewAdapter(logger)
		return cloudxiam.NewAdapterWrapper(ad, func(ctx context.Context, account *domain.CloudAccount, req *cloudxtypes.CreateUserRequest) (*domain.CloudUser, error) {
			params := &CreateUserParams{
				Username:    req.Username,
				DisplayName: req.DisplayName,
				Email:       req.Email,
			}
			return ad.CreateUser(ctx, account, params)
		}), nil
	})
}
