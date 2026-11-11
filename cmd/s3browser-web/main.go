package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"s3browser/frontend"
	"s3browser/internal/backend"
	"s3browser/internal/service"
	"s3browser/internal/sizer"
	"s3browser/internal/transfers"
	"s3browser/internal/webserver"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Lỗi:", err)
		os.Exit(1)
	}
}

func run() error {
	listen := flag.String("listen", "127.0.0.1:8080", "Địa chỉ nghe")
	tokenFlag := flag.String("token", "", "Token cố định (dài ít nhất 16 ký tự; mặc định sinh ngẫu nhiên)")
	tlsCert := flag.String("tls-cert", "", "Đường dẫn tệp chứng chỉ TLS")
	tlsKey := flag.String("tls-key", "", "Đường dẫn tệp khóa riêng TLS")
	noBrowser := flag.Bool("no-browser", false, "Không tự mở trình duyệt khi khởi động")
	flag.Parse()

	if (*tlsCert == "") != (*tlsKey == "") {
		return errors.New("cần cung cấp cả -tls-cert và -tls-key để bật HTTPS")
	}
	useTLS := *tlsCert != "" && *tlsKey != ""

	token := *tokenFlag
	if token == "" {
		token = os.Getenv("S3BROWSER_WEB_TOKEN")
	}
	if token != "" {
		if len(token) < 16 {
			return errors.New("token tự đặt phải dài ít nhất 16 ký tự")
		}
	} else {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return fmt.Errorf("không sinh được token ngẫu nhiên: %w", err)
		}
		token = base64.RawURLEncoding.EncodeToString(buf)
	}

	var webSrv atomic.Pointer[webserver.Server]
	b := backend.Default(func(job transfers.Job) {
		if s := webSrv.Load(); s != nil {
			s.PublishTransfer(job)
		}
	}, func(ev sizer.Event) {
		if s := webSrv.Load(); s != nil {
			s.PublishSize(ev)
		}
	})
	defer b.Close()

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return fmt.Errorf("không mở được cổng nghe %s: %w", *listen, err)
	}
	defer ln.Close()

	actualAddr := ln.Addr().String()
	srv, err := webserver.New(webserver.Config{
		Service:    service.New(b),
		Assets:     frontend.FS,
		Token:      token,
		ListenAddr: actualAddr,
		TLS:        useTLS,
	})
	if err != nil {
		return err
	}
	webSrv.Store(srv)
	defer srv.Close()

	if !webserver.IsLoopbackAddr(*listen) {
		fmt.Fprintln(os.Stderr, "Cảnh báo: Đang nghe trên địa chỉ không phải loopback. Nên bật TLS (-tls-cert, -tls-key) hoặc đặt sau reverse proxy có HTTPS.")
	}

	scheme := "http"
	if useTLS {
		scheme = "https"
	}
	fmt.Printf("Đang nghe tại: %s\n", actualAddr)
	openURL := fmt.Sprintf("%s://%s/?token=%s", scheme, displayHostPort(*listen, actualAddr), token)
	fmt.Printf("Mở: %s\n", openURL)

	httpSrv := srv.HTTPServer()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Chỉ tự mở khi nghe loopback: máy chủ từ xa không có trình duyệt để mở.
	if !*noBrowser && webserver.IsLoopbackAddr(*listen) {
		openBrowser(openURL)
	}

	errCh := make(chan error, 1)
	go func() {
		var serveErr error
		if useTLS {
			serveErr = httpSrv.ServeTLS(ln, *tlsCert, *tlsKey)
		} else {
			serveErr = httpSrv.Serve(ln)
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		srv.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
		return <-errCh
	}
}

func displayHostPort(configured, actual string) string {
	host, port, err := net.SplitHostPort(actual)
	if err != nil {
		return actual
	}
	cfgHost, _, cfgErr := net.SplitHostPort(configured)
	if cfgErr == nil && strings.EqualFold(cfgHost, "localhost") {
		return net.JoinHostPort("localhost", port)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return net.JoinHostPort("127.0.0.1", port)
	}
	return actual
}

// openBrowser mở URL bằng trình duyệt mặc định; lỗi (ví dụ máy không có giao diện) được bỏ qua
// vì người dùng vẫn có dòng "Mở:" để mở tay.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if cmd.Start() == nil {
		go func() { _ = cmd.Wait() }()
	}
}
