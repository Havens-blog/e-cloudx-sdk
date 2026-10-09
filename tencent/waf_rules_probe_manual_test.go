// Package tencent_test WAF 防护规则/规则数/防护模式实盘探测(manual probe,只读)。
//
// 目的:验证 DescribeDomainRules 门神规则回捞后 RuleCount/Rules/ProtectionMode 落地。
// 自包含:DSN + 解密密钥从 config/prod.yaml 读取(不回显凭证),经 nasprobe 只读加载
// 活跃 tencent 账号,调适配器 ListInstances 打印前几域的规则摘要。
//
// Hard Rule:只读,只打印概要(规则名/等级),不动生产表。
package tencent_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/tencent"
	"github.com/gotomicro/ego/core/elog"
)

// TestProbeTencentWAFRules 打印前几域规则数/模式/规则摘要。
func TestProbeTencentWAFRules(t *testing.T) {
	setupProbeEnv(t)
	ak, sk, _ := loadTencentCreds(t)
	if ak == "" {
		t.Skip("无 tencent 凭证,跳过")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	// WAF 防护域名固定 ap-guangzhou
	adapter := tencent.NewWAFAdapter(ak, sk, "ap-guangzhou", elog.DefaultLogger)
	instances, err := adapter.ListInstances(ctx, "ap-guangzhou")
	if err != nil {
		t.Fatalf("ListInstances 失败: %v", err)
	}

	shown := 0
	for _, ins := range instances {
		if len(ins.Rules) == 0 || shown >= 5 {
			continue
		}
		shown++
		t.Logf("[%s] rules=%d mode=%q", ins.InstanceName, ins.RuleCount, ins.ProtectionMode)
		for j, r := range ins.Rules {
			if j >= 2 {
				break
			}
			t.Logf("     rule[%d] name=%q level=%q type=%q status=%q", j, r.Name, r.Level, r.Type, r.Status)
		}
	}
	t.Logf("=== 共 %d 实例,有规则 %d ===", len(instances), shown)
}

// setupProbeEnv 从 config/prod.yaml 读取 Mongo DSN 与解密密钥注入探测 env(凭证不回显)。
func setupProbeEnv(t *testing.T) {
	raw, err := os.ReadFile("../../../../config/prod.yaml")
	if err != nil {
		t.Logf("未找到 config/prod.yaml: %v", err)
		return
	}
	var dsn, user, pass, key string
	inMongo, inSec := false, false
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "mongodb:"):
			inMongo, inSec = true, false
		case strings.HasPrefix(trimmed, "security:"):
			inMongo, inSec = false, true
		case inMongo && strings.HasPrefix(trimmed, "dsn:"):
			dsn = strings.TrimSpace(strings.TrimPrefix(trimmed, "dsn:"))
		case inMongo && strings.HasPrefix(trimmed, "username:"):
			user = strings.TrimSpace(strings.TrimPrefix(trimmed, "username:"))
		case inMongo && strings.HasPrefix(trimmed, "password:"):
			pass = strings.TrimSpace(strings.TrimPrefix(trimmed, "password:"))
		case inSec && strings.HasPrefix(trimmed, "encryption_key:"):
			key = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "encryption_key:")), `"'`)
		}
	}
	if dsn != "" && user != "" {
		dsn = strings.Replace(dsn, "mongodb://", "mongodb://"+user+":"+pass+"@", 1)
		os.Setenv("NAS_PROBE_MONGODB_DSN", dsn)
	}
	if key != "" {
		os.Setenv("CAM_ENCRYPTION_KEY", key)
	}
}