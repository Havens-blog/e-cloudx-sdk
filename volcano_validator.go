package cloudx

import (
	"context"
	"fmt"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/volcengine/volcengine-go-sdk/service/ecs"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// VolcanoValidator 火山引擎验证器
type VolcanoValidator struct{}

// NewVolcanoValidator 创建火山引擎验证器
func NewVolcanoValidator() CloudValidator {
	return &VolcanoValidator{}
}

// ValidateCredentials 验证火山引擎凭证
func (v *VolcanoValidator) ValidateCredentials(ctx context.Context, account *domain.CloudAccount) (*ValidationResult, error) {
	startTime := time.Now()

	// 验证凭证格式
	if err := v.validateCredentialFormat(account); err != nil {
		return &ValidationResult{
			Valid:        false,
			Message:      err.Error(),
			ValidatedAt:  time.Now(),
			ResponseTime: time.Since(startTime).Milliseconds(),
		}, nil
	}

	// 调用 API 验证
	if err := v.callVolcanoAPI(ctx, account); err != nil {
		return &ValidationResult{
			Valid:        false,
			Message:      fmt.Sprintf("火山引擎 API 调用失败: %v", err),
			ValidatedAt:  time.Now(),
			ResponseTime: time.Since(startTime).Milliseconds(),
		}, nil
	}

	// 获取支持的地域
	regions, err := v.GetSupportedRegions(ctx, account)
	if err != nil {
		regions = account.Regions
	}

	return &ValidationResult{
		Valid:        true,
		Message:      "火山引擎凭证验证成功",
		Regions:      regions,
		Permissions:  []string{"ecs:DescribeInstances", "iam:ListUsers"},
		AccountInfo:  fmt.Sprintf("AccessKeyId: %s", maskAccessKey(account.AccessKeyID)),
		ValidatedAt:  time.Now(),
		ResponseTime: time.Since(startTime).Milliseconds(),
	}, nil
}

// GetSupportedRegions 获取火山引擎支持的地域
func (v *VolcanoValidator) GetSupportedRegions(ctx context.Context, account *domain.CloudAccount) ([]string, error) {
	defaultRegion := "cn-beijing"
	if len(account.Regions) > 0 {
		defaultRegion = account.Regions[0]
	}

	config := volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(account.AccessKeyID, account.AccessKeySecret, "")).
		WithRegion(defaultRegion)

	sess, err := session.NewSession(config)
	if err != nil {
		return nil, fmt.Errorf("创建火山引擎会话失败: %w", err)
	}

	client := ecs.New(sess)
	input := &ecs.DescribeRegionsInput{}

	result, err := client.DescribeRegions(input)
	if err != nil {
		return nil, fmt.Errorf("获取地域列表失败: %w", err)
	}

	var regions []string
	if result.Regions != nil {
		for _, region := range result.Regions {
			if region.RegionId != nil {
				regions = append(regions, *region.RegionId)
			}
		}
	}

	return regions, nil
}

// TestConnection 测试火山引擎连接
func (v *VolcanoValidator) TestConnection(ctx context.Context, account *domain.CloudAccount) error {
	return v.callVolcanoAPI(ctx, account)
}

// validateCredentialFormat 验证凭证格式
func (v *VolcanoValidator) validateCredentialFormat(account *domain.CloudAccount) error {
	// 火山引擎 AccessKeyId 通常以 AK 开头
	if len(account.AccessKeyID) < 16 {
		return fmt.Errorf("火山引擎 AccessKeyId 长度不足")
	}

	if len(account.AccessKeySecret) < 16 {
		return fmt.Errorf("火山引擎 AccessKeySecret 长度不足")
	}

	return nil
}

// callVolcanoAPI 调用火山引擎 API 进行验证
func (v *VolcanoValidator) callVolcanoAPI(ctx context.Context, account *domain.CloudAccount) error {
	defaultRegion := "cn-beijing"
	if len(account.Regions) > 0 {
		defaultRegion = account.Regions[0]
	}

	config := volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(account.AccessKeyID, account.AccessKeySecret, "")).
		WithRegion(defaultRegion)

	sess, err := session.NewSession(config)
	if err != nil {
		return fmt.Errorf("创建火山引擎会话失败: %w", err)
	}

	client := ecs.New(sess)
	input := &ecs.DescribeRegionsInput{}

	_, err = client.DescribeRegions(input)
	if err != nil {
		return fmt.Errorf("火山引擎 API 调用失败: %w", err)
	}

	return nil
}
