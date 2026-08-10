# Ứng dụng duy trì ngữ cảnh liền mạch giữa các phiên làm việc bằng cách tự động ghi lại các quan sát về việc sử dụng công cụ, tạo ra các bản tóm tắt ngữ nghĩa và cung cấp chúng cho các phiên làm việc sau này. Điều này cho phép Antigravity CLI / Claude Code duy trì tính liên tục của kiến thức về các dự án ngay cả sau khi các phiên làm việc kết thúc hoặc kết nối lại.

## Tech stack
- Golang cho hiệu năng cao
- Lưu trữ: Sqlite (mặc định). PostgreSQL, MySQL, hoặc vector database ...
- ...

## Ý tưởng
Tham khảo từ 2 repo này để xây dựng dự án: [Claude Mem](https://github.com/thedotmack/claude-mem), [Mempalace](https://github.com/mempalace/mempalace)

## Hiện trạng
Hiện tại có tới tận 3 thiết bị sử dụng CLI (Claude Code, Antigravity CLI), chủ yếu là [Antigravity CLI](https://antigravity.google/product/antigravity-cli) là PC1 (trên công ty), PC2 (tại nhà), Laptop (tại nhà).
Mỗi lần cần thảo luận hay gì thi phải nói lại toàn bộ lịch sử trò chuyện, cho nên là cực kỳ bất tiện.

## Mục tiêu:
**Tự động** lưu trữ, trích xuất và duy trì liền mạch cuộc trò chuyện và kiến thức được đúc kết lại qua toàn bộ các thiết bị mà không bị ngắt quãng.

