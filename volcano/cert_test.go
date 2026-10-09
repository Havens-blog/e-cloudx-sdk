package volcano

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/volcengine/volcengine-go-sdk/service/certificateservice"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/request"
)

// 火山云证书库适配器测试：fake SDK 严格按 volcengine-go-sdk v1.2.9
// certificateservice 模型构造响应，覆盖分页枚举、List/Get 两阶段过滤、
// 单实例失败不中断、链解析口径（CAS ParseCertAndKey 语义）与私钥不外泄。

// ==================== 测试公共设施 ====================

// certTestCreds 测试云账号（凭证仅内存传递）
func certTestCreds() *domain.CloudAccount {
	return &domain.CloudAccount{
		Name:            "volcano-main",
		Provider:        domain.CloudProviderVolcano,
		AccessKeyID:     "test-ak",
		AccessKeySecret: "test-sk",
		Regions:         []string{"cn-beijing"},
	}
}

// newTestCertAdapter 注入 fake 客户端工厂并缩小分页（覆盖翻页分支）
func newTestCertAdapter(client certLibraryAPI, pageSize int32) *CertAdapter {
	return &CertAdapter{
		logger:       elog.DefaultLogger,
		listPageSize: pageSize,
		newClient: func(*domain.CloudAccount) (certLibraryAPI, error) {
			return client, nil
		},
	}
}

// fakeCertLibraryClient certificateservice SDK 窄接口 fake（仅两只读方法，
// 构造性保证适配器无写通路）；响应字段严格按 SDK v1.2.9 模型填充。
type fakeCertLibraryClient struct {
	pages       [][]*certificateservice.InstanceForCertificateGetInstanceListOutput
	total       int32
	failOnPage  int32 // 该页码返回 listErr（页级失败注入）
	listErr     error
	listCalls   int
	pageNumbers []int32
	pageSizes   []int32
	details     map[string]*certificateservice.CertificateGetInstanceOutput
	getErrs     map[string]error
	getIDs      []string // Get 调用序（断言过滤实例不调 Get、失败不中断后续）
}

func (f *fakeCertLibraryClient) CertificateGetInstanceListWithContext(_ context.Context, input *certificateservice.CertificateGetInstanceListInput, _ ...request.Option) (*certificateservice.CertificateGetInstanceListOutput, error) {
	f.listCalls++
	pageNum := volcengine.Int32Value(input.PageNumber)
	f.pageNumbers = append(f.pageNumbers, pageNum)
	f.pageSizes = append(f.pageSizes, volcengine.Int32Value(input.PageSize))
	if f.listErr != nil && f.failOnPage == pageNum {
		return nil, f.listErr
	}
	idx := int(pageNum) - 1
	if idx < 0 || idx >= len(f.pages) {
		return &certificateservice.CertificateGetInstanceListOutput{TotalCount: volcengine.Int32(f.total)}, nil
	}
	return &certificateservice.CertificateGetInstanceListOutput{
		Instances:  f.pages[idx],
		PageNumber: input.PageNumber,
		PageSize:   input.PageSize,
		TotalCount: volcengine.Int32(f.total),
	}, nil
}

func (f *fakeCertLibraryClient) CertificateGetInstanceWithContext(_ context.Context, input *certificateservice.CertificateGetInstanceInput, _ ...request.Option) (*certificateservice.CertificateGetInstanceOutput, error) {
	id := volcengine.StringValue(input.InstanceId)
	f.getIDs = append(f.getIDs, id)
	if err, ok := f.getErrs[id]; ok {
		return nil, err
	}
	if detail, ok := f.details[id]; ok {
		return detail, nil
	}
	return nil, fmt.Errorf("fake: no detail preset for instance %s", id)
}

// certTestChain 测试证书链夹具：leaf 由自签中间 CA 签发（云侧链序：叶在前）
type certTestChain struct {
	LeafPEM  string
	InterPEM string
	Leaf     *x509.Certificate
}

