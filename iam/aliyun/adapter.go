package aliyun

import (
	cloudxiam "github.com/Havens-blog/e-cloudx-sdk/iam"

	"context"
	stderrors "errors"
	"fmt"
	"sync"

	aliyuncommon "github.com/Havens-blog/e-cloudx-sdk/common/aliyun"
	"github.com/Havens-blog/e-cloudx-sdk/common/ratelimit"
	"github.com/Havens-blog/e-cloudx-sdk/common/retry"
	cloudxtypes "github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	aliyunerrors "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ram"
	"github.com/gotomicro/ego/core/elog"
)

// Adapter 阿里云IAM适配器
type Adapter struct {
	logger      *elog.Component
	rateLimiter *ratelimit.RateLimiter

	// userNames 缓存阿里云数值型 UserId -> UserName。RAM 的 GetUser / ListPoliciesForUser
	// 只接受 UserName，而 ListUsers 下发的是 UserId，两者之间必须先做一次映射。
	// 适配器实例由工厂按账号全局缓存，因此映射可跨调用复用。
	userNames   sync.Map
	namesLoaded bool
	namesMu     sync.Mutex
	// loadMu 串行化「全量拉取用户列表」本身；namesMu 只保护 namesLoaded 标志位，
	// 二者都不在分页网络 IO 期间持有，避免慢调用阻塞其它 goroutine 的读取。
	loadMu sync.Mutex
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

		// 转换器用户组数据，同时回填 UserId -> UserName 映射
		for _, ramUser := range response.Users.User {
			user := ConvertRAMUserToCloudUser(ramUser, account)
			allUsers = append(allUsers, user)
			a.rememberUserName(ramUser.UserId, ramUser.UserName)
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

	return a.getUserWith(ctx, client, account, userID)
}

// getUserWith 是 GetUser 的实现体。入参既可以是 UserName，也可以是 ListUsers 下发的
// 数值型 UserId：RAM 的 GetUser 只接受 UserName，因此这里先解析一次。
func (a *Adapter) getUserWith(
	ctx context.Context,
	client ramUserClient,
	account *domain.CloudAccount,
	userID string,
) (*domain.CloudUser, error) {
	userName, err := a.resolveUserName(ctx, client, userID)
	if err != nil {
		return nil, err
	}

	request := ram.CreateGetUserRequest()
	request.Scheme = "https"
	request.UserName = userName

	var response *ram.GetUserResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.GetUser(request)
		return e
	})

	if err != nil {
		if isRAMUserGoneError(err) {
			a.logger.Warn("aliyun ram user no longer exists",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.String("user_id", userID),
				elog.String("user_name", userName),
				elog.FieldErr(err))
			return nil, fmt.Errorf("%w: %w", cloudxiam.ErrCloudUserNotFound, err)
		}
		a.logger.Error("get aliyun ram user failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("user_id", userID),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to get RAM user: %w", err)
	}

	user := ConvertRAMUserToCloudUser(response.User, account)
	a.rememberUserName(response.User.UserId, response.User.UserName)

	// 获取用户的AccessKey信息
	akRequest := ram.CreateListAccessKeysRequest()
	akRequest.Scheme = "https"
	akRequest.UserName = userName

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

	return a.getUserPoliciesWith(ctx, client, account, userID)
}

