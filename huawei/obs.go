package huawei

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-obs/obs"
)

// OBSAdapter 华为云 OBS (对象存储服务) 适配器
type OBSAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component

	// OSS 指标查询(CES SYS.OBS)状态,见 obs_metrics.go
	// ossCesHooks 测试注入钩子(非 nil 时替代真实 CES 客户端创建)
	ossCesHooks *cesMetricHooks
}

// NewOBSAdapter 创建 OBS 适配器
func NewOBSAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *OBSAdapter {
	return &OBSAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建 OBS 客户端
func (a *OBSAdapter) createClient(region string) (*obs.ObsClient, error) {
	if region == "" {
		region = a.defaultRegion
	}

	endpoint := fmt.Sprintf("obs.%s.myhuaweicloud.com", region)
	return obs.New(a.accessKeyID, a.accessKeySecret, endpoint)
}

// ListBuckets 获取存储桶列表
func (a *OBSAdapter) ListBuckets(ctx context.Context, region string) ([]types.OSSBucket, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建OBS客户端失败: %w", err)
	}
	defer client.Close()

	output, err := client.ListBuckets(nil)
	if err != nil {
		return nil, fmt.Errorf("获取OBS存储桶列表失败: %w", err)
	}

	var buckets []types.OSSBucket
	for _, bucket := range output.Buckets {
		ossBucket := a.convertToOSSBucket(&bucket)

		// 如果指定了 region，只返回该 region 的 bucket
		if region != "" && ossBucket.Region != region {
			continue
		}

		// N+1 回填存储类型与标签:ListBuckets 响应不含 StorageClass 与 Tags,
		// 与详情接口(GetBucket)保持同源调用——GetBucketMetadata 取存储类型、
		// GetBucketTagging 取标签,口径一致。每桶 2 次额外请求,请求数随桶数
		// 线性增长;失败(无权限/未设置)静默降级为空,前端显示 '-',不打断列表。
		// OBS SDK 内部自动跟随 301 跨区重定向,直接用列表客户端即可。
		metaOutput, err := client.GetBucketMetadata(&obs.GetBucketMetadataInput{Bucket: bucket.Name})
		if err == nil {
			ossBucket.StorageClass = string(metaOutput.StorageClass)
		}

		tagOutput, err := client.GetBucketTagging(bucket.Name)
		if err == nil {
			ossBucket.Tags = mapOBSTags(tagOutput.Tags)
		}

		// 获取 bucket 统计信息（存储大小和对象数量）
		stats, err := a.GetBucketStats(ctx, bucket.Name)
		if err == nil && stats != nil {
			ossBucket.ObjectCount = stats.ObjectCount
			ossBucket.StorageSize = stats.StorageSize
		} else {
			a.logger.Warn("获取OBS存储桶统计信息失败",
				elog.String("bucket", bucket.Name),
				elog.FieldErr(err))
		}

		buckets = append(buckets, ossBucket)
	}

	return buckets, nil
}

// GetBucket 获取单个存储桶详情
func (a *OBSAdapter) GetBucket(ctx context.Context, bucketName string) (*types.OSSBucket, error) {
	client, err := a.createClient("")
	if err != nil {
		return nil, fmt.Errorf("创建OBS客户端失败: %w", err)
	}
	defer client.Close()

	// 获取 bucket 位置
	locationOutput, err := client.GetBucketLocation(bucketName)
	if err != nil {
		return nil, fmt.Errorf("获取存储桶位置失败: %w", err)
	}

	region := locationOutput.Location
	bucket := &types.OSSBucket{
		BucketName:       bucketName,
		Region:           region,
		Location:         region,
		Provider:         string(domain.CloudProviderHuawei),
		ExtranetEndpoint: fmt.Sprintf("%s.obs.%s.myhuaweicloud.com", bucketName, region),
		IntranetEndpoint: fmt.Sprintf("%s.obs.%s.myhuaweicloud.com", bucketName, region),
	}

	// 获取 ACL
	aclOutput, err := client.GetBucketAcl(bucketName)
	if err == nil {
		bucket.ACL = a.parseACL(aclOutput)
	}

	// 获取版本控制状态
	versionOutput, err := client.GetBucketVersioning(bucketName)
	if err == nil {
		bucket.Versioning = string(versionOutput.Status)
	}

	// 获取加密配置
	encryptOutput, err := client.GetBucketEncryption(bucketName)
	if err == nil {
		bucket.ServerSideEncryption = encryptOutput.SSEAlgorithm
		bucket.KMSKeyID = encryptOutput.KMSMasterKeyID
	}

	// 获取存储类型
	metaOutput, err := client.GetBucketMetadata(&obs.GetBucketMetadataInput{Bucket: bucketName})
	if err == nil {
		bucket.StorageClass = string(metaOutput.StorageClass)
	}

	// 获取桶标签(与列表口径一致:未设置标签或失败均静默留空)
	tagOutput, err := client.GetBucketTagging(bucketName)
	if err == nil {
		bucket.Tags = mapOBSTags(tagOutput.Tags)
	}

	return bucket, nil
}

