// Package tencent_test 直接查 MongoDB 里腾讯 WAF 实例 source_ips 是否回捞(manual probe,只读)。
package tencent_test

import (
	"context"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestProbeTencentWAFSourceInDB(t *testing.T) {
	setupProbeEnv(t)
	dsn := os.Getenv("NAS_PROBE_MONGODB_DSN")
	if dsn == "" {
		t.Skip("无 mongo DSN,跳过")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(dsn))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Disconnect(ctx)

	coll := client.Database("ecam").Collection("ecam_instance")

	// 1. 按 protected_hosts=api.jlc.com 且 provider=tencent 找 WAF 实例
	cur, err := coll.Find(ctx, bson.M{"attributes.protected_hosts": "api.jlc.com", "attributes.provider": "tencent"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	defer cur.Close(ctx)
	t.Log("=== tencent WAF instances for api.jlc.com ===")
	cnt := 0
	for cur.Next(ctx) {
		var doc struct {
			ID         int64                  `bson:"id"`
			ModelUID   string                 `bson:"model_uid"`
			AssetID    string                 `bson:"asset_id"`
			Attributes map[string]interface{} `bson:"attributes"`
		}
		if err := cur.Decode(&doc); err != nil {
			t.Fatalf("decode: %v", err)
		}
		cnt++
		t.Logf("id=%d model=%s asset=%s", doc.ID, doc.ModelUID, doc.AssetID)
		t.Logf("  edition=%v source_ips=%v cname=%v", doc.Attributes["edition"], doc.Attributes["source_ips"], doc.Attributes["cname"])
	}
	if cnt == 0 {
		t.Log("未找到 tencent WAF 实例(protected_hosts=api.jlc.com + provider=tencent)")
	}

	// 2. 顺便看 ALB 实例的 address 字段(匹配 key)
	albCur, err := coll.Find(ctx, bson.M{"model_uid": bson.M{"$regex": "alb"}})
	if err != nil {
		t.Fatalf("find alb: %v", err)
	}
	defer albCur.Close(ctx)
	t.Log("=== ALB instances (address 字段, 采样 5) ===")
	n := 0
	for albCur.Next(ctx) && n < 5 {
		var doc struct {
			ID         int64                  `bson:"id"`
			ModelUID   string                 `bson:"model_uid"`
			Attributes map[string]interface{} `bson:"attributes"`
		}
		if err := albCur.Decode(&doc); err != nil {
			t.Fatalf("decode alb: %v", err)
		}
		n++
		t.Logf("id=%d model=%s address=%v cname=%v", doc.ID, doc.ModelUID, doc.Attributes["address"], doc.Attributes["cname"])
	}
}