// genCertTestChain 现场生成 leaf+中间 CA 两块链（标准库，不落盘；夹具私钥仅存测试内存）
func genCertTestChain(t *testing.T, cn string, sans []string) certTestChain {
	t.Helper()
	interKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	interTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "e-cam test intermediate CA"},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	interDER, err := x509.CreateCertificate(rand.Reader, interTmpl, interTmpl, &interKey.PublicKey, interKey)
	require.NoError(t, err)
	interCert, err := x509.ParseCertificate(interDER)
	require.NoError(t, err)

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		DNSNames:     sans,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, interCert, &leafKey.PublicKey, interKey)
	require.NoError(t, err)
	leafCert, err := x509.ParseCertificate(leafDER)
	require.NoError(t, err)
	return certTestChain{
		LeafPEM:  certTestPEM(leafDER),
		InterPEM: certTestPEM(interDER),
		Leaf:     leafCert,
	}
}

func certTestPEM(der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// listInstance 构造 CertificateGetInstanceList 输出实例（最小字段集）
func listInstance(id, status string, revoked bool) *certificateservice.InstanceForCertificateGetInstanceListOutput {
	return &certificateservice.InstanceForCertificateGetInstanceListOutput{
		InstanceId:           volcengine.String(id),
		CommonName:           volcengine.String("cn-" + id),
		Status:               volcengine.String(status),
		IsCertificateRevoked: volcengine.Bool(revoked),
	}
}

// issuedDetail 已签发实例详情（Chain=叶在前 leaf+中间）
func issuedDetail(chain certTestChain) *certificateservice.CertificateGetInstanceOutput {
	return &certificateservice.CertificateGetInstanceOutput{
		Status:               volcengine.String(certStatusIssued),
		IsCertificateRevoked: volcengine.Bool(false),
		CertificateDetail: &certificateservice.CertificateDetailForCertificateGetInstanceOutput{
			Chain: []*string{volcengine.String(chain.LeafPEM), volcengine.String(chain.InterPEM)},
		},
	}
}

// ==================== AC1：分页枚举全部实例 ====================

func TestListCertificatesPaginatesAllInstances(t *testing.T) {
	chain := genCertTestChain(t, "svc.example.com", []string{"svc.example.com"})
	ids := []string{"inst-1", "inst-2", "inst-3", "inst-4", "inst-5"}
	client := &fakeCertLibraryClient{
		pages: [][]*certificateservice.InstanceForCertificateGetInstanceListOutput{
			{listInstance(ids[0], certStatusIssued, false), listInstance(ids[1], certStatusIssued, false)},
			{listInstance(ids[2], certStatusIssued, false), listInstance(ids[3], certStatusIssued, false)},
			{listInstance(ids[4], certStatusIssued, false)},
		},
		total:   int32(len(ids)),
		details: map[string]*certificateservice.CertificateGetInstanceOutput{},
	}
	for _, id := range ids {
		client.details[id] = issuedDetail(chain)
	}
	adapter := newTestCertAdapter(client, 2)

	items, err := adapter.ListCertificates(context.Background(), certTestCreds())
	require.NoError(t, err)

	// 页数：5 实例 / 每页 2 → 3 页；PageNumber 自 1 递增、PageSize 原样透传
	assert.Equal(t, 3, client.listCalls)
	assert.Equal(t, []int32{1, 2, 3}, client.pageNumbers)
	assert.Equal(t, []int32{2, 2, 2}, client.pageSizes)
	// 实例数与顺序：与云侧分页序一致
	gotIDs := make([]string, 0, len(items))
	for _, item := range items {
		gotIDs = append(gotIDs, item.CloudCertID)
	}
	assert.Equal(t, ids, gotIDs)
	// Get 逐实例下发
	assert.Equal(t, ids, client.getIDs)
}

// ==================== AC3：List/Get 两阶段过滤 ====================

func TestListCertificatesFiltersRevokedAndNonIssued(t *testing.T) {
	chain := genCertTestChain(t, "keep.example.com", []string{"keep.example.com"})
	client := &fakeCertLibraryClient{
		pages: [][]*certificateservice.InstanceForCertificateGetInstanceListOutput{{
			listInstance("ok", certStatusIssued, false),
			listInstance("revoked", certStatusIssued, true),
			listInstance("pending", "Pending", false),
			listInstance("failed", "Failed", false),
			listInstance("revoked-status", certStatusRevoked, false),
		}},
		total:   5,
		details: map[string]*certificateservice.CertificateGetInstanceOutput{"ok": issuedDetail(chain)},
	}
	adapter := newTestCertAdapter(client, 10)

	items, err := adapter.ListCertificates(context.Background(), certTestCreds())
	require.NoError(t, err)

	require.Len(t, items, 1)
	assert.Equal(t, "ok", items[0].CloudCertID)
	// 过滤实例不发起 Get（省云 API）
	assert.Equal(t, []string{"ok"}, client.getIDs)
}

func TestGetCertificateFiltersRevokedOrNonIssued(t *testing.T) {
	chain := genCertTestChain(t, "x.example.com", []string{"x.example.com"})
	revoked := issuedDetail(chain)
	revoked.IsCertificateRevoked = volcengine.Bool(true)
	pending := issuedDetail(chain)
	pending.Status = volcengine.String("Pending")
	client := &fakeCertLibraryClient{details: map[string]*certificateservice.CertificateGetInstanceOutput{
		"cert-revoked": revoked,
		"cert-pending": pending,
	}}
	adapter := newTestCertAdapter(client, 10)
	ctx := context.Background()

	item, err := adapter.GetCertificate(ctx, certTestCreds(), "cert-revoked")
	assert.Zero(t, item)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCertFiltered)

	item, err = adapter.GetCertificate(ctx, certTestCreds(), "cert-pending")
	assert.Zero(t, item)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCertFiltered)
}

