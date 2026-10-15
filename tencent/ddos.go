package tencent

import (
	"context"
	"fmt"
	"strings"

	cloudx "github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	antiddos "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/antiddos/v20200309"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

// 编译期断言:DDOSAdapter 实现 cloudx.DDOSAdapter
var _ cloudx.DDOSAdapter = (*DDOSAdapter)(nil)

// DDOSAdapter 腾讯云DDoS防护适配器。
// 数据源为 antiddos v20200309(全局服务),合并两条产品线:
//   - 高防包 BGP:   DescribeListBGPInstances
//   - 高防IP BGPIP: DescribeListBGPIPInstances
type DDOSAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewDDOSAdapter 创建腾讯云DDoS防护适配器
func NewDDOSAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *DDOSAdapter {
	return &DDOSAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createAntiddosClient 创建 antiddos 客户端。
// antiddos 是腾讯云全局服务,region 固定 ap-guangzhou,与实例所在地域无关
// (实例地域由返回数据里的 Region.Region 提供)。
func (a *DDOSAdapter) createAntiddosClient() (*antiddos.Client, error) {
	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "antiddos.tencentcloudapi.com"
	return antiddos.NewClient(credential, "ap-guangzhou", cpf)
}

// ListInstances 获取腾讯云DDoS防护实例列表(高防包 + 高防IP)
func (a *DDOSAdapter) ListInstances(ctx context.Context, region string) ([]types.DDOSInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个DDoS防护实例详情
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
	return nil, fmt.Errorf("DDoS防护实例不存在: %s", instanceID)
}

// ListInstancesByIDs 批量获取DDoS防护实例(逐个查找;单个失败仅告警跳过,与 WAF 语义一致)
func (a *DDOSAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.DDOSInstance, error) {
	var result []types.DDOSInstance
	for _, id := range instanceIDs {
		inst, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取腾讯云DDoS防护实例失败", elog.String("instance_id", id), elog.FieldErr(err))
			continue
		}
		result = append(result, *inst)
	}
	return result, nil
}

// GetInstanceStatus 获取DDoS防护实例状态
// 返回厂商原值:idle 正常(无攻击)/attacking 攻击中/blocking 封堵中/
// creating 创建中/deblocking 解封中/isolate 回收隔离中。
func (a *DDOSAdapter) GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error) {
	inst, err := a.GetInstance(ctx, region, instanceID)
	if err != nil {
		return "", err
	}
	return inst.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取腾讯云DDoS防护实例列表。
// 过滤支持:filter.InstanceName 非空 → 名称大小写不敏感包含(内存过滤);
// filter.Status/Region/PageNumber/PageSize 暂不支持——SDK 两个产品线的过滤
// 参数集合不一致(如 BGPIP 有 FilterStatus 但语义枚举有出入),统一在内存
// 过滤更稳妥,收到时忽略不做下推。
// fail-fast 语义:高防包、高防IP 任一产品线列表失败,整轮直接返回 error。
// 若只返回另一产品线的子集,同步执行器会按 diff“云端已无这些资产”把本地
// 已存的该产品线行整批误删(限流/瞬时失败时尤其常见),故与 WAF 域名源一致:
// 列表失败宁可整轮不写,也不返回不完整子集。
func (a *DDOSAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.DDOSInstanceFilter) ([]types.DDOSInstance, error) {
	var allInstances []types.DDOSInstance

	// 1. 高防包 BGP(DescribeListBGPInstances)
	bgpList, err := a.listBGPInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取腾讯云高防包实例列表失败: %w", err)
	}
	allInstances = append(allInstances, bgpList...)

	// 2. 高防IP BGPIP(DescribeListBGPIPInstances)
	bgpipList, err := a.listBGPIPInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取腾讯云高防IP实例列表失败: %w", err)
	}
	allInstances = append(allInstances, bgpipList...)

	// 内存过滤:仅支持按实例名称(大小写不敏感包含)
	if filter != nil && filter.InstanceName != "" {
		allInstances = filterDDOSInstancesByName(allInstances, filter.InstanceName)
	}

	a.logger.Info("获取腾讯云DDoS防护实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))
	return allInstances, nil
}

