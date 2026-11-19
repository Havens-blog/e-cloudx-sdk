package domain

import (
	"regexp"
	"testing"
)

func TestIsAttachmentAssetType(t *testing.T) {
	attachments := []string{"eni", "vswitch", "security_group", "disk", "snapshot", "image", "ddos_domain"}
	for _, at := range attachments {
		if !IsAttachmentAssetType(at) {
			t.Errorf("%s 应为附属资源", at)
		}
	}
	entities := []string{"ecs", "rds", "vpc", "eip", "lb", "cdn", "waf", "ddos", "oss", "nas", "kafka", "elasticsearch", "redis", "mongodb", "dns"}
	for _, at := range entities {
		if IsAttachmentAssetType(at) {
			t.Errorf("%s 不应为附属资源", at)
		}
	}
}

func TestAttachmentModelUIDPattern(t *testing.T) {
	re := regexp.MustCompile(AttachmentModelUIDPattern())

	match := []string{
		"aliyun_eni", "volcengine_eni", "huawei_eni",
		"aliyun_security_group", "tencent_security_group",
		"volcengine_vswitch", "aliyun_disk", "aws_snapshot", "huawei_image",
		"aliyun_ddos_domain", "cloud_ddos_domain",
	}
	for _, uid := range match {
		if !re.MatchString(uid) {
			t.Errorf("%s 应命中附属资源正则", uid)
		}
	}

	noMatch := []string{
		"aliyun_ecs", "volcano_ecs", "aws_rds", "aliyun_oss",
		"aliyun_elasticsearch", "volcengine_mongodb", "cloud_vm", "ecs",
		"aliyun_ddos", "tencent_ddos", "cloud_ddos",
	}
	for _, uid := range noMatch {
		if re.MatchString(uid) {
			t.Errorf("%s 不应命中附属资源正则", uid)
		}
	}
}

// TestDDOSDomainIsAttachment 锁定 DDoS 防护域名的两条语义（2026-11-19）：
//  1. ddos_domain 是附属资源——高防实例挂上百个域名，计入「资产总数」
//     会让 DDoS 从 1 个实例膨胀成上百个；
//  2. 反向提取必须区分 ddos 与 ddos_domain，不能把实例本身当成域名对象。
func TestDDOSDomainIsAttachment(t *testing.T) {
	if !IsAttachmentAssetType("ddos_domain") {
		t.Error("ddos_domain 应为附属资源")
	}
	if IsAttachmentAssetType("ddos") {
		t.Error("ddos 实例本身不应为附属资源")
	}
	re := regexp.MustCompile(AttachmentModelUIDPattern())
	if !re.MatchString("aliyun_ddos_domain") {
		t.Error("aliyun_ddos_domain 应命中附属资源正则")
	}
	if re.MatchString("aliyun_ddos") {
		t.Error("aliyun_ddos（实例本身）不应命中附属资源正则")
	}
	if got := ExtractAssetType("aliyun_ddos_domain"); got != "ddos_domain" {
		t.Errorf("ExtractAssetType(aliyun_ddos_domain) = %q, want ddos_domain", got)
	}
	if got := ExtractAssetType("aliyun_ddos"); got != "ddos" {
		t.Errorf("ExtractAssetType(aliyun_ddos) = %q, want ddos", got)
	}
}
