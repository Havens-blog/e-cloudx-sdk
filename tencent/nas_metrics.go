package tencent

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	cam "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cam/v20190116"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	monitor "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/monitor/v20180724"
)

// tencent NAS 指标适配器(NASMetricQuerier 尽力而为厂商之一,monitor 子包)。
//
// 规格:proposal「单位归一化与字段语义」+ probe-report §1.5/§4(M1 定案:
// tencent 维持尽力而为,0 实例无覆盖损失,monitor 子包照常实现,一行依赖)。
// 实例数为 0 故指标名未经实盘收敛,按官方监控指标文档定案:
//   - Namespace: QCE/CFS;
//   - 维度: appid + FileSystemId(appid 为账号 APPID,经 CAM GetUserAppId
//     解析并缓存,仿 aliyun 适配器 resolveUserID 先例);
//   - 已用/存储量:Storage(官方文档标注单位 GB;proposal「换算对照」与 AC
//     定案按「厂商原始返回为字节 → 采集边界 /1024^3 归一 GB」实现,两处口径
//     以 Reference Files 为准。实盘 0 实例无法活体核验,若实测发现单位偏差,
//     写入路径 [1MB,1PB] 数量级自检会把错行整批拒绝并进入失败计数,可观测
//     不会静默落库,届时按实测修正换算);
//   - 容量使用率:StorageUsage(%);
//   - 总容量:无直接指标(与华为同构),按 used ÷ (StorageUsage/100) 派生,
//     usage≤0 时派生无效 → capacity=0(写路径打 zero_exception,禁 NaN)。
//
// Hard Rule:指标路径按实例真实 region 创建云监控客户端,不做全局 region 推断。

var _ cloudx.NASMetricQuerier = (*CFSAdapter)(nil)

const (
	// nasMonitorNamespace CFS 云监控 namespace(官方文档定案)
	nasMonitorNamespace = "QCE/CFS"
	// nasMetricStorage 文件系统存储量(官方文档单位 GB;按 proposal 口径以字节换算,见文件头注释)
	nasMetricStorage = "Storage"
	// nasMetricStorageUsage 容量使用率(%)
	nasMetricStorageUsage = "StorageUsage"
	// nasDimAppID 云监控维度:账号 APPID(CFS 指标维度组合必带)
	nasDimAppID = "appid"
	// nasDimFileSystemID 云监控维度:文件系统 ID
	nasDimFileSystemID = "FileSystemId"
	// nasDailyPeriod 天粒度聚合(GetMonitorData Period 单位秒)
	nasDailyPeriod = uint64(86400)
	// nasMetricMaxRangeDays 指标查询区间上限(天),与回填最长窗口对齐留缓冲
	nasMetricMaxRangeDays = 92
)

// nasMonitorClient 云监控 GetMonitorData 最小接口(测试桩注入)。
type nasMonitorClient interface {
	GetMonitorData(request *monitor.GetMonitorDataRequest) (*monitor.GetMonitorDataResponse, error)
}

// nasCamClient CAM GetUserAppId 最小接口(测试桩注入)。
type nasCamClient interface {
	GetUserAppId(request *cam.GetUserAppIdRequest) (*cam.GetUserAppIdResponse, error)
}

// nasMetricHooks NAS 指标查询测试注入钩子(非 nil 时替代真实依赖;仅单测使用)。
type nasMetricHooks struct {
	monitorFactory func(region string) (nasMonitorClient, error)
	camFactory     func() (nasCamClient, error)
}

// GetNASMetrics 查询 [startDate, endDate](含两端,YYYY-MM-DD)内该文件系统
// 的逐日存储量/使用率指标。region 为实例所在地域,本适配器按该 region 创建
// 云监控客户端,不做全局 region 推断。
//
// 失败路径(API 错误/超时/鉴权):打 ERROR 并携带 error 字段后返回 error,
// 由执行器记入失败计数(proposal「失败可观测性」);真实无数据点返回空切片
// + nil error(非调用失败,不触发失败计数)。
func (a *CFSAdapter) GetNASMetrics(ctx context.Context, fsID, fsName, region, startDate, endDate string) ([]types.NASMetric, error) {
	if fsID == "" {
		return nil, fmt.Errorf("腾讯云NAS指标查询需要文件系统ID")
	}
	if region == "" {
		return nil, fmt.Errorf("腾讯云NAS指标查询需要实例 region(按实例所在 region 查询,不做全局推断)")
	}
	dates, err := nasMetricDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("腾讯云NAS指标查询已取消: %w", err)
	}

	client, err := a.createNASMonitorClient(region)
	if err != nil {
		a.logMetricFailure(fsID, region, err)
		return nil, err
	}
	appid, err := a.resolveAppID()
	if err != nil {
		a.logMetricFailure(fsID, region, err)
		return nil, err
	}

	startT, endT := nasMonitorRangeBounds(dates)

	storageDaily, err := a.fetchNASMonitorDaily(client, appid, fsID, nasMetricStorage, startT, endT)
	if err != nil {
		a.logMetricFailure(fsID, region, err)
		return nil, err
	}
	if len(storageDaily) == 0 {
		// 真实无指标数据(实例无上报),非调用失败:空切片不触发失败计数
		return []types.NASMetric{}, nil
	}
	usageDaily, err := a.fetchNASMonitorDaily(client, appid, fsID, nasMetricStorageUsage, startT, endT)
	if err != nil {
		a.logMetricFailure(fsID, region, err)
		return nil, err
	}

	return buildTencentNASMetrics(fsID, fsName, dates, storageDaily, usageDaily), nil
}

