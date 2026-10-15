package aliyun

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	bgp "github.com/aliyun/alibaba-cloud-sdk-go/services/ddosbgp"
	coo "github.com/aliyun/alibaba-cloud-sdk-go/services/ddoscoo"
	"github.com/gotomicro/ego/core/elog"
)

// ddosPageSize DDoS 两条产品线列表接口的单页大小上限。
// ddosbgp DescribeInstanceList 与 ddoscoo DescribeInstances 的 PageSize 上限均为 100。
const ddosPageSize = 100

// ddosTimeLayout DDoS 实例时间字段的格式化布局。
// 与 waf.go convertResourceToInstance 的时间口径一致:毫秒时间戳经
// time.UnixMilli 后按本布局输出(时间戳 <=0 时输出空串)。
const ddosTimeLayout = "2006-01-02T15:04:05Z"

const (
	// editionDDoSBGP ddosbgp(DDoS原生防护)产品线在通用结构中的 Edition 标识。
	editionDDoSBGP = "DDoS原生防护"
	// editionDDoSCoo ddoscoo(DDoS高防)产品线在通用结构中的 Edition 标识。
	editionDDoSCoo = "DDoS高防"
)

// DDOSAdapter 阿里云 DDoS 防护适配器,同时同步两条产品线并合并返回:
//   - DDoS 原生防护(ddosbgp):DescribeInstanceList
//   - DDoS 高防(ddoscoo):DescribeInstances
type DDOSAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewDDOSAdapter 创建 DDoS 防护适配器。
func NewDDOSAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *DDOSAdapter {
	return &DDOSAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createDDoSBGPClient 创建 DDoS 原生防护(ddosbgp)客户端。
func (a *DDOSAdapter) createDDoSBGPClient(region string) (*bgp.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}
	return bgp.NewClientWithAccessKey(region, a.accessKeyID, a.accessKeySecret)
}

// createDDoSCooClient 创建 DDoS 高防(ddoscoo)客户端。
func (a *DDOSAdapter) createDDoSCooClient(region string) (*coo.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}
	return coo.NewClientWithAccessKey(region, a.accessKeyID, a.accessKeySecret)
}

// ListInstances 获取 DDoS 防护实例列表(两条产品线合并)。
func (a *DDOSAdapter) ListInstances(ctx context.Context, region string) ([]types.DDOSInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个 DDoS 防护实例。
// 两个产品线的列表接口都不支持按实例 ID 精确查询,故在合并后的列表内按
// InstanceID 精确查找;找不到返回错误。
func (a *DDOSAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.DDOSInstance, error) {
	instances, err := a.ListInstances(ctx, region)
	if err != nil {
		return nil, err
	}
	return pickInstanceByID(instances, instanceID)
}

// ListInstancesByIDs 批量获取 DDoS 防护实例。
// 逐个 GetInstance,单个失败仅告警并继续(与 waf.go 的 ListInstancesByIDs 同款语义)。
func (a *DDOSAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.DDOSInstance, error) {
	var result []types.DDOSInstance
	for _, id := range instanceIDs {
		inst, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取DDoS防护实例失败", elog.String("instance_id", id), elog.FieldErr(err))
			continue
		}
		result = append(result, *inst)
	}
	return result, nil
}

