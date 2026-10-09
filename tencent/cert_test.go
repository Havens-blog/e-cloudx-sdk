package tencent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/common/ratelimit"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcerr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
)

// ==================== 测试公共设施 ====================

// newTestCertAdapter 构造可注入 fake SDK 客户端的证书适配器（限流器放宽避免测试排队）
func newTestCertAdapter(t *testing.T) *CertAdapter {
	t.Helper()
	adapter := NewCertAdapter(elog.DefaultLogger)
	adapter.rateLimiter = ratelimit.NewRateLimiter(5000)
	return adapter
}

func testCertCreds() *domain.CloudAccount {
	return &domain.CloudAccount{
		Name:            "tencent-main",
		Provider:        domain.CloudProviderTencent,
		AccessKeyID:     "test-ak",
		AccessKeySecret: "test-sk",
		Regions:         []string{"ap-guangzhou", "ap-shanghai"},
	}
}

var (
	errFakeThrottling = tcerr.NewTencentCloudSDKError("RequestLimitExceeded", "请求过于频繁，请稍后再试", "req-throttle")
	errFakeNotFound   = tcerr.NewTencentCloudSDKError("ResourceNotFound.Certificate", "证书不存在", "req-notfound")
	errFakeDenied     = tcerr.NewTencentCloudSDKError("AuthFailure", "鉴权失败", "req-denied")
)

// fakeSSLCall 记录单次 SSL 服务调用
type fakeSSLCall struct {
	action string
	params map[string]interface{}
}

// fakeSSLCaller SSL 服务 fake（响应体为含 Response 信封的 JSON 原文）
type fakeSSLCaller struct {
	calls      []fakeSSLCall
	uploadErr  error
	detailErr  error
	deleteErr  error
	uploadBody string
	detailBody string
	deleteBody string // DeleteCertificate 响应体；缺省 DeleteResult=true（同步删除完成）

	deleteTaskCalls  int      // DescribeDeleteCertificatesTaskResult 调用计数
	deleteTaskBodies []string // 逐次响应体；耗尽沿用末值
	deleteTaskErrFn  func(call int) error
}

func (f *fakeSSLCaller) call(action string, params map[string]interface{}) (json.RawMessage, error) {
	f.calls = append(f.calls, fakeSSLCall{action: action, params: params})
	switch action {
	case "UploadCertificate":
		if f.uploadErr != nil {
			return nil, f.uploadErr
		}
		body := f.uploadBody
		if body == "" {
			body = `{"Response":{"CertificateId":"987654321"}}`
		}
		return json.RawMessage(body), nil
	case "DescribeCertificateDetail":
		if f.detailErr != nil {
			return nil, f.detailErr
		}
		body := f.detailBody
		if body == "" {
			body = `{"Response":{"CertFingerprint":"AB:CD:EF:01","CertEndTime":"2027-01-02 08:00:00"}}`
		}
		return json.RawMessage(body), nil
	case "DeleteCertificate":
		if f.deleteErr != nil {
			return nil, f.deleteErr
		}
		body := f.deleteBody
		if body == "" {
			body = `{"Response":{"DeleteResult":true}}`
		}
		return json.RawMessage(body), nil
	case "DescribeDeleteCertificatesTaskResult":
		f.deleteTaskCalls++
		if f.deleteTaskErrFn != nil {
			if err := f.deleteTaskErrFn(f.deleteTaskCalls); err != nil {
				return nil, err
			}
		}
		if len(f.deleteTaskBodies) == 0 {
			return json.RawMessage(`{"Response":{"DeleteTaskResult":[]}}`), nil
		}
		idx := f.deleteTaskCalls - 1
		if idx >= len(f.deleteTaskBodies) {
			idx = len(f.deleteTaskBodies) - 1
		}
		return json.RawMessage(f.deleteTaskBodies[idx]), nil
	}
	return nil, fmt.Errorf("unexpected ssl action %s", action)
}

