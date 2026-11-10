//go:build live

package s3svc

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"s3browser/internal/apperr"
	"s3browser/internal/profiles"
)

// Kiểm thử sống, chỉ-đọc, với endpoint thật. Chạy bằng: go test -tags live -run TestSong ./internal/s3svc
// Cần biến môi trường S3_LIVE_ENDPOINT, S3_LIVE_ACCESS_KEY, S3_LIVE_SECRET_KEY (không ghi vào mã).
type nguonMotHoSo struct{ p profiles.Profile }

func (n nguonMotHoSo) Get(string) (profiles.Profile, error) { return n.p, nil }

// Chỉ-đọc: GetBucketPolicy trên mọi bucket; bucket chưa có policy phải trả chuỗi rỗng, không lỗi.
func TestSongDocPolicy(t *testing.T) {
	endpoint, access, secret := os.Getenv("S3_LIVE_ENDPOINT"), os.Getenv("S3_LIVE_ACCESS_KEY"), os.Getenv("S3_LIVE_SECRET_KEY")
	if endpoint == "" || access == "" || secret == "" {
		t.Skip("thiếu biến môi trường S3_LIVE_*")
	}
	if !strings.HasPrefix(endpoint, "http") {
		endpoint = "https://" + endpoint
	}
	path := true
	filter := &apperr.Filter{}
	filter.Remember(access, secret)
	svc := New(nguonMotHoSo{profiles.Profile{ID: "live", Endpoint: endpoint, Region: "us-east-1", AccessKeyID: access, SecretAccessKey: secret, ForcePathStyle: &path}}, filter)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	buckets, err := svc.ListBuckets(ctx, "live")
	if err != nil {
		t.Fatalf("ListBuckets: %v", err)
	}
	for _, b := range buckets {
		policy, err := svc.GetBucketPolicy(ctx, "live", b.Name)
		if err != nil {
			t.Fatalf("GetBucketPolicy %s: %v", b.Name, err)
		}
		t.Logf("bucket %s: có policy = %v", b.Name, policy != "")
	}
}

func TestSongChiDoc(t *testing.T) {
	endpoint, access, secret := os.Getenv("S3_LIVE_ENDPOINT"), os.Getenv("S3_LIVE_ACCESS_KEY"), os.Getenv("S3_LIVE_SECRET_KEY")
	if endpoint == "" || access == "" || secret == "" {
		t.Skip("thiếu biến môi trường S3_LIVE_*")
	}
	if !strings.HasPrefix(endpoint, "http") {
		endpoint = "https://" + endpoint
	}
	path := true
	p := profiles.Profile{ID: "live", Endpoint: endpoint, Region: "us-east-1", AccessKeyID: access, SecretAccessKey: secret, ForcePathStyle: &path}
	filter := &apperr.Filter{}
	filter.Remember(access, secret)
	svc := New(nguonMotHoSo{p}, filter)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	buckets, err := svc.ListBuckets(ctx, "live")
	if err != nil {
		t.Fatalf("ListBuckets: %v", err)
	}
	t.Logf("số bucket: %d", len(buckets))
	for _, b := range buckets {
		root, err := svc.List(ctx, "live", b.Name, "", "")
		if err != nil {
			t.Fatalf("List gốc %s: %v", b.Name, err)
		}
		t.Logf("bucket %s: %d thư mục, %d đối tượng, còn trang: %v", b.Name, len(root.Folders), len(root.Objects), root.NextToken != nil)
		if len(root.Folders) == 0 {
			continue
		}
		sub, err := svc.List(ctx, "live", b.Name, root.Folders[0].Prefix, "")
		if err != nil {
			t.Fatalf("List thư mục con: %v", err)
		}
		t.Logf("  thư mục con %s: %d thư mục, %d đối tượng", root.Folders[0].Prefix, len(sub.Folders), len(sub.Objects))
		for _, f := range sub.Folders {
			leaf, err := svc.List(ctx, "live", b.Name, f.Prefix, "")
			if err != nil || len(leaf.Objects) == 0 {
				continue
			}
			obj := leaf.Objects[0]
			head, err := svc.Head(ctx, "live", b.Name, obj.Key)
			if err != nil {
				t.Fatalf("Head: %v", err)
			}
			t.Logf("  head %s: size=%d type=%s", obj.Key, head.Size, head.ContentType)
			url, err := svc.Presign(ctx, "live", b.Name, obj.Key, 300)
			if err != nil || !strings.Contains(url, "X-Amz-Signature") {
				t.Fatalf("Presign: %v", err)
			}
			t.Log("  presign: tạo được URL ký (không in)")
			return
		}
	}
}
