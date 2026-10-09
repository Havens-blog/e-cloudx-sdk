package aliyun

import (
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ram"
)

// CreateRAMClient 创建阿里云RAM客户端
func CreateRAMClient(account *domain.CloudAccount) (*ram.Client, error) {
	if account == nil {
		return nil, fmt.Errorf("cloud account cannot be nil")
	}

	// 创建RAM客户端
	client, err := ram.NewClientWithAccessKey(
		"cn-hangzhou", // RAM服务使用固定区域
		account.AccessKeyID,
		account.AccessKeySecret,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create RAM client: %w", err)
	}

	return client, nil
}
