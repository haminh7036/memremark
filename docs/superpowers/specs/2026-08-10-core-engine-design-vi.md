# Core Engine — Tài liệu thiết kế (bản tiếng Việt)

> Bản dịch tham khảo từ `2026-08-10-core-engine-design.md`. Nếu có sai khác, bản tiếng Anh là bản gốc chuẩn.

Trạng thái: Đã duyệt, sẵn sàng lập kế hoạch triển khai
Ngày: 2026-08-10
Phạm vi: Core Engine của memremark (trên 1 máy). Sync Layer đồng bộ đa thiết bị là một spec riêng, làm sau.

## 1. Vấn đề & Mục tiêu

Người dùng làm việc trên 3 thiết bị (PC1 ở công ty, PC2 ở nhà, Laptop ở nhà), mỗi máy chạy Claude Code và/hoặc Antigravity CLI. Hiện tại mỗi session mới đều bắt đầu từ con số 0 — mọi thảo luận, quyết định, kiến thức về dự án trước đó đều phải giải thích lại thủ công.

Core Engine sẽ tự động ghi lại những gì xảy ra trong 1 session CLI, lưu trữ bền vững, đúc kết thành tri thức có thể tái sử dụng, và nạp lại tri thức đó cho các session sau **trên cùng một máy** — mà người dùng không cần thao tác gì thêm. Việc lan truyền tri thức đó **giữa các thiết bị** không nằm trong phạm vi spec này; đó là việc của Sync Layer (spec kế tiếp), chỉ làm sau khi Core Engine đã có dữ liệu đáng để đồng bộ.

## 2. Tham khảo từ các công cụ có sẵn

Hai công cụ đã được khảo sát trực tiếp (đọc source code local / trên GitHub) để làm căn cứ thiết kế, thay vì đoán mò:

- **claude-mem** (`thedotmack/claude-mem`, có source plugin sẵn ở local): daemon nền chạy dài hạn, khởi động từ hook `SessionStart`; `PostToolUse` ghi log quan sát thô; `Stop` kích hoạt một lượt tóm tắt bằng LLM (có debounce) thông qua `@anthropic-ai/claude-agent-sdk`; trí nhớ được phân vùng theo từng project qua cột `project_id`/`projectPath` trong 1 file `~/.claude-mem/claude-mem.db` duy nhất.
- **Mempalace** (`MemPalace/mempalace`, đã fetch README + docs): lưu trữ **local-first, nguyên văn** (không tóm tắt), đánh index bằng embedding (mặc định ChromaDB); tổ chức trí nhớ theo **wing** (1 người, 1 project, hoặc 1 chủ đề — cấp cao nhất), **room** (chủ đề con trong 1 wing, tự động phát hiện từ cấu trúc thư mục), **hall** (phân loại theo dạng trí nhớ: facts, events, discoveries, preferences, advice), và **tunnel** (liên kết chéo giữa các wing khi chúng có chung tên room — dùng cho trường hợp nhiều người dùng chung 1 palace). Có sẵn hook auto-save cho Claude Code, Codex CLI, và Cursor; ngoài ra có 1 plugin chính thức riêng thêm hook `Stop`/`PreInvocation` cho **Antigravity IDE**.
- **Antigravity CLI** (`google-antigravity/antigravity-cli`, đọc CHANGELOG từ file nguồn archive bản v1.1.11): xác nhận có hệ thống `hooks.json` riêng (toàn cục `~/.gemini/config/hooks.json` hoặc theo từng workspace `<workspace>/.agents/hooks.json`), hỗ trợ hook `PostToolUse` (có matcher), `Stop`, và `PostInvocation`, cùng với chế độ headless/print (`agy -p "..." --output-format json|stream-json --json-schema <file>`) — gần như song song với hệ thống hook của Claude Code và `-p --output-format json`.

Core Engine của memremark là sự **kết hợp có chủ đích** của cả hai: lưu trữ nguyên văn (theo Mempalace) để không mất thông tin, cộng với tóm tắt định kỳ bằng LLM (theo claude-mem) để tạo ra "tri thức đúc kết" đúng như mục tiêu của dự án đề ra — được tổ chức theo từ vựng wing/hall của Mempalace nhưng đã cắt gọt cho vừa với nhu cầu thực tế của 1 người dùng dùng nhiều CLI.

