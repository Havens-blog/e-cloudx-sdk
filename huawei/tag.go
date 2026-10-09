package huawei

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	ecs "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2"
	ecsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2/model"
	ecsregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2/region"
)

const (
	huaweiTagMaxRetries    = 3
	huaweiTagBaseBackoffMs = 200
)

// TagAdapterImpl 华为云标签适配器
type TagAdapterImpl struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewTagAdapter 创建华为云标签适配器
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

// createECSClient 创建 ECS 客户端（华为云标签通过 ECS API 操作）
func (a *TagAdapterImpl) createECSClient(region string) (*ecs.EcsClient, error) {
	reg := a.resolveRegion(region)
	auth, err := basic.NewCredentialsBuilder().
		WithAk(a.accessKeyID).
		WithSk(a.accessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("huawei: create credentials failed: %w", err)
	}

	regionObj, err := ecsregion.SafeValueOf(reg)
	if err != nil {
		return nil, fmt.Errorf("huawei: unsupported region: %s", reg)
	}

	hcClient, err := ecs.EcsClientBuilder().
		WithRegion(regionObj).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("huawei: create ECS client failed: %w", err)
	}

	return ecs.NewEcsClient(hcClient), nil
}

// retryWithBackoff 指数退避重试
func (a *TagAdapterImpl) retryWithBackoff(operation func() error) error {
	var lastErr error
	for attempt := 0; attempt <= huaweiTagMaxRetries; attempt++ {
		lastErr = operation()
		if lastErr == nil {
			return nil
		}
		if attempt < huaweiTagMaxRetries {
			backoff := time.Duration(float64(huaweiTagBaseBackoffMs)*math.Pow(2, float64(attempt))) * time.Millisecond
			time.Sleep(backoff)
		}
	}
	return fmt.Errorf("huawei: tag API failed after %d retries: %w", huaweiTagMaxRetries, lastErr)
}

// ListTagKeys 查询标签键列表
func (a *TagAdapterImpl) ListTagKeys(ctx context.Context, region string) ([]string, error) {
	client, err := a.createECSClient(region)
	if err != nil {
		return nil, err
	}

	var keys []string
	err = a.retryWithBackoff(func() error {
		response, e := client.ListServerTags(&ecsmodel.ListServerTagsRequest{})
		if e != nil {
			return e
		}
		if response.Tags == nil {
			return nil
		}
		seen := make(map[string]bool)
		for _, tag := range *response.Tags {
			if !seen[tag.Key] {
				keys = append(keys, tag.Key)
				seen[tag.Key] = true
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
	client, err := a.createECSClient(region)
	if err != nil {
		return nil, err
	}

	var values []string
	err = a.retryWithBackoff(func() error {
		response, e := client.ListServerTags(&ecsmodel.ListServerTagsRequest{})
		if e != nil {
			return e
		}
		if response.Tags == nil {
			return nil
		}
		for _, tag := range *response.Tags {
			if tag.Key == key && tag.Values != nil {
				for _, v := range *tag.Values {
					values = append(values, v)
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
	client, err := a.createECSClient(region)
	if err != nil {
		return nil, err
	}

	tags := make(map[string]string)
	err = a.retryWithBackoff(func() error {
		response, e := client.ShowServerTags(&ecsmodel.ShowServerTagsRequest{
			ServerId: resourceID,
		})
		if e != nil {
			return e
		}
		if response.Tags == nil {
			return nil
		}
		for _, tag := range *response.Tags {
			val := ""
			if tag.Value != nil {
				val = *tag.Value
			}
			tags[tag.Key] = val
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tags, nil
}

// TagResource 为资源绑定标签
func (a *TagAdapterImpl) TagResource(ctx context.Context, region, resourceType, resourceID string, tags map[string]string) error {
	client, err := a.createECSClient(region)
	if err != nil {
		return err
	}

	serverTags := make([]ecsmodel.BatchAddServerTag, 0, len(tags))
	for k, v := range tags {
		val := v
		serverTags = append(serverTags, ecsmodel.BatchAddServerTag{Key: k, Value: &val})
	}

	return a.retryWithBackoff(func() error {
		_, e := client.BatchCreateServerTags(&ecsmodel.BatchCreateServerTagsRequest{
			ServerId: resourceID,
			Body: &ecsmodel.BatchCreateServerTagsRequestBody{
				Action: ecsmodel.GetBatchCreateServerTagsRequestBodyActionEnum().CREATE,
				Tags:   serverTags,
			},
		})
		return e
	})
}

// UntagResource 解绑资源标签
func (a *TagAdapterImpl) UntagResource(ctx context.Context, region, resourceType, resourceID string, tagKeys []string) error {
	client, err := a.createECSClient(region)
	if err != nil {
		return err
	}

	serverTags := make([]ecsmodel.ServerTag, 0, len(tagKeys))
	for _, k := range tagKeys {
		serverTags = append(serverTags, ecsmodel.ServerTag{Key: k})
	}

	return a.retryWithBackoff(func() error {
		_, e := client.BatchDeleteServerTags(&ecsmodel.BatchDeleteServerTagsRequest{
			ServerId: resourceID,
			Body: &ecsmodel.BatchDeleteServerTagsRequestBody{
				Action: ecsmodel.GetBatchDeleteServerTagsRequestBodyActionEnum().DELETE,
				Tags:   serverTags,
			},
		})
		return e
	})
}

// ListResourcesByTag 按标签查询资源列表
func (a *TagAdapterImpl) ListResourcesByTag(ctx context.Context, region, key, value string) ([]cloudx.TaggedResource, error) {
	client, err := a.createECSClient(region)
	if err != nil {
		return nil, err
	}

	resolvedRegion := a.resolveRegion(region)
	var resources []cloudx.TaggedResource
	tagFilter := key + "=" + value
	err = a.retryWithBackoff(func() error {
		response, e := client.ListServersDetails(&ecsmodel.ListServersDetailsRequest{
			Tags: &tagFilter,
		})
		if e != nil {
			return e
		}
		if response.Servers == nil {
			return nil
		}
		for _, srv := range *response.Servers {
			resources = append(resources, cloudx.TaggedResource{
				ResourceType: "ecs",
				ResourceID:   srv.Id,
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
