package cloudx

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
)

// AWSValidator AWS 验证器
type AWSValidator struct{}

// NewAWSValidator 创建 AWS 验证器
func NewAWSValidator() CloudValidator {
	return &AWSValidator{}
}

// ValidateCredentials 验证 AWS 凭证
func (v *AWSValidator) ValidateCredentials(ctx context.Context, account *domain.CloudAccount) (*ValidationResult, error) {
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

	// 调用 AWS API 验证
	if err := v.callAWSAPI(ctx, account); err != nil {
		return &ValidationResult{
			Valid:        false,
			Message:      fmt.Sprintf("AWS API 调用失败败: %v", err),
			ValidatedAt:  time.Now(),
			ResponseTime: time.Since(startTime).Milliseconds(),
		}, nil
	}

	// 获取支持的地域
	regions, err := v.GetSupportedRegions(ctx, account)
	if err != nil {
		regions = account.Regions // 降级处理，使用账号配置的区域
	}

	return &ValidationResult{
		Valid:        true,
		Message:      "AWS 凭证验证成功功",
		Regions:      regions,
		Permissions:  []string{"ec2:DescribeInstances", "rds:DescribeDBInstances", "s3:ListBuckets"},
		AccountInfo:  fmt.Sprintf("AccessKeyId: %s", maskAccessKey(account.AccessKeyID)),
		ValidatedAt:  time.Now(),
		ResponseTime: time.Since(startTime).Milliseconds(),
	}, nil
}

// GetSupportedRegions 获取 AWS 支持的地域
func (v *AWSValidator) GetSupportedRegions(ctx context.Context, account *domain.CloudAccount) ([]string, error) {
	// TODO: 调用 AWS API 获取真实地域列表
	return []string{
		"us-east-1",      // 美国东部（弗吉尼亚北部）
		"us-east-2",      // 美国东部（俄亥俄）
		"us-west-1",      // 美国西部（加利福尼亚北部）
		"us-west-2",      // 美国西部（俄勒冈）
		"ap-south-1",     // 亚太地区（孟买）
		"ap-northeast-1", // 亚太地区（东京）
		"ap-northeast-2", // 亚太地区（首尔）
		"ap-southeast-1", // 亚太地区（新加坡）
		"ap-southeast-2", // 亚太地区（悉尼）
		"ca-central-1",   // 加拿大（中部）
		"eu-central-1",   // 欧洲（法兰克福）
		"eu-west-1",      // 欧洲（爱尔兰）
		"eu-west-2",      // 欧洲（伦敦）
		"eu-west-3",      // 欧洲（巴黎）
		"sa-east-1",      // 南美洲（圣保罗）
	}, nil
}

// TestConnection 测试 AWS 连接
func (v *AWSValidator) TestConnection(ctx context.Context, account *domain.CloudAccount) error {
	return v.callAWSAPI(ctx, account)
}

// validateCredentialFormat 验证 AWS 凭证格式
func (v *AWSValidator) validateCredentialFormat(account *domain.CloudAccount) error {
	// AWS Access Key ID 通常以 AKIA 开头，长度为 20 位
	if len(account.AccessKeyID) != 20 {
		return fmt.Errorf("AWS Access Key ID 长度应为 20 位，当前为 %d 位", len(account.AccessKeyID))
	}

	if !strings.HasPrefix(account.AccessKeyID, "AKIA") {
		return fmt.Errorf("AWS Access Key ID 应以 AKIA 开头")
	}

	// Secret Access Key 长度通常为 40 位
	if len(account.AccessKeySecret) != 40 {
		return fmt.Errorf("AWS Secret Access Key 长度应为 40 位，当前为 %d 位", len(account.AccessKeySecret))
	}

	return nil
}

// callAWSAPI 调用 AWS API 进行验证
func (v *AWSValidator) callAWSAPI(ctx context.Context, account *domain.CloudAccount) error {
	// TODO: 实际集成功 AWS SDK
	// 可以调用 STS GetCallerIdentity 接口来验证凭证

	// 模拟 API 调用
	select {
	case <-ctx.Done():
		return ErrConnectionTimeout
	case <-time.After(150 * time.Millisecond):
		// 模拟验证逻辑
		if account.AccessKeyID == "AKIA_invalid_key_test" {
			return ErrInvalidCredentials
		}
		return nil
	}
}