// logMetricFailure 适配器维度失败可观测性:ERROR + error 字段。
func (a *CFSAdapter) logMetricFailure(fsID, region string, err error) {
	a.logger.Error("腾讯云NAS指标查询失败",
		elog.String("fs_id", fsID),
		elog.String("region", region),
		elog.String("provider", "tencent"),
		elog.FieldErr(err))
}

// createNASMonitorClient 按实例 region 创建(带缓存的)云监控客户端。
func (a *CFSAdapter) createNASMonitorClient(region string) (nasMonitorClient, error) {
	if a.nasMonitorHooks != nil && a.nasMonitorHooks.monitorFactory != nil {
		return a.nasMonitorHooks.monitorFactory(region)
	}
	a.nasMonitorMu.Lock()
	defer a.nasMonitorMu.Unlock()
	if c, ok := a.nasMonitorClients[region]; ok {
		return c, nil
	}
	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "monitor.tencentcloudapi.com"
	client, err := monitor.NewClient(credential, region, cpf)
	if err != nil {
		return nil, fmt.Errorf("创建云监控客户端失败: %w", err)
	}
	if a.nasMonitorClients == nil {
		a.nasMonitorClients = make(map[string]nasMonitorClient)
	}
	a.nasMonitorClients[region] = client
	return client, nil
}

// resolveAppID 解析腾讯云账号 APPID(CFS 云监控维度必带),进程内缓存。
func (a *CFSAdapter) resolveAppID() (string, error) {
	if a.nasMonitorHooks != nil && a.nasMonitorHooks.camFactory != nil {
		client, err := a.nasMonitorHooks.camFactory()
		if err != nil {
			return "", err
		}
		return appIDOf(client)
	}
	a.appidOnce.Do(func() {
		credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
		cpf := profile.NewClientProfile()
		cpf.HttpProfile.Endpoint = "cam.tencentcloudapi.com"
		client, err := cam.NewClient(credential, "", cpf)
		if err != nil {
			a.appidErr = fmt.Errorf("创建CAM客户端失败: %w", err)
			return
		}
		a.appidValue, a.appidErr = appIDOf(client)
	})
	if a.appidErr != nil {
		return "", a.appidErr
	}
	return a.appidValue, nil
}

// appIDOf 从 CAM GetUserAppId 响应提取 APPID。
func appIDOf(client nasCamClient) (string, error) {
	resp, err := client.GetUserAppId(cam.NewGetUserAppIdRequest())
	if err != nil {
		return "", fmt.Errorf("获取腾讯云账号 APPID 失败: %w", err)
	}
	if resp == nil || resp.Response == nil || resp.Response.AppId == nil {
		return "", fmt.Errorf("获取腾讯云账号 APPID 失败: 响应为空")
	}
	return strconv.FormatUint(*resp.Response.AppId, 10), nil
}

// fetchNASMonitorDaily 查询单指标 [startT, endT] 的天粒度数据点。
// GetMonitorData 单请求单指标,数据点上限 7200,天粒度 × 92 天远在限内,
// 无需翻页。
func (a *CFSAdapter) fetchNASMonitorDaily(client nasMonitorClient, appid, fsID, metricName string, startT, endT time.Time) (map[string]float64, error) {
	request := monitor.NewGetMonitorDataRequest()
	request.Namespace = common.StringPtr(nasMonitorNamespace)
	request.MetricName = common.StringPtr(metricName)
	request.Period = common.Uint64Ptr(nasDailyPeriod)
	// 维度键顺序按官方文档:appid 在前、FileSystemId 在后
	request.Instances = []*monitor.Instance{{
		Dimensions: []*monitor.Dimension{
			{Name: common.StringPtr(nasDimAppID), Value: common.StringPtr(appid)},
			{Name: common.StringPtr(nasDimFileSystemID), Value: common.StringPtr(fsID)},
		},
	}}
	request.StartTime = common.StringPtr(formatNasMonitorTime(startT))
	request.EndTime = common.StringPtr(formatNasMonitorTime(endT))

	response, err := client.GetMonitorData(request)
	if err != nil {
		return nil, fmt.Errorf("查询NAS指标 %s 失败: %w", metricName, err)
	}
	return aggregateNASMonitorDaily(response), nil
}

// ==================== 纯解析/聚合函数(单测覆盖) ====================

