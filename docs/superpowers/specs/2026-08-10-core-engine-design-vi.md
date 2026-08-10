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

- **claude-mem** (`thedotmack/claude-mem`, có source plugin sẵn ở local): daemon nền chạy dài hạn, khởi động từ hook `SessionStart`; `PostToolUse` ghi log quan sát thô; `Stop` kích hoạt một lượt tóm tắt bằng LLM (có debounce) thông qua `@anthropic-ai/claude-agent-sdk`; trí nhớ được phân vùng theo từng project qua cột `project_id`/`projectPath` trong 1 file `~/.claude-mem/claude-mem.db` duy nhất. Quan trọng: hook không phải nguồn dữ liệu duy nhất — source code còn có `transcriptWatcher`/`transcriptMirrorBatcher` đọc thẳng file transcript gốc của Claude Code (`~/.claude/projects/*.jsonl`). Hook chỉ đóng vai trò tín hiệu "vừa có chuyện xảy ra" (nhanh, độ trễ thấp); file transcript mới là nguồn sự thật thực sự nó đọc dữ liệu từ đó.
- **Mempalace** (`MemPalace/mempalace`, đã fetch README + docs): lưu trữ **local-first, nguyên văn** (không tóm tắt), đánh index bằng embedding (mặc định ChromaDB); tổ chức trí nhớ theo **wing** (1 người, 1 project, hoặc 1 chủ đề — cấp cao nhất), **room** (chủ đề con trong 1 wing, tự động phát hiện từ cấu trúc thư mục), **hall** (phân loại theo dạng trí nhớ: facts, events, discoveries, preferences, advice), và **tunnel** (liên kết chéo giữa các wing khi chúng có chung tên room — dùng cho trường hợp nhiều người dùng chung 1 palace). Lệnh khai thác chính của nó, `mempalace mine <transcript-dir>`, cũng đọc thẳng file transcript chứ không chỉ dựa vào hook. Có sẵn hook auto-save cho Claude Code, Codex CLI, và Cursor; ngoài ra có 1 plugin chính thức riêng thêm hook `Stop`/`PreInvocation` cho **Antigravity IDE**.
- **Antigravity CLI** (`google-antigravity/antigravity-cli`): đã khảo sát bằng thực nghiệm thật, không chỉ dựa vào docs — xem mục 2.1. Xác nhận qua test trực tiếp trên binary đã cài (bản v1.1.7, rồi v1.1.11): có hệ thống `hooks.json` riêng (toàn cục `~/.gemini/config/hooks.json` hoặc theo workspace `<workspace>/.agents/hooks.json`) và chế độ headless/print hoạt động thật (`agy -p "..." --output-format json`). Tuy nhiên, hook `PostToolUse`/`Stop` **load được nhưng không thực thi** trong môi trường này, cả ở chế độ headless lẫn tương tác. Dù vậy, Antigravity CLI vẫn tự lưu toàn bộ transcript hội thoại xuống đĩa, trong các SQLite riêng cho từng conversation tại `~/.gemini/antigravity-cli/conversations/*.db` (bảng `steps`), cùng 1 file index `~/.gemini/antigravity-cli/conversation_summaries.db` map `conversation_id` sang `workspace_uris`.

Core Engine của memremark là sự **kết hợp có chủ đích** của cả hai: lưu trữ nguyên văn (theo Mempalace) để không mất thông tin, cộng với tóm tắt định kỳ bằng LLM (theo claude-mem) để tạo ra "tri thức đúc kết" đúng như mục tiêu của dự án đề ra — được tổ chức theo từ vựng wing/hall của Mempalace nhưng đã cắt gọt cho vừa với nhu cầu thực tế của 1 người dùng dùng nhiều CLI. Theo đúng tiền lệ của cả 2 công cụ tham khảo — và được xác nhận là cần thiết qua đợt khảo sát hook của Antigravity CLI bên dưới — **việc ghi verbatim sẽ đọc thẳng transcript gốc của từng CLI; hook chỉ dùng như 1 tín hiệu trigger tùy chọn, không bao giờ là đường dẫn dữ liệu duy nhất.**

