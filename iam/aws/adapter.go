package aws

import (
	cloudxiam "github.com/Havens-blog/e-cloudx-sdk/iam"

	"context"
	"fmt"

	awscommon "github.com/Havens-blog/e-cloudx-sdk/common/aws"
	"github.com/Havens-blog/e-cloudx-sdk/common/ratelimit"
	"github.com/Havens-blog/e-cloudx-sdk/common/retry"
	cloudxtypes "github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/gotomicro/ego/core/elog"
)

// Adapter AWS IAM 适配器�?
type Adapter struct {
	logger      *elog.Component
	rateLimiter *ratelimit.RateLimiter
}

// NewAdapter 创建 AWS IAM 适配器器实例例�?
func NewAdapter(logger *elog.Component) *Adapter {
	return &Adapter{
		logger:      logger,
		rateLimiter: ratelimit.NewRateLimiter(10), // 10 QPS
	}
}

// ValidateCredentials 验证凭证
func (a *Adapter) ValidateCredentials(ctx context.Context, account *domain.CloudAccount) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := awscommon.CreateIAMClient(account)
	if err != nil {
		return err
	}

	// 尝试获取当前用户组信息来验证凭�?
	_, err = client.GetUser(ctx, &iam.GetUserInput{})
	if err != nil {
		a.logger.Error("validate aws credentials failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.FieldErr(err))
		return fmt.Errorf("invalid aws credentials: %w", err)
	}

	a.logger.Info("validate aws credentials success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)))

	return nil
}

// ListUsers 获取 IAM 用户组列表
func (a *Adapter) ListUsers(ctx context.Context, account *domain.CloudAccount) ([]*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := awscommon.CreateIAMClient(account)
	if err != nil {
		return nil, err
	}

	var allUsers []*domain.CloudUser

	// 使用分页器获取所有用�?
	paginator := iam.NewListUsersPaginator(client, &iam.ListUsersInput{
		MaxItems: aws.Int32(100),
	})

	for paginator.HasMorePages() {
		var page *iam.ListUsersOutput
		err := a.retryWithBackoff(ctx, func() error {
			var e error
			page, e = paginator.NextPage(ctx)
			return e
		})

		if err != nil {
			a.logger.Error("list aws iam users failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.FieldErr(err))
			return nil, fmt.Errorf("failed to list IAM users: %w", err)
		}

		// 转换器用户组数据
		for _, iamUser := range page.Users {
			user := ConvertIAMUserToCloudUser(iamUser, account)
			allUsers = append(allUsers, user)
		}
	}

	a.logger.Info("list aws iam users success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.Int("count", len(allUsers)))

	return allUsers, nil
}

// GetUser 获取用户组详情
func (a *Adapter) GetUser(ctx context.Context, account *domain.CloudAccount, userID string) (*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := awscommon.CreateIAMClient(account)
	if err != nil {
		return nil, err
	}

	var response *iam.GetUserOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.GetUser(ctx, &iam.GetUserInput{
			UserName: aws.String(userID),
		})
		return e
	})

	if err != nil {
		a.logger.Error("get aws iam user failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("user_id", userID),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to get IAM user: %w", err)
	}

	user := ConvertIAMUserToCloudUser(*response.User, account)

	// 获取用户组�?AccessKey 信息
	var akResponse *iam.ListAccessKeysOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		akResponse, e = client.ListAccessKeys(ctx, &iam.ListAccessKeysInput{
			UserName: aws.String(userID),
		})
		return e
	})

	if err == nil && akResponse != nil {
		user.Metadata.AccessKeyCount = len(akResponse.AccessKeyMetadata)
	}

	a.logger.Info("get aws iam user success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("user_id", userID))

	return user, nil
}

// CreateUser 创建用户组
func (a *Adapter) CreateUser(ctx context.Context, account *domain.CloudAccount, req *CreateUserParams) (*domain.CloudUser, error) {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := awscommon.CreateIAMClient(account)
	if err != nil {
		return nil, err
	}

	input := &iam.CreateUserInput{
		UserName: aws.String(req.Username),
	}

	if req.Path != "" {
		input.Path = aws.String(req.Path)
	}

	if len(req.Tags) > 0 {
		var tags []types.Tag
		for key, value := range req.Tags {
			tags = append(tags, types.Tag{
				Key:   aws.String(key),
				Value: aws.String(value),
			})
		}
		input.Tags = tags
	}

	var response *iam.CreateUserOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		response, e = client.CreateUser(ctx, input)
		return e
	})

	if err != nil {
		a.logger.Error("create aws iam user failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("username", req.Username),
			elog.FieldErr(err))
		return nil, fmt.Errorf("failed to create IAM user: %w", err)
	}

	user := ConvertIAMUserToCloudUser(*response.User, account)

	a.logger.Info("create aws iam user success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("username", req.Username))

	return user, nil
}

