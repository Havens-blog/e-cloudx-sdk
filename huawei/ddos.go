package huawei

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	aadv1 "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/aad/v1"
	aadmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/aad/v1/model"
	aadregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/aad/v1/region"
)

var _ cloudx.DDOSAdapter = (*DDOSAdapter)(nil)

// DDOSAdapter 华为云Anti-DDoS(DDoS高防 AAD)适配器。
// 同步的是 DDoS 高防实例(实例级,含实例下防护 IP),数据源为 aad/v1 的 ListInstance。
//
// 注意:AAD 是华为云全局服务——SDK 的 region 表只收录 cn-north-4 与 ap-southeast-1,
// 且两者均指向同一个全局端点 https://aad.myhuaweicloud.com;ListInstance 的请求路径
// (/v1/aad/instances)不含任何地域,响应结构也没有地域字段。因此 region 参数对列表
// 结果没有任何影响,仅在响应为空地域时兜底填充通用的 Region 字段,不参与路由。
type DDOSAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewDDOSAdapter 创建华为云Anti-DDoS适配器
func NewDDOSAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *DDOSAdapter {
	return &DDOSAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建AAD客户端。
// AAD 是全局服务,地域不参与路由(见 DDOSAdapter 注释):先用 SafeValueOf 按 waf.go 的
// 口径校验传入地域,若账号地域不在 SDK 地域表内(如 cn-north-1),则回退到 SDK 默认的
// CN_NORTH_4——两者的端点都是全局的 aad.myhuaweicloud.com,回退不影响列表结果。
func (a *DDOSAdapter) createClient(reg string) (*aadv1.AadClient, error) {
	if reg == "" {
		reg = a.defaultRegion
	}

	auth, err := basic.NewCredentialsBuilder().
		WithAk(a.accessKeyID).
		WithSk(a.accessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}

	r, err := aadregion.SafeValueOf(reg)
	if err != nil {
		a.logger.Warn("AAD地域不在SDK地域表内,回退到全局地域cn-north-4",
			elog.String("region", reg))
		r = aadregion.CN_NORTH_4
	}

	client, err := aadv1.AadClientBuilder().
		WithRegion(r).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云AAD客户端失败: %w", err)
	}

	return aadv1.NewAadClient(client), nil
}

// ListInstances 获取DDoS高防实例列表
func (a *DDOSAdapter) ListInstances(ctx context.Context, region string) ([]types.DDOSInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个DDoS高防实例详情(列表内查找)
func (a *DDOSAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.DDOSInstance, error) {
	instances, err := a.ListInstances(ctx, region)
	if err != nil {
		return nil, err
	}
	for _, inst := range instances {
		if inst.InstanceID == instanceID {
			return &inst, nil
		}
	}
	return nil, fmt.Errorf("DDoS高防实例不存在: %s", instanceID)
}

// ListInstancesByIDs 批量获取DDoS高防实例(逐个查询,单个失败仅告警不中断)
func (a *DDOSAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.DDOSInstance, error) {
	var result []types.DDOSInstance
	for _, id := range instanceIDs {
		inst, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取DDoS高防实例失败", elog.String("instance_id", id), elog.FieldErr(err))
			continue
		}
		result = append(result, *inst)
	}
	return result, nil
}

