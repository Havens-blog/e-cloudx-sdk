package aliyun

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/common/retry"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ram"
	"github.com/gotomicro/ego/core/elog"
)

// Client 阿里云公共客户端
// 提供认证、限流、重试等公共能力
type Client struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
	rateLimiter     *RateLimiter
	ecsClients      map[string]*ecs.Client
	ramClient       *ram.Client
	mu              sync.RWMutex
}

// NewClient 创建阿里云客户端
func NewClient(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *Client {
	if defaultRegion == "" {
		defaultRegion = "cn-hangzhou"
	}

	return &Client{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
		rateLimiter:     NewRateLimiter(20), // 20 QPS
		ecsClients:      make(map[string]*ecs.Client),
	}
}

// GetECSClient 获取或创建指定地域的ECS客户端
func (c *Client) GetECSClient(region string) (*ecs.Client, error) {
	c.mu.RLock()
	if client, ok := c.ecsClients[region]; ok {
		c.mu.RUnlock()
		return client, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// 双重检查
	if client, ok := c.ecsClients[region]; ok {
		return client, nil
	}

	credential := credentials.NewAccessKeyCredential(c.accessKeyID, c.accessKeySecret)
	config := sdk.NewConfig()
	config.Scheme = "https"

	client, err := ecs.NewClientWithOptions(region, config, credential)
	if err != nil {
		return nil, fmt.Errorf("创建ECS客户端失败: %w", err)
	}

	c.ecsClients[region] = client
	return client, nil
}

// GetRAMClient 获取RAM客户端
func (c *Client) GetRAMClient() (*ram.Client, error) {
	c.mu.RLock()
	if c.ramClient != nil {
		c.mu.RUnlock()
		return c.ramClient, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ramClient != nil {
		return c.ramClient, nil
	}

	// RAM服务使用固定区域
	client, err := ram.NewClientWithAccessKey("cn-hangzhou", c.accessKeyID, c.accessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("创建RAM客户端失败: %w", err)
	}

	c.ramClient = client
	return client, nil
}

// CreateRAMClientFromAccount 从云账号创建RAM客户端
func CreateRAMClientFromAccount(account *domain.CloudAccount) (*ram.Client, error) {
	if account == nil {
		return nil, fmt.Errorf("cloud account cannot be nil")
	}

	client, err := ram.NewClientWithAccessKey(
		"cn-hangzhou",
		account.AccessKeyID,
		account.AccessKeySecret,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create RAM client: %w", err)
	}

	return client, nil
}

// WaitRateLimit 等待限流
func (c *Client) WaitRateLimit(ctx context.Context) error {
	return c.rateLimiter.Wait(ctx)
}

// RetryWithBackoff 使用指数退避策略重试
func (c *Client) RetryWithBackoff(ctx context.Context, operation func() error) error {
	return retry.WithBackoff(ctx, 3, operation, func(err error) bool {
		if IsThrottlingError(err) {
			c.logger.Warn("阿里云API限流，正在重试", elog.FieldErr(err))
			return true
		}
		return false
	})
}

// RateLimiter 限流器
type RateLimiter struct {
	ticker   *time.Ticker
	tokens   chan struct{}
	qps      int
	stopOnce sync.Once
	stopCh   chan struct{}
}

// NewRateLimiter 创建限流器
func NewRateLimiter(qps int) *RateLimiter {
	if qps <= 0 {
		qps = 10
	}

	rl := &RateLimiter{
		ticker: time.NewTicker(time.Second / time.Duration(qps)),
		tokens: make(chan struct{}, qps),
		qps:    qps,
		stopCh: make(chan struct{}),
	}

	// 初始化令牌
	for i := 0; i < qps; i++ {
		rl.tokens <- struct{}{}
	}

	// 启动令牌补充协程
	go rl.refill()

	return rl
}

// refill 补充令牌
func (rl *RateLimiter) refill() {
	for {
		select {
		case <-rl.ticker.C:
			select {
			case rl.tokens <- struct{}{}:
			default:
				// 令牌桶已满
			}
		case <-rl.stopCh:
			return
		}
	}
}

// Wait 等待获取令牌
func (rl *RateLimiter) Wait(ctx context.Context) error {
	select {
	case <-rl.tokens:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stop 停止限流器
func (rl *RateLimiter) Stop() {
	rl.stopOnce.Do(func() {
		rl.ticker.Stop()
		close(rl.stopCh)
	})
}

// IsThrottlingError 判断是否为限流错误
func IsThrottlingError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return contains(errStr, "Throttling") ||
		contains(errStr, "throttling") ||
		contains(errStr, "TooManyRequests") ||
		contains(errStr, "ServiceUnavailable")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsImpl(s, substr))
}

func containsImpl(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
