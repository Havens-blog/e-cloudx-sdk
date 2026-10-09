package huawei

import (
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/config"
	iam "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/iam/v3"
	iamregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/iam/v3/region"
)

// CreateIAMClient 创建华为云 IAM 客户端
func CreateIAMClient(account *domain.CloudAccount) (*iam.IamClient, error) {
	if account.AccessKeyID == "" || account.AccessKeySecret == "" {
		return nil, fmt.Errorf("huawei cloud access key id or secret is empty")
	}

	// 创建认证信息
	auth := basic.NewCredentialsBuilder().
		WithAk(account.AccessKeyID).
		WithSk(account.AccessKeySecret).
		Build()

	// 华为云 IAM 是全局服务，使用 cn-north-4 作为默认区域
	region := iamregion.CN_NORTH_4
	if len(account.Regions) > 0 && account.Regions[0] != "" {
		// 尝试从字符串解析区域
		if r, err := iamregion.SafeValueOf(account.Regions[0]); err == nil {
			region = r
		}
	}

	// 创建客户端配置
	hcConfig := config.DefaultHttpConfig().
		WithIgnoreSSLVerification(false).
		WithTimeout(30)

	// 创建 IAM 客户端
	client := iam.NewIamClient(
		iam.IamClientBuilder().
			WithRegion(region).
			WithCredential(auth).
			WithHttpConfig(hcConfig).
			Build())

	return client, nil
}
