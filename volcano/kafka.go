package volcano

import (
"context"
"fmt"
"slices"
"time"

"github.com/Havens-blog/e-cloudx-sdk/types"
"github.com/gotomicro/ego/core/elog"
"github.com/volcengine/volcengine-go-sdk/service/kafka"
"github.com/volcengine/volcengine-go-sdk/volcengine"
"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// KafkaAdapter Volcano Engine Kafka Adapter
type KafkaAdapter struct {
accessKeyID     string
accessKeySecret string
defaultRegion   string
logger          *elog.Component
}

// NewKafkaAdapter creates a Kafka adapter
func NewKafkaAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *KafkaAdapter {
return &KafkaAdapter{
accessKeyID:     accessKeyID,
accessKeySecret: accessKeySecret,
defaultRegion:   defaultRegion,
logger:          logger,
}
}

// createClient creates a Kafka client
func (a *KafkaAdapter) createClient(region string) (*kafka.KAFKA, error) {
if region == "" {
region = a.defaultRegion
}

config := volcengine.NewConfig().
WithCredentials(credentials.NewStaticCredentials(a.accessKeyID, a.accessKeySecret, "")).
WithRegion(region)

sess, err := session.NewSession(config)
if err != nil {
return nil, fmt.Errorf("failed to create session: %w", err)
}

return kafka.New(sess), nil
}

// ListInstances gets Kafka instance list
func (a *KafkaAdapter) ListInstances(ctx context.Context, region string) ([]types.KafkaInstance, error) {
client, err := a.createClient(region)
if err != nil {
return nil, fmt.Errorf("failed to create Kafka client: %w", err)
}

var instances []types.KafkaInstance
pageNumber := int32(1)
pageSize := int32(100)

for {
input := &kafka.DescribeInstancesInput{
PageNumber: volcengine.Int32(pageNumber),
PageSize:   volcengine.Int32(pageSize),
}

output, err := client.DescribeInstances(input)
if err != nil {
return nil, fmt.Errorf("failed to get Kafka instance list: %w", err)
}

if len(output.InstancesInfo) == 0 {
break
}

for _, inst := range output.InstancesInfo {
instance := a.convertToKafkaInstance(inst, region)
instances = append(instances, instance)
}

if len(output.InstancesInfo) < int(pageSize) {
break
}
pageNumber++
}

return instances, nil
}

// GetInstance gets a single Kafka instance detail
func (a *KafkaAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.KafkaInstance, error) {
client, err := a.createClient(region)
if err != nil {
return nil, fmt.Errorf("failed to create Kafka client: %w", err)
}

input := &kafka.DescribeInstanceDetailInput{
InstanceId: volcengine.String(instanceID),
}

output, err := client.DescribeInstanceDetail(input)
if err != nil {
return nil, fmt.Errorf("failed to get Kafka instance detail: %w", err)
}

if output.BasicInstanceInfo == nil {
return nil, fmt.Errorf("Kafka instance not found: %s", instanceID)
}

instance := a.convertDetailToKafkaInstance(output, region)
return &instance, nil
}

// ListInstancesByIDs gets Kafka instances by IDs
func (a *KafkaAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.KafkaInstance, error) {
var instances []types.KafkaInstance
for _, id := range instanceIDs {
instance, err := a.GetInstance(ctx, region, id)
if err != nil {
a.logger.Warn("failed to get Kafka instance", elog.String("instance_id", id), elog.FieldErr(err))
continue
}
instances = append(instances, *instance)
}
return instances, nil
}

// GetInstanceStatus gets instance status
func (a *KafkaAdapter) GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error) {
instance, err := a.GetInstance(ctx, region, instanceID)
if err != nil {
return "", err
}
return instance.Status, nil
}

// ListInstancesWithFilter gets instances with filter
func (a *KafkaAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.KafkaInstanceFilter) ([]types.KafkaInstance, error) {
instances, err := a.ListInstances(ctx, region)
if err != nil {
return nil, err
}

if filter == nil {
return instances, nil
}

var filtered []types.KafkaInstance
for _, inst := range instances {
if len(filter.Status) > 0 && !slices.Contains(filter.Status, inst.Status) {
continue
}
if filter.InstanceName != "" && inst.InstanceName != filter.InstanceName {
continue
}
if filter.VPCID != "" && inst.VPCID != filter.VPCID {
continue
}
filtered = append(filtered, inst)
}

return filtered, nil
}

