package apperr_test

import (
	"encoding/json"
	"github.com/aws/smithy-go"
	"net/url"
	"s3browser/internal/apperr"
	"s3browser/internal/testutil"
	"strings"
	"testing"
)

func TestLỗiJSONCheCảMãVàThôngĐiệp(t *testing.T) {
	sensitive := testutil.Random() + " /+"
	key := testutil.Random()
	filter := &apperr.Filter{}
	filter.Remember(sensitive, key)
	err := filter.Describe(&smithy.GenericAPIError{Code: "SignatureDoesNotMatch", Message: "Máy chủ từ chối " + sensitive + " " + url.QueryEscape(sensitive) + " " + strings.ReplaceAll(url.QueryEscape(sensitive), "+", "%20") + " " + key})
	b, _ := json.Marshal(err)
	if strings.Contains(string(b), sensitive) || strings.Contains(string(b), key) || strings.Contains(string(b), url.QueryEscape(sensitive)) {
		t.Fatal("Lỗi chứa dữ liệu nhạy cảm")
	}
	if err.Code != "SignatureDoesNotMatch" || !strings.Contains(err.Message, "Máy chủ từ chối") || !strings.Contains(err.Message, "forcePathStyle") {
		t.Fatal("Mất mã hoặc gợi ý")
	}
	hidden := filter.Describe(apperr.New(key, "Nội dung "+sensitive))
	if strings.Contains(hidden.Error(), key) {
		t.Fatal("Mã lỗi chưa được che")
	}
	var decoded map[string]string
	if json.Unmarshal([]byte(err.Error()), &decoded) != nil || len(decoded) != 2 {
		t.Fatal("Lỗi không phải JSON đúng hợp đồng")
	}
}