// ==================== AC2：Chain 解析口径（CAS ParseCertAndKey 语义） ====================

func TestGetCertificateParsesChainFields(t *testing.T) {
	chain := genCertTestChain(t, "svc.example.com", []string{"svc.example.com", "alt.example.com"})
	client := &fakeCertLibraryClient{details: map[string]*certificateservice.CertificateGetInstanceOutput{
		"cert-1": issuedDetail(chain),
	}}
	adapter := newTestCertAdapter(client, 10)

	item, err := adapter.GetCertificate(context.Background(), certTestCreds(), "cert-1")
	require.NoError(t, err)

	// 指纹：SHA256(leaf.Raw) 小写 hex（64 字符，对齐台账 ^[0-9a-f]{64}$）
	sum := sha256.Sum256(chain.Leaf.Raw)
	assert.Equal(t, hex.EncodeToString(sum[:]), item.Fingerprint)
	assert.Regexp(t, "^[0-9a-f]{64}$", item.Fingerprint)
	// CN/SAN：leaf Subject CN 与 DNS SAN（IP SAN 不计入域名口径）
	assert.Equal(t, "svc.example.com", item.CommonName)
	assert.Equal(t, []string{"svc.example.com", "alt.example.com"}, item.San)
	// 有效期：leaf 有效期
	assert.Equal(t, chain.Leaf.NotBefore, item.NotBefore)
	assert.Equal(t, chain.Leaf.NotAfter, item.NotAfter)
	assert.Equal(t, certStatusIssued, item.Status)
	// 链材料：两块 CERTIFICATE、叶在前、无私钥块
	assert.Equal(t, 2, strings.Count(item.CertChainPEM, "-----BEGIN CERTIFICATE-----"))
	first, _ := pem.Decode([]byte(item.CertChainPEM))
	require.NotNil(t, first)
	assert.Equal(t, chain.Leaf.Raw, first.Bytes)
	assert.NotContains(t, item.CertChainPEM, "PRIVATE KEY")
}

func TestGetCertificateChainParseFailures(t *testing.T) {
	chain := genCertTestChain(t, "y.example.com", []string{"y.example.com"})
	pkBlock := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1, 2, 3}}))
	client := &fakeCertLibraryClient{details: map[string]*certificateservice.CertificateGetInstanceOutput{
		"cert-garbage": {Status: volcengine.String(certStatusIssued), CertificateDetail: &certificateservice.CertificateDetailForCertificateGetInstanceOutput{
			Chain: []*string{volcengine.String("not-a-pem-block")},
		}},
		"cert-empty":      {Status: volcengine.String(certStatusIssued), CertificateDetail: &certificateservice.CertificateDetailForCertificateGetInstanceOutput{}},
		"cert-nil-detail": {Status: volcengine.String(certStatusIssued)},
		"cert-noncert-pem": {Status: volcengine.String(certStatusIssued), CertificateDetail: &certificateservice.CertificateDetailForCertificateGetInstanceOutput{
			Chain: []*string{volcengine.String(pkBlock)},
		}},
		"cert-bad-der": {Status: volcengine.String(certStatusIssued), CertificateDetail: &certificateservice.CertificateDetailForCertificateGetInstanceOutput{
			Chain: []*string{volcengine.String(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{9, 9, 9}})))},
		}},
		"cert-ok": issuedDetail(chain),
	}}
	adapter := newTestCertAdapter(client, 10)
	ctx := context.Background()

	for _, id := range []string{"cert-garbage", "cert-empty", "cert-nil-detail", "cert-noncert-pem", "cert-bad-der"} {
		item, err := adapter.GetCertificate(ctx, certTestCreds(), id)
		assert.Zero(t, item, id)
		require.Error(t, err, id)
		assert.NotErrorIs(t, err, ErrCertFiltered, id) // 链解析失败 ≠ 过滤语义
	}

	// 正常实例不受同库失败影响（独立调用互不干扰）
	item, err := adapter.GetCertificate(ctx, certTestCreds(), "cert-ok")
	require.NoError(t, err)
	assert.NotEmpty(t, item.Fingerprint)
}