// convertToKafkaInstance converts to unified Kafka instance structure
func (a *KafkaAdapter) convertToKafkaInstance(inst *kafka.InstancesInfoForDescribeInstancesOutput, region string) types.KafkaInstance {
instance := types.KafkaInstance{
Region:   region,
Provider: "volcano",
}

// 状态走规范表 types.KafkaStatus("volcano", ...)（P0-3：原实现恒 "running"，创建/删除/异常态全部误报运行中）
if inst.InstanceStatus != nil {
instance.Status = types.KafkaStatus("volcano", *inst.InstanceStatus)
} else {
instance.Status = "unknown"
}
if inst.InstanceId != nil {
instance.InstanceID = *inst.InstanceId
}
if inst.InstanceName != nil {
instance.InstanceName = *inst.InstanceName
}
if inst.InstanceDescription != nil {
instance.Description = *inst.InstanceDescription
}
if inst.Version != nil {
instance.Version = *inst.Version
}
if inst.StorageSpace != nil {
instance.DiskSize = int64(*inst.StorageSpace)
}
if inst.UsedStorageSpace != nil {
instance.DiskUsed = int64(*inst.UsedStorageSpace)
}
if inst.ComputeSpec != nil {
instance.SpecType = *inst.ComputeSpec
}
if inst.VpcId != nil {
instance.VPCID = *inst.VpcId
}
if inst.SubnetId != nil {
instance.VSwitchID = *inst.SubnetId
}
if inst.CreateTime != nil {
if t, err := time.Parse("2006-01-02T15:04:05Z", *inst.CreateTime); err == nil {
instance.CreationTime = t
}
}
if inst.ZoneId != nil {
instance.Zone = *inst.ZoneId
}
// 补充此前未映射的 SDK 已返回字段：存储类型/项目/标签/计费类型
if inst.StorageType != nil {
instance.DiskType = *inst.StorageType
}
if inst.ProjectName != nil {
instance.ProjectName = *inst.ProjectName
}
if len(inst.Tags) > 0 {
instance.Tags = volcKafkaTagsToStrings(inst.Tags)
}
if inst.ChargeDetail != nil {
if inst.ChargeDetail.ChargeType != nil {
instance.ChargeType = *inst.ChargeDetail.ChargeType
}
if inst.ChargeDetail.ChargeExpireTime != nil {
if t, err := time.Parse("2006-01-02T15:04:05Z", *inst.ChargeDetail.ChargeExpireTime); err == nil {
instance.ExpiredTime = t
}
}
}

return instance
}

// convertDetailToKafkaInstance converts from detail output
func (a *KafkaAdapter) convertDetailToKafkaInstance(output *kafka.DescribeInstanceDetailOutput, region string) types.KafkaInstance {
instance := types.KafkaInstance{
Region:   region,
Provider: "volcano",
}

if output.BasicInstanceInfo != nil {
info := output.BasicInstanceInfo
// 状态走规范表 types.KafkaStatus("volcano", ...)（P0-3：原实现恒 "running"）
if info.InstanceStatus != nil {
instance.Status = types.KafkaStatus("volcano", *info.InstanceStatus)
} else {
instance.Status = "unknown"
}
if info.InstanceId != nil {
instance.InstanceID = *info.InstanceId
}
if info.InstanceName != nil {
instance.InstanceName = *info.InstanceName
}
if info.InstanceDescription != nil {
instance.Description = *info.InstanceDescription
}
if info.Version != nil {
instance.Version = *info.Version
}
if info.StorageSpace != nil {
instance.DiskSize = int64(*info.StorageSpace)
}
if info.UsedStorageSpace != nil {
instance.DiskUsed = int64(*info.UsedStorageSpace)
}
if info.ComputeSpec != nil {
instance.SpecType = *info.ComputeSpec
}
if info.VpcId != nil {
instance.VPCID = *info.VpcId
}
if info.SubnetId != nil {
instance.VSwitchID = *info.SubnetId
}
if info.CreateTime != nil {
if t, err := time.Parse("2006-01-02T15:04:05Z", *info.CreateTime); err == nil {
instance.CreationTime = t
}
}
if info.ZoneId != nil {
instance.Zone = *info.ZoneId
}
// 补充此前未映射的 SDK 已返回字段：存储类型/项目/标签
if info.StorageType != nil {
instance.DiskType = *info.StorageType
}
if info.ProjectName != nil {
instance.ProjectName = *info.ProjectName
}
if len(info.Tags) > 0 {
instance.Tags = volcKafkaTagsToStrings(info.Tags)
}
}

// 计费信息来自顶层 ChargeDetail：计费类型 + 过期时间
if output.ChargeDetail != nil {
if output.ChargeDetail.ChargeType != nil {
instance.ChargeType = *output.ChargeDetail.ChargeType
}
if output.ChargeDetail.ChargeExpireTime != nil {
if t, err := time.Parse("2006-01-02T15:04:05Z", *output.ChargeDetail.ChargeExpireTime); err == nil {
instance.ExpiredTime = t
}
}
}

return instance
}

// volcKafkaTagsToStrings 火山 Kafka SDK 标签 map[string]*string → 统一 map[string]string
func volcKafkaTagsToStrings(src map[string]*string) map[string]string {
tags := make(map[string]string, len(src))
for k, v := range src {
if v != nil {
tags[k] = *v
}
}
return tags
}
