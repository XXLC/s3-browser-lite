# Đặc tả S3 Browser Lite (Go + Wails v2)

Bản nhẹ của S3 Browser OSS. Hành vi, tính năng, bất biến bảo mật GIỐNG HỆT bản Electron; bản Electron nằm trong `reference/electron/` để đối chiếu (CHỈ ĐỌC, không sửa, không build): `reference/electron/docs/SPEC.md` là đặc tả gốc, `reference/electron/src/main/*.js` là hiện thực chuẩn, `reference/electron/test/*.js` là các bất biến đã được kiểm thử. Khi đặc tả này và bản Electron lệch nhau thì ưu tiên đặc tả này, và nêu rõ trong báo cáo.

## Stack cố định

- Go 1.27, Wails v2.17 (WebView2), Windows x64. Module `s3browser`.
- AWS SDK Go v2 (`service/s3`, `feature/s3/manager`, `credentials`). KHÔNG thêm phụ thuộc mới nếu chưa vendor; mọi gói đã có trong `vendor/`. Build ngoại tuyến: `GOFLAGS=-mod=vendor`.
- Frontend: HTML/CSS/JS thuần trong `frontend/dist/` (dùng lại renderer của bản Electron). Không bundler, không npm.
- Kiểm thử: `go test ./...`, giả lập S3 qua interface hoặc `net/http/httptest`, không gọi mạng thật.

## Cấu trúc

```
main.go                      cấu hình Wails (bind App, bật EnableFileDrop)
app.go                       struct App: các phương thức được bind (mỏng, chỉ kiểm tra tham số rồi gọi nội bộ)
internal/profiles/           lưu hồ sơ, mã hóa secret bằng Windows DPAPI
internal/s3svc/              toàn bộ gọi S3, cache client theo profileId
internal/transfers/          hàng đợi truyền tệp
internal/apperr/             lỗi có mã + thông điệp tiếng Việt
frontend/dist/               giao diện (index.html, app.js, styles.css, i18n.js, api-shim.js)
```

## Hồ sơ

Trường JSON (camelCase, y như bản Electron): `id, name, endpoint, region, accessKeyId, secretAccessKey, hasSecret, forcePathStyle, allowInsecureTls, defaultBucket`. Quy tắc chuẩn hóa, mặc định, xác thực giống `reference/electron/src/main/profiles.js`.

- Lưu `%APPDATA%\S3BrowserLite\profiles.json` (dùng `os.UserConfigDir`). `secretAccessKey` mã hóa bằng DPAPI (`CryptProtectData` của `golang.org/x/sys/windows`, phạm vi người dùng hiện tại) rồi lưu base64. DPAPI lỗi thì từ chối lưu secret, báo lỗi rõ, KHÔNG lưu rõ.
- Renderer không bao giờ nhận `secretAccessKey`; chỉ nhận `hasSecret`. Để trống secret khi sửa nghĩa là giữ nguyên. Xuất JSON không kèm secret. Nhập JSON luôn xóa secret.

## Client S3 (bắt buộc đúng, nếu không sẽ hỏng với S3 tương thích)

- Dựng `aws.Config` thủ công, KHÔNG gọi `config.LoadDefaultConfig`, không để SDK lấy credential từ môi trường, tệp `~/.aws` hay IMDS. Thiếu khóa thì báo `CredentialsRequired`.
- Credentials tĩnh; `BaseEndpoint` khi có endpoint; `UsePathStyle = forcePathStyle`; `Region`.
- BẮT BUỘC đặt `RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired` và `ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired`. Mặc định mới của SDK thêm checksum CRC32/trailer mà MinIO bản cũ, Ceph RGW, ECS và nhiều S3 tương thích từ chối.
- `allowInsecureTls` chỉ áp dụng cho `http.Client` riêng của đúng hồ sơ đó (`TLSClientConfig.InsecureSkipVerify`), không đụng `http.DefaultTransport`.
- Upload dùng `manager.Uploader` (multipart tự động, đồng thời 2). Tải xuống ghi ra tệp tạm rồi đổi tên; hủy thì xóa tệp tạm.
- Chống path traversal khi tải xuống đúng như `safeDownloadPath` trong `reference/electron/src/main/transfers.js`, và thư mục đích chỉ được là thư mục người dùng đã chọn qua hộp thoại của Go (ghi nhớ trong App, giống `downloadDirectories`).
- Xóa nhiều đối tượng: chia lô tối đa 1000 khóa, kiểm tra lỗi từng khóa trong phản hồi.
- Không ghi secret/accessKeyId vào log hay thông báo lỗi (lọc cả chuỗi gốc lẫn dạng URL-encode), như `errors.js` bản Electron.

