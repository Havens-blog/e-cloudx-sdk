package aliyun

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/cms"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/sts"
	"github.com/gotomicro/ego/core/elog"
)

// aliyun NAS 指标适配器(NASMetricQuerier 必达厂商之一)。
//
// 规格:proposal「单位归一化与字段语义」+ probe-report §1.1/§4(M1 定案:
// 必达 = aliyun/huawei/aws;aliyun 指标名/namespace 为 CMS 标准路径)。
//
// 指标口径(Namespace=acs_nas,单位均为字节,采集边界 /1024^3 归一 GB):
//   - 通用型 NAS(按量弹性容量):已用数据量 AlignedSize(不含低频存储);
//     无真实总容量指标 → capacity=0(写路径打 qc_status=zero_exception,
//     实盘 capacity=10485760 为名义上限,不可信,probe-report §1.1);
//   - 极速型 NAS:总存储空间 ExtremeCapacity / 已使用数据量 ExtremeCapacityUsed。
//
// 适配器不预知文件系统类型(接口签名仅 fsID/fsName/region),按数据择优:
// 先查 ExtremeCapacityUsed,无数据点再查 AlignedSize;总容量只对极速型存在。
//
// Dimensions 需按文档顺序携带 {"userId","fileSystemId"}(help.aliyun.com
// 「查看通用型NAS容量监控数据」),userId 经 STS GetCallerIdentity 解析并缓存。

var _ cloudx.NASMetricQuerier = (*NASAdapter)(nil)

const (
	// nasMetricNamespace 阿里云 NAS 云监控 namespace(官方文档定案)
	nasMetricNamespace = "acs_nas"
	// nasMetricAlignedSize 通用型 NAS 已用数据量(字节,不含低频存储)
	nasMetricAlignedSize = "AlignedSize"
	// nasMetricExtremeCapacity 极速型 NAS 总存储空间(字节)
	nasMetricExtremeCapacity = "ExtremeCapacity"
	// nasMetricExtremeCapacityUsed 极速型 NAS 已使用数据量(字节)
	nasMetricExtremeCapacityUsed = "ExtremeCapacityUsed"
	// nasDailyPeriod 天粒度聚合(CMS Period 单位秒)
	nasDailyPeriod = "86400"
)

// cmsMetricClient CMS DescribeMetricList 最小接口(测试桩注入)。
type cmsMetricClient interface {
	DescribeMetricList(request *cms.DescribeMetricListRequest) (*cms.DescribeMetricListResponse, error)
}

// nasMetricHooks NAS 指标查询测试注入钩子(非 nil 时替代真实依赖;仅单测使用)。
type nasMetricHooks struct {
	cmsFactory    func(region string) (cmsMetricClient, error)
	resolveUserID func() (string, error)
}

// GetNASMetrics 查询 [startDate, endDate](含两端,YYYY-MM-DD)内该文件系统
// 的逐日容量/用量指标。region 为实例所在地域(调用方从实例元数据取),
// 本适配器按该 region 创建 CMS 客户端,不做全局 region 推断。
//
// 失败路径(API 错误/超时/鉴权):打 ERROR 并携带 error 字段后返回 error,
// 由执行器记入失败计数(proposal「失败可观测性」);真实无数据点返回空切片
// + nil error(非调用失败,不触发失败计数)。
func (a *NASAdapter) GetNASMetrics(ctx context.Context, fsID, fsName, region, startDate, endDate string) ([]types.NASMetric, error) {
	if fsID == "" {
		return nil, fmt.Errorf("阿里云NAS指标查询需要文件系统ID")
	}
	if region == "" {
		return nil, fmt.Errorf("阿里云NAS指标查询需要实例 region(按实例所在 region 查询,不做全局推断)")
	}
	dates, err := metricDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("阿里云NAS指标查询已取消: %w", err)
	}

	client, err := a.createCMSClient(region)
	if err != nil {
		a.logMetricFailure(fsID, region, err)
		return nil, err
	}
	userID, err := a.resolveUserID()
	if err != nil {
		a.logMetricFailure(fsID, region, err)
		return nil, err
	}

	fromMs, toMs := nasRangeBoundsMs(dates)

	// 已用容量:极速型优先,无数据点回退通用型 AlignedSize(按数据择优,不预知类型)
	usedPoints, err := a.fetchNASDaily(client, userID, fsID, nasMetricExtremeCapacityUsed, fromMs, toMs)
	if err != nil {
		a.logMetricFailure(fsID, region, err)
		return nil, err
	}
	if len(usedPoints) == 0 {
		usedPoints, err = a.fetchNASDaily(client, userID, fsID, nasMetricAlignedSize, fromMs, toMs)
		if err != nil {
			a.logMetricFailure(fsID, region, err)
			return nil, err
		}
	}
	if len(usedPoints) == 0 {
		// 真实无指标数据(实例无上报),非调用失败:空切片不触发失败计数
		return []types.NASMetric{}, nil
	}

	// 总容量:仅极速型有真实指标;通用型无该指标 → capacity=0(写路径 zero_exception)
	capPoints, err := a.fetchNASDaily(client, userID, fsID, nasMetricExtremeCapacity, fromMs, toMs)
	if err != nil {
		a.logMetricFailure(fsID, region, err)
		return nil, err
	}

	usedDaily := aggregateCMSDaily(usedPoints)
	capDaily := aggregateCMSDaily(capPoints)
	return buildAliyunNASMetrics(fsID, fsName, dates, usedDaily, capDaily), nil
}

