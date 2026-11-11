# S3 Browser Lite

S3 Browser Lite là ứng dụng máy tính để bàn và máy chủ web tự chứa giúp quản lý kho lưu trữ AWS S3 và các dịch vụ S3 tương thích (MinIO, Ceph RGW...). Dự án được chuyển đổi từ bản Electron sang kiến trúc Go và Wails v2, giữ nguyên toàn bộ giao diện, tính năng và các bất biến bảo mật của bản gốc nhưng không đóng gói kèm nhân Chromium và Node.js.

## So sánh với bản Electron (kích thước và tài nguyên)

Bản này dùng WebView2 có sẵn trên Windows thay vì đóng gói kèm Chromium và Node.js, nên tệp phân phối nhỏ hơn nhiều. Lợi ích về bộ nhớ chạy thì không có (xem số đo bên dưới): WebView2 vẫn chạy nhiều tiến trình nên RAM tương đương bản Electron.

### Số đo (một lần đo, Windows 11, sau khoảng 8 giây khởi động, chưa mở hồ sơ nào)

| Tiêu chí | Bản Electron | Bản S3 Browser Lite (Go + Wails) |
| --- | --- | --- |
| Tệp phân phối | 105,5 MB (portable) | 17,7 MB (`S3BrowserLite.exe`) |
| Số tiến trình | 4 | 7 (gồm các tiến trình WebView2) |
| Working set của cả cây tiến trình | khoảng 309 MB | khoảng 338 MB |
| Private memory của cả cây tiến trình | khoảng 165 MB | khoảng 212 MB |
| Thời gian khởi động lạnh | chưa đo | chưa đo |

Working set tính trùng các trang bộ nhớ dùng chung giữa các tiến trình, nên số đo chỉ mang tính tham khảo.

## Tính năng chính

- Quản lý hồ sơ kết nối: thêm, sửa, xóa, nhân bản, kiểm tra kết nối, nhập và xuất danh sách hồ sơ định dạng JSON.
- Hỗ trợ S3 tương thích: cấu hình endpoint tùy chỉnh, vùng (region), Path-Style hoặc Virtual-Hosted-Style, tự động tương thích với MinIO cũ, Ceph RGW và ECS nhờ chế độ tính checksum khi bắt buộc (`WhenRequired`).
- Duyệt đa tab: mở nhiều tab làm việc đồng thời trên các hồ sơ và bucket khác nhau.
- Quản lý bucket và đối tượng: liệt kê và tạo bucket (kiểm tra quy tắc tên 3-63 ký tự và `LocationConstraint`), đọc/ghi/xóa bucket policy, duyệt thư mục phân trang theo token, tính dung lượng thư mục/bucket, xem thông tin chi tiết (head metadata), tạo thư mục, đổi tên, di chuyển, xóa lô tối đa 1000 khóa và xóa theo tiền tố.
- Truyền tệp tin: tải lên (multipart tự động, hỗ trợ chọn tệp/thư mục hoặc kéo thả trực tiếp vào giao diện), tải xuống an toàn qua tệp tạm, sao chép đối tượng trực tiếp giữa các hồ sơ/endpoint khác nhau, điều chỉnh độ đồng thời, hủy và thử lại tác vụ trong hàng đợi.
- Tạo liên kết tạm thời (Presigned URL) và sao chép nhanh vào clipboard.

## Yêu cầu hệ thống

- Bản desktop: Windows 10 hoặc Windows 11 (x64, có Microsoft Edge WebView2 Runtime), hoặc macOS (bản universal, build qua GitHub Actions; chưa ký và chưa notarize nên lần đầu mở phải chuột phải chọn Open).
- Bản web (`cmd/s3browser-web`): Windows, Linux, macOS (amd64 và arm64), không phụ thuộc Wails hay cgo, dùng với trình duyệt web hiện đại.

## Cài đặt công cụ phát triển (Toolchain)

1. Cài đặt Go 1.27 (x64 cho Windows).
2. Cài đặt Wails CLI v2 (phiên bản dự kiến v2.17, cần xác minh phiên bản phát hành trong môi trường của bạn):

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

Lưu ý: Nếu làm việc trong môi trường không có mạng, cần đảm bảo tệp thực thi `wails.exe` đã có sẵn trong `PATH` (ví dụ `C:\Users\<tên>\go\bin`) và sử dụng các gói đã được vendor sẵn trong thư mục `vendor/`.

## Build ứng dụng (bản desktop)

Mọi phụ thuộc Go đã nằm trong `vendor/`, giao diện là HTML/CSS/JS thuần trong `frontend/dist/` nên không cần Node.js hay npm.

