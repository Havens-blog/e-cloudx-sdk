// Package huawei 华为云 RDS 指标适配器(RDSMetricQuerier 必达厂商之一)。
//
// 平移蓝本:internal/shared/cloudx/huawei/nas_metrics.go / disk_metrics.go
// (CES BatchListMetricData + 同包 aggregateCESDaily/cesMetricsOf/dayStartUnixMilli/
// cesPeriodPtr/cesFilterPtr/metricDateRange 全量复用);RDS 是地域性资源且维度键
// 按引擎分派,querier 签名带 region + engine(cloudx.RDSMetricQuerier)。
//
// 指标口径(T1 探测定案,probe-report §1.2 / logs/rds_probe_huawei.txt):
//   - Namespace = SYS.RDS,单次 BatchListMetricData 批量查询四指标;
//   - 维度键按引擎分派:postgresql → postgresql_cluster_id(4 名全 PASS),
//     其余引擎(MySQL 实盘 8/13,空值兜底)→ rds_cluster_id;
//   - CPU=rds001_cpu_util、内存=rds002_mem_util、磁盘=rds039_disk_util,
//     均为 % 直给(实盘非零样本见 probe-report),无换算;
//   - 连接数绝对值:mysql → rds006_conn_count(集群连接数)、
//     postgresql → rds042_database_connections(数据库连接数),count 直给取整;
//   - 指标缺失时该字段留 0(适配器不填假值),四指标全 0 由写路径 DAO 打
//     qc_status=zero_exception;某日四指标均无数据点不出行(缺失日不填假值)。
//
// 失败路径三分:调用失败 ERROR+error;真实无指标数据返回空切片(INFO 语义,
// 不计失败);region 不在 CES 支持列表显式报错(不做单 region 静默回退)。
// Hard Rule:指标路径按实例真实 region 创建 CES 客户端。
package huawei

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	cesv1 "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1"
	cesv1model "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1/model"
	cesv1region "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ces/v1/region"
)

var _ cloudx.RDSMetricQuerier = (*RDSAdapter)(nil)

// 指标命名空间/指标名/维度名(T1 探测定案,不得臆造未验证名)
const (
	rdsCESNamespace     = "SYS.RDS"
	rdsCESMetricCPU     = "rds001_cpu_util"
	rdsCESMetricMemory  = "rds002_mem_util"
	rdsCESMetricDisk    = "rds039_disk_util"
	rdsCESMetricConnMy  = "rds006_conn_count"
	rdsCESMetricConnPG  = "rds042_database_connections"
	rdsCESDimMySQL      = "rds_cluster_id"
	rdsCESDimPostgreSQL = "postgresql_cluster_id"
)

// dispatchHuaweiRDSDimAndConn 按引擎返回维度键与连接指标名(探测定案分派):
// postgresql → postgresql_cluster_id + rds042_database_connections;
// 其余引擎(MySQL 实盘 8/13,空值兜底)→ rds_cluster_id + rds006_conn_count。
func dispatchHuaweiRDSDimAndConn(engine string) (dimKey, connMetric string) {
	if strings.Contains(strings.ToLower(engine), "postgres") {
		return rdsCESDimPostgreSQL, rdsCESMetricConnPG
	}
	return rdsCESDimMySQL, rdsCESMetricConnMy
}