// logMetricFailure 适配器维度失败可观测性:ERROR + error 字段。
func (a *NASAdapter) logMetricFailure(fsID, region string, err error) {
	a.logger.Error("阿里云NAS指标查询失败",
		elog.String("fs_id", fsID),
		elog.String("region", region),
		elog.String("provider", "aliyun"),
		elog.FieldErr(err))
}

// createCMSClient 按实例 region 创建(带缓存的)CMS 客户端。
func (a *NASAdapter) createCMSClient(region string) (cmsMetricClient, error) {
	if a.metricHooks != nil && a.metricHooks.cmsFactory != nil {
		return a.metricHooks.cmsFactory(region)
	}
	a.metricMu.Lock()
	defer a.metricMu.Unlock()
	if c, ok := a.metricClients[region]; ok {
		return c, nil
	}
	credential := credentials.NewAccessKeyCredential(a.accessKeyID, a.accessKeySecret)
	config := sdk.NewConfig()
	config.Scheme = "https"
	client, err := cms.NewClientWithOptions(region, config, credential)
	if err != nil {
		return nil, fmt.Errorf("创建CMS客户端失败: %w", err)
	}
	client.Domain = fmt.Sprintf("metrics.%s.aliyuncs.com", region)
	if a.metricClients == nil {
		a.metricClients = make(map[string]cmsMetricClient)
	}
	a.metricClients[region] = client
	return client, nil
}

// resolveUserID 解析阿里云账号 userId(CMS NAS 指标维度必需),进程内缓存。
func (a *NASAdapter) resolveUserID() (string, error) {
	if a.metricHooks != nil && a.metricHooks.resolveUserID != nil {
		return a.metricHooks.resolveUserID()
	}
	a.userIDOnce.Do(func() {
		// STS 必须走 https:NewClientWithAccessKey 简写默认 Scheme=HTTP,
		// 会被网关拒 InvalidProtocol.NeedSsl;此处与 createCMSClient/GetECSClient 同款显式 https。
		credential := credentials.NewAccessKeyCredential(a.accessKeyID, a.accessKeySecret)
		config := sdk.NewConfig()
		config.Scheme = "https"
		client, err := sts.NewClientWithOptions("cn-hangzhou", config, credential)
		if err != nil {
			a.userIDErr = fmt.Errorf("创建STS客户端失败: %w", err)
			return
		}
		client.Domain = "sts.aliyuncs.com"
		resp, err := client.GetCallerIdentity(sts.CreateGetCallerIdentityRequest())
		if err != nil {
			a.userIDErr = fmt.Errorf("获取阿里云账号 userId 失败: %w", err)
			return
		}
		if resp == nil || resp.UserId == "" {
			a.userIDErr = fmt.Errorf("获取阿里云账号 userId 失败: 响应为空")
			return
		}
		a.userIDValue = resp.UserId
	})
	if a.userIDErr != nil {
		return "", a.userIDErr
	}
	return a.userIDValue, nil
}

// fetchNASDaily 查询单指标 [fromMs, toMs] 的天粒度数据点(NextToken 翻页聚合)。
func (a *NASAdapter) fetchNASDaily(client cmsMetricClient, userID, fsID, metricName string, fromMs, toMs int64) ([]cmsDataPoint, error) {
	var all []cmsDataPoint
	nextToken := ""
	for page := 0; page < 10; page++ {
		request := cms.CreateDescribeMetricListRequest()
		request.Namespace = nasMetricNamespace
		request.MetricName = metricName
		request.Period = nasDailyPeriod
		request.Length = "1000"
		// 维度键顺序按官方文档:userId 在前、fileSystemId 在后
		request.Dimensions = fmt.Sprintf(`{"userId":%q,"fileSystemId":%q}`, userID, fsID)
		request.StartTime = strconv.FormatInt(fromMs, 10)
		request.EndTime = strconv.FormatInt(toMs, 10)
		if nextToken != "" {
			request.NextToken = nextToken
		}
		response, err := client.DescribeMetricList(request)
		if err != nil {
			return nil, fmt.Errorf("查询NAS指标 %s 失败: %w", metricName, err)
		}
		if response == nil {
			break
		}
		points, err := parseCMSDatapoints(response.Datapoints)
		if err != nil {
			return nil, fmt.Errorf("解析NAS指标 %s 响应失败: %w", metricName, err)
		}
		all = append(all, points...)
		if response.NextToken == "" {
			break
		}
		nextToken = response.NextToken
	}
	return all, nil
}