## 3. Ngoài phạm vi (chủ động loại trừ khỏi spec này)

- Đồng bộ trí nhớ giữa các thiết bị (Sync Layer — spec riêng).
- Semantic/vector search trên nội dung verbatim. Ở v1, việc nạp ngữ cảnh chỉ dựa trên **độ mới** (bản tóm tắt gần nhất), chưa tìm kiếm theo độ tương đồng ngữ nghĩa.
- Logic tự động phát hiện `room`. Cột này đã có sẵn trong schema để sau này thêm mà không cần migrate, nhưng hiện chưa xây logic phát hiện.
- `tunnel` (liên kết chéo giữa các wing). Tính năng này giải quyết bài toán nhiều người dùng chung 1 palace; người dùng ở đây là 1 cá nhân dùng nhiều thiết bị của chính mình, nên không có gì cần liên kết.
- Bất kỳ CLI nào khác ngoài Claude Code và Antigravity CLI.

## 4. Kiến trúc

Một daemon nền (`memremarkd`) chạy dài hạn trên mỗi máy, được khởi động theo kiểu idempotent (chạy nhiều lần không gây lỗi/trùng lặp). Hook của mỗi CLI sẽ gọi một adapter mỏng (1 script/binary nhỏ) để giao tiếp với daemon qua Unix socket cục bộ (loopback HTTP là phương án dự phòng chấp nhận được nếu socket gặp vấn đề tương thích đa nền tảng).

Lưu trữ là **1 file SQLite duy nhất trên mỗi máy**, tại `~/.memremark/memremark.db`. Một file duy nhất, không tách riêng theo từng project — đúng với cách cả 2 công cụ tham khảo thực sự vận hành (chúng phân vùng logic qua cột dữ liệu, chứ không tách file vật lý), đồng thời giúp Sync Layer sau này đơn giản hơn (chỉ cần đồng bộ 1 file, thay vì một tập hợp file không giới hạn).

```
CLI (Claude Code / Antigravity CLI)
   |  hook được kích hoạt (PostToolUse / Stop / tương đương SessionStart)
   v
Adapter (riêng cho từng CLI: phân tích JSON của hook, giao tiếp với daemon)
   |  socket cục bộ
   v
memremarkd (daemon chạy nền dài hạn)
   |
   v
SQLite (~/.memremark/memremark.db)
```

## 5. Adapter

Mỗi CLI có 1 adapter đảm nhiệm 2 việc:

1. **Phân tích payload của hook** — chuẩn hóa JSON hook của CLI đó thành 1 struct nội bộ dùng chung `Observation` (tên tool, tóm tắt tham số/kết quả, session id, timestamp, cwd).
2. **Gọi headless** — spawn chế độ non-interactive của chính CLI đó để chạy hội thoại phụ tóm tắt, nhờ vậy memremark không bao giờ cần tự có API key LLM hay tự trả phí riêng.

| Khả năng | Claude Code | Antigravity CLI |
|---|---|---|
| Cấu hình hook | plugin `hooks.json` | `hooks.json` (toàn cục hoặc theo workspace) |
| Hook sử dụng | `PostToolUse`, `Stop`, `SessionStart` | `PostToolUse`, `Stop`, `PostInvocation` |
| Gọi headless | `claude -p "..." --output-format json` | `agy -p "..." --output-format json --json-schema <file>` |

`SessionStart` (Claude Code) và `PostInvocation` (Antigravity CLI) đều kích hoạt vào lúc/gần lúc bắt đầu 1 session, và là điểm để nạp lại ngữ cảnh trước đó — được coi là tương đương nhau trong interface của adapter (`OnSessionStart`).

## 6. Luồng dữ liệu