// deleteTaskResultCalls 统计 DescribeDeleteCertificatesTaskResult 调用次数（并发不涉及）。
func (f *fakeSSLCaller) deleteTaskResultCalls() int {
	return f.deleteTaskCalls
}

// genTestCertPEM 生成自签测试证书，返回 PEM 与 SHA256 指纹（hex 小写）
func genTestCertPEM(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "cert-test.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		DNSNames:     []string{"cert-test.example.com"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	sum := sha256.Sum256(der)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), hex.EncodeToString(sum[:])
}

// lastSSLCall 取最近一次调用的参数（调用存在为前置断言）
func lastSSLCall(t *testing.T, fake *fakeSSLCaller) fakeSSLCall {
	t.Helper()
	require.NotEmpty(t, fake.calls)
	return fake.calls[len(fake.calls)-1]
}

// ==================== UploadCert ====================

func TestCertAdapterUploadCert(t *testing.T) {
	pemStr := "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----"

	t.Run("成功上传并返回云证书ID", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeSSLCaller{}
		adapter.newSSLCaller = func(creds *domain.CloudAccount) (sslCertCaller, error) { return fake, nil }

		id, err := adapter.UploadCert(context.Background(), testCertCreds(), CertProductCDN, "cert-2026", pemStr, "key")
		require.NoError(t, err)
		assert.Equal(t, "987654321", id)

		call := lastSSLCall(t, fake)
		assert.Equal(t, "UploadCertificate", call.action)
		assert.Equal(t, pemStr, call.params["CertificatePublicKey"])
		assert.Equal(t, "key", call.params["CertificatePrivateKey"])
		assert.Equal(t, "cert-2026", call.params["Alias"])
		assert.Equal(t, true, call.params["Repeatable"])
	})

	t.Run("CertificateId为空时回退RepeatCertId", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeSSLCaller{uploadBody: `{"Response":{"CertificateId":"","RepeatCertId":"111222333"}}`}
		adapter.newSSLCaller = func(creds *domain.CloudAccount) (sslCertCaller, error) { return fake, nil }

		id, err := adapter.UploadCert(context.Background(), testCertCreds(), CertProductCLB, "n", pemStr, "key")
		require.NoError(t, err)
		assert.Equal(t, "111222333", id)
	})

	t.Run("响应无证书ID显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newSSLCaller = func(creds *domain.CloudAccount) (sslCertCaller, error) {
			return &fakeSSLCaller{uploadBody: `{"Response":{}}`}, nil
		}
		_, err := adapter.UploadCert(context.Background(), testCertCreds(), CertProductCDN, "n", pemStr, "key")
		require.Error(t, err)
	})

	t.Run("响应体非法报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newSSLCaller = func(creds *domain.CloudAccount) (sslCertCaller, error) {
			return &fakeSSLCaller{uploadBody: `not-json`}, nil
		}
		_, err := adapter.UploadCert(context.Background(), testCertCreds(), CertProductCDN, "n", pemStr, "key")
		require.Error(t, err)
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newSSLCaller = func(creds *domain.CloudAccount) (sslCertCaller, error) {
			return &fakeSSLCaller{uploadErr: errFakeThrottling}, nil
		}
		_, err := adapter.UploadCert(context.Background(), testCertCreds(), CertProductCDN, "n", pemStr, "key")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
		assert.ErrorIs(t, err, cloudx.ErrCloudRateLimited)
	})

	t.Run("不支持的产品显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		_, err := adapter.UploadCert(context.Background(), testCertCreds(), "dcdn", "n", pemStr, "key")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCertProductNotSupported)
	})

	t.Run("参数缺失与空凭证显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		_, err := adapter.UploadCert(context.Background(), testCertCreds(), CertProductCDN, "", pemStr, "key")
		require.Error(t, err)
		_, err = adapter.UploadCert(context.Background(), testCertCreds(), CertProductCDN, "n", "", "key")
		require.Error(t, err)
		_, err = adapter.UploadCert(context.Background(), testCertCreds(), CertProductCDN, "n", pemStr, "")
		require.Error(t, err)
		_, err = adapter.UploadCert(context.Background(), nil, CertProductCDN, "n", pemStr, "key")
		require.Error(t, err)
	})
}