Thiết lập biến môi trường (nếu build ngoại tuyến trong thư mục dự án) và chạy lệnh build:

```powershell
$env:GOFLAGS = "-mod=vendor"
$env:GOCACHE = "$PWD\.gocache"
wails build -s
```

Sau khi hoàn tất, tệp thực thi độc lập được tạo tại:

```text
build/bin/S3BrowserLite.exe
```

## Chạy thử (bản desktop)

Dự án không có bước biên dịch hay đóng gói frontend (không dùng bundler), vì vậy không cần chạy `wails dev`. Để chạy thử, hãy build ứng dụng rồi chạy trực tiếp tệp thực thi:

```powershell
wails build -s
.\build\bin\S3BrowserLite.exe
```

## Bản web (`cmd/s3browser-web`)

Bản web là một tệp thực thi Go duy nhất, nhúng sẵn giao diện `frontend/dist/` và dùng chung toàn bộ nghiệp vụ với bản desktop qua gói `internal/service`. Bản web không phụ thuộc Wails và không cần cgo (`CGO_ENABLED=0`).

### Dùng bản dựng sẵn (không cần cài Go)

Tải tệp phù hợp ở trang Releases, giải nén rồi chạy; trình duyệt tự mở và đã đăng nhập sẵn bằng token của lần chạy đó:

| Hệ điều hành | Tệp | Cách chạy |
| --- | --- | --- |
| Windows x64 | `s3browser-web-<phiên bản>-windows-amd64.zip` | Bấm đúp `s3browser-web.exe` (SmartScreen: More info, Run anyway). Đóng cửa sổ console để tắt |
| Linux x64 / ARM64 | `s3browser-web-<phiên bản>-linux-amd64.tar.gz` / `-linux-arm64.tar.gz` | `tar xzf <tệp> && ./s3browser-web` (máy chủ không có giao diện: xem mục bảo mật, dùng đường hầm SSH hoặc `-listen`) |
| macOS Intel / Apple Silicon | `s3browser-web-<phiên bản>-darwin-amd64.tar.gz` / `-darwin-arm64.tar.gz` | `tar xzf <tệp> && xattr -d com.apple.quarantine s3browser-web && ./s3browser-web` |

### Chạy bản web từ mã nguồn

```powershell
$env:GOFLAGS = "-mod=vendor"
$env:GOCACHE = "$PWD\.gocache"
$env:CGO_ENABLED = "0"
go run ./cmd/s3browser-web
```

Khi khởi động, máy chủ in địa chỉ nghe và đường dẫn đăng nhập nhanh kèm token trên một dòng:

```text
Đang nghe tại: 127.0.0.1:8080
Mở: http://127.0.0.1:8080/?token=<token>
```

Mở đường dẫn trên trong trình duyệt để máy chủ đặt cookie phiên (`s3b_session`) và chuyển hướng về `/`. Khi tắt tiến trình bằng `SIGINT`/`SIGTERM`, máy chủ tự đóng kết nối S3, dừng hàng đợi và xóa sạch thư mục lưu tạm.

### Tham số dòng lệnh và biến môi trường

| Cờ | Mặc định | Ý nghĩa |
| --- | --- | --- |
| `-listen` | `127.0.0.1:8080` | Địa chỉ và cổng lắng nghe HTTP/HTTPS |
| `-token` | rỗng | Token xác thực cố định (dài ít nhất 16 ký tự). Nếu để trống, đọc từ biến môi trường `S3BROWSER_WEB_TOKEN` (cờ `-token` được ưu tiên hơn); nếu vẫn trống thì sinh ngẫu nhiên 32 byte (`base64url`) mỗi lần chạy |
| `-tls-cert` | rỗng | Đường dẫn tệp chứng chỉ TLS (cần truyền cùng `-tls-key` để bật HTTPS) |
| `-tls-key` | rỗng | Đường dẫn tệp khóa riêng TLS (cần truyền cùng `-tls-cert` để bật HTTPS) |
| `-no-browser` | tắt | Không tự mở trình duyệt. Mặc định máy chủ tự mở trình duyệt mặc định khi nghe loopback; nghe địa chỉ khác thì không mở |

### Bảo mật bản web