// ==================== AC4：单实例失败不中断 List ====================

func TestListCertificatesPartialFailureReturnsCollectedAndError(t *testing.T) {
	chain := genCertTestChain(t, "p.example.com", []string{"p.example.com"})
	garbageDetail := &certificateservice.CertificateGetInstanceOutput{
		Status: volcengine.String(certStatusIssued),
		CertificateDetail: &certificateservice.CertificateDetailForCertificateGetInstanceOutput{
			Chain: []*string{volcengine.String("garbage")},
		},
	}
	client := &fakeCertLibraryClient{
		pages: [][]*certificateservice.InstanceForCertificateGetInstanceListOutput{{
			listInstance("i1-ok", certStatusIssued, false),
			listInstance("i2-get-err", certStatusIssued, false),
			listInstance("i3-parse-err", certStatusIssued, false),
			listInstance("i4-ok", certStatusIssued, false),
		}},
		total: 4,
		details: map[string]*certificateservice.CertificateGetInstanceOutput{
			"i1-ok":        issuedDetail(chain),
			"i3-parse-err": garbageDetail,
			"i4-ok":        issuedDetail(chain),
		},
		getErrs: map[string]error{"i2-get-err": errors.New("volcano api 500")},
	}
	adapter := newTestCertAdapter(client, 10)

	items, err := adapter.ListCertificates(context.Background(), certTestCreds())
	require.Error(t, err) // 已获取部分 + 错误
	require.Len(t, items, 2)
	assert.Equal(t, "i1-ok", items[0].CloudCertID)
	assert.Equal(t, "i4-ok", items[1].CloudCertID)
	// 失败后继续处理后续条目（i4 在 i2/i3 失败之后仍被采集）
	assert.Equal(t, []string{"i1-ok", "i2-get-err", "i3-parse-err", "i4-ok"}, client.getIDs)
	// 聚合错误点名失败实例
	assert.Contains(t, err.Error(), "i2-get-err")
	assert.Contains(t, err.Error(), "i3-parse-err")
}

func TestListCertificatesPageErrorReturnsPartialAndError(t *testing.T) {
	chain := genCertTestChain(t, "g.example.com", []string{"g.example.com"})
	client := &fakeCertLibraryClient{
		pages: [][]*certificateservice.InstanceForCertificateGetInstanceListOutput{{
			listInstance("first", certStatusIssued, false),
		}},
		total:      3,
		failOnPage: 2,
		listErr:    errors.New("volcano api throttled"),
		details:    map[string]*certificateservice.CertificateGetInstanceOutput{"first": issuedDetail(chain)},
	}
	adapter := newTestCertAdapter(client, 1) // 每页 1 条：第 1 页整页 → 继续请求第 2 页

	items, err := adapter.ListCertificates(context.Background(), certTestCreds())
	require.Error(t, err)
	require.Len(t, items, 1) // 第 2 页失败：返回第 1 页已获取部分
	assert.Equal(t, "first", items[0].CloudCertID)
	assert.Contains(t, err.Error(), "certificate_get_instance_list")
}

// ==================== AC5：私钥不进返回结构/不进日志 ====================