### 2.1 Khảo sát hook của Antigravity CLI (kết quả thực nghiệm, ngày 2026-08-10)

Test trực tiếp trên binary `agy` đã cài, không chỉ dựa vào CHANGELOG:

1. **Schema.** Cách diễn đạt trong CHANGELOG và quy ước hooks.json của Claude Code khiến ta nghĩ file sẽ có dạng giống Claude Code (`{"hooks": {"PostToolUse": [{"matcher": "*", "hooks": [{"type": "command", "command": "..."}]}]}}`). Điều này **sai** với Antigravity CLI và bị lỗi parse (xác nhận qua `~/.gemini/antigravity-cli/cli.log`: `invalid hook "hooks": command hook must specify 'command'`). Mọi plugin đang cài trên máy test được port từ Claude Code (superpowers, context-mode, hookify, ralph-loop, security-guidance, claude-security, learning-output-style, explanatory-output-style) đều lỗi parse tương tự dưới Antigravity CLI — một lỗi có sẵn, không liên quan trực tiếp nhưng đáng để biết, không thuộc phạm vi sửa ở đây. Schema đúng, tìm ra bằng cách thử và đọc lỗi parse thật: mỗi loại hook map trực tiếp tới 1 object (không phải mảng, không có wrapper `"hooks"`):
   ```json
   {
     "PostToolUse": { "matcher": "*", "command": "..." },
     "Stop": { "command": "..." }
   }
   ```
   Schema này load sạch, không lỗi (`cli.log`: `loaded 2 named hooks from 1 hooks.json file(s)`).
2. **Thực thi.** Dù đã có schema đúng, workspace đã trust, và có tool call thật (một lần ghi file thật, xác nhận trên đĩa) — cả `PostToolUse` lẫn `Stop` **đều không bao giờ thực thi** lệnh command đã cấu hình — không file output, không lỗi, không dấu vết thực thi trong log. Test lại sau khi người dùng tự nâng cấp `agy` từ 1.1.7 lên 1.1.11 (bản có CHANGELOG ghi rõ sửa lỗi thứ tự thực thi của Stop hook): kết quả vẫn vậy. Test lại trong phiên tương tác thật (không chỉ headless `-p`): kết quả vẫn vậy. Đây được coi là một hạn chế đã xác nhận, tái lập được của hook loại command trong môi trường này, không còn là rủi ro chưa kiểm chứng nữa.
3. **Phương án thay thế khả thi.** Dù hook không chạy, `agy` vẫn tự lưu đầy đủ mọi thứ cần thiết: SQLite riêng cho từng conversation tại `~/.gemini/antigravity-cli/conversations/*.db` (chế độ WAL — có file `.db-wal`/`.db-shm`) với bảng `steps`, cùng 1 database index `conversation_summaries.db` có bảng `conversation_summaries` map `conversation_id` → `workspace_uris`. Đây chính là nguồn mà adapter Antigravity sẽ đọc trực tiếp để lấy verbatim (xem mục 5, 6).

## 3. Ngoài phạm vi (chủ động loại trừ khỏi spec này)

- Đồng bộ trí nhớ giữa các thiết bị (Sync Layer — spec riêng).
- Semantic/vector search trên nội dung verbatim. Ở v1, việc nạp ngữ cảnh chỉ dựa trên **độ mới** (bản tóm tắt gần nhất), chưa tìm kiếm theo độ tương đồng ngữ nghĩa.
- Logic tự động phát hiện `room`. Cột này đã có sẵn trong schema để sau này thêm mà không cần migrate, nhưng hiện chưa xây logic phát hiện.
- `tunnel` (liên kết chéo giữa các wing). Tính năng này giải quyết bài toán nhiều người dùng chung 1 palace; người dùng ở đây là 1 cá nhân dùng nhiều thiết bị của chính mình, nên không có gì cần liên kết.
- Bất kỳ CLI nào khác ngoài Claude Code và Antigravity CLI.