// getUserPoliciesWith 是 GetUserPolicies 的实现体。userID 支持 UserName 或数值型 UserId
// （会自动解析），因为 RAM 的 ListPoliciesForUser 只接受 UserName。
func (a *Adapter) getUserPoliciesWith(
	ctx context.Context,
	client ramUserClient,
	account *domain.CloudAccount,
	userID string,
) ([]domain.PermissionPolicy, error) {
	userName, err := a.resolveUserName(ctx, client, userID)
	if err != nil {
		return nil, err
	}

	request := ram.CreateListPoliciesForUserRequest()
	request.Scheme = "https"
	request.UserName = userName

	var response *ram.ListPoliciesForUserResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.ListPoliciesForUser(request)
		return e
	})

	if err != nil {
		if isRAMUserGoneError(err) {
			a.logger.Warn("aliyun ram user no longer exists, skip policies",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.String("user_id", userID),
				elog.String("user_name", userName),
				elog.FieldErr(err))
			return nil, fmt.Errorf("%w: %w", cloudxiam.ErrCloudUserNotFound, err)
		}
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
		elog.String("user_name", userName),
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

// ramUserClient 是用户相关操作需要的最小客户端接口，便于单测注入假客户端
// （阿里云 SDK 的 *ram.Client 天然满足该接口）。
type ramUserClient interface {
	ListUsers(request *ram.ListUsersRequest) (*ram.ListUsersResponse, error)
	GetUser(request *ram.GetUserRequest) (*ram.GetUserResponse, error)
	ListAccessKeys(request *ram.ListAccessKeysRequest) (*ram.ListAccessKeysResponse, error)
	ListPoliciesForUser(request *ram.ListPoliciesForUserRequest) (*ram.ListPoliciesForUserResponse, error)
}

// rememberUserName 缓存 UserId -> UserName 映射（ListUsers 与 GetUser 都会回填）。
func (a *Adapter) rememberUserName(userID, userName string) {
	if userID == "" || userName == "" {
		return
	}
	a.userNames.Store(userID, userName)
}

func (a *Adapter) cachedUserName(userID string) (string, bool) {
	value, ok := a.userNames.Load(userID)
	if !ok {
		return "", false
	}
	userName, ok := value.(string)
	if !ok || userName == "" {
		return "", false
	}
	return userName, true
}

// isRAMUserID 判断标识是否为阿里云数值型 UserId（RAM 的 UserName 不会是纯数字）。
func isRAMUserID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// resolveUserName 把调用方传入的 userID 解析成 GetUser / ListPoliciesForUser 所需的 UserName。
func (a *Adapter) resolveUserName(ctx context.Context, client ramUserClient, userID string) (string, error) {
	if userID == "" {
		return "", fmt.Errorf("%w: empty user id", cloudxiam.ErrCloudUserNotFound)
	}

	if userName, ok := a.cachedUserName(userID); ok {
		return userName, nil
	}

	// 非纯数字：调用方传入的已经是 UserName。
	if !isRAMUserID(userID) {
		return userID, nil
	}

	if err := a.loadUserNames(ctx, client); err != nil {
		return "", err
	}

	if userName, ok := a.cachedUserName(userID); ok {
		return userName, nil
	}

	return "", fmt.Errorf("%w: user id %s", cloudxiam.ErrCloudUserNotFound, userID)
}

// loadUserNames 全量拉取一次用户列表以填充 UserId -> UserName 映射（每个适配器实例只做一次）。
// 分页期间的网络 IO 不持有任何锁：映射先收集到本地变量，全部成功后一次性发布，
// 既避免「半成品映射」被读到，也避免慢调用阻塞并发读取。
func (a *Adapter) loadUserNames(ctx context.Context, client ramUserClient) error {
	if a.userNamesLoaded() {
		return nil
	}

	a.loadMu.Lock()
	defer a.loadMu.Unlock()

	if a.userNamesLoaded() {
		return nil
	}

	names := make(map[string]string)
	marker := ""
	for {
		request := ram.CreateListUsersRequest()
		request.Scheme = "https"
		request.MaxItems = "100"
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
			return fmt.Errorf("failed to load RAM users for name mapping: %w", err)
		}

		for i := range response.Users.User {
			user := response.Users.User[i]
			names[user.UserId] = user.UserName
		}

		if !response.IsTruncated {
			break
		}
		marker = response.Marker
	}

	for userID, userName := range names {
		a.userNames.Store(userID, userName)
	}

	a.namesMu.Lock()
	a.namesLoaded = true
	a.namesMu.Unlock()
	return nil
}

// userNamesLoaded 报告 UserId -> UserName 全量映射是否已就绪。
func (a *Adapter) userNamesLoaded() bool {
	a.namesMu.Lock()
	defer a.namesMu.Unlock()
	return a.namesLoaded
}

// ramUserGoneErrorCode 是阿里云 RAM 表示「用户不存在」的结构化错误码。
const ramUserGoneErrorCode = "EntityNotExist.User"

// isRAMUserGoneError 判断错误是否为「云端用户已不存在」（例如 RAM 用户已被删除）。
// 只认 SDK 的结构化错误码，不对错误文本做子串匹配：文本会随语言/格式变化，
// 也可能恰好出现在其它错误（如参数回显）里，从而把真实故障误判成「用户已删除」。
func isRAMUserGoneError(err error) bool {
	if err == nil {
		return false
	}
	var serverErr *aliyunerrors.ServerError
	if !stderrors.As(err, &serverErr) {
		return false
	}
	return serverErr.ErrorCode() == ramUserGoneErrorCode
}