// parseACL 解析 ACL
func (a *OBSAdapter) parseACL(output *obs.GetBucketAclOutput) string {
	if output == nil {
		return "private"
	}

	hasPublicRead := false
	hasPublicWrite := false

	for _, grant := range output.Grants {
		if grant.Grantee.URI == obs.GroupAllUsers {
			if grant.Permission == obs.PermissionRead {
				hasPublicRead = true
			}
			if grant.Permission == obs.PermissionWrite {
				hasPublicWrite = true
			}
		}
	}

	if hasPublicRead && hasPublicWrite {
		return "public-read-write"
	}
	if hasPublicRead {
		return "public-read"
	}
	return "private"
}

// GetBucketStats 获取存储桶统计信息
func (a *OBSAdapter) GetBucketStats(ctx context.Context, bucketName string) (*types.OSSBucketStats, error) {
	client, err := a.createClient("")
	if err != nil {
		return nil, fmt.Errorf("创建OBS客户端失败: %w", err)
	}
	defer client.Close()

	output, err := client.GetBucketStorageInfo(bucketName)
	if err != nil {
		return nil, fmt.Errorf("获取存储桶统计信息失败: %w", err)
	}

	return &types.OSSBucketStats{
		BucketName:  bucketName,
		ObjectCount: int64(output.ObjectNumber),
		StorageSize: output.Size,
	}, nil
}

// ListBucketsWithFilter 带过滤条件获取存储桶列表
func (a *OBSAdapter) ListBucketsWithFilter(ctx context.Context, region string, filter *types.OSSBucketFilter) ([]types.OSSBucket, error) {
	buckets, err := a.ListBuckets(ctx, region)
	if err != nil {
		return nil, err
	}

	if filter == nil {
		return buckets, nil
	}

	var filtered []types.OSSBucket
	for _, bucket := range buckets {
		// 按前缀过滤
		if filter.Prefix != "" && len(bucket.BucketName) >= len(filter.Prefix) {
			if bucket.BucketName[:len(filter.Prefix)] != filter.Prefix {
				continue
			}
		}
		// 按存储类型过滤
		if filter.StorageClass != "" && bucket.StorageClass != filter.StorageClass {
			continue
		}
		filtered = append(filtered, bucket)
	}

	return filtered, nil
}

// convertToOSSBucket 转换为统一的 OSS 存储桶结构。
// ListBuckets 响应不含存储类型与标签,这里的转换不填写这两个字段
// (避免伪造),由 ListBuckets/GetBucket 的 N+1 回填(GetBucketMetadata/
// GetBucketTagging),列表与详情口径一致。
func (a *OBSAdapter) convertToOSSBucket(bucket *obs.Bucket) types.OSSBucket {
	ossBucket := types.OSSBucket{
		BucketName:   bucket.Name,
		Region:       bucket.Location,
		Location:     bucket.Location,
		CreationTime: bucket.CreationDate,
		Provider:     string(domain.CloudProviderHuawei),
	}

	if bucket.Location != "" {
		ossBucket.ExtranetEndpoint = fmt.Sprintf("%s.obs.%s.myhuaweicloud.com", bucket.Name, bucket.Location)
		ossBucket.IntranetEndpoint = fmt.Sprintf("%s.obs.%s.myhuaweicloud.com", bucket.Name, bucket.Location)
	}

	return ossBucket
}

// mapOBSTags 将 OBS GetBucketTagging 返回的 Tags 规整为统一的标签 map。
// 列表与详情共用,保证口径一致;空 Key 或无标签时返回 nil(即不填)。
func mapOBSTags(tags []obs.Tag) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		if t.Key == "" {
			continue
		}
		m[t.Key] = t.Value
	}
	return m
}