## 4. Kiến trúc

Một daemon nền (`memremarkd`) chạy dài hạn trên mỗi máy, được khởi động theo kiểu idempotent (chạy nhiều lần không gây lỗi/trùng lặp). Đầu vào chính của nó là **kho transcript gốc trên đĩa của từng CLI**, được daemon đọc/poll trực tiếp. Hook — ở nơi nào nó load được và thực sự thực thi — chỉ dùng để đánh thức daemon đọc ngay lập tức thay vì chờ đến lượt poll kế tiếp, không bao giờ là cách duy nhất để 1 observation đến được daemon. Vẫn có 1 adapter mỏng cho từng CLI, nhưng nhiệm vụ của nó chuyển từ "phân tích payload hook" sang "biết transcript của CLI đó nằm ở đâu và đọc thế nào".

Lưu trữ là **1 file SQLite duy nhất trên mỗi máy**, tại `~/.memremark/memremark.db`. Một file duy nhất, không tách riêng theo từng project — đúng với cách cả 2 công cụ tham khảo thực sự vận hành (chúng phân vùng logic qua cột dữ liệu, chứ không tách file vật lý), đồng thời giúp Sync Layer sau này đơn giản hơn (chỉ cần đồng bộ 1 file, thay vì một tập hợp file không giới hạn).

```
CLI (Claude Code / Antigravity CLI)
   |  tự ghi transcript của nó trong lúc hoạt động
   v
Kho transcript gốc của CLI
   (Claude Code:     ~/.claude/projects/<project>/*.jsonl)
   (Antigravity CLI: ~/.gemini/antigravity-cli/conversations/*.db + conversation_summaries.db)
   |
   |  đọc read-only theo kiểu tail/poll (+ hook đánh thức, tùy chọn, best-effort)
   v
Adapter (riêng cho từng CLI: biết định dạng/vị trí transcript, chuẩn hóa thành Observation)
   v
memremarkd (daemon chạy nền dài hạn)
   |
   v
SQLite (~/.memremark/memremark.db)
```

## 5. Adapter

Mỗi CLI có 1 adapter đảm nhiệm 3 việc:

1. **Đọc transcript (chính, bắt buộc)** — đọc read-only kho transcript gốc của CLI đó, chuẩn hóa từng bước thành 1 struct nội bộ dùng chung `Observation` (tên tool, tóm tắt tham số/kết quả, session id, timestamp, cwd). Việc này không phụ thuộc hook và vẫn hoạt động ngay cả khi hook không bao giờ chạy.
2. **Lắng nghe hook (tùy chọn, best-effort)** — nếu hook của CLI đó thực sự thực thi được trong môi trường người dùng, dùng nó chỉ để trigger đọc lại ngay lập tức thay vì chờ đến chu kỳ poll kế tiếp. Không bao giờ là trigger duy nhất.
3. **Gọi headless** — spawn chế độ non-interactive của chính CLI đó để chạy hội thoại phụ tóm tắt, nhờ vậy memremark không bao giờ cần tự có API key LLM hay tự trả phí riêng. Đã xác nhận hoạt động tốt cho cả 2 CLI (xem mục 2.1).

