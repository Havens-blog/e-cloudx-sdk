package aliyun

import (
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/resourcemanager"
)

// formatGroupName 合成可读资源组名称：有中文显示名（备注）且与代码名不同 → 「代码（中文）」；
// 否则退化为代码名（中文名缺失或与代码相同时）。Name 为空时直接降级到中文名。
func formatGroupName(name, displayName string) string {
	if name == "" {
		return displayName
	}
	if displayName == "" || displayName == name {
		return name
	}
	return name + "（" + displayName + "）"
}

// ListResourceGroupNames 拉取阿里云账号下全部资源组，返回 ID -> 合成名称 映射。
// 资源组为账号级（不区分地域），数量通常很少（<100），单页 100 兜底翻页。
// 名称按 formatGroupName 合成（代码 + 中文备注）；结果供资产同步写入 attributes.resource_group_name。
func ListResourceGroupNames(account *domain.CloudAccount) (map[string]string, error) {
	if account == nil {
		return nil, fmt.Errorf("账号不能为空")
	}

	client, err := resourcemanager.NewClientWithAccessKey("cn-hangzhou", account.AccessKeyID, account.AccessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("创建资源管理器客户端失败: %w", err)
	}

	result := make(map[string]string)
	request := resourcemanager.CreateListResourceGroupsRequest()
	request.Scheme = "https"
	request.PageSize = requests.NewInteger(100)

	collected := 0
	for page := 1; ; page++ {
		request.PageNumber = requests.NewInteger(page)
		resp, err := client.ListResourceGroups(request)
		if err != nil {
			return nil, fmt.Errorf("列出阿里云资源组失败: %w", err)
		}

		groups := resp.ResourceGroups.ResourceGroup
		collected += len(groups)
		for _, rg := range groups {
			if rg.Id == "" {
				continue
			}
			if name := formatGroupName(rg.Name, rg.DisplayName); name != "" {
				result[rg.Id] = name
			}
		}

		// 末页判定：空页，或已收齐 TotalCount（阿里云 list 接口 TotalCount 可信）。
		// 不以「返回条数 < 请求的 PageSize」判末页——API 可能缩小页大小，会漏翻。
		if len(groups) == 0 || (resp.TotalCount > 0 && collected >= resp.TotalCount) {
			break
		}
	}

	return result, nil
}