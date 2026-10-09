// Package aliyun 阿里云 RDS 指标适配器(RDSMetricQuerier 必达厂商之一)。
//
// 平移蓝本:internal/shared/cloudx/aliyun/nas_metrics.go / disk_metrics.go
// (CMS DescribeMetricList + 同包 parseCMSDatapoints/aggregateCMSDaily/
// metricDateRange/nasRangeBoundsMs 全量复用);RDS 是地域性资源且多引擎分派,
// querier 签名带 region + engine(cloudx.RDSMetricQuerier)。
//
// 指标口径(T1 探测定案,probe-report §1.1 / logs/rds_probe_aliyun.txt):
//   - Namespace = acs_rds_dashboard,维度 {"instanceId": rdsID}——探测以单维
//     instanceId 实盘非零验证(5 实例 84 PASS),不需要 NAS 的 userId 维度;
//   - 指标名按引擎分派:SQLServer → SQLServer_CpuUsage / SQLServer_Memory_Usage
//     / SQLServer_DiskUsage(引擎专属名;通用名实盘为 SQLServer 同值口径,
//     但 MemoryUsage 空窗口,按探测择优选引擎专属名);其余引擎(MySQL/PostgreSQL
//     /MariaDB/未知引擎空值兜底)→ 无前缀通用名 CpuUsage / MemoryUsage /
//     DiskUsage;PostgreSQL 专属名实盘未采样验证(未定案),不臆造,通用名兜底,
//     无数据自然空行,待二期复核;
//   - CPU/内存/磁盘均为 % 直给(实盘样本见 probe-report),无换算;
//   - 连接数:无前缀引擎只有 ConnectionUsage(% 使用率口径),须换算绝对值——
//     经 DescribeDBInstanceAttribute 取实例 SDK 字段 MaxConnections,
//     connections = round(usage% × MaxConnections / 100)(换算依据探测后补,
//     注释留痕);SQLServer 无实例级连接指标(探测只有 Group* 群组聚合口径,
//     且 ConnectionUsage 空窗口),connections 留 0 不臆造;
//   - 行存在性:某日四指标至少一个有数据点才出该日行;缺失指标该字段留 0
//     (适配器不填假值),四指标全 0 的 zero_exception 由写路径 DAO 打标。
//
// 失败路径三分:调用失败 ERROR+error 列 failures;真实无指标数据返回空切片
// (INFO 语义,不计失败);MaxConnections 元数据按需获取,只在有连接数据点时调用。
// Hard Rule:指标路径按实例真实 region 创建 CMS 客户端,不做全局 region 推断。
package aliyun

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/cms"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/rds"
	"github.com/gotomicro/ego/core/elog"
)

var _ cloudx.RDSMetricQuerier = (*RDSAdapter)(nil)

// 指标命名空间/指标名/维度名(T1 探测定案,不得臆造未验证名)
const (
	rdsMetricNamespace       = "acs_rds_dashboard"
	rdsMetricCPU             = "CpuUsage"
	rdsMetricMemory          = "MemoryUsage"
	rdsMetricDisk            = "DiskUsage"
	rdsMetricConnectionUsage = "ConnectionUsage"
	rdsMetricSQLServerCPU    = "SQLServer_CpuUsage"
	rdsMetricSQLServerMemory = "SQLServer_Memory_Usage"
	rdsMetricSQLServerDisk   = "SQLServer_DiskUsage"
	rdsDimInstanceID         = "instanceId"
	rdsDailyPeriod           = "86400"
)

// dispatchAliyunRDSMetricNames 按引擎返回四指标名(探测定案前缀分派):
// SQLServer 走引擎专属名且无实例级连接指标(conn 返回空串,连接查询整体跳过);
// 其余引擎(MySQL/PG/MariaDB/空)走无前缀通用名。
func dispatchAliyunRDSMetricNames(engine string) (cpu, mem, disk, conn string) {
	if strings.Contains(strings.ToLower(engine), "sqlserver") {
		return rdsMetricSQLServerCPU, rdsMetricSQLServerMemory, rdsMetricSQLServerDisk, ""
	}
	return rdsMetricCPU, rdsMetricMemory, rdsMetricDisk, rdsMetricConnectionUsage
}