// listBGPInstances 分页拉取高防包(BGP)实例列表。
// 分页参数 Offset/Limit 均为 *uint64,Limit 固定 100(接口最大 100),
// 循环直到本页不足一页或累计达到 Total。
func (a *DDOSAdapter) listBGPInstances(ctx context.Context) ([]types.DDOSInstance, error) {
	client, err := a.createAntiddosClient()
	if err != nil {
		return nil, fmt.Errorf("创建antiddos客户端失败: %w", err)
	}

	var allInstances []types.DDOSInstance
	var offset uint64 = 0
	var limit uint64 = 100

	for {
		request := antiddos.NewDescribeListBGPInstancesRequest()
		request.Offset = &offset
		request.Limit = &limit

		response, err := client.DescribeListBGPInstancesWithContext(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("获取高防包实例列表失败: %w", err)
		}

		page := response.Response.InstanceList
		for _, inst := range page {
			if inst == nil {
				continue
			}
			converted := convertBGPInstanceToDDOS(inst)
			// InstanceDetail.InstanceId 缺失时没有稳定主键参与 diff,跳过该条
			// (不做降级ID:伪造的 ID 在 diff 里反而会造成行错配/误删)。
			if converted.InstanceID == "" {
				a.logger.Warn("高防包实例缺少InstanceId,跳过", elog.String("instance_name", converted.InstanceName))
				continue
			}
			allInstances = append(allInstances, converted)
		}

		if !shouldContinuePaging(len(allInstances), len(page), int(limit), response.Response.Total) {
			break
		}
		offset += limit
	}

	return allInstances, nil
}

// listBGPIPInstances 分页拉取高防IP(BGPIP)实例列表(分页规则同 listBGPInstances)。
func (a *DDOSAdapter) listBGPIPInstances(ctx context.Context) ([]types.DDOSInstance, error) {
	client, err := a.createAntiddosClient()
	if err != nil {
		return nil, fmt.Errorf("创建antiddos客户端失败: %w", err)
	}

	var allInstances []types.DDOSInstance
	var offset uint64 = 0
	var limit uint64 = 100

	for {
		request := antiddos.NewDescribeListBGPIPInstancesRequest()
		request.Offset = &offset
		request.Limit = &limit

		response, err := client.DescribeListBGPIPInstancesWithContext(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("获取高防IP实例列表失败: %w", err)
		}

		page := response.Response.InstanceList
		for _, inst := range page {
			if inst == nil {
				continue
			}
			converted := convertBGPIPInstanceToDDOS(inst)
			if converted.InstanceID == "" {
				a.logger.Warn("高防IP实例缺少InstanceId,跳过", elog.String("instance_name", converted.InstanceName))
				continue
			}
			allInstances = append(allInstances, converted)
		}

		if !shouldContinuePaging(len(allInstances), len(page), int(limit), response.Response.Total) {
			break
		}
		offset += limit
	}

	return allInstances, nil
}

// shouldContinuePaging 高防实例列表分页判定(纯函数,便于单测):
// 上一页返回为空/不足一页(短页),或累计条数已达到接口返回的 Total,即停止翻页。
// accumulated=已累计条数;pageLen=上一页实际返回条数;limit=每页条数;
// total=接口 Total(nil 表示未知,此时仅依赖短页判定)。
func shouldContinuePaging(accumulated, pageLen, limit int, total *uint64) bool {
	if pageLen == 0 || pageLen < limit {
		return false
	}
	if total != nil && uint64(accumulated) >= *total {
		return false
	}
	return true
}

