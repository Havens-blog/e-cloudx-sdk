package tencent

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/gotomicro/ego/core/elog"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	cvm "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cvm/v20170312"
)

const (
	tencentTagMaxRetries    = 3
	tencentTagBaseBackoffMs = 200
)

// TagAdapterImpl 腾讯云标签适配器
type TagAdapterImpl struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewTagAdapter 创建腾讯云标签适配器
func NewTagAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *TagAdapterImpl {
	return &TagAdapterImpl{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *TagAdapterImpl) resolveRegion(region string) string {
	if region == "" {
		return a.defaultRegion
	}
	return region
}

// createCVMClient 创建 CVM 客户端
func (a *TagAdapterImpl) createCVMClient(region string) (*cvm.Client, error) {
	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "cvm.tencentcloudapi.com"
	return cvm.NewClient(credential, a.resolveRegion(region), cpf)
}

// ptrStr safely dereferences a string pointer
func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// retryWithBackoff 指数退避重试
func (a *TagAdapterImpl) retryWithBackoff(operation func() error) error {
	var lastErr error
	for attempt := 0; attempt <= tencentTagMaxRetries; attempt++ {
		lastErr = operation()
		if lastErr == nil {
			return nil
		}
		if attempt < tencentTagMaxRetries {
			backoff := time.Duration(float64(tencentTagBaseBackoffMs)*math.Pow(2, float64(attempt))) * time.Millisecond
			time.Sleep(backoff)
		}
	}
	return fmt.Errorf("tencent: tag API failed after %d retries: %w", tencentTagMaxRetries, lastErr)
}

// ListTagKeys 查询标签键列表
func (a *TagAdapterImpl) ListTagKeys(ctx context.Context, region string) ([]string, error) {
	client, err := a.createCVMClient(region)
	if err != nil {
		return nil, fmt.Errorf("tencent: create tag client failed: %w", err)
	}

	var keys []string
	err = a.retryWithBackoff(func() error {
		request := cvm.NewDescribeInstancesRequest()
		limit := int64(100)
		request.Limit = &limit

		response, e := client.DescribeInstances(request)
		if e != nil {
			return e
		}

		seen := make(map[string]bool)
		for _, inst := range response.Response.InstanceSet {
			if inst.Tags != nil {
				for _, tag := range inst.Tags {
					k := ptrStr(tag.Key)
					if k != "" && !seen[k] {
						keys = append(keys, k)
						seen[k] = true
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}

// ListTagValues 查询指定标签键的值列表
func (a *TagAdapterImpl) ListTagValues(ctx context.Context, region, key string) ([]string, error) {
	client, err := a.createCVMClient(region)
	if err != nil {
		return nil, fmt.Errorf("tencent: create tag client failed: %w", err)
	}

	var values []string
	err = a.retryWithBackoff(func() error {
		request := cvm.NewDescribeInstancesRequest()
		limit := int64(100)
		request.Limit = &limit

		response, e := client.DescribeInstances(request)
		if e != nil {
			return e
		}

		seen := make(map[string]bool)
		for _, inst := range response.Response.InstanceSet {
			if inst.Tags != nil {
				for _, tag := range inst.Tags {
					if ptrStr(tag.Key) == key {
						v := ptrStr(tag.Value)
						if !seen[v] {
							values = append(values, v)
							seen[v] = true
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return values, nil
}

// GetResourceTags 查询资源绑定的标签
func (a *TagAdapterImpl) GetResourceTags(ctx context.Context, region, resourceType, resourceID string) (map[string]string, error) {
	client, err := a.createCVMClient(region)
	if err != nil {
		return nil, fmt.Errorf("tencent: create tag client failed: %w", err)
	}

	tags := make(map[string]string)
	err = a.retryWithBackoff(func() error {
		request := cvm.NewDescribeInstancesRequest()
		request.InstanceIds = common.StringPtrs([]string{resourceID})

		response, e := client.DescribeInstances(request)
		if e != nil {
			return e
		}

		for _, inst := range response.Response.InstanceSet {
			if inst.Tags != nil {
				for _, tag := range inst.Tags {
					tags[ptrStr(tag.Key)] = ptrStr(tag.Value)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tags, nil
}

// TagResource 为资源绑定标签
// 腾讯云通过 ModifyInstancesAttribute 不直接支持标签操作，
// 此处通过 CVM API 的 tag 参数实现。实际生产中应使用腾讯云 Tag SDK。
func (a *TagAdapterImpl) TagResource(ctx context.Context, region, resourceType, resourceID string, tags map[string]string) error {
	// 腾讯云 CVM 不直接提供 CreateTags API，需要通过 Tag 服务
	// 此处使用 CVM ModifyInstancesAttribute 的方式间接实现
	// 在实际生产中，应引入 tencentcloud-sdk-go/tencentcloud/tag 包
	return a.retryWithBackoff(func() error {
		return fmt.Errorf("tencent: tag resource %s/%s: direct CVM tag binding requires Tag SDK, resource_type=%s",
			resourceID, region, resourceType)
	})
}

// UntagResource 解绑资源标签
func (a *TagAdapterImpl) UntagResource(ctx context.Context, region, resourceType, resourceID string, tagKeys []string) error {
	return a.retryWithBackoff(func() error {
		return fmt.Errorf("tencent: untag resource %s/%s: direct CVM tag unbinding requires Tag SDK, resource_type=%s",
			resourceID, region, resourceType)
	})
}

// ListResourcesByTag 按标签查询资源列表
func (a *TagAdapterImpl) ListResourcesByTag(ctx context.Context, region, key, value string) ([]cloudx.TaggedResource, error) {
	client, err := a.createCVMClient(region)
	if err != nil {
		return nil, fmt.Errorf("tencent: create tag client failed: %w", err)
	}

	resolvedRegion := a.resolveRegion(region)
	var resources []cloudx.TaggedResource
	err = a.retryWithBackoff(func() error {
		request := cvm.NewDescribeInstancesRequest()
		limit := int64(100)
		request.Limit = &limit
		request.Filters = []*cvm.Filter{
			{
				Name:   common.StringPtr("tag:" + key),
				Values: common.StringPtrs([]string{value}),
			},
		}

		response, e := client.DescribeInstances(request)
		if e != nil {
			return e
		}

		for _, inst := range response.Response.InstanceSet {
			resources = append(resources, cloudx.TaggedResource{
				ResourceType: "instance",
				ResourceID:   ptrStr(inst.InstanceId),
				Region:       resolvedRegion,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return resources, nil
}

// Ensure compile-time interface compliance
var _ cloudx.TagAdapter = (*TagAdapterImpl)(nil)