// GetRDSMetrics 采集指定 RDS 实例 [startDate, endDate] 的每日指标。
// 实现 cloudx.RDSMetricQuerier;仅新增方法,不改动 RDSAdapter 既有结构
// (struct 零改动约定——CMS 客户端每次按 instanceId+region 现建)。
func (a *RDSAdapter) GetRDSMetrics(ctx context.Context, rdsID, instanceName, region, engine, startDate, endDate string) ([]types.RDSMetric, error) {
	if rdsID == "" {
		return nil, fmt.Errorf("阿里云RDS指标查询需要实例ID")
	}
	if region == "" {
		return nil, fmt.Errorf("阿里云RDS指标查询需要实例 region(指标路径不做全局推断)")
	}
	dates, err := metricDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("阿里云RDS指标查询已取消: %w", err)
	}

	client, err := a.createRDSMetricClient(region)
	if err != nil {
		a.logRDSMetricFailure(rdsID, engine, region, err)
		return nil, err
	}

	fromMs, toMs := nasRangeBoundsMs(dates)
	cpuName, memName, diskName, connName := dispatchAliyunRDSMetricNames(engine)

	fetch := func(name string) (map[string]float64, error) {
		if name == "" {
			return nil, nil
		}
		points, err := a.fetchRDSMetricDaily(client, rdsID, name, fromMs, toMs)
		if err != nil {
			return nil, fmt.Errorf("查询RDS指标 %s 失败: %w", name, err)
		}
		return aggregateCMSDaily(points), nil
	}

	cpuDaily, err := fetch(cpuName)
	if err != nil {
		a.logRDSMetricFailure(rdsID, engine, region, err)
		return nil, err
	}
	memDaily, err := fetch(memName)
	if err != nil {
		a.logRDSMetricFailure(rdsID, engine, region, err)
		return nil, err
	}
	diskDaily, err := fetch(diskName)
	if err != nil {
		a.logRDSMetricFailure(rdsID, engine, region, err)
		return nil, err
	}
	usageDaily, err := fetch(connName)
	if err != nil {
		a.logRDSMetricFailure(rdsID, engine, region, err)
		return nil, err
	}

	// ConnectionUsage(%) → 连接数绝对值:唯一有据可依的口径是实例规格
	// MaxConnections(DescribeDBInstanceAttribute SDK 字段;探测后补的换算依据)。
	// 触发条件:确有连接数据点才取元数据(SQLServer 无该指标时 usageDaily 为 nil)。
	connDaily, err := a.dailyConnectionCount(ctx, region, rdsID, usageDaily)
	if err != nil {
		a.logRDSMetricFailure(rdsID, engine, region, fmt.Errorf("连接数换算失败: %w", err))
		return nil, err
	}

	return buildAliyunRDSMetrics(rdsID, instanceName, engine, dates, cpuDaily, memDaily, diskDaily, connDaily), nil
}

// dailyConnectionCount 把 ConnectionUsage(% 使用率)日值换算成连接数绝对值:
// connections = round(usage% × MaxConnections / 100);MaxConnections<=0 时
// 视为换算不可靠 → 该指标整体留 0 且 warn 留痕(不伪造,Hard Rule)。
// usageDaily 为 nil 或空(无连接指标/无数据点)时不取元数据,直接返回 nil。
func (a *RDSAdapter) dailyConnectionCount(ctx context.Context, region, rdsID string, usageDaily map[string]float64) (map[string]float64, error) {
	if len(usageDaily) == 0 {
		return nil, nil
	}
	maxConn, err := a.fetchMaxConnections(ctx, region, rdsID)
	if err != nil {
		return nil, err
	}
	if maxConn <= 0 {
		a.logger.Warn("RDS实例MaxConnections为0,连接数换算不可靠,该指标留0",
			elog.String("rds_id", rdsID),
			elog.Int64("max_connections", maxConn))
		return nil, nil
	}
	return convertAliyunConnectionUsage(usageDaily, maxConn), nil
}

// convertAliyunConnectionUsage 纯换算函数(单测覆盖):
// connections = round(usage% × maxConn / 100)。
func convertAliyunConnectionUsage(usageDaily map[string]float64, maxConn int64) map[string]float64 {
	out := make(map[string]float64, len(usageDaily))
	for date, usage := range usageDaily {
		out[date] = math.Round(usage * float64(maxConn) / 100)
	}
	return out
}

// fetchMaxConnections 取实例 MaxConnections(连接数换算的唯一有据输入)。
// 复用 RDSAdapter 既有 getClient(region) 走 DescribeDBInstanceAttribute。
func (a *RDSAdapter) fetchMaxConnections(ctx context.Context, region, rdsID string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	client, err := a.getClient(region)
	if err != nil {
		return 0, err
	}
	request := rds.CreateDescribeDBInstanceAttributeRequest()
	request.Scheme = "https"
	request.DBInstanceId = rdsID
	response, err := client.DescribeDBInstanceAttribute(request)
	if err != nil {
		return 0, fmt.Errorf("获取RDS实例MaxConnections失败: %w", err)
	}
	if response == nil || len(response.Items.DBInstanceAttribute) == 0 {
		return 0, fmt.Errorf("获取RDS实例MaxConnections失败: 实例 %s 不存在", rdsID)
	}
	return int64(response.Items.DBInstanceAttribute[0].MaxConnections), nil
}