- Luôn bắt buộc xác thực bằng token (so sánh hằng thời gian bằng `crypto/subtle`), không có chế độ tắt xác thực.
- Cookie phiên `s3b_session` bật `HttpOnly`, `SameSite=Strict`, `Path=/`, và thêm `Secure` khi bật TLS.
- Chống DNS rebinding: khi nghe trên địa chỉ loopback (`127.0.0.1`, `localhost`, `[::1]`), máy chủ chỉ chấp nhận tiêu đề `Host` là `localhost`, `127.0.0.1` hoặc `[::1]` kèm đúng cổng đang nghe; sai trả `403`.
- Chống CSRF: mọi yêu cầu `POST` có tiêu đề `Origin` đều bắt buộc `Origin` khớp đúng `scheme://Host`; sai trả `403`.
- Nếu cấu hình `-listen` ra địa chỉ ngoài loopback (ví dụ `0.0.0.0:8080`), máy chủ in cảnh báo khuyến nghị bật TLS (`-tls-cert`, `-tls-key`) hoặc đặt sau reverse proxy có HTTPS.

### Khác biệt so với bản desktop

- Tải lên: trình duyệt gửi tệp hoặc thư mục qua `POST /api/upload` (`multipart/form-data`), máy chủ lưu tạm trên đĩa rồi đưa vào hàng đợi truyền tệp chuẩn để giữ nguyên tiến độ, giới hạn đồng thời, hủy và thử lại. Khi tác vụ hoàn tất (`completed`), thư mục tạm của tác vụ đó được xóa ngay; nếu lỗi hoặc hủy thì giữ lại để có thể thử lại cho tới khi tắt máy chủ.
- Tải xuống: không có hộp thoại chọn thư mục hệ thống của máy chủ và không đi qua hàng đợi truyền tệp. Máy chủ phát luồng trực tiếp từ S3 về trình duyệt qua cơ chế tải xuống của trình duyệt (một tệp thì trả nguyên tệp; nhiều tệp hoặc thư mục thì đóng gói luồng `zip.Store` không nén).
- Hồ sơ kết nối dùng chung tệp `profiles.json` trong `os.UserConfigDir()/S3BrowserLite` với bản desktop.

### Khóa mã hóa hồ sơ trên Linux và giới hạn

Trên Linux (và các nền tảng ngoài Windows/macOS), `secretAccessKey` trong `profiles.json` được mã hóa bằng AES-256-GCM với khóa chủ lấy theo thứ tự ưu tiên:

1. Biến môi trường `S3BROWSER_MASTER_KEY` (chuỗi base64 chuẩn của đúng 32 byte) nếu được đặt; nếu sai định dạng, ứng dụng báo lỗi rõ ràng và từ chối lưu.
2. Tệp khóa `os.UserConfigDir()/S3BrowserLite/profiles.key` (32 byte ngẫu nhiên, quyền tệp `0600`, thư mục `0700`), chỉ được tự động tạo khi mã hóa lần đầu (thao tác chỉ đọc không tạo tệp khóa).

Giới hạn bảo mật cần lưu ý: khi dùng tệp `profiles.key`, khóa nằm cạnh dữ liệu cấu hình nên chỉ chống lộ vô tình (ví dụ khi sao lưu hoặc chép nhầm tệp `profiles.json`), không chống được kẻ tấn công đã có quyền đọc thư mục cấu hình của người dùng. Nếu cần mức bảo vệ mạnh hơn trên Linux, hãy nạp khóa chủ qua biến môi trường `S3BROWSER_MASTER_KEY` từ kho bí mật bên ngoài.

### Build chéo bản web

Vì không dùng cgo, có thể biên dịch chéo bản web cho mọi hệ điều hành và kiến trúc từ một máy duy nhất:

```powershell
$env:GOFLAGS = "-mod=vendor"
$env:GOCACHE = "$PWD\.gocache"
$env:CGO_ENABLED = "0"

# Linux (amd64 / arm64)
$env:GOOS = "linux"; $env:GOARCH = "amd64"; go build -o build/web/s3browser-web-linux-amd64 ./cmd/s3browser-web
$env:GOOS = "linux"; $env:GOARCH = "arm64"; go build -o build/web/s3browser-web-linux-arm64 ./cmd/s3browser-web

# macOS (amd64 / arm64)
$env:GOOS = "darwin"; $env:GOARCH = "amd64"; go build -o build/web/s3browser-web-darwin-amd64 ./cmd/s3browser-web
$env:GOOS = "darwin"; $env:GOARCH = "arm64"; go build -o build/web/s3browser-web-darwin-arm64 ./cmd/s3browser-web

# Windows (amd64)
$env:GOOS = "windows"; $env:GOARCH = "amd64"; go build -o build/web/s3browser-web-windows-amd64.exe ./cmd/s3browser-web
```

## Kiểm thử

