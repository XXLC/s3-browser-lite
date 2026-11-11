package webserver

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"s3browser/frontend"
	"s3browser/internal/apperr"
	"s3browser/internal/s3svc"
	"s3browser/internal/service"
	"s3browser/internal/sizer"
	"s3browser/internal/transfers"
	"s3browser/internal/validate"
)

const (
	cookieName   = "s3b_session"
	maxRPCBody   = 4 * 1024 * 1024
	maxUploadCnt = 10000
	sseBufferLen = 64
)

// Config cấu hình máy chủ web.
type Config struct {
	Service      *service.Service
	Assets       fs.FS
	Token        string
	ListenAddr   string
	TLS          bool
	TempDir      string
	LoginDelay   time.Duration
	PingInterval time.Duration
}

type sseMessage struct {
	event string
	data  []byte
}

// Server phục vụ giao diện web, RPC, SSE, tải lên và tải xuống qua HTTP.
type Server struct {
	service        *service.Service
	svcValue       reflect.Value
	svcType        reflect.Type
	errorType      reflect.Type
	assets         fs.FS
	indexHTML      []byte
	token          string
	listenAddr     string
	listenPort     string
	loopbackListen bool
	tls            bool
	tempDir        string
	ownsTempDir    bool
	loginDelay     time.Duration
	pingInterval   time.Duration

	sseMu   sync.Mutex
	clients map[chan sseMessage]struct{}
	closed  chan struct{}
	closeMu sync.Once

	cleanupMu     sync.Mutex
	cleanupDirs   map[string]string
	completedJobs map[string]bool
}

// New khởi tạo máy chủ web và kiểm tra biến đổi index.html ngay khi khởi động.
func New(cfg Config) (*Server, error) {
	if cfg.Service == nil || cfg.Service.Backend == nil {
		return nil, errors.New("thiếu dịch vụ nghiệp vụ")
	}
	if len(cfg.Token) < 16 {
		return nil, errors.New("token xác thực phải dài ít nhất 16 ký tự")
	}
	assets := cfg.Assets
	if assets == nil {
		assets = frontend.FS
	}
	if _, err := fs.Stat(assets, "index.html"); err != nil {
		if sub, subErr := fs.Sub(assets, "dist"); subErr == nil {
			assets = sub
		}
	}
	rawIndex, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, fmt.Errorf("không đọc được index.html: %w", err)
	}
	indexHTML, err := transformIndexHTML(string(rawIndex))
	if err != nil {
		return nil, err
	}

	listenAddr := cfg.ListenAddr
	if listenAddr == "" {
		listenAddr = "127.0.0.1:8080"
	}
	loopback, port := parseListenAddr(listenAddr)

	tempDir := cfg.TempDir
	ownsTempDir := false
	if tempDir == "" {
		tempDir, err = os.MkdirTemp("", "s3browser-web-*")
		if err != nil {
			return nil, fmt.Errorf("không tạo được thư mục lưu tạm: %w", err)
		}
		ownsTempDir = true
	} else if err := os.MkdirAll(tempDir, 0700); err != nil {
		return nil, fmt.Errorf("không tạo được thư mục lưu tạm: %w", err)
	}

	loginDelay := cfg.LoginDelay
	if loginDelay == 0 {
		loginDelay = 500 * time.Millisecond
	} else if loginDelay < 0 {
		loginDelay = 0
	}
	pingInterval := cfg.PingInterval
	if pingInterval <= 0 {
		pingInterval = 20 * time.Second
	}

	return &Server{
		service:        cfg.Service,
		svcValue:       reflect.ValueOf(cfg.Service),
		svcType:        reflect.TypeOf(cfg.Service),
		errorType:      reflect.TypeOf((*error)(nil)).Elem(),
		assets:         assets,
		indexHTML:      []byte(indexHTML),
		token:          cfg.Token,
		listenAddr:     listenAddr,
		listenPort:     port,
		loopbackListen: loopback,
		tls:            cfg.TLS,
		tempDir:        tempDir,
		ownsTempDir:    ownsTempDir,
		loginDelay:     loginDelay,
		pingInterval:   pingInterval,
		clients:        map[chan sseMessage]struct{}{},
		closed:         make(chan struct{}),
		cleanupDirs:    map[string]string{},
		completedJobs:  map[string]bool{},
	}, nil
}

