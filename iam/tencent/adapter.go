package tencent

import (
	cloudxiam "github.com/Havens-blog/e-cloudx-sdk/iam"

	"context"
	"fmt"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/common/ratelimit"
	"github.com/Havens-blog/e-cloudx-sdk/common/retry"
	tencentcommon "github.com/Havens-blog/e-cloudx-sdk/common/tencent"
	cloudxtypes "github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	cam "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cam/v20190116"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
)

// Adapter 腾讯�?CAM 适配器�?
type Adapter struct {
	logger      *elog.Component
	rateLimiter *ratelimit.RateLimiter
}

// NewAdapter 创建腾讯�?CAM 适配器器实例例�?
func NewAdapter(logger *elog.Component) *Adapter {
	return &Adapter{
		logger:      logger,
		rateLimiter: ratelimit.NewRateLimiter(15), // 15 QPS
	}
}

// ValidateCredentials 验证凭证
func (a *Adapter) ValidateCredentials(ctx context.Context, account *domain.CloudAccount) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := tencentcommon.CreateCAMClient(account)
	if err != nil {
		return err
	}

	// 尝试获取当前账号信息来验证凭�?
	request := cam.NewListUsersRequest()

	err = a.retryWithBackoff(ctx, func() error {
		_, e := client.ListUsers(request)
		return e
	})

	if err != nil {
		a.logger.Error("validate tencent cloud credentials failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.FieldErr(err))
		return fmt.Errorf("invalid tencent cloud credentials: %w", err)
	}

	a.logger.Info("validate tencent cloud credentials success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)))

	return nil
}

// ListUsers 获取 CAM 用户组列表
func (a *Adapter) ListUsers(ctx context.Context, account *domain.CloudAccount) ([]*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := tencentcommon.CreateCAMClient(account)
	if err != nil {
		return nil, err
	}

	var allUsers []*domain.CloudUser

	request := cam.NewListUsersRequest()

	var response *cam.ListUsersResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.ListUsers(request)
		return e
	})

	if err != nil {
		a.logger.Error("list tencent cloud cam users failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to list CAM users: %w", err)
	}

	// 转换器用户组数据
	if response.Response.Data != nil {
		for _, tencentUser := range response.Response.Data {
			user := ConvertTencentUserToCloudUser(tencentUser, account)
			allUsers = append(allUsers, user)
		}
	}

	a.logger.Info("list tencent cloud cam users success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.Int("count", len(allUsers)))

	return allUsers, nil
}

// GetUser 获取用户组详情
func (a *Adapter) GetUser(ctx context.Context, account *domain.CloudAccount, userID string) (*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := tencentcommon.CreateCAMClient(account)
	if err != nil {
		return nil, err
	}

	request := cam.NewGetUserRequest()
	request.Name = common.StringPtr(userID)

	var response *cam.GetUserResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.GetUser(request)
		return e
	})

	if err != nil {
		a.logger.Error("get tencent cloud cam user failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("user_id", userID),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to get CAM user: %w", err)
	}

	// 转换器用户组数据
	subAccountInfo := &cam.SubAccountInfo{
		Uin:          response.Response.Uin,
		Name:         response.Response.Name,
		Remark:       response.Response.Remark,
		ConsoleLogin: response.Response.ConsoleLogin,
	}

	user := ConvertTencentUserToCloudUser(subAccountInfo, account)

	a.logger.Info("get tencent cloud cam user success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("user_id", userID))

	return user, nil
}