// GetRDSMetrics 采集指定 RDS 实例 [startDate, endDate] 的每日指标。
// 实现 cloudx.RDSMetricQuerier;仅新增方法,不改动 RDSAdapter 既有结构
// (struct 零改动约定——CES 客户端每次按真实 region 现建)。
func (a *RDSAdapter) GetRDSMetrics(ctx context.Context, rdsID, instanceName, region, engine, startDate, endDate string) ([]types.RDSMetric, error) {
	if rdsID == "" {
		return nil, fmt.Errorf("华为云RDS指标查询需要实例ID")
	}
	if region == "" {
		return nil, fmt.Errorf("华为云RDS指标查询需要实例 region(指标路径不做静默回退)")
	}
	dates, err := metricDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("华为云RDS指标查询已取消: %w", err)
	}

	client, err := a.createRDSMetricCESClient(region)
	if err != nil {
		a.logRDSMetricFailure(rdsID, engine, region, err)
		return nil, err
	}

	dimKey, connMetric := dispatchHuaweiRDSDimAndConn(engine)
	from := dayStartUnixMilli(dates[0])
	to := dayStartUnixMilli(dates[len(dates)-1]) + 24*3600*1000 - 1
	request := &cesv1model.BatchListMetricDataRequest{
		Body: &cesv1model.BatchListMetricDataRequestBody{
			Metrics: rdsCESMetricCandidates(rdsID, dimKey, connMetric),
			Period:  cesPeriodPtr(cesv1model.GetBatchPeriodEnum().E_86400),
			Filter:  cesFilterPtr(cesv1model.GetFilterEnum().AVERAGE),
			From:    from,
			To:      to,
		},
	}
	response, err := client.BatchListMetricData(request)
	if err != nil {
		a.logRDSMetricFailure(rdsID, engine, region, err)
		return nil, err
	}
	if response == nil || response.Metrics == nil {
		return nil, fmt.Errorf("华为云RDS指标响应为空")
	}

	// 四序列按 MetricName 从批量响应中取日值(aggregateCESDaily 按运营时区归日,
	// 同日多点保留最后出现的点;AVERAGE 口径下 Average 必有值)
	metrics := *response.Metrics
	cpuDaily := rdsCESDailyByMetric(metrics, rdsCESMetricCPU)
	memDaily := rdsCESDailyByMetric(metrics, rdsCESMetricMemory)
	diskDaily := rdsCESDailyByMetric(metrics, rdsCESMetricDisk)
	connDaily := rdsCESDailyByMetric(metrics, connMetric)

	// 全部指标均无数据点 = 真实无指标上报,返回空切片(不计失败)
	if len(cpuDaily) == 0 && len(memDaily) == 0 && len(diskDaily) == 0 && len(connDaily) == 0 {
		return []types.RDSMetric{}, nil
	}

	return buildHuaweiRDSMetrics(rdsID, instanceName, engine, dates, cpuDaily, memDaily, diskDaily, connDaily), nil
}

// rdsCESMetricCandidates 单实例四指标批量查询体(维度键已按引擎分派)。
func rdsCESMetricCandidates(rdsID, dimKey, connMetric string) []cesv1model.MetricInfo {
	dimensions := []cesv1model.MetricsDimension{{Name: dimKey, Value: rdsID}}
	names := []string{rdsCESMetricCPU, rdsCESMetricMemory, rdsCESMetricDisk, connMetric}
	out := make([]cesv1model.MetricInfo, 0, len(names))
	for _, name := range names {
		out = append(out, cesv1model.MetricInfo{
			Namespace:  rdsCESNamespace,
			MetricName: name,
			Dimensions: dimensions,
		})
	}
	return out
}

// rdsCESDailyByMetric 从批量响应指标列表中按 MetricName 取日聚合值(无匹配/
// 无数据点返回空 map)。
func rdsCESDailyByMetric(metrics []cesv1model.BatchMetricData, want string) map[string]float64 {
	for i := range metrics {
		m := &metrics[i]
		if m.MetricName != want || len(m.Datapoints) == 0 {
			continue
		}
		return aggregateCESDaily(m.Datapoints)
	}
	return nil
}

// createRDSMetricCESClient 按实例真实 region 现建 CES v1 客户端(凭证取自
// RDSAdapter 既有 account 域,结构零改动;region 无效显式报错不回退)。
func (a *RDSAdapter) createRDSMetricCESClient(region string) (cesMetricClient, error) {
	auth, err := basic.NewCredentialsBuilder().
		WithAk(a.account.AccessKeyID).
		WithSk(a.account.AccessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}
	regionObj, err := cesv1region.SafeValueOf(region)
	if err != nil {
		return nil, fmt.Errorf("CES region %s 不在支持列表(RDS指标路径不做静默回退): %w", region, err)
	}
	client, err := cesv1.CesClientBuilder().
		WithRegion(regionObj).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建CES客户端失败: %w", err)
	}
	return cesv1.NewCesClient(client), nil
}

// logRDSMetricFailure 适配器维度失败可观测性:ERROR + error 字段。
func (a *RDSAdapter) logRDSMetricFailure(rdsID, engine, region string, err error) {
	a.logger.Error("华为云RDS指标查询失败",
		elog.String("rds_id", rdsID),
		elog.String("engine", engine),
		elog.String("region", region),
		elog.String("provider", "huawei"),
		elog.FieldErr(err))
}

// ==================== 纯解析/聚合函数(单测覆盖) ====================

// buildHuaweiRDSMetrics 按日期序列组装指标行:
//   - 某日四指标均无数据点则跳过(缺失日不填充假值);
//   - CPU/内存/磁盘 % 直给透传;连接数 count 直给取整;缺失指标该字段留 0
//     (适配器不填假值,四指标全 0 由写路径 DAO 打 zero_exception);
//   - engine 原样透传(写库留痕)。
func buildHuaweiRDSMetrics(rdsID, instanceName, engine string, dates []string, cpuDaily, memDaily, diskDaily, connDaily map[string]float64) []types.RDSMetric {
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
			Provider:      "huawei",
		})
	}
	return metrics
}