## Hợp đồng bind (Go `App` <-> JavaScript)

Wails v2 bind phương thức công khai của `*App` thành `window.go.main.App.<Tên>(...)`, trả Promise. Mỗi phương thức Go trả `(giá trị, error)`. Lỗi LUÔN được trả về dưới dạng chuỗi JSON `{"code":"<Mã gốc>","message":"<Thông điệp tiếng Việt>"}` (tạo bằng `apperr`), để shim dựng lại `Error` có `code`. Tham số `token` rỗng nghĩa là không có.

Hồ sơ: `ProfilesList() []Profile`, `ProfilesSave(p Profile) Profile`, `ProfilesRemove(id string) bool`, `ProfilesDuplicate(id string) Profile`, `ProfilesTestID(id string) []Bucket`, `ProfilesTestDraft(p Profile) []Bucket`, `ProfilesImport(json string) []Profile`, `ProfilesExport() string`.

S3: `S3ListBuckets(id string) []Bucket`, `S3CreateBucket(id, name string) Bucket`, `S3List(id, bucket, prefix, token string) ListResult`, `S3Head(id, bucket, key string) HeadResult`, `S3Delete(id, bucket string, keys []string) int`, `S3DeletePrefix(id, bucket, prefix string) int`, `S3CreateFolder(id, bucket, prefix string) bool`, `S3Move(id, bucket, from, to string) int`, `S3Presign(id, bucket, key string, expirySec int) string`.

Truyền tệp: `TransfersUpload(id, bucket, prefix string, paths []string) []Job`, `TransfersDownload(id, bucket string, keys []string, dir string) []Job`, `TransfersCopy(srcID, srcBucket string, keys []string, dstID, dstBucket, prefix string) []Job`, `TransfersCancel(id string) bool`, `TransfersRetry(id string) bool`, `TransfersList() []Job`, `TransfersSetConcurrency(n int) int`.
Sự kiện đẩy về giao diện: `runtime.EventsEmit(ctx, "transfers:update", job)` với `job` cùng dạng `view(job)` trong `reference/electron/src/main/transfers.js`.

Hộp thoại và tiện ích: `DialogsUpload(directory bool) []string` (hộp thoại chọn tệp/thư mục của Wails, nhiều mục), `DialogsDownloadDirectory() string` (rỗng nếu hủy), `ClipboardWriteText(text string) bool`.

Kiểu dữ liệu, tên trường JSON y hệt dạng trả về của bản Electron: `Bucket{name, creationDate}`, `ListResult{folders:[{prefix}], objects:[{key,size,lastModified,etag}], nextToken|null}`, `HeadResult{size,etag,contentType,metadata,lastModified}`.

Kéo thả tệp: bật `options.DragAndDrop{EnableFileDrop: true}`; giao diện nhận đường dẫn bằng `runtime.OnFileDrop`. Go không cần xử lý riêng, chỉ cần bật cờ.

## Tính năng và bất biến

Đủ như `reference/electron/docs/SPEC.md` (hồ sơ, nhiều tab, duyệt/phân trang, upload/download/xóa/tạo thư mục/đổi tên/di chuyển/sao chép giữa endpoint, presign, bảng truyền tệp, tạo bucket). Thêm: `S3CreateBucket` kiểm tra tên 3 đến 63 ký tự theo `BUCKET_NAME` trong `s3service.js`, chỉ gửi `LocationConstraint` khi hồ sơ KHÔNG có endpoint tùy chỉnh và region khác us-east-1.

## Tiêu chí hoàn thành

- `go vet ./...` sạch, `go test ./...` xanh, có test cho: mã hóa/giải mã hồ sơ (DPAPI qua interface để test), không rò secret, dựng client (endpoint, path style, checksum WhenRequired, TLS lỏng riêng từng hồ sơ), phân trang, xóa theo lô 1000, tên bucket, LocationConstraint, chống path traversal, hàng đợi (đồng thời, hủy).
- `wails build -s` ra được `build/bin/S3BrowserLite.exe`.
