// Package aws WAF 实例数据只读探测(manual probe)。
//
// 目的:`go test` 只读连 ecam_instance 集合,打印 aws_waf 实例的
// protected_hosts / source_ips / description,用于核对「AWS WAF 源站与防护域名」回捞结果。
//
// Hard Rule:只读查询,不动生产表;凭证从 config/prod.yaml 读取,不得硬编码/回显到日志。
package aws

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// TestProbeAWSWAFInstances 打印 AWS WAF 实例的防护域名与源站字段现状。
func TestProbeAWSWAFInstances(t *testing.T) {
	dsn := probeDSNFromConfig(t)
	if dsn == "" {
		t.Skip("无法从 config/prod.yaml 解析 mongodb 配置,跳过")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(dsn))
	if err != nil {
		t.Fatalf("连接 MongoDB 失败: %v", err)
	}
	defer func() {
		dc, cc := context.WithTimeout(context.Background(), 5*time.Second)
		defer cc()
		_ = client.Disconnect(dc)
	}()

	coll := client.Database("ecam").Collection("ecam_instance")
	cur, err := coll.Find(ctx, bson.M{"model_uid": "aws_waf"})
	if err != nil {
		t.Fatalf("查询 aws_waf 失败: %v", err)
	}
	defer cur.Close(ctx)

	type doc struct {
		AssetID    string                 `bson:"asset_id"`
		AssetName  string                 `bson:"asset_name"`
		AccountID  int64                  `bson:"account_id"`
		Region     string                 `bson:"region"`
		Attributes map[string]any `bson:"attributes"`
	}
	n := 0
	for cur.Next(ctx) {
		var d doc
		if err := cur.Decode(&d); err != nil {
			t.Logf("解码失败: %v", err)
			continue
		}
		n++
		t.Logf("[%d] %s | %s (account=%d, region=%s)", n, d.AssetID, d.AssetName, d.AccountID, fieldStr(d.Attributes, "region"))
		t.Logf("     protected_hosts = %v", fieldStrSlice(d.Attributes, "protected_hosts"))
		t.Logf("     source_ips      = %v", fieldStrSlice(d.Attributes, "source_ips"))
		t.Logf("     description     = %q", fieldStr(d.Attributes, "description"))
	}
	if err := cur.Err(); err != nil {
		t.Fatalf("遍历失败: %v", err)
	}
	t.Logf("=== 共 %d 个 aws_waf 实例 ===", n)
}

// probeDSNFromConfig 从 config/prod.yaml 逐行提取 mongodb 段 dsn/username/password,
// 拼装带凭证 DSN(凭证不在日志回显,仅用于只读连接)。
func probeDSNFromConfig(t *testing.T) string {
	// 测试 cwd 为 internal/shared/cloudx/aws,仓库根为上 4 级。
	path := "../../../../config/prod.yaml"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Logf("未找到 %s: %v", path, err)
		return ""
	}
	var dsn, user, pass string
	inMongo := false
	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case strings.HasPrefix(strings.TrimSpace(line), "mongodb:"):
			inMongo = true
		case inMongo && strings.HasPrefix(strings.TrimSpace(line), "dsn:"):
			dsn = strings.TrimSpace(strings.TrimSpace(line)[4:])
		case inMongo && strings.HasPrefix(strings.TrimSpace(line), "username:"):
			user = strings.TrimSpace(strings.TrimSpace(line)[9:])
		case inMongo && strings.HasPrefix(strings.TrimSpace(line), "password:"):
			pass = strings.TrimSpace(strings.TrimSpace(line)[9:])
		case inMongo && !strings.HasPrefix(line, " ") && strings.TrimSpace(line) == "":
			inMongo = false
		case inMongo && !strings.HasPrefix(line, " ") && strings.Contains(line, ":"):
			inMongo = false
		}
	}
	if dsn == "" {
		return ""
	}
	if user == "" {
		return dsn
	}
	return strings.Replace(dsn, "mongodb://", fmt.Sprintf("mongodb://%s:%s@", user, pass), 1)
}

func fieldStr(attrs map[string]any, key string) string {
	if v, ok := attrs[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func fieldStrSlice(attrs map[string]any, key string) []string {
	v, ok := attrs[key]
	if !ok {
		return nil
	}
	if arr, ok := v.([]any); ok {
		out := make([]string, 0, len(arr))
		for _, e := range arr {
			out = append(out, fmt.Sprintf("%v", e))
		}
		return out
	}
	if arr, ok := v.([]string); ok {
		return arr
	}
	return nil
}