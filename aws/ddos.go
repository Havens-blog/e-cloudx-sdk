package aws

// AWS Shield(DDoS)适配器。
//
// Shield 与地域级“实例列表”型 DDoS 产品不同,是账号级全局订阅服务:
//   - 一个账号最多一条 Shield Advanced 订阅(DescribeSubscription);Shield Standard
//     免费防护没有独立订阅对象(DescribeSubscription 返回 ResourceNotFoundException);
//   - 全局唯一端点 shield.us-east-1.amazonaws.com,SDK 默认端点解析器
//     (IsRegionalized=false, PartitionEndpoint=aws-global)把任意地域统一解析到该
//     端点(凭证作用域 us-east-1),因此客户端 region 固定 us-east-1;
//   - 统一格式里以“一条订阅实例”落地,InstanceID 用固定键 shield-subscription
//     (Shield 无实例 ID 概念,账号级唯一订阅)。
//
// 适配要点(与 SDK 实际结构对齐):
//   - shield v1.43.3 的 Subscription.TimeCommitment 是 TimeCommitmentInSeconds
//     int64(秒),不存在 "DURATION"/"DURATIONLESS" 字符串枚举。因此映射为:
//     >0 → 有固定时长承诺的 Advanced 订阅;==0 → 无时长承诺(相当于
//     "DURATIONLESS" 语义)归入 Standard;<0 → 异常值,原值透出不臆断。

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/shield"
	shieldtypes "github.com/aws/aws-sdk-go-v2/service/shield/types"
	"github.com/gotomicro/ego/core/elog"
)

// DDOSAdapter AWS Shield适配器
type DDOSAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string // 与其他适配器构造对齐;Shield 为全局服务,请求固定 shieldGlobalRegion
	logger          *elog.Component
}

const (
	// shieldGlobalRegion Shield 全局端点固定地域。
	// SDK 默认端点解析器(IsRegionalized=false, PartitionEndpoint=aws-global)
	// 将任意地域解析到 shield.us-east-1.amazonaws.com(凭证作用域 us-east-1),
	// 故客户端与返回行的 Region 都固定为 us-east-1,不随调用方 region 变化。
	shieldGlobalRegion = "us-east-1"

	// shieldSubscriptionInstanceID Shield 无实例 ID 概念(账号级唯一订阅),
	// 统一格式需要主键,使用固定键代表“本账号的 Shield 订阅”。
	shieldSubscriptionInstanceID = "shield-subscription"

	// ddosTimeLayout 时间格式 "2006-01-02 15:04:05"。
	// SDK 时间戳本身即 UTC,统一显式转 UTC 再格式化,避免本地时区影响口径。
	ddosTimeLayout = "2006-01-02 15:04:05"
)

// NewDDOSAdapter 创建Shield(DDoS)适配器
func NewDDOSAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *DDOSAdapter {
	return &DDOSAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建Shield客户端。
// Shield 为账号级全局服务,region 固定 shieldGlobalRegion("us-east-1"),
// 忽略调用方传入的 region(端点全球唯一)。
func (a *DDOSAdapter) createClient(ctx context.Context) (*shield.Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(shieldGlobalRegion),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			a.accessKeyID, a.accessKeySecret, "",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("加载AWS配置失败: %w", err)
	}
	return shield.NewFromConfig(cfg), nil
}

