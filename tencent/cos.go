package tencent

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	cos "github.com/tencentyun/cos-go-sdk-v5"
)

// COSAdapter 腾讯云 COS (对象存储) 适配器
type COSAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component

	// ossMonitorClients OSS 指标云监控客户端(按账号 defaultRegion)缓存(oss_metrics.go)
	ossMonitorClients map[string]nasMonitorClient
	// ossMonitorMu 保护 ossMonitorClients 的并发创建
	ossMonitorMu sync.Mutex
	// ossMetricHooks OSS 指标查询测试注入钩子(非 nil 时替代真实云监控客户端创建;
	// 仅单测使用),见 oss_metrics.go
	ossMetricHooks *ossMetricHooks
}

// NewCOSAdapter 创建 COS 适配器
func NewCOSAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *COSAdapter {
	return &COSAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createServiceClient 创建 COS 服务客户端 (用于列出所有 bucket)
func (a *COSAdapter) createServiceClient() *cos.Client {
	su, _ := url.Parse("https://service.cos.myqcloud.com")
	b := &cos.BaseURL{ServiceURL: su}
	return cos.NewClient(b, &http.Client{
		Transport: &cos.AuthorizationTransport{
			SecretID:  a.accessKeyID,
			SecretKey: a.accessKeySecret,
		},
	})
}

// createBucketClient 创建 COS bucket 客户端
func (a *COSAdapter) createBucketClient(bucketName, region string) *cos.Client {
	bucketURL, _ := url.Parse(fmt.Sprintf("https://%s.cos.%s.myqcloud.com", bucketName, region))
	b := &cos.BaseURL{BucketURL: bucketURL}
	return cos.NewClient(b, &http.Client{
		Transport: &cos.AuthorizationTransport{
			SecretID:  a.accessKeyID,
			SecretKey: a.accessKeySecret,
		},
	})
}

// ListBuckets 获取存储桶列表
func (a *COSAdapter) ListBuckets(ctx context.Context, region string) ([]types.OSSBucket, error) {
	client := a.createServiceClient()

	result, _, err := client.Service.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取COS存储桶列表失败: %w", err)
	}

	var buckets []types.OSSBucket
	for _, bucket := range result.Buckets {
		ossBucket := a.convertToOSSBucket(&bucket)

		// 如果指定了 region，只返回该 region 的 bucket
		if region != "" && ossBucket.Region != region {
			continue
		}

		// N+1 回填标签:Service.Get 列表响应不含标签,需逐桶 GET Bucket ?tagging。
		// 请求数随桶数线性增长(标签为审计要求字段,故接受该成本;
		// 客户端按桶创建、仅构造 URL 无网络开销);
		// 无标签桶返回 404 NoSuchTagSet 属正常,失败均静默降级为空,不打断列表。
		bucketClient := a.createBucketClient(bucket.Name, ossBucket.Region)
		if tagResult, _, err := bucketClient.Bucket.GetTagging(ctx); err == nil {
			ossBucket.Tags = a.mapCOSTags(tagResult.TagSet)
		}

		// 获取 bucket 统计信息（存储大小和对象数量）
		stats, err := a.GetBucketStats(ctx, bucket.Name)
		if err == nil && stats != nil {
			ossBucket.ObjectCount = stats.ObjectCount
			ossBucket.StorageSize = stats.StorageSize
		} else {
			a.logger.Warn("获取COS存储桶统计信息失败",
				elog.String("bucket", bucket.Name),
				elog.FieldErr(err))
		}

		buckets = append(buckets, ossBucket)
	}

	return buckets, nil
}