// nasMetricCSTZone NAS 指标按运营时区(Asia/Shanghai)取日,勿改用服务器本地时区
var nasMetricCSTZone = time.FixedZone("CST", 8*3600)

// nasMetricDateRange 解析 [startDate, endDate](含两端)为日期切片(YYYY-MM-DD)。
func nasMetricDateRange(startDate, endDate string) ([]string, error) {
	start, err := time.ParseInLocation("2006-01-02", startDate, nasMetricCSTZone)
	if err != nil {
		return nil, fmt.Errorf("解析 startDate 失败: %w", err)
	}
	end, err := time.ParseInLocation("2006-01-02", endDate, nasMetricCSTZone)
	if err != nil {
		return nil, fmt.Errorf("解析 endDate 失败: %w", err)
	}
	if end.Before(start) {
		return nil, fmt.Errorf("startDate %s 晚于 endDate %s", startDate, endDate)
	}
	dates := make([]string, 0, int(end.Sub(start)/24/time.Hour)+1)
	for t := start; !t.After(end); t = t.AddDate(0, 0, 1) {
		if len(dates) >= nasMetricMaxRangeDays {
			return nil, fmt.Errorf("指标查询区间超过 %d 天", nasMetricMaxRangeDays)
		}
		dates = append(dates, t.Format("2006-01-02"))
	}
	return dates, nil
}

// nasMonitorRangeBounds 指标查询窗口 [CST 首日 00:00, 末日 +24h)。
// GetMonitorData 的 86400 秒桶与 epoch 对齐,CST 日边界恰为整桶。
func nasMonitorRangeBounds(dates []string) (time.Time, time.Time) {
	if len(dates) == 0 {
		return time.Time{}, time.Time{}
	}
	start, err := time.ParseInLocation("2006-01-02", dates[0], nasMetricCSTZone)
	if err != nil {
		return time.Time{}, time.Time{}
	}
	end, err := time.ParseInLocation("2006-01-02", dates[len(dates)-1], nasMetricCSTZone)
	if err != nil {
		return time.Time{}, time.Time{}
	}
	return start, end.Add(24 * time.Hour)
}

// formatNasMonitorTime GetMonitorData 起止时间格式(官方示例 "2018-09-22T19:51:23+08:00")。
func formatNasMonitorTime(t time.Time) string {
	return t.In(nasMetricCSTZone).Format("2006-01-02T15:04:05+08:00")
}

// aggregateNASMonitorDaily 数据点按运营时区归到自然日;同日多点保留最后出现的
// 点(日末态快照口径)。响应无数据(DataPoints 为空)返回空 map。
func aggregateNASMonitorDaily(response *monitor.GetMonitorDataResponse) map[string]float64 {
	result := make(map[string]float64)
	if response == nil || response.Response == nil {
		return result
	}
	for _, dp := range response.Response.DataPoints {
		if dp == nil || len(dp.Timestamps) != len(dp.Values) {
			continue
		}
		for i, ts := range dp.Timestamps {
			if ts == nil || dp.Values[i] == nil {
				continue // 无值数据点跳过,不伪造 0
			}
			date := time.Unix(int64(*ts), 0).In(nasMetricCSTZone).Format("2006-01-02")
			result[date] = *dp.Values[i]
		}
	}
	return result
}

// deriveNASCapacityFromUsage 腾讯无总容量直接指标,按使用率派生:
// capacity = used ÷ (StorageUsage/100)。usage<=0 时派生无效:返回 0
// (写路径打 zero_exception),禁止 NaN(与华为派生同构)。
func deriveNASCapacityFromUsage(usedGB, usagePercent float64) float64 {
	if usagePercent <= 0 {
		return 0
	}
	return usedGB / (usagePercent / 100)
}

// buildTencentNASMetrics 按日期序列组装指标行:
//   - used 缺失日跳过不落库(缺失日不填充假值);
//   - 字节 → GB 换算在采集边界(types.BytesToGB,Hard Rule,与 T3 三厂商共用);
//   - capacity 由 StorageUsage 派生,usage 缺失/为 0 时 capacity=0(零值例外
//     放行打标由写路径处理),不写 NaN。
func buildTencentNASMetrics(fsID, fsName string, dates []string, storageDaily, usageDaily map[string]float64) []types.NASMetric {
	metrics := make([]types.NASMetric, 0, len(dates))
	for _, d := range dates {
		raw, ok := storageDaily[d]
		if !ok {
			continue
		}
		usage := 0.0
		if usageDaily != nil {
			usage = usageDaily[d]
		}
		usedGB := types.BytesToGB(raw)
		metrics = append(metrics, types.NASMetric{
			FsID:         fsID,
			FsName:       fsName,
			Date:         d,
			Capacity:     deriveNASCapacityFromUsage(usedGB, usage),
			UsedCapacity: usedGB,
			Provider:     "tencent",
			// AccountID 由执行器(T4)按云账号回填,适配器无账号上下文
		})
	}
	return metrics
}