// createRDSMetricClient 按实例 region 现建 CMS 客户端(每次调用新建,不缓存:
// RDSAdapter 结构零改动约定,探测/主链路参数不落结构字段)。
func (a *RDSAdapter) createRDSMetricClient(region string) (cmsMetricClient, error) {
	credential := credentials.NewAccessKeyCredential(a.accessKeyID, a.accessKeySecret)
	config := sdk.NewConfig()
	config.Scheme = "https"
	client, err := cms.NewClientWithOptions(region, config, credential)
	if err != nil {
		return nil, fmt.Errorf("创建CMS客户端失败: %w", err)
	}
	client.Domain = fmt.Sprintf("metrics.%s.aliyuncs.com", region)
	return client, nil
}

// fetchRDSMetricDaily 查询单指标 [fromMs, toMs] 天粒度数据点(NextToken 翻页聚合)。
// 平移 fetchNASDaily:维度改为单键 {"instanceId": rdsID}(探测实证维度,不附加 userId)。
func (a *RDSAdapter) fetchRDSMetricDaily(client cmsMetricClient, rdsID, metricName string, fromMs, toMs int64) ([]cmsDataPoint, error) {
	var all []cmsDataPoint
	nextToken := ""
	for page := 0; page < 10; page++ {
		request := cms.CreateDescribeMetricListRequest()
		request.Namespace = rdsMetricNamespace
		request.MetricName = metricName
		request.Period = rdsDailyPeriod
		request.Length = "1000"
		request.Dimensions = fmt.Sprintf(`{%q:%q}`, rdsDimInstanceID, rdsID)
		request.StartTime = strconv.FormatInt(fromMs, 10)
		request.EndTime = strconv.FormatInt(toMs, 10)
		if nextToken != "" {
			request.NextToken = nextToken
		}
		response, err := client.DescribeMetricList(request)
		if err != nil {
			return nil, fmt.Errorf("查询RDS指标 %s 失败: %w", metricName, err)
		}
		if response == nil {
			break
		}
		points, err := parseCMSDatapoints(response.Datapoints)
		if err != nil {
			return nil, fmt.Errorf("解析RDS指标 %s 响应失败: %w", metricName, err)
		}
		all = append(all, points...)
		if response.NextToken == "" {
			break
		}
		nextToken = response.NextToken
	}
	return all, nil
}

// logRDSMetricFailure 适配器维度失败可观测性:ERROR + error 字段。
func (a *RDSAdapter) logRDSMetricFailure(rdsID, engine, region string, err error) {
	a.logger.Error("阿里云RDS指标查询失败",
		elog.String("rds_id", rdsID),
		elog.String("engine", engine),
		elog.String("region", region),
		elog.String("provider", "aliyun"),
		elog.FieldErr(err))
}

// ==================== 纯解析/聚合函数(单测覆盖) ====================

// buildAliyunRDSMetrics 按日期序列组装指标行:
//   - 某日四指标均无数据点则跳过(缺失日不填充假值);
//   - 缺失指标该字段留 0(适配器不填假值),四指标全 0 由写路径 DAO 打
//     qc_status=zero_exception(执行器/适配器不越权打标);
//   - CPU/内存/磁盘 % 直给透传;连接数为已换算绝对值取整;engine 原样透传
//     (写库留痕,读取侧引擎甄别与分派均以此为准)。
func buildAliyunRDSMetrics(rdsID, instanceName, engine string, dates []string, cpuDaily, memDaily, diskDaily, connDaily map[string]float64) []types.RDSMetric {
	metrics := make([]types.RDSMetric, 0, len(dates))
	for _, d := range dates {
		cpu, okC := cpuDaily[d]
		mem, okM := memDaily[d]
		disk, okD := diskDaily[d]
		conn, okN := connDaily[d]
		if !okC && !okM && !okD && !okN {
			continue
		}
		metrics = append(metrics, types.RDSMetric{
			RdsID:         rdsID,
			InstanceName:  instanceName,
			Date:          d,
			CPUPercent:    cpu,
			MemoryPercent: mem,
			DiskPercent:   disk,
			Connections:   int64(math.Round(conn)),
			Engine:        engine,
			Provider:      "aliyun",
		})
	}
	return metrics
}