1. **Ghi verbatim** — khi `PostToolUse` xảy ra (cả 2 CLI), adapter gửi observation đó cho daemon, daemon ghi thẳng vào SQLite thành 1 dòng `drawer` (`type = 'verbatim'`, `hall = 'event'`). Không gọi LLM. Đây là nguồn sự thật (source of truth) và không thể mất thông tin.
2. **Tóm tắt** — khi `Stop` xảy ra (cả 2 CLI, kết thúc 1 lượt phản hồi của agent), adapter báo cho daemon. Daemon debounce (chờ vài giây không có hoạt động mới, để gộp các lượt phản hồi dồn dập liên tiếp lại), sau đó spawn 1 hội thoại phụ headless thông qua chính CLI của session đó, yêu cầu nó đúc kết các dòng verbatim đã tích lũy kể từ lần tóm tắt trước thành một hoặc nhiều dòng `drawer` tóm tắt (`type = 'summary'`), mỗi dòng được phân loại vào 1 `hall` (`fact`, `discovery`, `preference`, hoặc `advice`).
3. **Nạp ngữ cảnh** — khi session bắt đầu (`SessionStart` / `PostInvocation`), daemon tra cứu `wing` tương ứng với thư mục làm việc hiện tại, truy vấn các dòng tóm tắt gần đây nhất theo thời gian, và trả về cho adapter để nạp vào ngữ cảnh của session mới.

## 7. Schema (SQLite)

```sql
wings (
  id INTEGER PRIMARY KEY,
  path TEXT UNIQUE NOT NULL,   -- đường dẫn thư mục project; tự động tạo khi có observation đầu tiên từ 1 path mới
  name TEXT NOT NULL,
  created_at INTEGER NOT NULL
)

drawers (
  id INTEGER PRIMARY KEY,
  wing_id INTEGER NOT NULL REFERENCES wings(id),
  room TEXT,                   -- có thể null; chưa dùng ở v1, dự phòng cho việc phân nhóm chủ đề con sau này
  type TEXT NOT NULL CHECK (type IN ('verbatim', 'summary')),
  hall TEXT NOT NULL CHECK (hall IN ('event', 'fact', 'discovery', 'preference', 'advice')),
  content TEXT NOT NULL,
  tool_name TEXT,               -- chỉ có giá trị khi type = 'verbatim'
  session_id TEXT NOT NULL,
  covers_from INTEGER,          -- chỉ có giá trị khi type = 'summary': khoảng thời gian của các dòng verbatim được đúc kết
  covers_to INTEGER,
  created_at INTEGER NOT NULL
)
```

Không có bảng `tunnels` (xem mục Ngoài phạm vi).

## 8. Xử lý lỗi

- **Daemon không phản hồi khi hook kích hoạt**: adapter fail nhanh với timeout ngắn, không làm chậm/kẹt trải nghiệm của CLI. Không mất dữ liệu vĩnh viễn — lần `PostToolUse`/`Stop` thành công tiếp theo sẽ tự tiếp tục từ trạng thái daemon đang có.
- **Gọi headless để tóm tắt thất bại** (hết hạn đăng nhập, mất mạng, bị rate limit): các dòng verbatim đã được lưu bền vững từ trước nên không mất gì. Daemon sẽ thử tóm tắt lại ở lần debounce `Stop` kế tiếp.
- **Daemon bị crash**: được khởi động lại một cách idempotent bởi lần kích hoạt `SessionStart`/`PostInvocation` kế tiếp (giống cách claude-mem làm với worker-service của nó).

## 9. Kiểm thử (Testing)

- Unit test cho các thao tác trên schema SQLite (insert/query drawers và wings) chạy trên SQLite in-memory hoặc file tạm.
- Test ở tầng adapter: đưa JSON hook thật của từng CLI (bắt lại từ lần hook thực sự kích hoạt) qua bộ phân tích, kiểm tra struct `Observation` kết quả.
- Một smoke test end-to-end: giả lập sự kiện `PostToolUse` + `Stop` gửi qua socket API của daemon, kiểm tra có đúng 1 dòng verbatim và (với headless invoker được stub) 1 dòng summary được ghi vào đúng như kỳ vọng.

## 10. Rủi ro còn tồn đọng

- Hệ thống hook của Antigravity CLI mới được xác nhận qua CHANGELOG công khai (có nhắc đến `hooks.json`, `PostToolUse`, `Stop`, `PostInvocation`, headless `-p`/`--output-format`), chưa chạy thử trực tiếp trên binary thật. Hình dạng chính xác của JSON payload từng loại hook cần được thu thập thực tế trong lúc triển khai, trước khi có thể chốt bộ phân tích (parser) của adapter.
- Độ dài cửa sổ debounce (chờ bao nhiêu giây không hoạt động mới tóm tắt) chưa được tinh chỉnh; bắt đầu theo tham khảo cách làm của claude-mem rồi điều chỉnh dựa trên thực tế sử dụng.