// ==================== 纯解析/聚合函数(单测覆盖) ====================

// cmsDataPoint 单个指标数据点(Timestamp 毫秒,Value 原始单位,通常为字节)。
type cmsDataPoint struct {
	Timestamp int64
	Value     float64
}

// parseCMSDatapoints 解析 DescribeMetricList 响应的 Datapoints JSON 字符串。
// 官方响应字段大小写不统一(timestamp/Timestamp、value/Value),逐键小写匹配;
// 缺 value 时兜底 average/maximum 聚合键;无法提取值的数据点跳过(不伪造 0)。
func parseCMSDatapoints(raw string) ([]cmsDataPoint, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "[]" {
		return nil, nil
	}
	var items []map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
		return nil, fmt.Errorf("解析 Datapoints JSON 失败: %w", err)
	}
	points := make([]cmsDataPoint, 0, len(items))
	for _, item := range items {
		var ts int64
		var val float64
		haveVal := false
		for k, v := range item {
			if strings.ToLower(k) != "timestamp" {
				continue
			}
			if f, ok := cmsNumber(v); ok {
				ts = int64(f)
			}
		}
		for k, v := range item {
			if strings.ToLower(k) != "value" {
				continue
			}
			if f, ok := cmsNumber(v); ok {
				val, haveVal = f, true
			}
		}
		if !haveVal {
			for _, alt := range []string{"average", "maximum"} {
				if f, ok := lookupCMSKey(item, alt); ok {
					val, haveVal = f, true
					break
				}
			}
		}
		if !haveVal {
			continue
		}
		points = append(points, cmsDataPoint{Timestamp: ts, Value: val})
	}
	return points, nil
}

// lookupCMSKey 大小写不敏感取键值(数值形态)。
func lookupCMSKey(item map[string]interface{}, key string) (float64, bool) {
	for k, v := range item {
		if strings.ToLower(k) == key {
			return cmsNumber(v)
		}
	}
	return 0, false
}

// cmsNumber 数值元素提取,容忍 float64/json.Number/字符串数值三形态
// (本包响应经 encoding/json 解码为 float64;对其他解码器形态兜底,
// 与华为包 coerceCESValue 同一防呆思想,防 47da689 同类静默归零)。
func cmsNumber(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// nasRangeBoundsMs 指标查询窗口 [CST 首日 00:00, 末日 +24h) 的毫秒时间戳
// (运营时区 Asia/Shanghai;End 取末日全天覆盖,排除歧义)。
func nasRangeBoundsMs(dates []string) (int64, int64) {
	if len(dates) == 0 {
		return 0, 0
	}
	start, err := time.ParseInLocation("2006-01-02", dates[0], metricCSTZone)
	if err != nil {
		return 0, 0
	}
	end, err := time.ParseInLocation("2006-01-02", dates[len(dates)-1], metricCSTZone)
	if err != nil {
		return 0, 0
	}
	return start.UnixMilli(), end.Add(24*time.Hour).UnixMilli() - 1
}

// aggregateCMSDaily 数据点按运营时区归到自然日;同日多点保留最后出现的点
// (日末态快照口径,proposal「日快照取值口径与采集窗口」)。
func aggregateCMSDaily(points []cmsDataPoint) map[string]float64 {
	result := make(map[string]float64, len(points))
	for _, p := range points {
		date := time.UnixMilli(p.Timestamp).In(metricCSTZone).Format("2006-01-02")
		result[date] = p.Value
	}
	return result
}

// buildAliyunNASMetrics 按日期序列组装指标行:
//   - used 缺失日跳过不落库(缺失日不填充假值);
//   - 字节 → GB 换算在采集边界(types.BytesToGB,Hard Rule);
//   - capacity 无指标数据时为 0(通用型弹性容量无总容量指标),
//     qc_status 由写路径(DAO)打 zero_exception,适配器不越权标注。
func buildAliyunNASMetrics(fsID, fsName string, dates []string, usedDaily, capDaily map[string]float64) []types.NASMetric {
	metrics := make([]types.NASMetric, 0, len(dates))
	for _, d := range dates {
		usedBytes, ok := usedDaily[d]
		if !ok {
			continue
		}
		metrics = append(metrics, types.NASMetric{
			FsID:         fsID,
			FsName:       fsName,
			Date:         d,
			Capacity:     types.BytesToGB(capDaily[d]), // 无数据=0,写路径打 zero_exception
			UsedCapacity: types.BytesToGB(usedBytes),
			Provider:     "aliyun",
			// AccountID 由执行器(T4)按云账号回填,适配器无账号上下文
		})
	}
	return metrics
}