// CreateUser 创建用户组
func (a *Adapter) CreateUser(ctx context.Context, account *domain.CloudAccount, params *CreateUserParams) (*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := tencentcommon.CreateCAMClient(account)
	if err != nil {
		return nil, err
	}

	request := cam.NewAddUserRequest()
	request.Name = common.StringPtr(params.Username)
	if params.Remark != "" {
		request.Remark = common.StringPtr(params.Remark)
	}
	// 默认允许控制台登�?
	request.ConsoleLogin = common.Uint64Ptr(1)

	var response *cam.AddUserResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.AddUser(request)
		return e
	})

	if err != nil {
		a.logger.Error("create tencent cloud cam user failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("username", params.Username),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to create CAM user: %w", err)
	}

	// 获取创建的用户组详�?
	user, err := a.GetUser(ctx, account, params.Username)
	if err != nil {
		a.logger.Warn("failed to get created user details",
			elog.String("username", params.Username),
			elog.FieldErr(err))
		// 返回基本信息
		now := time.Now()
		user = &domain.CloudUser{
			Username:       params.Username,
			UserType:       domain.CloudUserTypeCAMUser,
			CloudAccountID: account.ID,
			Provider:       domain.CloudProviderTencent,
			CloudUserID:    uint64ToString(response.Response.Uin),
			Status:         domain.CloudUserStatusActive,
			TenantID:       account.TenantID,
			CreateTime:     now,
			UpdateTime:     now,
			CTime:          now.Unix(),
			UTime:          now.Unix(),
		}
	}

	a.logger.Info("create tencent cloud cam user success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("username", params.Username))

	return user, nil
}

// DeleteUser 删除用户组
func (a *Adapter) DeleteUser(ctx context.Context, account *domain.CloudAccount, userID string) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := tencentcommon.CreateCAMClient(account)
	if err != nil {
		return err
	}

	request := cam.NewDeleteUserRequest()
	request.Name = common.StringPtr(userID)
	// 强制删除，即使用户组有关联的策略或用户组�?
	request.Force = common.Uint64Ptr(1)

	err = a.retryWithBackoff(ctx, func() error {
		_, e := client.DeleteUser(request)
		return e
	})

	if err != nil {
		a.logger.Error("delete tencent cloud cam user failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("user_id", userID),
			elog.FieldErr(err))
		return fmt.Errorf("failed to delete CAM user: %w", err)
	}

	a.logger.Info("delete tencent cloud cam user success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("user_id", userID))

	return nil
}

