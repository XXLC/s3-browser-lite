package transfers

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"s3browser/internal/testutil"
	"testing"
)

func TestChặnJunctionVàTảiLênLiênKết(t *testing.T) {
	root := testutil.Temporary(t)
	outside := testutil.Temporary(t)
	link := filepath.Join(root, "liên-kết")
	if err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).Run(); err != nil {
		t.Fatal("Không tạo được junction kiểm thử")
	}
	expectCode(t, EnsureSafeDirectory(root, link), "UnsafePath")
	files := []localFile{}
	expectCode(t, localFiles(context.Background(), link, "liên-kết", &files), "UnsafePath")
	_ = os.Remove(link)
}
