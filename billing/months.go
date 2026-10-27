package billing

import "time"

// EnumerateMonths 返回 [start, end] 时间范围覆盖的所有账期月（含首尾月），
// 格式 "2006-01"。账单类云 API 基本都按账期月查询，适配器必须逐月遍历
// 整个范围，才能满足"返回整个时间范围账单明细"的适配器契约：
//
//	cycles := billing.EnumerateMonths(params.StartTime, params.EndTime)
//	for _, cycle := range cycles { ... 按 cycle 分页拉取当月明细 ... }
//
// 若只查起始月（历史实现），增量采集的起点一旦落在历史账期，每轮都会
// 重复拉取同一批旧账单，后续账期永远不会被覆盖。
func EnumerateMonths(start, end time.Time) []string {
	if end.Before(start) {
		start, end = end, start
	}
	current := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
	endMonth := time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, time.UTC)

	var months []string
	for !current.After(endMonth) {
		months = append(months, current.Format("2006-01"))
		current = current.AddDate(0, 1, 0)
	}
	return months
}