// ==================== GetCert ====================

func TestCertAdapterGetCert(t *testing.T) {
	t.Run("返回PEM解析的SHA256指纹与有效期", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		pemStr, fingerprint := genTestCertPEM(t)
		detail := fmt.Sprintf(`{"Response":{"CertificatePublicKey":%s}}`, mustJSONString(t, pemStr))
		fake := &fakeSSLCaller{detailBody: detail}
		adapter.newSSLCaller = func(creds *domain.CloudAccount) (sslCertCaller, error) { return fake, nil }

		info, err := adapter.GetCert(context.Background(), testCertCreds(), "987654321")
		require.NoError(t, err)
		assert.True(t, info.Exists)
		assert.Equal(t, fingerprint, info.Fingerprint)
		assert.True(t, info.NotAfter.After(time.Now().Add(364*24*time.Hour)))

		call := lastSSLCall(t, fake)
		assert.Equal(t, "DescribeCertificateDetail", call.action)
		assert.Equal(t, "987654321", call.params["CertificateId"])
	})

	t.Run("无PEM时回退云侧指纹字段与GMT+8有效期", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newSSLCaller = func(creds *domain.CloudAccount) (sslCertCaller, error) {
			return &fakeSSLCaller{detailBody: `{"Response":{"CertFingerprint":"AB:CD:EF:01","CertEndTime":"2027-01-02 08:00:00"}}`}, nil
		}
		info, err := adapter.GetCert(context.Background(), testCertCreds(), "987654321")
		require.NoError(t, err)
		assert.True(t, info.Exists)
		assert.Equal(t, "abcdef01", info.Fingerprint)
		// GMT+8 的 08:00 应归一化为 UTC 当日 00:00
		assert.Equal(t, 2027, info.NotAfter.UTC().Year())
		assert.Equal(t, 0, info.NotAfter.UTC().Hour())
	})

	t.Run("证书不存在返回Exists=false", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newSSLCaller = func(creds *domain.CloudAccount) (sslCertCaller, error) {
			return &fakeSSLCaller{detailErr: errFakeNotFound}, nil
		}
		info, err := adapter.GetCert(context.Background(), testCertCreds(), "1")
		require.NoError(t, err)
		assert.False(t, info.Exists)
		assert.Empty(t, info.Fingerprint)
		assert.True(t, info.NotAfter.IsZero())
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newSSLCaller = func(creds *domain.CloudAccount) (sslCertCaller, error) {
			return &fakeSSLCaller{detailErr: errFakeThrottling}, nil
		}
		_, err := adapter.GetCert(context.Background(), testCertCreds(), "987654321")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})

	t.Run("其他云侧错误带上下文透传", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newSSLCaller = func(creds *domain.CloudAccount) (sslCertCaller, error) {
			return &fakeSSLCaller{detailErr: errFakeDenied}, nil
		}
		_, err := adapter.GetCert(context.Background(), testCertCreds(), "987654321")
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrCloudRateLimited)
		assert.Contains(t, err.Error(), "ssl")
	})

	t.Run("空证书ID与空凭证显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		_, err := adapter.GetCert(context.Background(), testCertCreds(), " ")
		require.Error(t, err)
		_, err = adapter.GetCert(context.Background(), nil, "987654321")
		require.Error(t, err)
	})

	t.Run("返回净化fullchain序列_私钥块被丢弃_叶在前", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		rawBundle, wantChain, leafFingerprint := genTestCertChainPEM(t)
		detail := fmt.Sprintf(`{"Response":{"CertificatePublicKey":%s}}`, mustJSONString(t, rawBundle))
		adapter.newSSLCaller = func(creds *domain.CloudAccount) (sslCertCaller, error) {
			return &fakeSSLCaller{detailBody: detail}, nil
		}

		info, err := adapter.GetCert(context.Background(), testCertCreds(), "987654321")
		require.NoError(t, err)
		assert.True(t, info.Exists)
		// 内容级断言：不含 PRIVATE KEY、含且仅含 CERTIFICATE 块（叶在前）
		assert.Equal(t, wantChain, info.CertChainPEM)
		assert.NotContains(t, info.CertChainPEM, "PRIVATE KEY")
		assert.True(t, strings.HasPrefix(info.CertChainPEM, "-----BEGIN CERTIFICATE-----"))
		// 指纹取净化序列首块（叶证书）
		assert.Equal(t, leafFingerprint, info.Fingerprint)
	})

	t.Run("无PEM时CertChainPEM为空", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newSSLCaller = func(creds *domain.CloudAccount) (sslCertCaller, error) {
			return &fakeSSLCaller{detailBody: `{"Response":{"CertFingerprint":"AB:CD:EF:01","CertEndTime":"2027-01-02 08:00:00"}}`}, nil
		}
		info, err := adapter.GetCert(context.Background(), testCertCreds(), "987654321")
		require.NoError(t, err)
		assert.True(t, info.Exists)
		assert.Empty(t, info.CertChainPEM)
	})
}

