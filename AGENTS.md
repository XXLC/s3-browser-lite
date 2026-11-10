# Quy tắc dự án S3 Browser Lite (Go + Wails)

- Đặc tả là docs/SPEC-GO.md và là hợp đồng; lệch đặc tả phải nêu rõ trong báo cáo.
- YAGNI, KISS, DRY. Không thêm tính năng ngoài đặc tả.
- Tiếng Việt có dấu đầy đủ ở mọi nơi: giao diện, comment, log, tài liệu, thông báo lỗi.
- Không dùng emoji, emoticon, ký hiệu trang trí trong bất kỳ đầu ra nào.
- Không ghi, in, hay chép secret (khóa truy cập) vào log, tệp, kiểm thử hay tin nhắn.
- Comment ngắn, chỉ giải thích lý do khi không hiển nhiên. Code Go theo gofmt.
- Chỉ sửa trong thư mục dự án này. Thư mục reference/ chỉ đọc.
- Môi trường build: PATH cần có D:\Programs\Go\bin và C:\Users\myste\go\bin. Đặt GOFLAGS=-mod=vendor và GOCACHE=<thư mục dự án>\.gocache để không ghi ra ngoài dự án. Không chạy go get hay go mod download (không có mạng); phụ thuộc đã vendor sẵn.
