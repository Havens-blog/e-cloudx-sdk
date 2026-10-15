package volcano

import (
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	iamSDK "github.com/volcengine/volcengine-go-sdk/service/iam"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// CreateIAMClient 创建火山云 IAM 客户端。
// IAM 为全局服务(SDK standard_resolver 中 "iam" IsGlobal=true),region 仅为
// SDK 端点解析占位,固定使用 cn-beijing 即可解析到全局端点 open.volcengineapi.com。
func CreateIAMClient(account *domain.CloudAccount) (*iamSDK.IAM, error) {
	if account == nil {
		return nil, fmt.Errorf("cloud account cannot be nil")
	}
	if account.AccessKeyID == "" || account.AccessKeySecret == "" {
		return nil, fmt.Errorf("volcano cloud access key id or secret is empty")
	}

	config := volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(
			account.AccessKeyID,
			account.AccessKeySecret,
			"",
		)).
		WithRegion("cn-beijing")

	sess, err := session.NewSession(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create volcano IAM session: %w", err)
	}

	return iamSDK.New(sess), nil
}
