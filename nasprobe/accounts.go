// Package nasprobe 探测用账号加载。
//
// 本包仅供探测任务(manual probe tests / 一次性探测)使用,生产代码不 import。
// Hard Rule:探测用只读凭证,只写样例行到日志,不动生产 ecam_nas_metric 表;
// 本文件只做**只读**的账号列举与密钥解密,探测调用方仅用其发起只读监控 API 查询。
package nasprobe

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Havens-blog/e-common-go/crypto"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ProbeAccount 探测用云账号(只读凭证载体;禁止打印 AK/SK)。
type ProbeAccount struct {
	ID       int64
	Name     string
	Provider string
	Regions  []string
	AK       string
	SK       string
}

// manual probe tests 使用的环境变量门控(全部未设置时按设计跳过):
//   - NAS_PROBE_MONGODB_DSN  可选:MongoDB DSN(设置后从库中加载活跃账号)
//   - NAS_PROBE_MONGODB_DB   可选:库名,默认 ecam
//   - CAM_ENCRYPTION_KEY     可选:AccessKeySecret 解密密钥(库中密文须解密时必填)
//   - NAS_PROBE_<VENDOR>_AK / _SK / _REGION(可选,逐厂商直填凭证,优先于库;
//     VENDOR ∈ VOLC / HUAWEI / AWS / ALIYUN / TENCENT)

// probeAccountsCollection 云账号集合(只读)
const probeAccountsCollection = "ecam_cloud_account"

// ProbeDSNEnabled 是否具备从库加载账号的条件。
func ProbeDSNEnabled() bool {
	return os.Getenv("NAS_PROBE_MONGODB_DSN") != ""
}

// LoadProbeAccounts 只读加载全部活跃云账号并解密 AccessKeySecret。
// 解密失败时按旧明文数据处理(与 cam repository.toDomain 同口径)。
func LoadProbeAccounts() ([]ProbeAccount, error) {
	dsn := os.Getenv("NAS_PROBE_MONGODB_DSN")
	if dsn == "" {
		return nil, fmt.Errorf("NAS_PROBE_MONGODB_DSN 未设置")
	}
	dbName := os.Getenv("NAS_PROBE_MONGODB_DB")
	if dbName == "" {
		dbName = "ecam"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(dsn))
	if err != nil {
		return nil, fmt.Errorf("连接 MongoDB 失败: %w", err)
	}
	defer func() {
		discCtx, discCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer discCancel()
		_ = client.Disconnect(discCtx)
	}()

	coll := client.Database(dbName).Collection(probeAccountsCollection)
	cur, err := coll.Find(ctx, bson.M{"status": "active"})
	if err != nil {
		return nil, fmt.Errorf("查询云账号失败: %w", err)
	}
	defer cur.Close(ctx)

	var accounts []ProbeAccount
	for cur.Next(ctx) {
		var doc struct {
			ID              int64    `bson:"id"`
			Name            string   `bson:"name"`
			Provider        string   `bson:"provider"`
			Regions         []string `bson:"regions"`
			AccessKeyID     string   `bson:"access_key_id"`
			AccessKeySecret string   `bson:"access_key_secret"`
		}
		if err := cur.Decode(&doc); err != nil {
			return nil, fmt.Errorf("解码云账号失败: %w", err)
		}
		acc := ProbeAccount{
			ID:       doc.ID,
			Name:     doc.Name,
			Provider: NormalizeProbeProvider(doc.Provider),
			Regions:  doc.Regions,
			AK:       doc.AccessKeyID,
			SK:       probeDecrypt(doc.AccessKeySecret),
		}
		if acc.AK != "" && acc.SK != "" {
			accounts = append(accounts, acc)
		}
	}
	if err := cur.Err(); err != nil {
		return nil, fmt.Errorf("遍历云账号失败: %w", err)
	}
	return accounts, nil
}

// probeDecrypt 解密库中密文;失败按旧明文处理(与生产 toDomain 同口径)。
func probeDecrypt(cipherText string) string {
	if cipherText == "" {
		return ""
	}
	if err := crypto.InitDefaultCrypto(""); err != nil {
		return cipherText
	}
	if plain, err := crypto.DecryptSecret(cipherText); err == nil {
		return plain
	}
	return cipherText
}

// NormalizeProbeProvider 归并火山双别名(volcengine → volcano)。
func NormalizeProbeProvider(p string) string {
	if p == "volcengine" {
		return "volcano"
	}
	return p
}

// EnvAK / EnvSK / EnvRegion 逐厂商直填凭证的环境变量读取(vendor 形如 "VOLC")。
func EnvAK(vendor string) string { return os.Getenv("NAS_PROBE_" + vendor + "_AK") }

// EnvSK 见 EnvAK。
func EnvSK(vendor string) string { return os.Getenv("NAS_PROBE_" + vendor + "_SK") }

// EnvRegion 见 EnvAK。
func EnvRegion(vendor string) string {
	return os.Getenv("NAS_PROBE_" + vendor + "_REGION")
}

// EnvRegions 逗号分隔的 region 列表(NAS_PROBE_<VENDOR>_REGIONS,优先);
// 未设置时回退单 region(可为空)。region 顺序去重保持稳定。
func EnvRegions(vendor string, fallback string) []string {
	raw := os.Getenv("NAS_PROBE_" + vendor + "_REGIONS")
	if raw == "" {
		raw = fallback
	}
	if raw == "" {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, 4)
	for _, r := range strings.Split(raw, ",") {
		r = strings.TrimSpace(r)
		if r != "" && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

// MaskAK 掩码展示 AK(探测日志用,避免泄漏)。
func MaskAK(ak string) string {
	if len(ak) > 8 {
		return ak[:4] + "****" + ak[len(ak)-4:]
	}
	return "****"
}