| Khả năng | Claude Code | Antigravity CLI |
|---|---|---|
| Kho transcript | `~/.claude/projects/<project>/*.jsonl` (append-only, mỗi dòng 1 sự kiện) | `~/.gemini/antigravity-cli/conversations/<conversation_id>.db` (SQLite, chế độ WAL, bảng `steps`) + `conversation_summaries.db` (`conversation_id` → `workspace_uris`) |
| Cấu hình hook (best-effort) | plugin `hooks.json` | `hooks.json` (toàn cục hoặc theo workspace); schema đã xác nhận ở mục 2.1, nhưng việc thực thi `PostToolUse`/`Stop` đã xác nhận **không hoạt động** ở bản v1.1.11 trong môi trường này — coi như chưa dùng được cho đến khi xác minh lại |
| Gọi headless | `claude -p "..." --output-format json` | `agy -p "..." --output-format json` |
| Điểm nạp lại ngữ cảnh khi bắt đầu session | hook `SessionStart` | Chưa có hook nào chạy được để dựa vào (mục 2.1); dùng cách poll `conversation_summaries.db` tìm `conversation_id` mới xuất hiện dưới workspace hiện tại làm tín hiệu "session mới đã bắt đầu" |

Việc đọc file `.db` đang sống (live) của Antigravity CLI bắt buộc phải **read-only tuyệt đối** (không bao giờ ghi, migrate, hay vacuum) kèm busy-timeout/retry cho trường hợp hiếm khi writer đang giữ lock — xem mục 8.

## 6. Luồng dữ liệu

1. **Ghi verbatim** — daemon poll định kỳ (và đọc lại ngay nếu có hook báo) nguồn transcript của từng project đã biết. Các bước/sự kiện mới được chuẩn hóa thành `Observation` và ghi thẳng vào SQLite thành 1 dòng `drawer` (`type = 'verbatim'`, `hall = 'event'`). Không gọi LLM. Đây là nguồn sự thật (source of truth), không thể mất thông tin, và không phụ thuộc vào việc hook có chạy hay không.
2. **Tóm tắt** — sau khi 1 lượt phản hồi kết thúc (phát hiện qua hook `Stop` nếu nó hoạt động, hoặc qua việc transcript không có hoạt động mới trong vài giây — cùng ý tưởng debounce dù theo cách nào), daemon spawn 1 hội thoại phụ headless thông qua chính CLI của session đó, yêu cầu nó đúc kết các dòng verbatim đã tích lũy kể từ lần tóm tắt trước thành một hoặc nhiều dòng `drawer` tóm tắt (`type = 'summary'`), mỗi dòng được phân loại vào 1 `hall` (`fact`, `discovery`, `preference`, hoặc `advice`).
3. **Nạp ngữ cảnh** — khi 1 session mới bắt đầu (phát hiện qua hook `SessionStart` với Claude Code, hoặc qua việc có `conversation_id` mới xuất hiện cho workspace hiện tại trong `conversation_summaries.db` của Antigravity CLI), daemon tra cứu `wing` tương ứng với thư mục làm việc hiện tại, truy vấn các dòng tóm tắt gần đây nhất theo thời gian, và nạp chúng vào ngữ cảnh của session mới.

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

- **Đọc file transcript/DB đang sống của 1 CLI**: luôn read-only, không bao giờ ghi/migrate/vacuum file của chính CLI đó. Riêng với SQLite của Antigravity CLI, mở kèm busy-timeout và retry-on-`SQLITE_BUSY`, vì chúng ở chế độ WAL và hiếm khi có thể đang bị `agy` ghi dở — đây là cách đọc đồng thời được hỗ trợ chính thức, không phải race condition cần né tránh bằng cấu trúc khác.
- **1 lượt poll không thấy gì mới / transcript tạm thời không đọc được** (file đang ghi dở, bị lock tạm thời): bỏ qua và thử lại ở lượt poll kế tiếp. Không mất dữ liệu vĩnh viễn — daemon không giữ state theo từng sự kiện để mất, nó chỉ quét lại từ mốc đã đọc gần nhất.
- **Hook kích hoạt nhưng daemon không phản hồi**: vô hại theo thiết kế, vì hook chỉ là tín hiệu đánh thức tùy chọn để poll sớm hơn; lượt poll theo lịch kế tiếp vẫn sẽ bắt được đúng dữ liệu đó.
- **Gọi headless để tóm tắt thất bại** (hết hạn đăng nhập, mất mạng, bị rate limit): các dòng verbatim đã được lưu bền vững từ trước nên không mất gì. Daemon sẽ thử tóm tắt lại ở lần debounce kế tiếp.
- **Daemon bị crash**: được khởi động lại một cách idempotent vào lần kế tiếp có gì đó kích hoạt nó (1 hook, nếu còn hoạt động, hoặc 1 lần kiểm tra nhẹ lúc CLI khởi động — xem thêm mục Rủi ro còn tồn đọng về đường nạp ngữ cảnh).