// DeleteUser 删除用户组
func (a *Adapter) DeleteUser(ctx context.Context, account *domain.CloudAccount, userID string) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := awscommon.CreateIAMClient(account)
	if err != nil {
		return err
	}

	// 先删除用户组的所�?AccessKey
	var akResponse *iam.ListAccessKeysOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		akResponse, e = client.ListAccessKeys(ctx, &iam.ListAccessKeysInput{
			UserName: aws.String(userID),
		})
		return e
	})

	if err == nil && akResponse != nil {
		for _, ak := range akResponse.AccessKeyMetadata {
			_ = a.retryWithBackoff(ctx, func() error {
				_, e := client.DeleteAccessKey(ctx, &iam.DeleteAccessKeyInput{
					UserName:    aws.String(userID),
					AccessKeyId: ak.AccessKeyId,
				})
				return e
			})
		}
	}

	// 分离用户组的所有策�?
	var policiesResponse *iam.ListAttachedUserPoliciesOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		policiesResponse, e = client.ListAttachedUserPolicies(ctx, &iam.ListAttachedUserPoliciesInput{
			UserName: aws.String(userID),
		})
		return e
	})

	if err == nil && policiesResponse != nil {
		for _, policy := range policiesResponse.AttachedPolicies {
			_ = a.retryWithBackoff(ctx, func() error {
				_, e := client.DetachUserPolicy(ctx, &iam.DetachUserPolicyInput{
					UserName:  aws.String(userID),
					PolicyArn: policy.PolicyArn,
				})
				return e
			})
		}
	}

	// 删除用户组
	err = a.retryWithBackoff(ctx, func() error {
		_, e := client.DeleteUser(ctx, &iam.DeleteUserInput{
			UserName: aws.String(userID),
		})
		return e
	})

	if err != nil {
		a.logger.Error("delete aws iam user failed",
			elog.String("account_id", fmt.Sprintf("%d", account.ID)),
			elog.String("user_id", userID),
			elog.FieldErr(err))
		return fmt.Errorf("failed to delete IAM user: %w", err)
	}

	a.logger.Info("delete aws iam user success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("user_id", userID))

	return nil
}

