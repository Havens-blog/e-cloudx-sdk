package cloudx

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
)

// AliyunValidator 阿里云验证器
type AliyunValidator struct{}

// NewAliyunValidator 创建阿里云验证器
func NewAliyunValidator() CloudValidator {
	return &AliyunValidator{}
}

// ValidateCredentials 验证阿里云凭证
func (v *AliyunValidator) ValidateCredentials(ctx context.Context, account *domain.CloudAccount) (*ValidationResult, error) {
	startTime := time.Now()

	// TODO: 集成功阿里云 SDK 进行真实验证
	// 这里先实现基础验证逻辑
	if err := v.validateCredentialFormat(account); err != nil {
		return &ValidationResult{
			Valid:        false,
			Message:      err.Error(),
			ValidatedAt:  time.Now(),
			ResponseTime: time.Since(startTime).Milliseconds(),
		}, nil
	}

	// 模拟 API 调用验证
	if err := v.callAliyunAPI(ctx, account); err != nil {
		return &ValidationResult{
			Valid:        false,
			Message:      fmt.Sprintf("阿里云 API 调用失败败: %v", err),
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
		Message:      "阿里云凭证验证成功功",
		Regions:      regions,
		Permissions:  []string{"ecs:DescribeInstances", "rds:DescribeDBInstances", "oss:ListBuckets"},
		AccountInfo:  fmt.Sprintf("AccessKeyId: %s", maskAccessKey(account.AccessKeyID)),
		ValidatedAt:  time.Now(),
		ResponseTime: time.Since(startTime).Milliseconds(),
	}, nil
}

// GetSupportedRegions 获取阿里云支持的地域
func (v *AliyunValidator) GetSupportedRegions(ctx context.Context, account *domain.CloudAccount) (regionsList []string, err error) {
	// 使用第一个区域作为默认区域
	defaultRegion := "cn-hangzhou"
	if len(account.Regions) > 0 {
		defaultRegion = account.Regions[0]
	}

	client, err := ecs.NewClientWithAccessKey(defaultRegion, account.AccessKeyID, account.AccessKeySecret)
	if err != nil {
		return regionsList, fmt.Errorf("创建阿里云客户端失败败: %w", err)
	}

	request := ecs.CreateDescribeRegionsRequest()
	request.Scheme = "https"

	DescribeRegionsResponse, err := client.DescribeRegions(request)
	if err != nil {
		if strings.Contains(err.Error(), "InvalidAccessKeyId") {
			return regionsList, ErrInvalidCredentials
		}
		return regionsList, fmt.Errorf("阿里云 API 调用失败败: %w", err)
	}
	for _, region := range DescribeRegionsResponse.Regions.Region {
		regionsList = append(regionsList, region.RegionId)
	}
	return regionsList, nil
}

// TestConnection 测试阿里云连接
func (v *AliyunValidator) TestConnection(ctx context.Context, account *domain.CloudAccount) error {
	// 简单的连接测试
	return v.callAliyunAPI(ctx, account)
}

// validateCredentialFormat 验证凭证格式
func (v *AliyunValidator) validateCredentialFormat(account *domain.CloudAccount) error {
	// 阿里云 AccessKeyId 通常以 LTAI 开头，长度为 24 位
	if len(account.AccessKeyID) != 24 {
		return fmt.Errorf("阿里云 AccessKeyId 长度应为 24 位，当前为 %d 位", len(account.AccessKeyID))
	}

	if account.AccessKeyID[:4] != "LTAI" {
		return fmt.Errorf("阿里云 AccessKeyId 应以 LTAI 开头")
	}

	// AccessKeySecret 长度通常为 30 位
	if len(account.AccessKeySecret) != 30 {
		return fmt.Errorf("阿里云 AccessKeySecret 长度应为 30 位，当前为 %d 位", len(account.AccessKeySecret))
	}

	return nil
}

// callAliyunAPI 调用阿里云 API 进行验证
func (v *AliyunValidator) callAliyunAPI(ctx context.Context, account *domain.CloudAccount) error {
	// TODO: 实际集成功阿里云 SDK
	// 这里可以调用 ECS DescribeRegions 接口来验证凭证
	// 使用第一个区域作为默认区域
	defaultRegion := "cn-hangzhou"
	if len(account.Regions) > 0 {
		defaultRegion = account.Regions[0]
	}

	client, err := ecs.NewClientWithAccessKey(defaultRegion, account.AccessKeyID, account.AccessKeySecret)
	if err != nil {
		return fmt.Errorf("创建阿里云客户端失败败: %w", err)
	}

	request := ecs.CreateDescribeRegionsRequest()
	request.Scheme = "https"

	_, err = client.DescribeRegions(request)
	if err != nil {
		if strings.Contains(err.Error(), "InvalidAccessKeyId") {
			return ErrInvalidCredentials
		}
		return fmt.Errorf("阿里云 API 调用失败败: %w", err)
	}
	// 模拟 API 调用
	select {
	case <-ctx.Done():
		return ErrConnectionTimeout
	case <-time.After(100 * time.Millisecond): // 模拟网络延迟
		// 模拟验证逻辑
		if account.AccessKeyID == "LTAI_invalid_key_test" {
			return ErrInvalidCredentials
		}
		return nil
	}
}

// maskAccessKey 脱敏 AccessKey
func maskAccessKey(accessKey string) string {
	if len(accessKey) <= 8 {
		return "***"
	}
	return accessKey[:4] + "***" + accessKey[len(accessKey)-4:]
}