func TestPrivateKeyNeverExposed(t *testing.T) {
	chain := genCertTestChain(t, "k.example.com", []string{"k.example.com"})
	const marker = "SECRET-PRIVATE-KEY-MATERIAL"
	detail := issuedDetail(chain)
	detail.CertificateDetail.PrivateKey = volcengine.String(marker)
	detail.EncryptionCertificateDetail = &certificateservice.EncryptionCertificateDetailForCertificateGetInstanceOutput{
		PrivateKey: volcengine.String(marker),
	}
	// 链里混入私钥块：净化后必须被丢弃
	detail.CertificateDetail.Chain = append(detail.CertificateDetail.Chain, volcengine.String(
		string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: []byte{4, 5, 6}}))+marker))
	client := &fakeCertLibraryClient{
		pages: [][]*certificateservice.InstanceForCertificateGetInstanceListOutput{{
			listInstance("k", certStatusIssued, false),
		}},
		total:   1,
		details: map[string]*certificateservice.CertificateGetInstanceOutput{"k": detail},
	}
	adapter := newTestCertAdapter(client, 10)
	ctx := context.Background()

	item, err := adapter.GetCertificate(ctx, certTestCreds(), "k")
	require.NoError(t, err)
	assert.NotContains(t, fmt.Sprintf("%+v", item), marker)
	assert.NotContains(t, item.CertChainPEM, "PRIVATE KEY")

	items, listErr := adapter.ListCertificates(ctx, certTestCreds())
	require.NoError(t, listErr)
	require.Len(t, items, 1)
	assert.NotContains(t, fmt.Sprintf("%+v", items), marker)

	// 过滤/解析失败路径的错误信息同样不含私钥材料
	revoked := issuedDetail(chain)
	revoked.IsCertificateRevoked = volcengine.Bool(true)
	client.details["k-revoked"] = revoked
	client.details["k-garbage"] = &certificateservice.CertificateGetInstanceOutput{
		Status: volcengine.String(certStatusIssued),
		CertificateDetail: &certificateservice.CertificateDetailForCertificateGetInstanceOutput{
			Chain: []*string{volcengine.String("garbage")},
		},
	}
	_, err = adapter.GetCertificate(ctx, certTestCreds(), "k-revoked")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), marker)
	_, err = adapter.GetCertificate(ctx, certTestCreds(), "k-garbage")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), marker)
}

// ==================== 输入校验与默认值 ====================

func TestCertAdapterInputValidation(t *testing.T) {
	client := &fakeCertLibraryClient{details: map[string]*certificateservice.CertificateGetInstanceOutput{}}
	adapter := newTestCertAdapter(client, 10)
	ctx := context.Background()

	items, err := adapter.ListCertificates(ctx, nil)
	assert.Nil(t, items)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil creds")

	item, err := adapter.GetCertificate(ctx, nil, "inst")
	assert.Zero(t, item)
	require.Error(t, err)

	item, err = adapter.GetCertificate(ctx, certTestCreds(), "  ")
	assert.Zero(t, item)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty instance id")
}

func TestCertAdapterPageSizeFallback(t *testing.T) {
	chain := genCertTestChain(t, "z.example.com", []string{"z.example.com"})
	client := &fakeCertLibraryClient{
		pages: [][]*certificateservice.InstanceForCertificateGetInstanceListOutput{
			{listInstance("a", certStatusIssued, false)},
		},
		total:   1,
		details: map[string]*certificateservice.CertificateGetInstanceOutput{"a": issuedDetail(chain)},
	}
	// 零值 listPageSize → 回退默认 50
	adapter := &CertAdapter{
		logger: elog.DefaultLogger,
		newClient: func(*domain.CloudAccount) (certLibraryAPI, error) {
			return client, nil
		},
	}
	items, err := adapter.ListCertificates(context.Background(), certTestCreds())
	require.NoError(t, err)
	assert.Len(t, items, 1)
	require.NotEmpty(t, client.pageSizes)
	assert.Equal(t, int32(certDefaultPageSize), client.pageSizes[0])
}

func TestNewCertAdapterDefaults(t *testing.T) {
	adapter := NewCertAdapter(nil) // nil logger → DefaultLogger
	assert.Equal(t, int32(certDefaultPageSize), adapter.listPageSize)
	assert.NotNil(t, adapter.newClient)
	// 真实 SDK 工厂：会话与客户端纯本地装配（不发网络请求）
	client, err := adapter.newClient(certTestCreds())
	require.NoError(t, err)
	assert.NotNil(t, client)
}
