package aws

import (
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

// CreateIAMClient 创建 AWS IAM 客户端
func CreateIAMClient(account *domain.CloudAccount) (*iam.Client, error) {
	if account == nil {
		return nil, fmt.Errorf("cloud account cannot be nil")
	}

	// 创建凭证提供者
	creds := credentials.NewStaticCredentialsProvider(
		account.AccessKeyID,
		account.AccessKeySecret,
		"", // session token (可选)
	)

	// 创建配置
	cfg := aws.Config{
		Region:      "us-east-1", // IAM 是全局服务，使用默认区域
		Credentials: creds,
	}

	// 创建 IAM 客户端
	client := iam.NewFromConfig(cfg)

	return client, nil
}
