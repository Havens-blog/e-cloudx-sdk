package retry

import (
	"context"
	"fmt"
	"time"
)

// WithBackoff 使用指数退避策略重试操作
// maxRetries: 最大重试次数
// operation: 需要重试的操作
// isRetryable: 判断错误是否可重试的函数
func WithBackoff(ctx context.Context, maxRetries int, operation func() error, isRetryable func(error) bool) error {
	var lastErr error
	backoff := time.Second

	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
				backoff *= 2 // 指数退避
				if backoff > 30*time.Second {
					backoff = 30 * time.Second // 最大30秒
				}
			}
		}

		err := operation()
		if err == nil {
			return nil
		}

		lastErr = err

		// 检查是否可重试
		if isRetryable != nil && isRetryable(err) {
			continue
		}

		// 不可重试的错误，直接返回
		if isRetryable != nil {
			return err
		}
	}

	return fmt.Errorf("max retries exceeded: %w", lastErr)
}