// ==================== CleanupOrphan ====================

func TestCertAdapterCleanupOrphan(t *testing.T) {
	t.Run("成功清理并携带云侧引用二次校验", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeSSLCaller{}
		adapter.newSSLCaller = func(creds *domain.CloudAccount) (sslCertCaller, error) { return fake, nil }

		err := adapter.CleanupOrphan(context.Background(), testCertCreds(), "987654321")
		require.NoError(t, err)

		call := lastSSLCall(t, fake)
		assert.Equal(t, "DeleteCertificate", call.action)
		assert.Equal(t, "987654321", call.params["CertificateId"])
		assert.Equal(t, true, call.params["IsCheckResource"])
	})

	t.Run("清理已不存在的证书视为幂等成功", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newSSLCaller = func(creds *domain.CloudAccount) (sslCertCaller, error) {
			return &fakeSSLCaller{deleteErr: errFakeNotFound}, nil
		}
		err := adapter.CleanupOrphan(context.Background(), testCertCreds(), "987654321")
		require.NoError(t, err)
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newSSLCaller = func(creds *domain.CloudAccount) (sslCertCaller, error) {
			return &fakeSSLCaller{deleteErr: errFakeThrottling}, nil
		}
		err := adapter.CleanupOrphan(context.Background(), testCertCreds(), "987654321")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})

	t.Run("空证书ID与空凭证显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		err := adapter.CleanupOrphan(context.Background(), testCertCreds(), "")
		require.Error(t, err)
		err = adapter.CleanupOrphan(context.Background(), nil, "987654321")
		require.Error(t, err)
	})

	// ==================== B1：异步删除轮询（poc-notes §5-B1/§6-C1 方案 B）===

	newAsyncAdapter := func(t *testing.T, fake *fakeSSLCaller) *CertAdapter {
		t.Helper()
		adapter := newTestCertAdapter(t)
		adapter.deletePollInterval = time.Millisecond
		adapter.deletePollAttempts = 5
		adapter.newSSLCaller = func(creds *domain.CloudAccount) (sslCertCaller, error) { return fake, nil }
		return adapter
	}

	t.Run("同步删除完成不触发轮询", func(t *testing.T) {
		fake := &fakeSSLCaller{deleteBody: `{"Response":{"DeleteResult":true,"TaskId":""}}`}
		adapter := newAsyncAdapter(t, fake)

		err := adapter.CleanupOrphan(context.Background(), testCertCreds(), "987654321")
		require.NoError(t, err)
		assert.Equal(t, 0, fake.deleteTaskResultCalls(), "DeleteResult=true 即已删除，不查任务结果")
	})

	t.Run("异步删除轮询至成功", func(t *testing.T) {
		fake := &fakeSSLCaller{
			deleteBody: `{"Response":{"DeleteResult":false,"TaskId":"1001"}}`,
			deleteTaskBodies: []string{
				`{"Response":{"DeleteTaskResult":[{"TaskId":"1001","CertId":"987654321","Status":0}]}}`,
				`{"Response":{"DeleteTaskResult":[{"TaskId":"1001","CertId":"987654321","Status":0}]}}`,
				`{"Response":{"DeleteTaskResult":[{"TaskId":"1001","CertId":"987654321","Status":1}]}}`,
			},
		}
		adapter := newAsyncAdapter(t, fake)

		err := adapter.CleanupOrphan(context.Background(), testCertCreds(), "987654321")
		require.NoError(t, err)
		assert.Equal(t, 3, fake.deleteTaskResultCalls(), "删除中×2 → 删除成功，共轮询 3 次")

		var taskCall *fakeSSLCall
		for i := range fake.calls {
			if fake.calls[i].action == "DescribeDeleteCertificatesTaskResult" {
				taskCall = &fake.calls[i]
			}
		}
		require.NotNil(t, taskCall)
		taskIds, ok := taskCall.params["TaskIds"].([]string)
		require.True(t, ok)
		assert.Equal(t, []string{"1001"}, taskIds)
	})

	t.Run("异步删除任务失败透传详情", func(t *testing.T) {
		fake := &fakeSSLCaller{
			deleteBody: `{"Response":{"DeleteResult":false,"TaskId":"1002"}}`,
			deleteTaskBodies: []string{
				`{"Response":{"DeleteTaskResult":[{"TaskId":"1002","Status":2,"Error":"FailedOperation.DeleteResourceFailed: 证书仍关联云资源"}]}}`,
			},
		}
		adapter := newAsyncAdapter(t, fake)

		err := adapter.CleanupOrphan(context.Background(), testCertCreds(), "987654321")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "删除失败")
		assert.Contains(t, err.Error(), "DeleteResourceFailed")
	})

	t.Run("Error字段非空按失败兜底（状态数值未实网复核防御）", func(t *testing.T) {
		fake := &fakeSSLCaller{
			deleteBody: `{"Response":{"DeleteResult":false,"TaskId":"1003"}}`,
			deleteTaskBodies: []string{
				`{"Response":{"DeleteTaskResult":[{"TaskId":"1003","Status":9,"Error":"unexpected status quirk"}]}}`,
			},
		}
		adapter := newAsyncAdapter(t, fake)

		err := adapter.CleanupOrphan(context.Background(), testCertCreds(), "987654321")
		require.Error(t, err, "状态值未登记时 Error 非空即判失败，不静默视为成功")
		assert.Contains(t, err.Error(), "unexpected status quirk")
	})

	t.Run("异步轮询预算耗尽有界返回", func(t *testing.T) {
		fake := &fakeSSLCaller{
			deleteBody: `{"Response":{"DeleteResult":false,"TaskId":"1004"}}`,
			deleteTaskBodies: []string{
				`{"Response":{"DeleteTaskResult":[{"TaskId":"1004","Status":0}]}}`,
			},
		}
		adapter := newAsyncAdapter(t, fake)
		adapter.deletePollAttempts = 3

		err := adapter.CleanupOrphan(context.Background(), testCertCreds(), "987654321")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unfinished")
		assert.Equal(t, 3, fake.deleteTaskResultCalls(), "轮询次数严格受限（禁无限轮询）")
	})

	t.Run("轮询查询瞬时错误容忍后恢复", func(t *testing.T) {
		fake := &fakeSSLCaller{
			deleteBody: `{"Response":{"DeleteResult":false,"TaskId":"1005"}}`,
			deleteTaskBodies: []string{
				`{"Response":{"DeleteTaskResult":[{"TaskId":"1005","Status":1}]}}`,
			},
			deleteTaskErrFn: func(call int) error {
				if call == 1 {
					return errFakeThrottling
				}
				return nil
			},
		}
		adapter := newAsyncAdapter(t, fake)

		err := adapter.CleanupOrphan(context.Background(), testCertCreds(), "987654321")
		require.NoError(t, err, "单次查询失败不中断轮询，预算内恢复")
		assert.Equal(t, 2, fake.deleteTaskResultCalls())
	})

	t.Run("响应无删除确认信号防御性失败", func(t *testing.T) {
		fake := &fakeSSLCaller{deleteBody: `{"Response":{}}`}
		adapter := newAsyncAdapter(t, fake)

		err := adapter.CleanupOrphan(context.Background(), testCertCreds(), "987654321")
		require.Error(t, err, "无 DeleteResult/TaskId 无法确认删除，宁失败待队列重放（B1 修正语义）")
		assert.Contains(t, err.Error(), "no delete confirmation")
	})
}

