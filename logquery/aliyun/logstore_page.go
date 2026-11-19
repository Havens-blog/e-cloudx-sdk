package aliyun

import "fmt"

// logStorePageSize SLS `GET /logstores` 的默认页大小。
//
// 关键事实（2026-11-19 实测）：该接口不带 offset/size 时服务端只回**第一页 500 条**，
// 且不报错、不返回总数，调用方无从感知还有后续页。jlc-prod-log 实测有 2593 个
// logstore（其中 592 个 <service>-business 应用日志 store），而裸 ListLogStore()
// 只看到 500 个（102 个 -business）——83% 的应用日志源被静默丢失。
// 因此阿里云侧所有 logstore 枚举都必须走 listAllLogStores 分页。
const logStorePageSize = 500

// logStorePageLimit 防御性页数上限（500 × 40 = 20000 个 store）。
// 正常 project 远小于此；触顶说明服务端分页行为异常，直接报错而不是静默截断。
const logStorePageLimit = 40

// logStoreLister 只依赖用到的分页枚举方法，便于单测替换。
type logStoreLister interface {
	ListLogStoreV2(project string, offset, size int, telemetryType string) ([]string, error)
}

// listAllLogStores 分页枚举 project 下的全部 logstore。
//
// 分页以「本页返回数 < pageSize」作为结束条件：SLS 不返回总数，只能靠页未满判断。
// 错误直接上抛——枚举不全比报错更危险（会静默少掉源，且用户无从发现）。
func listAllLogStores(client logStoreLister, project string) ([]string, error) {
	var all []string
	for page := 0; page < logStorePageLimit; page++ {
		names, err := client.ListLogStoreV2(project, len(all), logStorePageSize, "")
		if err != nil {
			return nil, err
		}
		all = append(all, names...)
		if len(names) < logStorePageSize {
			return all, nil
		}
	}
	return nil, fmt.Errorf("aliyun logquery: project %s 的 logstore 枚举超过 %d 页上限（已取 %d 个），疑似服务端分页异常",
		project, logStorePageLimit, len(all))
}