// ListInstances 获取Shield订阅实例列表(Shield 账号级全局服务,最多一条)。
func (a *DDOSAdapter) ListInstances(ctx context.Context, region string) ([]types.DDOSInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// ListInstancesWithFilter 带过滤条件获取Shield订阅实例列表。
//
// region 无意义:Shield 是全局服务,订阅与防护对象与地域无关,忽略。
// filter 仅 InstanceName(包含匹配,大小写不敏感)/Status(精确匹配)生效;
// Region/PageNumber/PageSize 对唯一的全局订阅实例无意义,予以忽略
// (DDOSInstanceFilter 未提供 Edition 维度,故 Edition 无法在此过滤)。
func (a *DDOSAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.DDOSInstanceFilter) ([]types.DDOSInstance, error) {
	client, err := a.createClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("创建Shield客户端失败: %w", err)
	}

	// 1) “订阅”主事实:DescribeSubscription 是判定订阅类型的唯一凭据。
	//    失败必须 fail-fast,不做降级返回:订阅类型都无法判定时若返回空列表或
	//    半成品行,上层执行器可能据此误判账号“无 DDoS 实例”而触发删除类动作。
	subOut, err := client.DescribeSubscription(ctx, &shield.DescribeSubscriptionInput{})
	if err != nil {
		return nil, fmt.Errorf("获取Shield订阅失败: %w", err)
	}
	if subOut.Subscription == nil {
		return nil, fmt.Errorf("Shield订阅为空: DescribeSubscription 成功但未返回订阅信息")
	}
	inst := convertSubscriptionToDDOS(subOut.Subscription)

	// 2) 防护对象列表:best-effort 降级。订阅为主事实,防护对象列表失败不影响
	//    主行(仅损失 ARN 明细),Warn 后仍返回订阅实例。
	protections, err := a.listProtections(ctx, client)
	if err != nil {
		a.logger.Warn("获取Shield防护对象列表失败,订阅行将不含防护对象", elog.FieldErr(err))
	} else {
		mergeProtections(&inst, protections)
	}

	instances := filterDDOSInstances([]types.DDOSInstance{inst}, filter)

	a.logger.Info("获取AWS Shield订阅列表成功",
		elog.String("region", region),
		elog.Int("count", len(instances)),
		elog.Int("protected_count", inst.ProtectedIPCount))
	return instances, nil
}

// listProtections 分页拉取本账号全部 Protection(NextToken 循环,直至为空)。
func (a *DDOSAdapter) listProtections(ctx context.Context, client *shield.Client) ([]shieldtypes.Protection, error) {
	var all []shieldtypes.Protection
	var nextToken *string
	for {
		out, err := client.ListProtections(ctx, &shield.ListProtectionsInput{
			NextToken: nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("获取Shield防护对象列表失败: %w", err)
		}
		all = append(all, out.Protections...)
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}
	return all, nil
}

// GetInstance 获取单个Shield实例。
// Shield 全局只有固定实例 shieldSubscriptionInstanceID,其余 ID 直接报不存在。
func (a *DDOSAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.DDOSInstance, error) {
	if instanceID != shieldSubscriptionInstanceID {
		return nil, fmt.Errorf("Shield实例不存在: %s(Shield 全局仅有固定订阅实例 %s)",
			instanceID, shieldSubscriptionInstanceID)
	}
	instances, err := a.ListInstances(ctx, region)
	if err != nil {
		return nil, err
	}
	if len(instances) == 0 {
		return nil, fmt.Errorf("Shield订阅实例不存在: %s", instanceID)
	}
	inst := instances[0]
	return &inst, nil
}

// ListInstancesByIDs 批量获取Shield实例(逐个 GetInstance,失败 Warn 跳过)。
func (a *DDOSAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.DDOSInstance, error) {
	var result []types.DDOSInstance
	for _, id := range instanceIDs {
		inst, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取Shield实例失败", elog.String("instance_id", id), elog.FieldErr(err))
			continue
		}
		result = append(result, *inst)
	}
	return result, nil
}

// GetInstanceStatus 获取实例状态。
// Shield 订阅模型无状态字段(只有 AutoRenew/ProactiveEngagementStatus 等配置项),
// 不臆造状态值,Status 恒为空串——空串是正常返回值而非错误。
func (a *DDOSAdapter) GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error) {
	inst, err := a.GetInstance(ctx, region, instanceID)
	if err != nil {
		return "", err
	}
	return inst.Status, nil
}