// ==================== BindResource / ListReferences 分发 ====================

func TestCertAdapterDispatch(t *testing.T) {
	t.Run("BindResource不支持的产品显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		err := adapter.BindResource(context.Background(), testCertCreds(), "dcdn", "res-1", "987654321")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCertProductNotSupported)
	})

	t.Run("ListReferences不支持的产品显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		_, err := adapter.ListReferences(context.Background(), testCertCreds(), "cos")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCertProductNotSupported)
	})

	t.Run("产品集合覆盖首期三产品", func(t *testing.T) {
		for _, product := range []string{CertProductCDN, CertProductWAF, CertProductCLB} {
			assert.True(t, certSupportedProducts[product], product)
		}
		assert.False(t, certSupportedProducts["alb"])
	})
}

// ==================== 辅助函数 ====================

func TestParseCertScopedResourceID(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		owner   string
		sub     string
		wantErr bool
	}{
		{name: "EdgeOne形态", in: "zone-2/www.example.com", owner: "zone-2", sub: "www.example.com"},
		{name: "CLB形态", in: "lb-abc/lbl-xyz", owner: "lb-abc", sub: "lbl-xyz"},
		{name: "缺子段", in: "zone-1/", wantErr: true},
		{name: "缺属主段", in: "/host", wantErr: true},
		{name: "无分隔符", in: "plain", wantErr: true},
		{name: "空串", in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, sub, err := parseCertScopedResourceID(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.owner, owner)
			assert.Equal(t, tt.sub, sub)
		})
	}
}