// GetInstanceStatus 获取 DDoS 防护实例状态。
func (a *DDOSAdapter) GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error) {
	inst, err := a.GetInstance(ctx, region, instanceID)
	if err != nil {
		return "", err
	}
	return inst.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取 DDoS 防护实例列表。
//
// 依次拉取 DDoS 原生防护(ddosbgp)与 DDoS 高防(ddoscoo)两条产品线后合并。
// 任一产品线出错即整体返回错误(fail-fast)——不允许把失败的产品线当空集合:
// 上游同步执行器以本次返回集合为基准删除未出现的本地行,若失败子集被当成
// "已删除",会把线上真实存在的实例误删,故必须两者都成功才合并返回。
//
// filter 仅内存过滤 InstanceName/Status(大小写不敏感包含匹配);
// 其余字段(Region/PageNumber/PageSize)不支持:两条产品线的列表 API 均为全局
// 服务、实例本身无地域概念,且分页参数只作用于单条产品线的 API 拉取循环,
// 对合并后的结果做分页会导致各产品线数据不完整,故直接忽略。
func (a *DDOSAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.DDOSInstanceFilter) ([]types.DDOSInstance, error) {
	if region == "" {
		region = a.defaultRegion
	}

	bgpInstances, err := a.listDDoSBGPInstances(region)
	if err != nil {
		return nil, fmt.Errorf("获取DDoS原生防护(ddosbgp)实例失败: %w", err)
	}

	cooInstances, err := a.listDDoSCooInstances(region)
	if err != nil {
		return nil, fmt.Errorf("获取DDoS高防(ddoscoo)实例失败: %w", err)
	}

	all := append(bgpInstances, cooInstances...)
	all = filterDDOSInstances(all, filter)

	a.logger.Info("获取阿里云DDoS防护实例列表成功",
		elog.String("region", region),
		elog.Int("count", len(all)))

	return all, nil
}

// listDDoSBGPInstances 分页拉取 DDoS 原生防护(ddosbgp)DescribeInstanceList,
// 循环直到返回条数小于单页大小或累计数达到 Total。
func (a *DDOSAdapter) listDDoSBGPInstances(region string) ([]types.DDOSInstance, error) {
	client, err := a.createDDoSBGPClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建DDoS原生防护客户端失败: %w", err)
	}

	var result []types.DDOSInstance
	pageNo := 1
	pageSize := ddosPageSize

	for {
		request := bgp.CreateDescribeInstanceListRequest()
		// DescribeInstanceListRequest 自身未声明 RegionId 字段,但内嵌的
		// requests.RpcRequest(baseRequest)携带 RegionId;赋值后 SDK 签名器
		// (sdk/auth/rpc_signature_composer.go)会将其注入 RegionId 查询参数
		// 用于按地域过滤,并参与地域端点解析。
		request.RegionId = region
		// 分页字段实证:PageNo/PageSize 均为 requests.Integer
		// (requests.NewInteger 赋值,与包内其他 adapter 一致)。
		request.PageNo = requests.NewInteger(pageNo)
		request.PageSize = requests.NewInteger(pageSize)

		response, err := client.DescribeInstanceList(request)
		if err != nil {
			return nil, fmt.Errorf("调用DescribeInstanceList失败: %w", err)
		}

		for _, inst := range response.InstanceList {
			result = append(result, convertDDoSBGPInstanceToDDOS(inst, region))
		}

		if len(response.InstanceList) < pageSize || int64(len(result)) >= response.Total {
			break
		}
		pageNo++
	}

	return result, nil
}

// listDDoSCooInstances 分页拉取 DDoS 高防(ddoscoo)DescribeInstances,
// 循环直到返回条数小于单页大小或累计数达到 TotalCount。
func (a *DDOSAdapter) listDDoSCooInstances(region string) ([]types.DDOSInstance, error) {
	client, err := a.createDDoSCooClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建DDoS高防客户端失败: %w", err)
	}

	var result []types.DDOSInstance
	pageNo := 1
	pageSize := ddosPageSize

	for {
		request := coo.CreateDescribeInstancesRequest()
		// DescribeInstancesRequest 自身未声明 RegionId 字段,但内嵌的
		// requests.RpcRequest(baseRequest)携带 RegionId;赋值后 SDK 签名器
		// (sdk/auth/rpc_signature_composer.go)会将其注入 RegionId 查询参数
		// 用于按地域过滤,并参与地域端点解析。
		request.RegionId = region
		// 分页字段实证:PageNumber/PageSize 均为普通 string 类型
		// (生成的 SDK 结构体如此,故用 strconv.Itoa 赋值)。
		request.PageNumber = strconv.Itoa(pageNo)
		request.PageSize = strconv.Itoa(pageSize)

		response, err := client.DescribeInstances(request)
		if err != nil {
			return nil, fmt.Errorf("调用DescribeInstances失败: %w", err)
		}

		for _, inst := range response.Instances {
			result = append(result, convertDDoSCooInstanceToDDOS(inst, region))
		}

		if len(response.Instances) < pageSize || int64(len(result)) >= response.TotalCount {
			break
		}
		pageNo++
	}

	return result, nil
}

// formatDDOSTime 将毫秒时间戳转换为时间字符串(纯函数,便于单测)。
// 与 waf.go 口径一致:时间戳 <=0 时返回空串。
func formatDDOSTime(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).Format(ddosTimeLayout)
}

