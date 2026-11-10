# Hợp đồng bucket policy và ARN, chỉnh bố cục thanh bên (chỉ bản Lite)

## Phía Go (một phiên phụ trách: app.go, internal/s3svc/**, *_test.go; KHÔNG sửa frontend/)

Thêm vào `s3svc.Service` ba phương thức (dùng `GetBucketPolicy`, `PutBucketPolicy`, `DeleteBucketPolicy` của AWS SDK Go v2, đã có trong vendor/ qua gói `service/s3`; nếu thiếu gói con nào trong vendor thì dừng và báo cáo, đừng thêm phụ thuộc):

- `GetBucketPolicy(ctx, id, bucket) (string, error)`: trả chuỗi JSON đã định dạng đẹp (`json.Indent`, thụt 2 dấu cách). Lỗi mã `NoSuchBucketPolicy` KHÔNG phải lỗi: trả chuỗi rỗng và `nil`. Nếu máy chủ trả nội dung không phải JSON hợp lệ thì trả nguyên văn.
- `PutBucketPolicy(ctx, id, bucket, policy string) error`: kiểm tra cục bộ trước khi gọi mạng: không rỗng, tối đa 20480 byte (giới hạn của S3), là một đối tượng JSON hợp lệ (`json.Unmarshal` vào `map[string]any`); sai thì trả `apperr` mã `InvalidPolicy` với thông điệp tiếng Việt nêu lý do (không in nội dung policy). Gửi đi dạng nén gọn (`json.Compact`).
- `DeleteBucketPolicy(ctx, id, bucket) error`.

Ba phương thức bind trên `*App` (mỏng, dùng `call(...)`, `validate.First(target(id, bucket), ...)`, không giữ khóa profile bận quá mức cần thiết, đặt `[]string{id}` như các phương thức S3 khác):

- `S3GetBucketPolicy(id, bucket string) (string, error)`
- `S3PutBucketPolicy(id, bucket, policy string) (bool, error)`: `validate.Text(policy, "Policy", 20480, false)`; trả `true` khi thành công.
- `S3DeleteBucketPolicy(id, bucket string) (bool, error)`

Không phát sự kiện, không cache. Test bằng giả lập HTTP như `service_test.go`: có policy / chưa có policy (mã `NoSuchBucketPolicy` ra chuỗi rỗng), định dạng đẹp, Put chặn rỗng / quá 20480 byte / không phải JSON / JSON không phải đối tượng, Put gửi đúng thân dạng nén gọn, Delete, lỗi mạng được lọc secret, và `app_test.go` kiểm bind (tham số sai bị từ chối trước khi gọi mạng).

## Phía giao diện (một phiên khác phụ trách: frontend/dist/**, tools/ui-preview/**; KHÔNG sửa Go)

`api-shim.js` thêm vào `window.api.s3`: `getBucketPolicy(id, bucket)` gọi `S3GetBucketPolicy`; `putBucketPolicy(id, bucket, policy)` gọi `S3PutBucketPolicy`; `deleteBucketPolicy(id, bucket)` gọi `S3DeleteBucketPolicy`.

### Bố cục thanh bên mới (từ trên xuống)

1. Tiêu đề "S3 Storage", danh sách hồ sơ (`#profiles`), rồi nút "Thêm Storage" (đổi nhãn của `#addProfile`, giữ id) nằm NGAY DƯỚI danh sách hồ sơ, cùng kiểu và cùng chiều rộng với nút "Tạo bucket mới".
2. Tiêu đề "Cấu hình kết nối Storage" và lưới nút: Sửa, Nhân bản, Xóa, Kiểm tra, Mở tab, Xuất JSON. BỎ nút "Thêm" và nút "Nhập JSON" khỏi lưới này.
3. Tiêu đề "Bucket của Storage hiện tại", danh sách bucket, nút "Tạo bucket mới" (giữ như hiện tại), rồi một hàng hai nút bằng nhau: "Policy bucket" (`#bucketPolicy`) và "ARN bucket" (`#bucketArn`). Cả hai chỉ bật khi tab hiện tại đã chọn bucket (cùng cách `render()` tắt/bật các nút theo bucket hiện nay), giữ nguyên mọi id đang có.

### Nhập JSON chuyển vào hộp thoại thêm Storage