## 9. Kiểm thử (Testing)

Mọi logic không tầm thường dưới đây đều phải có unit test khi triển khai — đây không phải tùy chọn:

- Unit test cho các thao tác trên schema SQLite (insert/query drawers và wings) chạy trên SQLite in-memory hoặc file tạm.
- Unit test cho bộ phân tích transcript của từng adapter: đưa vào 1 đoạn transcript thật đã thu thập (1 đoạn `.jsonl` thật cho Claude Code; 1 đoạn dữ liệu bảng `steps` thật cho Antigravity CLI) và kiểm tra struct `Observation` kết quả đúng — kể cả các trường hợp biên như file đang ghi dở/bị cắt cụt và transcript rỗng.
- Unit test cho logic debounce/phát hiện nhàn rỗi quyết định khi nào trigger tóm tắt, độc lập với bất kỳ CLI thật nào.
- Một smoke test end-to-end: giả lập sẵn 1 file/DB transcript, chạy 1 chu kỳ poll của daemon, kiểm tra có đúng 1 dòng verbatim được ghi; sau đó, với headless invoker được stub thay cho hội thoại phụ CLI thật, kiểm tra 1 dòng summary cũng được ghi đúng.

## 10. Rủi ro còn tồn đọng

- **Schema bảng `steps` trong `conversations/*.db` của Antigravity CLI chưa có tài liệu chính thức** (là chi tiết triển khai nội bộ, không phải API công khai) và mới chỉ được kiểm tra sơ bộ (danh sách bảng, chưa đến từng cột) trong đợt khảo sát này. Cần reverse-engineer đầy đủ khi triển khai, và — vì không có tài liệu chính thức — có thể thay đổi mà không báo trước giữa các bản `agy`; bộ phân tích của adapter nên báo lỗi rõ ràng (không âm thầm bỏ dữ liệu) nếu schema thực tế không khớp với những gì nó kỳ vọng.
- **Đường nạp ngữ cảnh (context injection) cho Antigravity CLI chưa được kiểm chứng.** Mục 2.1 đã xác nhận hook `PostToolUse`/`Stop` load được nhưng không thực thi; chưa test xem cơ chế nạp ngữ cảnh dựa trên hook (tương đương `SessionStart` của Claude Code) có khá hơn không. Vì cùng dùng chung 1 engine thực thi hook bên dưới, khả năng cao là cũng không hoạt động. Mục 5/6 đã thiết kế phòng trước điều này bằng cách không bắt buộc phải có hook hoạt động — poll `conversation_summaries.db` tìm `conversation_id` mới là phương án dự phòng — nhưng *daemon thực sự sẽ đưa nội dung vào ngữ cảnh của 1 session Antigravity CLI mới bằng cách nào* (chưa xác nhận có cơ chế nào tương đương việc `SessionStart` hook của Claude Code cho phép "chèn" output vào ngữ cảnh) vẫn còn bỏ ngỏ và cần khảo sát thực tế thêm trước khi hoàn thiện adapter này.
- Độ dài cửa sổ debounce (chờ bao nhiêu giây không hoạt động mới tóm tắt) chưa được tinh chỉnh; bắt đầu theo tham khảo cách làm của claude-mem rồi điều chỉnh dựa trên thực tế sử dụng.
