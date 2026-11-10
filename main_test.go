package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNoStoreDatCacheControlVaGoiTiep(t *testing.T) {
	called := false
	handler := noStore(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNotFound)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api-shim.js", nil))
	if !called {
		t.Fatal("handler kế tiếp không được gọi")
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, cần no-store (kể cả với phản hồi 404)", got)
	}
}