Trong hộp thoại hồ sơ (`#profileDialog`), khi đang THÊM mới (không phải sửa) hiện một nút "Nhập từ JSON" (đặt trên đầu hộp thoại, cạnh tiêu đề hoặc ngay dưới). Bấm vào chạy đúng luồng nhập JSON hiện có (`importProfiles`: hộp nhập văn bản JSON, gọi `api.profiles.import`), nhập thành công thì đóng hộp thoại hồ sơ, tải lại danh sách hồ sơ và báo `t.imported`. Khi sửa hồ sơ có sẵn thì ẩn nút này. Giữ nguyên hành vi lưu, kiểm tra, hủy của hộp thoại.

### Hộp thoại Policy bucket (`#policyDialog`, phần tử `<dialog>` mới)

Tiêu đề "Policy của bucket <tên bucket>". Vùng văn bản nhiều dòng, phông đơn cách (monospace), rộng, cao khoảng 18 dòng, `spellcheck="false"`. Khi mở: gọi `getBucketPolicy`; đang tải thì vô hiệu các nút. Có policy thì nạp vào vùng văn bản; chưa có (chuỗi rỗng) thì điền khung mẫu `{ "Version": "2012-10-17", "Statement": [] }` định dạng đẹp kèm dòng chú thích "Bucket chưa có policy. Đây chỉ là khung, hãy tự điền Statement và Resource theo ARN của nhà cung cấp (xem nút ARN bucket)." Ba nút: "Lưu policy" (kiểm tra JSON hợp lệ phía giao diện trước bằng `JSON.parse`, sai thì hiện lỗi ngay trong hộp thoại, không đóng; đúng thì gọi `putBucketPolicy`, thành công thì đóng và `notify`), "Xóa policy" (chỉ bật khi bucket đang có policy; hỏi xác nhận bằng hộp xác nhận đơn giản có sẵn trong ứng dụng rồi gọi `deleteBucketPolicy`, thành công thì đóng và `notify`), "Hủy". Lỗi từ Go hiện trong vùng lỗi của hộp thoại (như `#profileError`). Cảnh báo cố định: "Policy quyết định ai được truy cập bucket. Sai policy có thể chặn hoặc mở quyền truy cập ngoài ý muốn."

### Hộp thoại ARN bucket (`#arnDialog`, `<dialog>` mới)

Một ô nhập "Tiền tố ARN" (sửa tay được) kèm danh sách chọn mẫu điền sẵn vào ô: `arn:aws:s3:::` ("AWS S3, MinIO, Ceph RGW"), `urn:ecs:s3:::` ("Dell ECS, cần xác minh với tài liệu của nhà cung cấp"). Mặc định chọn mẫu đầu tiên, nhớ lựa chọn theo hồ sơ trong `localStorage` (khóa `arnPrefix:<profileId>`, bọc try/catch, trang vẫn chạy khi không có localStorage). Phía dưới liệt kê các dòng, mỗi dòng gồm nhãn, giá trị (phông đơn cách, chọn được) và nút "Sao chép" (dùng `api.clipboard.writeText`, báo `t.copied`): (1) "ARN bucket": tiền tố + tên bucket; (2) "ARN mọi đối tượng": tiền tố + tên bucket + `/*`; (3) chỉ khi đang ở trong một thư mục: "ARN thư mục hiện tại": tiền tố + tên bucket + `/` + prefix hiện tại + `*`. Giá trị cập nhật ngay khi sửa ô tiền tố. Chú thích cố định: "Định dạng ARN khác nhau theo nhà cung cấp S3. Nếu policy bị từ chối, hãy kiểm tra lại tiền tố với tài liệu của nhà cung cấp."

### Yêu cầu chung

Mọi chuỗi mới vào `i18n.js` (tiếng Việt có dấu, không emoji), không script inline (CSP), không thêm thư viện. Giữ nguyên mọi hành vi và id đang có. CSS dùng biến màu sẵn có, hỗ trợ sáng/tối. Cập nhật `tools/ui-preview/mock.js` (thêm hàm giả cho ba hàm policy; bucket có policy mẫu và bucket chưa có) và `preview.html` để chụp ảnh được: bố cục chính, hộp thoại thêm Storage (có nút Nhập từ JSON), hộp thoại Policy (có và chưa có policy), hộp thoại ARN.