func transformIndexHTML(content string) (string, error) {
	const (
		oldScript = `src="./api-shim.js"`
		newScript = `src="./api-web.js"`
		oldCSP    = `form-action 'none'`
		newCSP    = `form-action 'self'`
		oldFrame  = `frame-src 'none'`
		newFrame  = `frame-src 'self'`
	)
	if !strings.Contains(content, oldScript) {
		return "", errors.New("index.html không chứa thẻ nạp ./api-shim.js")
	}
	if !strings.Contains(content, oldCSP) {
		return "", errors.New("index.html không chứa cấu hình CSP form-action 'none'")
	}
	// Tải xuống dùng form ẩn gửi vào iframe ẩn cùng nguồn gốc.
	if !strings.Contains(content, oldFrame) {
		return "", errors.New("index.html không chứa cấu hình CSP frame-src 'none'")
	}
	out := strings.Replace(content, oldScript, newScript, 1)
	out = strings.Replace(out, oldCSP, newCSP, 1)
	out = strings.Replace(out, oldFrame, newFrame, 1)
	return out, nil
}

func parseListenAddr(addr string) (loopback bool, port string) {
	host, p, err := net.SplitHostPort(addr)
	if err != nil {
		return false, ""
	}
	if strings.EqualFold(host, "localhost") {
		return true, p
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback(), p
}

// IsLoopbackAddr kiểm tra địa chỉ nghe có phải loopback hay không.
func IsLoopbackAddr(addr string) bool {
	loopback, _ := parseListenAddr(addr)
	return loopback
}

// HTTPServer tạo http.Server với ReadHeaderTimeout 10 giây và không đặt WriteTimeout.
func (s *Server) HTTPServer() *http.Server {
	return &http.Server{
		Addr:              s.listenAddr,
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// TempDir trả về đường dẫn thư mục lưu tạm của máy chủ.
func (s *Server) TempDir() string {
	return s.tempDir
}

// Close đóng các kết nối sự kiện và xóa toàn bộ thư mục lưu tạm của tiến trình.
func (s *Server) Close() {
	s.closeMu.Do(func() {
		close(s.closed)
		s.sseMu.Lock()
		for ch := range s.clients {
			delete(s.clients, ch)
		}
		s.sseMu.Unlock()
		if s.ownsTempDir || s.tempDir != "" {
			_ = os.RemoveAll(s.tempDir)
		}
	})
}

// PublishTransfer phát sự kiện cập nhật truyền tệp qua SSE và dọn thư mục tạm khi hoàn tất.
func (s *Server) PublishTransfer(job transfers.Job) {
	if job.Status == "completed" {
		s.cleanupMu.Lock()
		if dir, ok := s.cleanupDirs[job.ID]; ok {
			delete(s.cleanupDirs, job.ID)
			_ = os.RemoveAll(dir)
			_ = os.Remove(filepath.Dir(dir))
		} else {
			s.completedJobs[job.ID] = true
		}
		s.cleanupMu.Unlock()
	}
	b, err := json.Marshal(job)
	if err == nil {
		s.broadcast("transfers:update", b)
	}
}

// PublishSize phát sự kiện cập nhật dung lượng qua SSE.
func (s *Server) PublishSize(ev sizer.Event) {
	b, err := json.Marshal(ev)
	if err == nil {
		s.broadcast("size:update", b)
	}
}

func (s *Server) broadcast(event string, data []byte) {
	msg := sseMessage{event: event, data: data}
	s.sseMu.Lock()
	defer s.sseMu.Unlock()
	for ch := range s.clients {
		select {
		case ch <- msg:
		default:
			// Bỏ qua gói khi trình duyệt đọc chậm để không chặn backend.
		}
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cache-Control", "no-store")

	if !s.validHost(r) {
		writeError(w, http.StatusForbidden, "Forbidden", "Tiêu đề Host không hợp lệ.")
		return
	}
	if r.Method == http.MethodPost {
		if origin := r.Header.Get("Origin"); origin != "" {
			scheme := "http"
			if s.tls || r.TLS != nil {
				scheme = "https"
			}
			if origin != scheme+"://"+r.Host {
				writeError(w, http.StatusForbidden, "Forbidden", "Nguồn gốc yêu cầu (Origin) không hợp lệ.")
				return
			}
		}
	}

	if r.Method == http.MethodGet && r.URL.Path == "/" && r.URL.Query().Has("token") {
		if s.validToken(r.URL.Query().Get("token")) {
			s.setSessionCookie(w, r)
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		s.renderLogin(w, http.StatusUnauthorized, "Token xác thực không đúng.")
		return
	}

	if r.URL.Path == "/login" {
		if r.Method != http.MethodPost {
			s.renderLogin(w, http.StatusOK, "")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		_ = r.ParseForm()
		candidate := r.PostFormValue("token")
		if s.validToken(candidate) || s.validToken(strings.TrimSpace(candidate)) {
			s.setSessionCookie(w, r)
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if s.loginDelay > 0 {
			select {
			case <-time.After(s.loginDelay):
			case <-r.Context().Done():
				return
			}
		}
		s.renderLogin(w, http.StatusUnauthorized, "Token xác thực không đúng.")
		return
	}

	if !s.authenticated(r) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusUnauthorized, "Unauthorized", "Phiên làm việc chưa được xác thực.")
			return
		}
		s.renderLogin(w, http.StatusOK, "")
		return
	}

	switch {
	case strings.HasPrefix(r.URL.Path, "/api/rpc/"):
		s.handleRPC(w, r)
	case r.URL.Path == "/api/events":
		s.handleEvents(w, r)
	case r.URL.Path == "/api/upload":
		s.handleUpload(w, r)
	case r.URL.Path == "/api/download":
		s.handleDownload(w, r)
	case r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/"):
		writeError(w, http.StatusNotFound, "NotFound", "Điểm cuối API không tồn tại.")
	default:
		s.handleStatic(w, r)
	}
}

func (s *Server) validHost(r *http.Request) bool {
	if r.Host == "" {
		return false
	}
	if !s.loopbackListen {
		return true
	}
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		return false
	}
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return false
	}
	if host == "::1" && !strings.HasPrefix(r.Host, "[::1]:") {
		return false
	}
	var localPort string
	if addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok && addr != nil {
		_, localPort, _ = net.SplitHostPort(addr.String())
	}
	if s.listenPort != "" && s.listenPort != "0" && port == s.listenPort {
		return true
	}
	if localPort != "" && port == localPort {
		return true
	}
	return false
}

func (s *Server) validToken(candidate string) bool {
	if candidate == "" {
		return false
	}
	a := sha256.Sum256([]byte(candidate))
	b := sha256.Sum256([]byte(s.token))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1 && subtle.ConstantTimeCompare([]byte(candidate), []byte(s.token)) == 1
}

func (s *Server) authenticated(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	return s.validToken(c.Value)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    s.token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.tls || r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) renderLogin(w http.ResponseWriter, status int, errMsg string) {
	const loginCSP = "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
	w.Header().Set("Content-Security-Policy", loginCSP)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	var errBlock string
	if errMsg != "" {
		errBlock = `<p class="error" role="alert">` + html.EscapeString(errMsg) + `</p>`
	}
	page := `<!doctype html>
<html lang="vi">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta http-equiv="Content-Security-Policy" content="` + loginCSP + `">
  <title>Đăng nhập - S3 Browser Lite</title>
  <style>
    body { font-family: system-ui, -apple-system, sans-serif; background: #0f172a; color: #f8fafc; display: flex; align-items: center; justify-content: center; min-height: 100vh; margin: 0; }
    main { background: #1e293b; padding: 2rem; border-radius: 0.5rem; width: 100%; max-width: 24rem; box-sizing: border-box; box-shadow: 0 10px 25px rgba(0,0,0,0.4); }
    h1 { margin: 0 0 1rem; font-size: 1.25rem; }
    label { display: block; margin-bottom: 0.5rem; font-size: 0.9rem; }
    input { width: 100%; padding: 0.6rem; border: 1px solid #475569; border-radius: 0.375rem; background: #0f172a; color: #f8fafc; box-sizing: border-box; margin-bottom: 1rem; }
    button { width: 100%; padding: 0.65rem; border: none; border-radius: 0.375rem; background: #2563eb; color: #ffffff; font-weight: 600; cursor: pointer; }
    button:hover { background: #1d4ed8; }
    .error { color: #f87171; margin: 0 0 1rem; font-size: 0.9rem; }
  </style>
</head>
<body>
  <main>
    <h1>S3 Browser Lite</h1>
    ` + errBlock + `
    <form method="post" action="/login">
      <label for="token">Nhập mã xác thực (token)</label>
      <input id="token" name="token" type="password" required autofocus autocomplete="current-password">
      <button type="submit">Đăng nhập</button>
    </form>
  </main>
</body>
</html>`
	_, _ = io.WriteString(w, page)
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "Phương thức HTTP không được hỗ trợ.")
		return
	}
	if r.URL.Path == "/api-shim.js" {
		writeError(w, http.StatusNotFound, "NotFound", "Không tìm thấy tài nguyên yêu cầu.")
		return
	}
	if r.URL.Path == "/" || r.URL.Path == "/index.html" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(s.indexHTML)
		}
		return
	}
	clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if clean == "" || clean == "." || strings.Contains(clean, "..") {
		writeError(w, http.StatusNotFound, "NotFound", "Không tìm thấy tài nguyên yêu cầu.")
		return
	}
	f, err := s.assets.Open(clean)
	if err != nil {
		writeError(w, http.StatusNotFound, "NotFound", "Không tìm thấy tài nguyên yêu cầu.")
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || stat.IsDir() {
		writeError(w, http.StatusNotFound, "NotFound", "Không tìm thấy tài nguyên yêu cầu.")
		return
	}
	w.Header().Set("Content-Type", contentTypeByExt(path.Ext(clean)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = io.Copy(w, f)
	}
}

func contentTypeByExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "Phương thức HTTP không được hỗ trợ.")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		writeError(w, http.StatusBadRequest, "InvalidContentType", "Yêu cầu RPC bắt buộc dùng Content-Type: application/json.")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/rpc/")
	if name == "" || strings.Contains(name, "/") {
		writeError(w, http.StatusNotFound, "MethodNotFound", "Phương thức RPC không tồn tại.")
		return
	}
	methodMeta, ok := s.svcType.MethodByName(name)
	if !ok || methodMeta.Type.NumOut() != 2 || methodMeta.Type.Out(1) != s.errorType {
		writeError(w, http.StatusNotFound, "MethodNotFound", "Phương thức RPC không tồn tại.")
		return
	}
	method := s.svcValue.Method(methodMeta.Index)
	mType := method.Type()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRPCBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidArguments", "Không đọc được thân yêu cầu RPC hoặc vượt quá 4 MiB.")
		return
	}
	var rawArgs []json.RawMessage
	if json.Unmarshal(body, &rawArgs) != nil || rawArgs == nil || len(rawArgs) != mType.NumIn() {
		writeError(w, http.StatusBadRequest, "InvalidArguments", "Tham số RPC phải là mảng JSON đúng số lượng.")
		return
	}

	in := make([]reflect.Value, mType.NumIn())
	for i := 0; i < mType.NumIn(); i++ {
		trimmed := bytes.TrimSpace(rawArgs[i])
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || (mType.In(i).Kind() == reflect.Struct && trimmed[0] != '{') {
			writeError(w, http.StatusBadRequest, "InvalidArguments", "Tham số RPC không hợp lệ.")
			return
		}
		argPtr := reflect.New(mType.In(i))
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.DisallowUnknownFields()
		if err := dec.Decode(argPtr.Interface()); err != nil {
			var appErr *apperr.Error
			if errors.As(err, &appErr) {
				writeAppError(w, http.StatusBadRequest, appErr)
				return
			}
			writeError(w, http.StatusBadRequest, "InvalidArguments", "Kiểu tham số RPC không hợp lệ.")
			return
		}
		if dec.More() {
			writeError(w, http.StatusBadRequest, "InvalidArguments", "Kiểu tham số RPC không hợp lệ.")
			return
		}
		in[i] = argPtr.Elem()
	}

	out := method.Call(in)
	if !out[1].IsNil() {
		callErr := out[1].Interface().(error)
		writeAppError(w, http.StatusBadRequest, s.describe(callErr))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": out[0].Interface()})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "Phương thức HTTP không được hỗ trợ.")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "StreamingUnsupported", "Máy chủ không hỗ trợ phát luồng sự kiện.")
		return
	}
	ch := make(chan sseMessage, sseBufferLen)
	s.sseMu.Lock()
	select {
	case <-s.closed:
		s.sseMu.Unlock()
		return
	default:
		s.clients[ch] = struct{}{}
	}
	s.sseMu.Unlock()
	defer func() {
		s.sseMu.Lock()
		delete(s.clients, ch)
		s.sseMu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(s.pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.closed:
			return
		case msg := <-ch:
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", msg.event, msg.data)
			flusher.Flush()
		case <-ticker.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "Phương thức HTTP không được hỗ trợ.")
		return
	}
	if s.service.Backend.Err != nil {
		writeAppError(w, http.StatusBadRequest, s.describe(s.service.Backend.Err))
		return
	}
	q := r.URL.Query()
	profileID := q.Get("profile")
	bucket := q.Get("bucket")
	prefix := q.Get("prefix")
	if err := validate.First(validate.ID(profileID), validate.Bucket(bucket), validate.Prefix(prefix)); err != nil {
		writeAppError(w, http.StatusBadRequest, s.describe(err))
		return
	}
	done, err := s.service.Backend.Begin(profileID)
	if err != nil {
		writeAppError(w, http.StatusBadRequest, s.describe(err))
		return
	}
	defer done()

	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidUpload", "Yêu cầu tải lên phải dùng multipart/form-data.")
		return
	}

	batchDir := filepath.Join(s.tempDir, uuid.NewString())
	queued := false
	defer func() {
		if !queued {
			_ = os.RemoveAll(batchDir)
		}
	}()

	type rootEntry struct {
		name     string
		slotDir  string
		rootPath string
		isDir    bool
	}
	var roots []*rootEntry
	rootIndex := map[string]*rootEntry{}
	fileCount := 0

	for {
		if err := r.Context().Err(); err != nil {
			return
		}
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "InvalidUpload", "Không đọc được dữ liệu multipart tải lên.")
			return
		}
		if part.FormName() != "path" {
			_ = part.Close()
			writeError(w, http.StatusBadRequest, "InvalidUpload", "Mỗi mục tải lên phải bắt đầu bằng trường path.")
			return
		}
		pathBytes, err := io.ReadAll(io.LimitReader(part, 8192))
		_ = part.Close()
		if err != nil {
			writeError(w, http.StatusBadRequest, "InvalidUpload", "Không đọc được đường dẫn tệp tải lên.")
			return
		}
		parts, err := validateUploadPath(string(pathBytes))
		if err != nil {
			writeAppError(w, http.StatusBadRequest, s.describe(err))
			return
		}
		fileCount++
		if fileCount > maxUploadCnt {
			writeError(w, http.StatusBadRequest, "InvalidArgument", "Mỗi yêu cầu chỉ hỗ trợ tối đa 10000 tệp.")
			return
		}

		filePart, err := mr.NextPart()
		if err != nil || filePart.FormName() != "file" {
			if filePart != nil {
				_ = filePart.Close()
			}
			writeError(w, http.StatusBadRequest, "InvalidUpload", "Thiếu phần dữ liệu file tương ứng với path.")
			return
		}

		rootName := parts[0]
		isDir := len(parts) > 1
		entry, exists := rootIndex[rootName]
		if !exists {
			slotDir := filepath.Join(batchDir, strconv.Itoa(len(roots)))
			entry = &rootEntry{
				name:     rootName,
				slotDir:  slotDir,
				rootPath: filepath.Join(slotDir, rootName),
				isDir:    isDir,
			}
			roots = append(roots, entry)
			rootIndex[rootName] = entry
		} else if entry.isDir != isDir || !isDir {
			_ = filePart.Close()
			writeError(w, http.StatusBadRequest, "InvalidUpload", "Đường dẫn tải lên bị trùng hoặc xung đột giữa tệp và thư mục.")
			return
		}

		targetPath := filepath.Join(append([]string{entry.slotDir}, parts...)...)
		rel, relErr := filepath.Rel(entry.slotDir, targetPath)
		if relErr != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			_ = filePart.Close()
			writeError(w, http.StatusBadRequest, "UnsafePath", "Đường dẫn tải lên không an toàn.")
			return
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0700); err != nil {
			_ = filePart.Close()
			writeError(w, http.StatusBadRequest, "InvalidUpload", "Không tạo được thư mục lưu tạm.")
			return
		}
		f, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			_ = filePart.Close()
			writeError(w, http.StatusBadRequest, "InvalidUpload", "Không tạo được tệp lưu tạm hoặc đường dẫn bị trùng.")
			return
		}
		_, copyErr := io.Copy(f, filePart)
		_ = filePart.Close()
		closeErr := f.Close()
		if copyErr != nil || closeErr != nil {
			writeError(w, http.StatusBadRequest, "InvalidUpload", "Không ghi được tệp lưu tạm.")
			return
		}
	}

	if err := r.Context().Err(); err != nil {
		return
	}
	if fileCount == 0 {
		writeError(w, http.StatusBadRequest, "InvalidArgument", "Không có tệp nào trong yêu cầu tải lên.")
		return
	}

	allJobs := make([]transfers.Job, 0, len(roots))
	for _, entry := range roots {
		jobs, err := s.service.Backend.Transfers.Upload(profileID, bucket, prefix, []string{entry.rootPath})
		if err != nil {
			// Xóa các thư mục chưa được xếp hàng nếu gặp lỗi giữa chừng.
			for _, j := range allJobs {
				_, _ = s.service.Backend.Transfers.Cancel(j.ID)
			}
			writeAppError(w, http.StatusBadRequest, s.describe(err))
			return
		}
		s.cleanupMu.Lock()
		for _, j := range jobs {
			if s.completedJobs[j.ID] {
				delete(s.completedJobs, j.ID)
				_ = os.RemoveAll(entry.slotDir)
				_ = os.Remove(batchDir)
			} else {
				s.cleanupDirs[j.ID] = entry.slotDir
			}
		}
		s.cleanupMu.Unlock()
		allJobs = append(allJobs, jobs...)
	}
	queued = true
	writeJSON(w, http.StatusOK, map[string]any{"result": allJobs})
}