// UpdateUserPermissions 更新用户组权限
func (a *Adapter) UpdateUserPermissions(ctx context.Context, account *domain.CloudAccount, userID string, policies []domain.PermissionPolicy) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := tencentcommon.CreateCAMClient(account)
	if err != nil {
		return err
	}

	// 获取用户组当前的策略列�?
	listRequest := cam.NewListAttachedUserPoliciesRequest()
	listRequest.TargetUin = common.Uint64Ptr(stringToUint64(userID))

	var listResponse *cam.ListAttachedUserPoliciesResponse
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		listResponse, e = client.ListAttachedUserPolicies(listRequest)
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
	currentPolicies := make(map[uint64]bool)
	if listResponse.Response.List != nil {
		for _, policy := range listResponse.Response.List {
			if policy.PolicyId != nil {
				currentPolicies[*policy.PolicyId] = true
			}
		}
	}

	// 构建目标策略映射
	targetPolicies := make(map[uint64]domain.PermissionPolicy)
	for _, policy := range policies {
		if policy.Provider == domain.CloudProviderTencent {
			policyID := stringToUint64(policy.PolicyID)
			targetPolicies[policyID] = policy
		}
	}

	// 分离需要附加和分离的策�?
	var toAttach []uint64
	var toDetach []uint64

	// 找出需要附加的策略
	for policyID := range targetPolicies {
		if !currentPolicies[policyID] {
			toAttach = append(toAttach, policyID)
		}
	}

	// 找出需要分离的策略
	for policyID := range currentPolicies {
		if _, exists := targetPolicies[policyID]; !exists {
			toDetach = append(toDetach, policyID)
		}
	}

	// 分离不需要的策略
	for _, policyID := range toDetach {
		detachRequest := cam.NewDetachUserPolicyRequest()
		detachRequest.DetachUin = common.Uint64Ptr(stringToUint64(userID))
		detachRequest.PolicyId = common.Uint64Ptr(policyID)

		err = a.retryWithBackoff(ctx, func() error {
			_, e := client.DetachUserPolicy(detachRequest)
			return e
		})

		if err != nil {
			a.logger.Error("detach policy from user failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.String("user_id", userID),
				elog.String("policy_id", fmt.Sprintf("%d", policyID)),
				elog.FieldErr(err))
			// 继续处理其他策略
		}
	}

	// 附加新策�?
	for _, policyID := range toAttach {
		attachRequest := cam.NewAttachUserPolicyRequest()
		attachRequest.AttachUin = common.Uint64Ptr(stringToUint64(userID))
		attachRequest.PolicyId = common.Uint64Ptr(policyID)

		err = a.retryWithBackoff(ctx, func() error {
			_, e := client.AttachUserPolicy(attachRequest)
			return e
		})

		if err != nil {
			a.logger.Error("attach policy to user failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.String("user_id", userID),
				elog.String("policy_id", fmt.Sprintf("%d", policyID)),
				elog.FieldErr(err))
			return fmt.Errorf("failed to attach policy %d: %w", policyID, err)
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

	client, err := tencentcommon.CreateCAMClient(account)
	if err != nil {
		return nil, err
	}

	var allPolicies []domain.PermissionPolicy
	page := uint64(1)
	pageSize := uint64(200)

	for {
		request := cam.NewListPoliciesRequest()
		request.Page = common.Uint64Ptr(page)
		request.Rp = common.Uint64Ptr(pageSize)
		// 获取所有类型的策略（预设策略和自定义策略）
		request.Scope = common.StringPtr("All")

		var response *cam.ListPoliciesResponse
		err := a.retryWithBackoff(ctx, func() error {
			var e error
			response, e = client.ListPolicies(request)
			return e
		})

		if err != nil {
			a.logger.Error("list tencent cloud policies failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.FieldErr(err))
			return nil, fmt.Errorf("failed to list policies: %w", err)
		}

		// 转换器策略数据
		if response.Response.List != nil {
			for _, tencentPolicy := range response.Response.List {
				policy := domain.PermissionPolicy{
					PolicyID:       uint64ToString(tencentPolicy.PolicyId),
					PolicyName:     getStringValue(tencentPolicy.PolicyName),
					PolicyDocument: getStringValue(tencentPolicy.Description),
					Provider:       domain.CloudProviderTencent,
					PolicyType:     ConvertPolicyType(getUint64Value(tencentPolicy.Type)),
				}
				allPolicies = append(allPolicies, policy)
			}
		}

		// 检查是否还有更多数�?
		if response.Response.List == nil || len(response.Response.List) < int(pageSize) {
			break
		}
		page++
	}

	a.logger.Info("list tencent cloud policies success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.Int("count", len(allPolicies)))

	return allPolicies, nil
}

// GetUserPolicies 获取用户的个人权限策略
func (a *Adapter) GetUserPolicies(ctx context.Context, account *domain.CloudAccount, userID string) ([]domain.PermissionPolicy, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	// TODO: 实现腾讯云用户个人权限查询
	// 目前返回空列表，后续完善
	a.logger.Warn("GetUserPolicies not fully implemented for tencent cloud",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("user_id", userID))

	return []domain.PermissionPolicy{}, nil
}

// retryWithBackoff 使用指数退避策略重�?
func (a *Adapter) retryWithBackoff(ctx context.Context, operation func() error) error {
	return retry.WithBackoff(ctx, 3, operation, func(err error) bool {
		if tencentcommon.IsThrottlingError(err) {
			a.logger.Warn("tencent cloud api throttled, retrying", elog.FieldErr(err))
			return true
		}
		return false
	})
}

func init() {
	cloudxiam.RegisterIAMAdapter(domain.CloudProviderTencent, func(logger *elog.Component) (cloudxiam.CloudIAMAdapter, error) {
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