// UpdateUserPermissions 更新用户组权限
func (a *Adapter) UpdateUserPermissions(ctx context.Context, account *domain.CloudAccount, userID string, policies []domain.PermissionPolicy) error {
	if err := a.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait failed: %w", err)
	}

	client, err := awscommon.CreateIAMClient(account)
	if err != nil {
		return err
	}

	// 获取用户组当前的策略列�?
	var listResponse *iam.ListAttachedUserPoliciesOutput
	err = a.retryWithBackoff(ctx, func() error {
		var e error
		listResponse, e = client.ListAttachedUserPolicies(ctx, &iam.ListAttachedUserPoliciesInput{
			UserName: aws.String(userID),
		})
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
	currentPolicies := make(map[string]string) // policyArn -> policyName
	for _, policy := range listResponse.AttachedPolicies {
		currentPolicies[*policy.PolicyArn] = *policy.PolicyName
	}

	// 构建目标策略映射
	targetPolicies := make(map[string]domain.PermissionPolicy)
	for _, policy := range policies {
		if policy.Provider == domain.CloudProviderAWS {
			targetPolicies[policy.PolicyID] = policy
		}
	}

	// 分离需要附加和分离的策�?
	var toAttach []domain.PermissionPolicy
	var toDetach []string

	// 找出需要附加的策略
	for policyArn, policy := range targetPolicies {
		if _, exists := currentPolicies[policyArn]; !exists {
			toAttach = append(toAttach, policy)
		}
	}

	// 找出需要分离的策略
	for policyArn := range currentPolicies {
		if _, exists := targetPolicies[policyArn]; !exists {
			toDetach = append(toDetach, policyArn)
		}
	}

	// 分离不需要的策略
	for _, policyArn := range toDetach {
		err = a.retryWithBackoff(ctx, func() error {
			_, e := client.DetachUserPolicy(ctx, &iam.DetachUserPolicyInput{
				UserName:  aws.String(userID),
				PolicyArn: aws.String(policyArn),
			})
			return e
		})

		if err != nil {
			a.logger.Error("detach policy from user failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.String("user_id", userID),
				elog.String("policy_arn", policyArn),
				elog.FieldErr(err))
			// 继续处理其他策略
		}
	}

	// 附加新策�?
	for _, policy := range toAttach {
		err = a.retryWithBackoff(ctx, func() error {
			_, e := client.AttachUserPolicy(ctx, &iam.AttachUserPolicyInput{
				UserName:  aws.String(userID),
				PolicyArn: aws.String(policy.PolicyID),
			})
			return e
		})

		if err != nil {
			a.logger.Error("attach policy to user failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.String("user_id", userID),
				elog.String("policy_arn", policy.PolicyID),
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

	client, err := awscommon.CreateIAMClient(account)
	if err != nil {
		return nil, err
	}

	var allPolicies []domain.PermissionPolicy

	// 使用分页器获取所有策�?
	paginator := iam.NewListPoliciesPaginator(client, &iam.ListPoliciesInput{
		Scope:    types.PolicyScopeTypeAll, // 获取所有策略（AWS 托管 + 客户托管�?
		MaxItems: aws.Int32(100),
	})

	for paginator.HasMorePages() {
		var page *iam.ListPoliciesOutput
		err := a.retryWithBackoff(ctx, func() error {
			var e error
			page, e = paginator.NextPage(ctx)
			return e
		})

		if err != nil {
			a.logger.Error("list aws policies failed",
				elog.String("account_id", fmt.Sprintf("%d", account.ID)),
				elog.FieldErr(err))
			return nil, fmt.Errorf("failed to list policies: %w", err)
		}

		// 转换器策略数据
		for _, iamPolicy := range page.Policies {
			policy := domain.PermissionPolicy{
				PolicyID:       *iamPolicy.Arn,
				PolicyName:     *iamPolicy.PolicyName,
				PolicyDocument: "", // AWS 需要单独调�?GetPolicyVersion 获取文档
				Provider:       domain.CloudProviderAWS,
				PolicyType:     ConvertPolicyScope(iamPolicy.Arn),
			}
			allPolicies = append(allPolicies, policy)
		}
	}

	a.logger.Info("list aws policies success",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.Int("count", len(allPolicies)))

	return allPolicies, nil
}

// GetUserPolicies 获取用户的个人权限策略
func (a *Adapter) GetUserPolicies(ctx context.Context, account *domain.CloudAccount, userID string) ([]domain.PermissionPolicy, error) {
	// TODO: 实现 AWS 用户个人权限查询
	// 目前返回空列表，后续完善
	a.logger.Warn("GetUserPolicies not fully implemented for aws",
		elog.String("account_id", fmt.Sprintf("%d", account.ID)),
		elog.String("user_id", userID))

	return []domain.PermissionPolicy{}, nil
}

// retryWithBackoff 使用指数退避策略重�?
func (a *Adapter) retryWithBackoff(ctx context.Context, operation func() error) error {
	return retry.WithBackoff(ctx, 3, operation, func(err error) bool {
		if awscommon.IsThrottlingError(err) {
			a.logger.Warn("aws api throttled, retrying", elog.FieldErr(err))
			return true
		}
		return false
	})
}

func init() {
	cloudxiam.RegisterIAMAdapter(domain.CloudProviderAWS, func(logger *elog.Component) (cloudxiam.CloudIAMAdapter, error) {
		ad := NewAdapter(logger)
		return cloudxiam.NewAdapterWrapper(ad, func(ctx context.Context, account *domain.CloudAccount, req *cloudxtypes.CreateUserRequest) (*domain.CloudUser, error) {
			params := &CreateUserParams{
				Username: req.Username,
				Path:     "/",
				Tags: map[string]string{
					"DisplayName": req.DisplayName,
					"Email":       req.Email,
				},
			}
			return ad.CreateUser(ctx, account, params)
		}), nil
	})
}