func TestParseCloudCertTime(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		year  int
		hour  int
		valid bool
	}{
		{name: "GMT+8空格时间", in: "2027-01-02 08:00:00", year: 2027, hour: 0, valid: true},
		{name: "GMT+8T分隔", in: "2027-01-02T08:00:00", year: 2027, hour: 0, valid: true},
		{name: "日期", in: "2027-01-02", year: 2027, hour: 16, valid: true},
		{name: "RFC3339带时区", in: "2027-01-02T08:00:00Z", year: 2027, hour: 8, valid: true},
		{name: "空串", in: "", valid: false},
		{name: "非法", in: "not-a-date", valid: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseCloudCertTime(tt.in)
			if !tt.valid {
				assert.False(t, ok)
				return
			}
			require.True(t, ok)
			assert.Equal(t, tt.year, got.UTC().Year())
			assert.Equal(t, tt.hour, got.UTC().Hour())
		})
	}
}

func TestIsCertRateLimited(t *testing.T) {
	assert.True(t, isCertRateLimited(errFakeThrottling))
	assert.True(t, isCertRateLimited(tcerr.NewTencentCloudSDKError("LimitExceeded", "超限", "")))
	assert.True(t, isCertRateLimited(tcerr.NewTencentCloudSDKError("RequestLimitExceeded.UinLimitExceeded", "超限", "")))
	assert.False(t, isCertRateLimited(errFakeDenied))
	// 包装后的限流错误仍可被 errors.As 识别
	assert.True(t, isCertRateLimited(fmt.Errorf("wrap: %w", errFakeThrottling)))
	assert.False(t, isCertRateLimited(nil))
}

