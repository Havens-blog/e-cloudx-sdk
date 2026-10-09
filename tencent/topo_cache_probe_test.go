// Package tencent_test 查拓扑缓存库(manual probe,只读)。
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

func TestProbeTopoCache(t *testing.T) {
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

	nodeColl := client.Database("ecam").Collection("ecam_topo_node")
	edgeColl := client.Database("ecam").Collection("ecam_topo_edge")

	// 1. WAF 节点在拓扑缓存里的 source_ips
	t.Log("=== topo_node inst-5949117 (WAF) ===")
	var n struct {
		ID         string                 `bson:"_id"`
		Type       string                 `bson:"type"`
		Attributes map[string]interface{} `bson:"attributes"`
	}
	err = nodeColl.FindOne(ctx, bson.M{"_id": "inst-5949117"}).Decode(&n)
	if err != nil {
		t.Logf("inst-5949117 不在拓扑库: %v", err)
	} else {
		t.Logf("id=%s type=%s source_ips=%v", n.ID, n.Type, n.Attributes["source_ips"])
	}

	// 2. 该 WAF 的所有出边/入边
	t.Log("=== edges involving inst-5949117 ===")
	cur, err := edgeColl.Find(ctx, bson.M{"$or": []bson.M{{"source_id": "inst-5949117"}, {"target_id": "inst-5949117"}}})
	if err != nil {
		t.Fatalf("edges find: %v", err)
	}
	defer cur.Close(ctx)
	cnt := 0
	for cur.Next(ctx) {
		var e struct {
			ID       string `bson:"_id"`
			SourceID string `bson:"source_id"`
			TargetID string `bson:"target_id"`
		}
		_ = cur.Decode(&e)
		t.Logf("  edge %s: %s -> %s", e.ID, e.SourceID, e.TargetID)
		cnt++
	}
	if cnt == 0 {
		t.Log("  (该 WAF 在拓扑库里没有任何边 —— 即断链点)")
	}

	// 3. ALB 在拓扑库里的节点是否存在
	t.Log("=== topo_node inst-51187306 (ALB) ===")
	err = nodeColl.FindOne(ctx, bson.M{"_id": "inst-51187306"}).Decode(&n)
	if err != nil {
		t.Logf("inst-51187306 不在拓扑库: %v", err)
	} else {
		t.Logf("id=%s type=%s address=%v", n.ID, n.Type, n.Attributes["address"])
	}
}