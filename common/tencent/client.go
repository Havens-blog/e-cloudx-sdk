package tencent

import (
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	cam "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cam/v20190116"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

// CreateCAMClient 创建腾讯云 CAM 客户端
func CreateCAMClient(account *domain.CloudAccount) (*cam.Client, error) {
	if account.AccessKeyID == "" || account.AccessKeySecret == "" {
		return nil, fmt.Errorf("tencent cloud secret id or secret key is empty")
	}

	// 创建认证信息
	credential := common.NewCredential(
		account.AccessKeyID,     // SecretId
		account.AccessKeySecret, // SecretKey
	)

	// 创建客户端配置
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "cam.tencentcloudapi.com"
	cpf.HttpProfile.ReqTimeout = 30

	// 腾讯云 CAM 是全局服务，不需要指定区域
	// 但 SDK 要求传入区域参数，使用 "ap-guangzhou" 作为默认值
	region := "ap-guangzhou"
	if len(account.Regions) > 0 && account.Regions[0] != "" {
		region = account.Regions[0]
	}

	// 创建 CAM 客户端
	client, err := cam.NewClient(credential, region, cpf)
	if err != nil {
		return nil, fmt.Errorf("failed to create tencent cloud CAM client: %w", err)
	}

	return client, nil
}
