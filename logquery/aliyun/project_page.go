package aliyun

import (
	"fmt"

	sls "github.com/aliyun/aliyun-log-go-sdk"
)

// projectPageSize SLS `GET /`（ListProject）的单页请求大小（服务端上限 500）。
//
// 关键事实（与 logstore_page.go 的 ListLogStore 同源）：厂商 SDK 的裸
// ListProject() 发不带 offset/size 的 `GET /`，SLS 服务端只回**首页**
// （默认条数），且不报错——账号下 project 排在后面的 `k8s-log-<clusterId>`
// 会被静默漏掉，K8s 事件日志源因此缺失。k8sEventTargets 是当前唯一的
// 裸 ListProject 调用方（provider.go），必须走 listAllProjectNames 分页。
const projectPageSize = 500

// projectPageLimit 防御性页数上限（500 × 20 = 10000 个 project）。
// 正常账号远小于此；触顶说明服务端分页行为异常，直接报错而不是静默截断。
const projectPageLimit = 20

// projectLister 只依赖用到的分页枚举方法，便于单测替换。
type projectLister interface {
	ListProjectV2(offset, size int) ([]sls.LogProject, int, int, error)
}

// listAllProjectNames 分页枚举当前地域的全部 project 名。
//
// 与 listAllLogStores 不同，ListProjectV2 的响应带 total，因此以
// 「已取数 >= total」为结束条件——即使服务端把单页压到比请求的
// pageSize 更小（分页上限收紧时），循环也能继续翻页直到取满。
// 错误直接上抛：枚举不全比报错更危险（会静默少源，且用户无从发现）。
func listAllProjectNames(client projectLister) ([]string, error) {
	var all []string
	total := -1
	for page := 0; page < projectPageLimit; page++ {
		projects, _, respTotal, err := client.ListProjectV2(len(all), projectPageSize)
		if err != nil {
			return nil, err
		}
		if respTotal > total {
			total = respTotal
		}
		for _, pr := range projects {
			all = append(all, pr.Name)
		}
		if len(projects) == 0 {
			break
		}
		if total >= 0 && len(all) >= total {
			return all, nil
		}
	}
	if total > 0 && len(all) < total {
		return nil, fmt.Errorf("aliyun logquery: project 枚举超过 %d 页上限（已取 %d / 共 %d），疑似服务端分页异常",
			projectPageLimit, len(all), total)
	}
	return all, nil
}