// GetInstanceStatus 获取DDoS高防实例状态
func (a *DDOSAdapter) GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error) {
	inst, err := a.GetInstance(ctx, region, instanceID)
	if err != nil {
		return "", err
	}
	return inst.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取DDoS高防实例列表。
// ListInstance 是 AAD 的一次性列表接口(请求体为空,无分页参数),单次调用取全量,
// 名称/状态过滤在内存中进行(名称大小写不敏感包含匹配)。
// 单次调用失败直接返回错误(fail-fast):宁可让调用方收到明确的错误,也不返回
// "疑似全量被删"的空列表,防止上层资源对账执行器据此误删真实资产。
func (a *DDOSAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.DDOSInstanceFilter) ([]types.DDOSInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建AAD客户端失败: %w", err)
	}

	reg := region
	if reg == "" {
		reg = a.defaultRegion
	}

	response, err := client.ListInstance(&aadmodel.ListInstanceRequest{})
	if err != nil {
		return nil, fmt.Errorf("获取DDoS高防实例列表失败: %w", err)
	}

	// Count 与 Items 是 SDK 同时返回的两个字段,两者长度不一致时以 Items 为准,
	// 不返回错误,仅 debug 日志提示(Count 为 nil 时无法比较,不提示)。
	itemsLen := 0
	if response.Items != nil {
		itemsLen = len(*response.Items)
	}
	if response.Count != nil && itemsLen != int(*response.Count) {
		a.logger.Debug("AAD列表Count与Items长度不一致,以Items为准",
			elog.Int32("count", *response.Count),
			elog.Int("items", itemsLen))
	}

	var result []types.DDOSInstance
	if response.Items != nil {
		for _, item := range *response.Items {
			inst := a.convertAADInstanceToDDOS(&item, reg)
			// 内存过滤:名称大小写不敏感包含匹配;状态精确匹配(厂商原值)
			if filter != nil {
				if filter.InstanceName != "" && !strings.Contains(strings.ToLower(inst.InstanceName), strings.ToLower(filter.InstanceName)) {
					continue
				}
				if filter.Status != "" && inst.Status != filter.Status {
					continue
				}
			}
			result = append(result, inst)
		}
	}

	a.logger.Info("获取华为云DDoS高防实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(result)))
	return result, nil
}

// convertAADInstanceToDDOS 将华为云 AAD 实例(InstanceInfo)映射为通用 DDOSInstance。
// 纯函数:只做字段映射,不读写适配器状态。
// 映射依据全部来自 SDK 结构体的 JSON tag(module cache 中注释为乱码,不回读):
//   - ExpireTime *int64 毫秒时间戳 → ExpiredTime,格式沿用 waf.go 的既有口径
//     "2006-01-02T15:04:05Z"(time.UnixMilli);
//   - ServiceBandwidth *int32 → ServiceBandwidth;SDK 未标注业务带宽单位,
//     BandwidthUnit 留空不猜;
//   - InstanceStatus *int32 → Status 数字原值转字符串;SDK 未标注状态枚举语义,
//     仅原值透出,不臆断为具体状态;
//   - Ips *[]InstanceIpInfo → ProtectedIPs,只取每个 InstanceIpInfo 的 Ip(空值跳过);
//     IP 级的 BasicBandwidth/ElasticBandwidth 是"每IP"口径且单位同样未标注,与实例级
//     字段语义不同,不并入实例级带宽(实例级 BasicBandwidth/ElasticBandwidth 保持 0);
//   - EnterpriseProjectId → ResourceGroupID,与 waf.go 把企业项目归到资源组的既有口径一致;
//   - Description 拼接 "%d 个防护IP" 可读摘要,非厂商字段;
//   - InstanceName 为空时保持空串,不补 "-" 占位。
func (a *DDOSAdapter) convertAADInstanceToDDOS(info *aadmodel.InstanceInfo, region string) types.DDOSInstance {
	if info == nil {
		return types.DDOSInstance{Region: region}
	}

	id := ""
	if info.InstanceId != nil {
		id = *info.InstanceId
	}
	name := ""
	if info.InstanceName != nil {
		name = *info.InstanceName
	}

	// 状态语义 SDK 未标注:数字原值转字符串透出
	status := ""
	if info.InstanceStatus != nil {
		status = strconv.Itoa(int(*info.InstanceStatus))
	}

	// 过期时间:毫秒时间戳,格式与 waf.go 的 Timestamp 处理保持一致
	expiredTime := ""
	if info.ExpireTime != nil {
		expiredTime = time.UnixMilli(*info.ExpireTime).Format("2006-01-02T15:04:05Z")
	}

	var serviceBandwidth int64
	if info.ServiceBandwidth != nil {
		serviceBandwidth = int64(*info.ServiceBandwidth)
	}

	var ips []string
	if info.Ips != nil {
		for _, ipInfo := range *info.Ips {
			if ipInfo.Ip != nil && *ipInfo.Ip != "" {
				ips = append(ips, *ipInfo.Ip)
			}
		}
	}

	resourceGroupID := ""
	if info.EnterpriseProjectId != nil {
		resourceGroupID = *info.EnterpriseProjectId
	}

	return types.DDOSInstance{
		InstanceID:       id,
		InstanceName:     name,
		Status:           status,
		Region:           region,
		Edition:          "Anti-DDoS",
		ServiceBandwidth: serviceBandwidth,
		BandwidthUnit:    "", // SDK 未标注业务带宽单位,留空不猜
		ProtectedIPs:     ips,
		ProtectedIPCount: len(ips),
		ExpiredTime:      expiredTime,
		ResourceGroupID:  resourceGroupID,
		Description:      fmt.Sprintf("%d 个防护IP", len(ips)),
		Provider:         "huawei",
		Tags:             make(map[string]string),
	}
}
