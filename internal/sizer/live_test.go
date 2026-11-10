//go:build live

package sizer

import (
	"os"
	"strings"
	"testing"
	"time"

	"s3browser/internal/apperr"
	"s3browser/internal/profiles"
	"s3browser/internal/s3svc"
)

// Kiểm thử sống, chỉ-đọc (chỉ ListObjectsV2). Chạy: go test -tags live -run TestSongTinhDungLuong ./internal/sizer
// Cần S3_LIVE_ENDPOINT, S3_LIVE_ACCESS_KEY, S3_LIVE_SECRET_KEY; tên bucket nhỏ qua S3_LIVE_BUCKETS (phân tách bằng dấu phẩy).
type motHoSo struct{ p profiles.Profile }

func (m motHoSo) Get(string) (profiles.Profile, error) { return m.p, nil }

func TestSongTinhDungLuong(t *testing.T) {
	endpoint, access, secret := os.Getenv("S3_LIVE_ENDPOINT"), os.Getenv("S3_LIVE_ACCESS_KEY"), os.Getenv("S3_LIVE_SECRET_KEY")
	buckets := strings.Split(os.Getenv("S3_LIVE_BUCKETS"), ",")
	if endpoint == "" || access == "" || secret == "" || buckets[0] == "" {
		t.Skip("thiếu biến môi trường S3_LIVE_*")
	}
	if !strings.HasPrefix(endpoint, "http") {
		endpoint = "https://" + endpoint
	}
	path := true
	filter := &apperr.Filter{}
	filter.Remember(access, secret)
	svc := s3svc.New(motHoSo{profiles.Profile{ID: "live", Endpoint: endpoint, Region: "us-east-1", AccessKeyID: access, SecretAccessKey: secret, ForcePathStyle: &path}}, filter)
	events := make(chan Event, 1024)
	s := New(svc, filter, func(e Event) { events <- e })
	defer s.Close()

	for _, b := range buckets {
		start := time.Now()
		if _, err := s.Start("live", strings.TrimSpace(b), "", false); err != nil {
			t.Fatal(err)
		}
		deadline := time.After(120 * time.Second)
		for done := false; !done; {
			select {
			case e := <-events:
				if e.Bucket != strings.TrimSpace(b) || e.Prefix != "" {
					continue
				}
				if e.State == "error" {
					t.Fatalf("bucket %s lỗi: %s", e.Bucket, e.Message)
				}
				if e.State == "done" {
					t.Logf("bucket %s: %d byte, %d đối tượng, mất %v", e.Bucket, e.Bytes, e.Objects, time.Since(start).Round(time.Millisecond))
					done = true
				}
			case <-deadline:
				t.Fatalf("bucket %s quá 120 giây", b)
			}
		}
	}
}