// filterDDOSInstancesByName 按实例名称内存过滤(大小写不敏感包含)。
func filterDDOSInstancesByName(instances []types.DDOSInstance, name string) []types.DDOSInstance {
	needle := strings.ToLower(name)
	filtered := make([]types.DDOSInstance, 0, len(instances))
	for _, inst := range instances {
		if strings.Contains(strings.ToLower(inst.InstanceName), needle) {
			filtered = append(filtered, inst)
		}
	}
	return filtered
}

// convertBGPInstanceToDDOS 将腾讯云高防包(BGP)实例转换为通用 DDOSInstance(纯函数)。
// 字段映射均以 SDK v20200309 注释实证:
//   - BasicBandwidth = SpecificationLimit.ProtectBandwidth,SDK 注释“保底防护峰值,单位Gbps”
//     → BandwidthUnit="Gbps"
//   - ServiceBandwidth = SpecificationLimit.ServiceBandWidth(SDK 注释仅“业务带宽”、
//     未单独标注单位,与保底带宽同源,按 Gbps 口径透传)
//   - AutoRenew = SpecificationLimit.AutoRenewFlag(注释:0=没有开启自动续费,1=开启了自动续费)
//   - ProtectedIPs = EipProductInfos[].Ip(高防包绑定 EIP 属于的云产品信息;
//     按映射约定采用该字段,不使用 InstanceDetail.EipList)
//   - Tags = TagInfoList(TagKey/TagValue 键值标签,BGPInstance 分页列表结构原生携带)
func convertBGPInstanceToDDOS(bgp *antiddos.BGPInstance) types.DDOSInstance {
	if bgp == nil {
		return types.DDOSInstance{Provider: "tencent"}
	}

	inst := types.DDOSInstance{
		Edition:  "高防包",
		Provider: "tencent",
	}

	if bgp.InstanceDetail != nil {
		inst.InstanceID = deref(bgp.InstanceDetail.InstanceId)
	}

	inst.InstanceName = deref(bgp.Name)
	inst.Status = deref(bgp.Status)
	inst.CreationTime = deref(bgp.CreatedTime)
	inst.ExpiredTime = deref(bgp.ExpiredTime)

	// 地域:Region.Region 为空时默认 ap-guangzhou
	inst.Region = "ap-guangzhou"
	if bgp.Region != nil && deref(bgp.Region.Region) != "" {
		inst.Region = *bgp.Region.Region
	}

	if bgp.SpecificationLimit != nil {
		inst.BasicBandwidth = int64(ddUint64(bgp.SpecificationLimit.ProtectBandwidth)) // 单位Gbps
		inst.ServiceBandwidth = int64(ddUint64(bgp.SpecificationLimit.ServiceBandWidth))
		inst.AutoRenew = bgp.SpecificationLimit.AutoRenewFlag != nil && *bgp.SpecificationLimit.AutoRenewFlag == 1
	}
	inst.BandwidthUnit = "Gbps"

	// 防护IP:高防包绑定的 EIP 云产品信息(跳过 nil/空 IP)
	if bgp.EipProductInfos != nil {
		for _, info := range bgp.EipProductInfos {
			if info != nil && info.Ip != nil && *info.Ip != "" {
				inst.ProtectedIPs = append(inst.ProtectedIPs, *info.Ip)
			}
		}
	}
	inst.ProtectedIPCount = len(inst.ProtectedIPs)

	inst.Tags = convertDDOSTags(bgp.TagInfoList)

	return inst
}