Toàn bộ kiểm thử đơn vị và tích hợp sử dụng giả lập nội bộ (interface hoặc `net/http/httptest`), không gọi ra mạng thật:

```powershell
$env:GOFLAGS = "-mod=vendor"
$env:GOCACHE = "$PWD\.gocache"
go vet ./...
go test ./...
```

## Cấu trúc thư mục

```text
main.go                      Khởi tạo Wails, bind App, bật kéo thả tệp (EnableFileDrop)
app.go                       Struct App bản desktop (nhúng internal/service, thêm hộp thoại và clipboard Wails)
cmd/s3browser-web/           Điểm vào tệp thực thi bản web độc lập (không phụ thuộc Wails/cgo)
frontend/                    Gói nhúng giao diện tĩnh (embed.go) và mã nguồn HTML/CSS/JS trong frontend/dist/
internal/service/            Phương thức nghiệp vụ dùng chung giữa bản desktop (Wails) và bản web (RPC)
internal/webserver/          Máy chủ HTTP/HTTPS, xác thực token/cookie, RPC phản chiếu, SSE, upload/download web
internal/backend/            Điều phối khóa hồ sơ, trạng thái dùng chung giữa S3, hàng đợi và bộ tính dung lượng
internal/profiles/           Lưu trữ hồ sơ, mã hóa khóa bí mật (Windows DPAPI, macOS Keychain, Linux AES-GCM)
internal/s3svc/              Tạo và cache S3 client theo hồ sơ, thực thi nghiệp vụ S3
internal/transfers/          Hàng đợi tải lên, tải xuống, sao chép và phát sự kiện tiến độ
internal/sizer/              Tính toán và đệm dung lượng bucket/thư mục bất đồng bộ
internal/dialogs/            Hộp thoại chọn tệp và thư mục tải lên cho bản desktop
internal/validate/           Kiểm tra tham số đầu vào (UUID, tên bucket, khóa, tiền tố)
internal/apperr/             Chuẩn hóa mã lỗi, lọc bí mật và trả thông điệp tiếng Việt
internal/testutil/           Tiện ích và dữ liệu giả lập phục vụ kiểm thử
reference/electron/          Mã nguồn và đặc tả bản Electron dùng để đối chiếu (chỉ đọc)
vendor/                      Các gói phụ thuộc Go đã vendor
```

## Bảo mật

- Bảo vệ khóa bí mật bằng DPAPI (Windows), Keychain (macOS, AES-256-GCM với khóa chủ nằm trong Keychain đăng nhập, mục `S3BrowserLite`/`profiles-key`, hồ sơ lưu ở `~/Library/Application Support/S3BrowserLite/profiles.json`), hoặc AES-256-GCM trên Linux (`S3BROWSER_MASTER_KEY` hoặc `profiles.key`). Trên Windows, `secretAccessKey` được mã hóa bằng Windows DPAPI (`CryptProtectData`, phạm vi người dùng hiện tại) trước khi lưu dạng base64 vào `%APPDATA%\S3BrowserLite\profiles.json`. Nếu kho mã hóa gặp lỗi, ứng dụng từ chối lưu và báo lỗi rõ ràng, tuyệt đối không lưu dạng văn bản rõ.
- Không rò rỉ bí mật: Tầng giao diện (renderer) không bao giờ nhận `secretAccessKey` mà chỉ nhận cờ `hasSecret`. Khi xuất hồ sơ ra JSON hoặc nhập từ JSON, khóa bí mật luôn bị loại bỏ. Mọi thông báo lỗi và nhật ký đều lọc bỏ `accessKeyId` và `secretAccessKey` (kể cả dạng đã mã hóa URL).
- Cô lập thông tin xác thực: `aws.Config` được dựng thủ công với khóa tĩnh của từng hồ sơ, không tự động đọc biến môi trường, tệp `~/.aws` hay IMDS.
- Cô lập cấu hình TLS lỏng: Tùy chọn `allowInsecureTls` chỉ gắn `InsecureSkipVerify` vào `http.Client` riêng của đúng hồ sơ đó, không làm thay đổi `http.DefaultTransport` của toàn ứng dụng.
- An toàn đường dẫn tải xuống và tải lên: Kiểm tra chống leo thang thư mục (path traversal); trên bản desktop chỉ cho phép ghi tệp tải xuống vào đúng thư mục người dùng đã chọn qua hộp thoại hệ thống.

## Giấy phép

Dự án được phát hành theo giấy phép MIT (cần xác minh tệp `LICENSE` ở thư mục gốc của kho mã nguồn).