// convertDDoSBGPInstanceToDDOS 将 ddosbgp.DescribeInstanceList 返回的 Instance
// 转换为通用 types.DDOSInstance(纯函数,便于单测)。
//
// 映射依据 ddosbgp struct_instance.go 的实证字段:
// InstanceID=InstanceId;InstanceName=Remark;Status=Status(原值透出,不臆断含义);
// Region=传入的 region 参数;Edition="DDoS原生防护";AutoRenew=AutoRenewal;
// CreationTime/ExpiredTime 由 GmtCreate/ExpireTime 毫秒时间戳格式化;
// Provider="aliyun";Description=Desc(为空时回退 Remark)。
//
// 带宽/CC上限、防护IP列表、计费方式、项目/资源组、标签等字段在 ddosbgp
// DescribeInstanceList 返回结构中不存在,一律留零值/空,不捏造。
func convertDDoSBGPInstanceToDDOS(inst bgp.Instance, region string) types.DDOSInstance {
	description := inst.Desc
	if description == "" {
		description = inst.Remark
	}
	return types.DDOSInstance{
		InstanceID:   inst.InstanceId,
		InstanceName: inst.Remark,
		Status:       inst.Status,
		Region:       region,
		Edition:      editionDDoSBGP,
		AutoRenew:    inst.AutoRenewal,
		CreationTime: formatDDOSTime(inst.GmtCreate),
		ExpiredTime:  formatDDOSTime(inst.ExpireTime),
		Description:  description,
		Provider:     "aliyun",
		Tags:         make(map[string]string),
	}
}

// convertDDoSCooInstanceToDDOS 将 ddoscoo.DescribeInstances 返回的 Instance
// 转换为通用 types.DDOSInstance(纯函数,便于单测)。
//
// 映射依据 ddoscoo struct_instance.go 的实证字段:
// InstanceID=InstanceId;InstanceName=Remark;Status=strconv.Itoa(Status)
// (原值透出为字符串,不臆断数值含义);Region=传入的 region 参数;
// Edition="DDoS高防";ProtectedIPs=Ip 非空时放入 []string{Ip};
// CreationTime/ExpiredTime 由 CreateTime/ExpireTime 毫秒时间戳格式化;
// Provider="aliyun";Description 用 fmt.Sprintf 透出原值
// Edition=<原值> IpMode=<原值>(仅记录原始枚举,不臆断语义)。
//
// 带宽/QPS 等字段 ddoscoo 列表接口返回结构中无可实证来源
// (IsFirstOpenBw/IsFirstOpenQps 为首次开通标记,非防护上限),
// 一律留零值/空,不捏造。
func convertDDoSCooInstanceToDDOS(inst coo.Instance, region string) types.DDOSInstance {
	var protectedIPs []string
	if inst.Ip != "" {
		protectedIPs = []string{inst.Ip}
	}
	return types.DDOSInstance{
		InstanceID:   inst.InstanceId,
		InstanceName: inst.Remark,
		Status:       strconv.Itoa(inst.Status),
		Region:       region,
		Edition:      editionDDoSCoo,
		ProtectedIPs: protectedIPs,
		CreationTime: formatDDOSTime(inst.CreateTime),
		ExpiredTime:  formatDDOSTime(inst.ExpireTime),
		Description:  fmt.Sprintf("Edition=%d IpMode=%s", inst.Edition, inst.IpMode),
		Provider:     "aliyun",
		Tags:         make(map[string]string),
	}
}

// pickInstanceByID 在实例列表中按 InstanceID 精确查找(纯函数,便于单测)。
// 找不到返回 error("DDoS防护实例不存在: %s")。
func pickInstanceByID(instances []types.DDOSInstance, id string) (*types.DDOSInstance, error) {
	for i := range instances {
		if instances[i].InstanceID == id {
			return &instances[i], nil
		}
	}
	return nil, fmt.Errorf("DDoS防护实例不存在: %s", id)
}

// filterDDOSInstances 在合并后的结果上做内存过滤(纯函数,便于单测)。
// 仅支持 InstanceName/Status 的大小写不敏感包含匹配,
// filter 其余字段(Region/PageNumber/PageSize)不支持,直接忽略。
func filterDDOSInstances(instances []types.DDOSInstance, filter *types.DDOSInstanceFilter) []types.DDOSInstance {
	if filter == nil {
		return instances
	}
	name := strings.ToLower(strings.TrimSpace(filter.InstanceName))
	status := strings.ToLower(strings.TrimSpace(filter.Status))
	if name == "" && status == "" {
		return instances
	}

	var result []types.DDOSInstance
	for _, inst := range instances {
		if name != "" && !strings.Contains(strings.ToLower(inst.InstanceName), name) {
			continue
		}
		if status != "" && !strings.Contains(strings.ToLower(inst.Status), status) {
			continue
		}
		result = append(result, inst)
	}
	return result
}