// convertSubscriptionToDDOS 把 Shield Subscription 转换为统一 DDOSInstance。
//
// Edition 映射以 SDK 实际字段 TimeCommitmentInSeconds(int64 秒)为凭据,
// 对应任务语义中 "DURATION"/"DURATIONLESS" 两形态加未知分支:
//   - >0:有固定时长承诺的 Advanced 订阅;
//   - ==0:无时长承诺(DURATIONLESS 语义),归入 Standard;
//   - <0:不可能出现的异常值,Edition 原值透出(不臆断)。
func convertSubscriptionToDDOS(sub *shieldtypes.Subscription) types.DDOSInstance {
	if sub == nil {
		// 防御性:nil 订阅 → 零值实例(仅单测路径可达;列表路径对 nil 订阅 fail-fast)。
		return types.DDOSInstance{}
	}

	var edition, name string
	switch {
	case sub.TimeCommitmentInSeconds > 0:
		edition, name = "Advanced", "AWS Shield Advanced"
	case sub.TimeCommitmentInSeconds == 0:
		edition, name = "Standard", "AWS Shield Standard"
	default: // <0 异常值:原值透出,不臆断订阅形态
		edition = strconv.FormatInt(sub.TimeCommitmentInSeconds, 10)
		name = "AWS Shield" // 订阅形态不明,名称不标注具体版本
	}

	inst := types.DDOSInstance{
		// Shield 无实例ID,账号级唯一订阅,统一格式用固定键。
		InstanceID:   shieldSubscriptionInstanceID,
		InstanceName: name,
		Edition:      edition,
		// Shield 全局端点,订阅不隶属于任何业务地域,固定 us-east-1。
		Region:   shieldGlobalRegion,
		Provider: "aws",
		// 订阅模型无状态字段,Status 置空(不臆造);GetInstanceStatus 因此返回空串。
		AutoRenew: sub.AutoRenew == shieldtypes.AutoRenewEnabled,
	}
	if sub.StartTime != nil {
		inst.CreationTime = sub.StartTime.UTC().Format(ddosTimeLayout)
	}
	if sub.EndTime != nil {
		inst.ExpiredTime = sub.EndTime.UTC().Format(ddosTimeLayout)
	}
	return inst
}

// mergeProtections 把 Protection 列表并入订阅实例。
//
// 字段语义:Shield 的防护对象是受防护资源的 ARN(如 EIP/CloudFront/ALB 的 ARN),
// 而非 IP 地址;统一格式中 ProtectedIPs 字段此处承载“受防护资源 ARN 列表”
// (字段名保持通用格式不变,仅内容为 ARN)。
func mergeProtections(inst *types.DDOSInstance, protections []shieldtypes.Protection) {
	if inst == nil {
		return
	}

	arns := make([]string, 0, len(protections))
	for _, p := range protections {
		if p.ResourceArn != nil && strings.TrimSpace(*p.ResourceArn) != "" {
			arns = append(arns, *p.ResourceArn)
		}
	}
	inst.ProtectedIPs = dedupStrings(arns)
	inst.ProtectedIPCount = len(inst.ProtectedIPs)
	inst.Description = fmt.Sprintf("受防护资源 %d 个", inst.ProtectedIPCount)
}

// filterDDOSInstances 内存侧过滤:
// InstanceName 包含匹配(大小写不敏感)、Status 精确匹配,多条件 AND。
// Region/PageNumber/PageSize 对 Shield 唯一的全局订阅实例无意义,忽略;
// DDOSInstanceFilter 无 Edition 维度,无法按 Edition 过滤。
func filterDDOSInstances(instances []types.DDOSInstance, filter *types.DDOSInstanceFilter) []types.DDOSInstance {
	if filter == nil {
		return instances
	}
	var out []types.DDOSInstance
	for _, inst := range instances {
		if filter.InstanceName != "" &&
			!strings.Contains(strings.ToLower(inst.InstanceName), strings.ToLower(filter.InstanceName)) {
			continue
		}
		if filter.Status != "" && inst.Status != filter.Status {
			continue
		}
		out = append(out, inst)
	}
	return out
}