// convertBGPIPInstanceToDDOS 将腾讯云高防IP(BGPIP)实例转换为通用 DDOSInstance(纯函数)。
// 字段映射均以 SDK v20200309 注释实证:
//   - BasicBandwidth = SpecificationLimit.ProtectBandwidth,SDK 注释“保底防护峰值,单位Mbps”
//     → BandwidthUnit="Mbps"
//   - ElasticBandwidth = SpecificationLimit.ElasticBandwidth,SDK 注释“弹性防护峰值,单位Mbps”
//   - ServiceBandwidth = SpecificationLimit.NormalBandwidth —— 核对该字段实际注释,
//     SDK v1.3.131 为“正常业务带宽,单位Mbps”(并非 kbps),原值即 Mbps,无需换算
//   - CCQPS = SpecificationLimit.ProtectCCQPS,SDK 注释“CC防护峰值,单位qps”
//   - ProtectedIPs = InstanceDetail.EipList(BGPIPInstance 无 EipProductInfos 字段)
//   - Description 透出高防弹性公网IP状态(EipAddressStatus,仅对高防弹性公网IP实例有效)
//   - Tags = TagInfoList(BGPIPInstance 分页列表结构原生携带)
func convertBGPIPInstanceToDDOS(bgpip *antiddos.BGPIPInstance) types.DDOSInstance {
	if bgpip == nil {
		return types.DDOSInstance{Provider: "tencent"}
	}

	inst := types.DDOSInstance{
		Edition:  "高防IP",
		Provider: "tencent",
	}

	if bgpip.InstanceDetail != nil {
		inst.InstanceID = deref(bgpip.InstanceDetail.InstanceId)
	}

	inst.InstanceName = deref(bgpip.Name)
	inst.Status = deref(bgpip.Status)
	inst.CreationTime = deref(bgpip.CreatedTime)
	inst.ExpiredTime = deref(bgpip.ExpiredTime)

	inst.Region = "ap-guangzhou"
	if bgpip.Region != nil && deref(bgpip.Region.Region) != "" {
		inst.Region = *bgpip.Region.Region
	}

	if bgpip.SpecificationLimit != nil {
		inst.BasicBandwidth = int64(ddUint64(bgpip.SpecificationLimit.ProtectBandwidth))   // 单位Mbps
		inst.ElasticBandwidth = int64(ddUint64(bgpip.SpecificationLimit.ElasticBandwidth)) // 单位Mbps
		inst.ServiceBandwidth = int64(ddUint64(bgpip.SpecificationLimit.NormalBandwidth))  // 单位Mbps(SDK注释实证,非kbps)
		inst.CCQPS = int64(ddUint64(bgpip.SpecificationLimit.ProtectCCQPS))                // 单位qps
		inst.AutoRenew = bgpip.SpecificationLimit.AutoRenewFlag != nil && *bgpip.SpecificationLimit.AutoRenewFlag == 1
	}
	inst.BandwidthUnit = "Mbps"

	if bgpip.InstanceDetail != nil {
		for _, ip := range bgpip.InstanceDetail.EipList {
			if ip != nil && *ip != "" {
				inst.ProtectedIPs = append(inst.ProtectedIPs, *ip)
			}
		}
	}
	inst.ProtectedIPCount = len(inst.ProtectedIPs)

	// 高防弹性公网IP状态原值透出(普通高防IP该字段为 nil,不追加后缀)
	inst.Description = "高防IP"
	if s := deref(bgpip.EipAddressStatus); s != "" {
		inst.Description = fmt.Sprintf("高防IP eip_address_status=%s", s)
	}

	inst.Tags = convertDDOSTags(bgpip.TagInfoList)

	return inst
}

// convertDDOSTags 将 antiddos TagInfo 列表(TagKey/TagValue)转换为通用 Tags map。
// 空 TagKey 或 nil 元素跳过;TagValue 为空保留键(与 EO 标签处理一致)。
func convertDDOSTags(list []*antiddos.TagInfo) map[string]string {
	tags := make(map[string]string)
	for _, t := range list {
		if t == nil {
			continue
		}
		key := deref(t.TagKey)
		if key == "" {
			continue
		}
		tags[key] = deref(t.TagValue)
	}
	return tags
}

// ddUint64 解引用 *uint64,空指针返回 0
func ddUint64(p *uint64) uint64 {
	if p == nil {
		return 0
	}
	return *p
}