func TestIsCertNotFoundError(t *testing.T) {
	assert.True(t, isCertNotFoundError(errFakeNotFound))
	assert.True(t, isCertNotFoundError(tcerr.NewTencentCloudSDKError("FailedOperation", "证书不存在或已被删除", "")))
	assert.True(t, isCertNotFoundError(fmt.Errorf("plain: cert not found")))
	assert.False(t, isCertNotFoundError(errFakeThrottling))
	assert.False(t, isCertNotFoundError(nil))
}

func TestCertCredsRegions(t *testing.T) {
	creds := testCertCreds()
	assert.Equal(t, []string{"ap-guangzhou", "ap-shanghai"}, certCredsRegions(creds))
	assert.Equal(t, "ap-guangzhou", certCredsRegion(creds))

	creds.Regions = nil
	assert.Equal(t, []string{"ap-guangzhou"}, certCredsRegions(creds))
	assert.Equal(t, "ap-guangzhou", certCredsRegion(nil))
}

// ==================== SSL 直调与适配器边界分支 ====================

func TestSSLRPCInvokerCallRejectsInvalidParams(t *testing.T) {
	// 非法参数类型在请求序列化阶段即失败（不触达云侧，离线可测）
	invoker, err := newSSLRPCInvoker(testCertCreds())
	require.NoError(t, err)
	_, err = invoker.call("UploadCertificate", map[string]interface{}{"bad": make(chan int)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported type")
}

func TestCertAdapterBoundaryBranches(t *testing.T) {
	// 空限流器/零分页大小回退默认
	adapter := NewCertAdapter(elog.DefaultLogger)
	adapter.rateLimiter = nil
	assert.NoError(t, adapter.waitRateLimit(context.Background()))
	adapter.listPageSize = 0
	assert.Equal(t, certDefaultPageSize, adapter.certPageSize())

	// 空日志回退默认 logger
	assert.NotNil(t, NewCertAdapter(nil))
}

// ==================== 真实客户端工厂离线构建 ====================

func TestCertAdapterRealClientFactories(t *testing.T) {
	// SDK 客户端构建不发起网络请求，可离线验证工厂与真实客户端满足接口签名
	creds := testCertCreds()
	adapter := NewCertAdapter(elog.DefaultLogger)

	sslCaller, err := adapter.newSSLCaller(creds)
	require.NoError(t, err)
	assert.NotNil(t, sslCaller)

	cdnClient, err := adapter.newCdnClient(creds)
	require.NoError(t, err)
	assert.NotNil(t, cdnClient)

	teoClient, err := adapter.newTeoClient(creds)
	require.NoError(t, err)
	assert.NotNil(t, teoClient)

	clbClient, err := adapter.newClbClient(creds, "ap-guangzhou")
	require.NoError(t, err)
	assert.NotNil(t, clbClient)
}

// mustJSONString 将字符串编码为 JSON 字符串字面量（PEM 含换行需转义）
func mustJSONString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	require.NoError(t, err)
	return string(b)
}

// certChainTestKeyPEM 手写私钥 PEM 块（净化内容级断言用，base64 内容不影响块类型过滤）
const certChainTestKeyPEM = "-----BEGIN EC PRIVATE KEY-----\nZmFrZS1rZXk=\n-----END EC PRIVATE KEY-----\n"

// genTestCertChainPEM 生成叶/中间 CA/自签根三张独立测试证书的 fullchain 材料：
// 返回（含私钥块前缀的原始 bundle，叶在前净化期望序列，叶证书指纹）。
func genTestCertChainPEM(t *testing.T) (rawBundle, wantChain, leafFingerprint string) {
	t.Helper()
	leaf, leafFingerprint := genTestCertPEM(t)
	intermediate, _ := genTestCertPEM(t)
	root, _ := genTestCertPEM(t)
	return certChainTestKeyPEM + leaf + intermediate + root, leaf + intermediate + root, leafFingerprint
}