func validateUploadPath(p string) ([]string, error) {
	if p == "" || validate.Length(p) > 1024 {
		return nil, apperr.New("InvalidArgument", "Đường dẫn tải lên không hợp lệ.")
	}
	if strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") || strings.Contains(p, `\`) || filepath.IsAbs(p) || filepath.VolumeName(p) != "" {
		return nil, apperr.New("UnsafePath", "Đường dẫn tải lên không được tuyệt đối hoặc chứa dấu gạch chéo ngược.")
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return nil, apperr.New("UnsafePath", "Đường dẫn tải lên chứa ký tự điều khiển không hợp lệ.")
		}
	}
	parts := strings.Split(p, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, `<>:"|?*`) {
			return nil, apperr.New("UnsafePath", "Phần tử đường dẫn tải lên không hợp lệ.")
		}
	}
	return parts, nil
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	if s.service.Backend.Err != nil {
		writeAppError(w, http.StatusBadRequest, s.describe(s.service.Backend.Err))
		return
	}
	var (
		profileID string
		bucket    string
		keys      []string
	)
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		profileID = q.Get("profile")
		bucket = q.Get("bucket")
		key := q.Get("key")
		if strings.HasSuffix(key, "/") {
			writeError(w, http.StatusBadRequest, "InvalidArgument", "Tải xuống GET chỉ hỗ trợ khóa tệp đơn không kết thúc bằng dấu /.")
			return
		}
		keys = []string{key}
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 16*1024*1024)
		if err := r.ParseForm(); err != nil {
			writeError(w, http.StatusBadRequest, "InvalidArgument", "Không đọc được biểu mẫu tải xuống.")
			return
		}
		profileID = r.FormValue("profile")
		bucket = r.FormValue("bucket")
		rawKeys := r.FormValue("keys")
		if json.Unmarshal([]byte(rawKeys), &keys) != nil || len(keys) == 0 || len(keys) > 10000 {
			writeError(w, http.StatusBadRequest, "InvalidArgument", "Danh sách khóa tải xuống phải là mảng JSON từ 1 đến 10000 khóa.")
			return
		}
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "Phương thức HTTP không được hỗ trợ.")
		return
	}

	if err := validate.First(validate.ID(profileID), validate.Bucket(bucket), validate.Strings(keys, 10000, validateDownloadKey)); err != nil {
		writeAppError(w, http.StatusBadRequest, s.describe(err))
		return
	}

	done, err := s.service.Backend.Begin(profileID)
	if err != nil {
		writeAppError(w, http.StatusBadRequest, s.describe(err))
		return
	}
	defer done()

	if len(keys) == 1 && !strings.HasSuffix(keys[0], "/") {
		s.serveSingleDownload(w, r, profileID, bucket, keys[0])
		return
	}
	s.serveZipDownload(w, r, profileID, bucket, keys)
}

