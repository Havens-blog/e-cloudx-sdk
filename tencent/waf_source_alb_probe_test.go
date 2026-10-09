// Package tencent_test 查 ALB 实例的匹配 key(manual probe,只读)。
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

func TestProbeALBMatchKey(t *testing.T) {
	setupProbeEnv(t)
	dsn := os.Getenv("NAS_PROBE_MONGODB_DSN")
	if dsn == "" {
		t.Skip("无 mongo DSN")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(dsn))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Disconnect(ctx)
	coll := client.Database("ecam").Collection("ecam_instance")

	// 1. 精确按 id=51187306 找 ALB
	t.Log("=== ALB id=51187306 ===")
	var doc struct {
		ID         int64                  `bson:"id"`
		ModelUID   string                 `bson:"model_uid"`
		AssetID    string                 `bson:"asset_id"`
		Attributes map[string]interface{} `bson:"attributes"`
	}
	err = coll.FindOne(ctx, bson.M{"id": 51187306}).Decode(&doc)
	if err != nil {
		t.Logf("id=51187306 未找到: %v", err)
	} else {
		t.Logf("id=%d model=%s asset=%s", doc.ID, doc.ModelUID, doc.AssetID)
		for _, k := range []string{"address", "cname", "dns_name", "endpoint", "vip", "load_balancer_type", "public_ip", "ip_address"} {
			t.Logf("  %s=%v", k, doc.Attributes[k])
		}
	}

	// 2. 反向: 哪些实例的 address/cname == alb 域名
	alb := "alb-1vpkxekjba48vbcw0y.cn-shenzhen.alb.aliyuncs.com"
	for _, field := range []string{"attributes.address", "attributes.cname", "attributes.dns_name", "attributes.endpoint", "attributes.public_dns"} {
		cur, e := coll.Find(ctx, bson.M{field: alb})
		if e != nil {
			continue
		}
		n := 0
		for cur.Next(ctx) && n < 3 {
			var d struct {
				ID       int64                  `bson:"id"`
				ModelUID string                 `bson:"model_uid"`
				Attr     map[string]interface{} `bson:"attributes"`
			}
			_ = cur.Decode(&d)
			t.Logf("[%s] id=%d model=%s", field, d.ID, d.ModelUID)
			n++
		}
		cur.Close(ctx)
	}
}