// GetBucket 获取单个存储桶详情
func (a *COSAdapter) GetBucket(ctx context.Context, bucketName string) (*types.OSSBucket, error) {
	// 从 bucket 名称中提取 region (格式: bucketname-appid)
	// 需要先列出所有 bucket 找到对应的 region
	serviceClient := a.createServiceClient()
	result, _, err := serviceClient.Service.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取存储桶列表失败: %w", err)
	}

	var targetBucket *cos.Bucket
	for i := range result.Buckets {
		if result.Buckets[i].Name == bucketName {
			targetBucket = &result.Buckets[i]
			break
		}
	}

	if targetBucket == nil {
		return nil, fmt.Errorf("存储桶不存在: %s", bucketName)
	}

	bucket := a.convertToOSSBucket(targetBucket)

	// 获取详细信息
	bucketClient := a.createBucketClient(bucketName, bucket.Region)

	// 获取 ACL
	aclResult, _, err := bucketClient.Bucket.GetACL(ctx)
	if err == nil {
		bucket.ACL = a.parseACL(aclResult)
	}

	// 获取版本控制状态
	versionResult, _, err := bucketClient.Bucket.GetVersioning(ctx)
	if err == nil {
		bucket.Versioning = versionResult.Status
	}

	// 获取加密配置
	encryptResult, _, err := bucketClient.Bucket.GetEncryption(ctx)
	if err == nil && encryptResult != nil && encryptResult.Rule != nil {
		bucket.ServerSideEncryption = encryptResult.Rule.SSEAlgorithm
		bucket.KMSKeyID = encryptResult.Rule.KMSMasterKeyID
	}

	// 获取桶标签(与列表口径一致:无标签桶返回 404 NoSuchTagSet,
	// 失败均静默留空,不视为错误)
	tagResult, _, err := bucketClient.Bucket.GetTagging(ctx)
	if err == nil {
		bucket.Tags = a.mapCOSTags(tagResult.TagSet)
	}

	return &bucket, nil
}

// parseACL 解析 ACL
func (a *COSAdapter) parseACL(result *cos.BucketGetACLResult) string {
	if result == nil {
		return "private"
	}

	hasPublicRead := false
	hasPublicWrite := false

	for _, grant := range result.AccessControlList {
		if grant.Grantee.URI == "http://cam.qcloud.com/groups/global/AllUsers" {
			if grant.Permission == "READ" {
				hasPublicRead = true
			}
			if grant.Permission == "WRITE" {
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
func (a *COSAdapter) GetBucketStats(ctx context.Context, bucketName string) (*types.OSSBucketStats, error) {
	// COS 没有直接的统计 API，需要通过清单或遍历对象
	// 这里返回基本信息
	return &types.OSSBucketStats{
		BucketName: bucketName,
	}, nil
}

// ListBucketsWithFilter 带过滤条件获取存储桶列表
func (a *COSAdapter) ListBucketsWithFilter(ctx context.Context, region string, filter *types.OSSBucketFilter) ([]types.OSSBucket, error) {
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

// convertToOSSBucket 转换为统一的 OSS 存储桶结构
func (a *COSAdapter) convertToOSSBucket(bucket *cos.Bucket) types.OSSBucket {
	// 从 Location 提取 region (如 ap-guangzhou)
	region := bucket.Region
	if region == "" {
		// 尝试从 bucket name 中提取
		parts := strings.Split(bucket.Name, "-")
		if len(parts) > 1 {
			// bucket name 格式: name-appid
		}
	}

	ossBucket := types.OSSBucket{
		BucketName:       bucket.Name,
		Region:           region,
		Location:         region,
		Provider:         string(domain.CloudProviderTencent),
		ExtranetEndpoint: fmt.Sprintf("%s.cos.%s.myqcloud.com", bucket.Name, region),
		IntranetEndpoint: fmt.Sprintf("%s.cos.%s.myqcloud.com", bucket.Name, region),
		// StorageClass 不填:COS 没有"桶级默认存储类型"概念(存储类型作用在
		// 对象/生命周期规则上),列表接口(Service.Get)与详情接口均不返回该字段;
		// 硬编码 "STANDARD" 是错误值,删去后置空,由前端显示 '-',列表与详情口径一致。
	}

	// CreationDate 是字符串类型，需要解析
	if bucket.CreationDate != "" {
		if t, err := time.Parse(time.RFC3339, bucket.CreationDate); err == nil {
			ossBucket.CreationTime = t
		}
	}

	return ossBucket
}

// mapCOSTags 将 COS GetTagging 返回的 TagSet 规整为统一的标签 map。
// 列表与详情共用,保证口径一致;空 Key 或无标签时返回 nil(即不填)。
func (a *COSAdapter) mapCOSTags(tagSet []cos.BucketTaggingTag) map[string]string {
	if len(tagSet) == 0 {
		return nil
	}
	tags := make(map[string]string, len(tagSet))
	for _, t := range tagSet {
		if t.Key == "" {
			continue
		}
		tags[t.Key] = t.Value
	}
	return tags
}

// 以下是为了兼容性保留的空实现
var _ = common.NewCredential
var _ = profile.NewClientProfile
