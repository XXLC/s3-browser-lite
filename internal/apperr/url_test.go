package apperr

import (
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestCheDạngEncodeURIComponentCủaJavaScript(t *testing.T) {
	value := uuid.NewString() + " !'()* /+"
	f := &Filter{}
	f.Remember(value)
	encoded := encodeURIComponent(value)
	e := f.Describe(New("AccessDenied", "Máy chủ từ chối "+encoded))
	if strings.Contains(e.Message, encoded) {
		t.Fatal("Dạng mã hóa JavaScript chưa được che")
	}
}
