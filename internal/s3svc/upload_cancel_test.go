package s3svc

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHủyUploadDọnMultipartBằngContextCònHiệuLực(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var aborted atomic.Bool
	svc, p := setup(t, func(r *http.Request) (*http.Response, error) {
		query := r.URL.Query()
		if _, ok := query["uploads"]; ok {
			return response(r, 200, `<InitiateMultipartUploadResult><UploadId>lượt-thử</UploadId></InitiateMultipartUploadResult>`), nil
		}
		if r.Method == http.MethodDelete {
			if r.Context().Err() != nil {
				t.Error("Context dọn multipart đã bị hủy")
			}
			aborted.Store(true)
			return response(r, 204, ""), nil
		}
		cancel()
		return nil, ctx.Err()
	})
	size := int64(6 * 1024 * 1024)
	err := svc.Upload(ctx, p.ID, "kho", Upload{Key: "tệp", Body: strings.NewReader(strings.Repeat("a", int(size))), Size: size}, nil)
	if err == nil || !aborted.Load() {
		t.Fatal("Hủy upload chưa dọn multipart")
	}
}