func validateDownloadKey(key string) error {
	if err := validate.Key(key); err != nil {
		return err
	}
	if strings.HasPrefix(key, "/") {
		return apperr.New("UnsafePath", "Khóa đối tượng không được bắt đầu bằng dấu /.")
	}
	trimmed := strings.TrimSuffix(key, "/")
	if trimmed == "" {
		return apperr.New("UnsafePath", "Khóa đối tượng không hợp lệ.")
	}
	for _, part := range strings.Split(trimmed, "/") {
		if part == "" || part == "." || part == ".." {
			return apperr.New("UnsafePath", "Khóa đối tượng chứa phần tử đường dẫn không an toàn.")
		}
	}
	return nil
}

func (s *Server) serveSingleDownload(w http.ResponseWriter, r *http.Request, profileID, bucket, key string) {
	dl, err := s.service.Backend.S3.Get(r.Context(), profileID, bucket, key)
	if err != nil {
		appErr := s.describe(err)
		writeAppError(w, statusForError(appErr), appErr)
		return
	}
	if dl.Body != nil {
		defer dl.Body.Close()
	}
	ct := dl.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", contentDisposition(path.Base(key)))
	if dl.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(dl.Size, 10))
	}
	w.WriteHeader(http.StatusOK)
	if dl.Body != nil {
		_, _ = io.Copy(w, dl.Body)
	}
}

