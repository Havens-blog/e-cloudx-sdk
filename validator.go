package cloudx

import (
	"context"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
)

// CloudValidator 云厂商验证器接口
type CloudValidator interface {
	// ValidateCredentials 验证云厂商凭证
	ValidateCredentials(ctx context.Context, account *domain.CloudAccount) (*ValidationResult, error)

	// GetSupportedRegions 获取支持的地域列表
	GetSupportedRegions(ctx context.Context, account *domain.CloudAccount) ([]string, error)

	// TestConnection 测试连接
	TestConnection(ctx context.Context, account *domain.CloudAccount) error
}

// ValidationResult 验证结果
type ValidationResult struct {
	Valid        bool      `json:"valid"`         // 是否有效
	Message      string    `json:"message"`       // 验证消息
	Regions      []string  `json:"regions"`       // 可用地域
	Permissions  []string  `json:"permissions"`   // 权限列表
	AccountInfo  string    `json:"account_info"`  // 账号信息
	ValidatedAt  time.Time `json:"validated_at"`  // 验证时间
	ResponseTime int64     `json:"response_time"` // 响应时间(毫秒)
}

// CloudValidatorFactory 云厂商验证器工厂
type CloudValidatorFactory interface {
	CreateValidator(provider domain.CloudProvider) (CloudValidator, error)
}

// DefaultCloudValidatorFactory 默认验证器工厂
type DefaultCloudValidatorFactory struct{}

// CreateValidator 创建验证器
func (f *DefaultCloudValidatorFactory) CreateValidator(provider domain.CloudProvider) (CloudValidator, error) {
	switch provider {
	case domain.CloudProviderAliyun:
		return NewAliyunValidator(), nil
	case domain.CloudProviderAWS:
		return NewAWSValidator(), nil
	case domain.CloudProviderAzure:
		return NewAzureValidator(), nil
	case domain.CloudProviderTencent:
		return NewTencentValidator(), nil
	case domain.CloudProviderHuawei:
		return NewHuaweiValidator(), nil
	case domain.CloudProviderVolcano, domain.CloudProviderVolcengine:
		return NewVolcanoValidator(), nil
	default:
		return nil, ErrUnsupportedProvider
	}
}

// NewCloudValidatorFactory 创建验证器工厂
func NewCloudValidatorFactory() CloudValidatorFactory {
	return &DefaultCloudValidatorFactory{}
}