func (s *Server) serveZipDownload(w http.ResponseWriter, r *http.Request, profileID, bucket string, keys []string) {
	if _, err := s.service.Backend.S3.Client(profileID); err != nil {
		appErr := s.describe(err)
		writeAppError(w, statusForError(appErr), appErr)
		return
	}

	zipName := bucket + ".zip"
	if len(keys) == 1 && strings.HasSuffix(keys[0], "/") {
		base := path.Base(strings.TrimSuffix(keys[0], "/"))
		if base != "" && base != "." && base != "/" {
			zipName = base + ".zip"
		}
	}

	var (
		started bool
		zw      *zip.Writer
	)
	startStream := func() {
		if started {
			return
		}
		started = true
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", contentDisposition(zipName))
		w.WriteHeader(http.StatusOK)
		zw = zip.NewWriter(w)
	}

	seen := map[string]bool{}
	writeObject := func(objKey string) error {
		if err := r.Context().Err(); err != nil {
			return err
		}
		if err := validateDownloadKey(objKey); err != nil {
			return err
		}
		if seen[objKey] {
			return nil
		}
		seen[objKey] = true
		if strings.HasSuffix(objKey, "/") {
			startStream()
			_, err := zw.CreateHeader(&zip.FileHeader{Name: objKey, Method: zip.Store})
			return err
		}
		dl, err := s.service.Backend.S3.Get(r.Context(), profileID, bucket, objKey)
		if err != nil {
			return err
		}
		if dl.Body != nil {
			defer dl.Body.Close()
		}
		startStream()
		entry, err := zw.CreateHeader(&zip.FileHeader{Name: objKey, Method: zip.Store})
		if err != nil {
			return err
		}
		if dl.Body != nil {
			_, err = io.Copy(entry, dl.Body)
		}
		return err
	}

	for _, key := range keys {
		if err := r.Context().Err(); err != nil {
			if started {
				panic(http.ErrAbortHandler)
			}
			return
		}
		var stepErr error
		if strings.HasSuffix(key, "/") {
			stepErr = s.service.Backend.S3.Walk(r.Context(), profileID, bucket, key, func(o s3svc.Object) error {
				return writeObject(o.Key)
			})
		} else {
			stepErr = writeObject(key)
		}
		if stepErr != nil {
			if !started {
				appErr := s.describe(stepErr)
				writeAppError(w, statusForError(appErr), appErr)
				return
			}
			panic(http.ErrAbortHandler)
		}
	}

	startStream()
	if err := zw.Close(); err != nil {
		panic(http.ErrAbortHandler)
	}
}

func contentDisposition(name string) string {
	if name == "" || name == "." || name == "/" {
		name = "download"
	}
	var ascii strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 0x20 && c < 0x7f && c != '"' && c != '\\' && c != ';' {
			ascii.WriteByte(c)
		} else {
			ascii.WriteByte('_')
		}
	}
	safeASCII := ascii.String()
	if safeASCII == "" {
		safeASCII = "download"
	}
	encoded := strings.ReplaceAll(url.QueryEscape(name), "+", "%20")
	return fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", safeASCII, encoded)
}

func statusForError(err *apperr.Error) int {
	if err == nil {
		return http.StatusBadRequest
	}
	switch err.Code {
	case "NoSuchKey", "NoSuchBucket", "NotFound", "ProfileNotFound":
		return http.StatusNotFound
	default:
		return http.StatusBadRequest
	}
}

// describe luôn đi qua Filter, giống call của bản desktop, để che khóa truy cập trong mọi lỗi.
func (s *Server) describe(err error) *apperr.Error {
	return s.service.Backend.Filter.Describe(err)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeAppError(w, status, &apperr.Error{Code: code, Message: message})
}

func writeAppError(w http.ResponseWriter, status int, err *apperr.Error) {
	writeJSON(w, status, err)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
