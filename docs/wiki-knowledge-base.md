# Wiki knowledge base

Tài liệu này ghi lại hai việc:

1. Kết quả nghiên cứu cách TencentDB Agent Memory
   (<https://github.com/TencentCloud/TencentDB-Agent-Memory>) xây Wiki knowledge
   base, kèm phần bộ nhớ và proxy liên quan.
2. Cách agent-tui tích hợp ý tưởng đó thành package `internal/wiki`.

Bộ nhớ hội thoại (L0–L3) và skill đã được port từ TencentDB từ trước, xem
[gateway-admin-và-bộ-nhớ.md](gateway-admin-và-bộ-nhớ.md). Phần còn thiếu là
Wiki, và tài liệu này nói về phần đó.

## 1. TencentDB Agent Memory gồm những gì

| Thành phần | Vai trò | Cổng |
|---|---|---|
| **Memory Core** | Bộ nhớ hội thoại phân tầng L0 → L1 → L2 → L3, và Skill | 8420 |
| **MemoryKnowledge** | **Wiki** (tài liệu → trang có liên kết) và **CodeGraph** (symbol, call graph) | 8421/8424 |
| **MemoryProxy** | Reverse proxy đứng giữa agent và LLM API, chèn bộ nhớ vào system prompt | 8096 |
| **MemoryPanel / Hub** | Giao diện quản lý team, agent, asset, ACL | 8125 |

Tất cả viết bằng TypeScript chạy trên Node 22, mặc định lưu bằng SQLite.

### 1.1 Wiki: "LLM wiki" theo kiểu Karpathy

Ý tưởng gốc của Karpathy: thay vì RAG cắt tài liệu thành chunk rồi tìm theo
vector mỗi lần hỏi, ta để LLM **đọc tài liệu một lần** và **viết lại thành một
wiki Markdown có liên kết**. Wiki này được duy trì và tích luỹ dần qua các lần
nạp tài liệu. Agent đọc wiki giống như người đọc wiki.

MemoryKnowledge làm theo đúng ý đó, với các quyết định sau:

- **File là nguồn sự thật, index chỉ là dữ liệu dẫn xuất.** Thư mục `raw/` giữ
  tài liệu gốc và không bao giờ bị sửa. Trang nằm ở
  `wiki/{sources,entities,concepts,comparisons,synthesis}/<slug>.md`, có
  frontmatter YAML (`type`, `title`, `description`, `sources`, `tags`). Index
  FTS5 và đồ thị liên kết được dựng lại hoàn toàn từ file sau mỗi lần ingest.
- **Ingest tăng dần theo sha256.** Mỗi file raw được xếp vào một trong ba nhóm:
  - mới hoặc đã đổi → đọc;
  - không đổi → bỏ qua, không gọi LLM;
  - đã bị xoá → cascade (xem bên dưới).
- **Ingest hai bước.** Bước 1, LLM *phân tích*: tóm tắt nguồn, liệt kê entity
  và concept, chỉ ra cái nào đã có trang, gợi ý liên kết chéo. Bước 2, LLM
  *viết trang* theo kế hoạch đó.
- **Kiểm tra độ chi tiết.** Một chủ đề chỉ được tách thành trang riêng khi thoả
  cả ba điều kiện: có danh tính độc lập, có quan hệ riêng, và có đủ nội dung.
  Nếu không, nó chỉ là một mục nhỏ trong trang cha.
- **Giao thức FILE block thay cho tool call.** LLM xuất trang trong
  `<<<FILE path="…">>> … <<<END>>>`. Parser chịu lỗi tốt:
  - bỏ block không đóng (output bị cắt giữa chừng);
  - chặn path thoát ra ngoài thư mục;
  - nếu không đọc được gì thì lưu output thô vào `_debug/`.
- **Path do hệ thống tính, không do LLM chọn.** Path luôn là
  `type → thư mục` cộng `slugify(title) → tên file`. Vì vậy cùng một tiêu đề
  luôn rơi vào cùng một file. Đây là toàn bộ cơ chế chống trùng.
- **Merge khi trang đã tồn tại.** Lần lượt theo thứ tự:
  1. Trang `locked` (do người sửa tay) → bỏ qua.
  2. Nội dung mới đã nằm sẵn trong trang cũ → chỉ hợp nhất `sources`, không
     gọi LLM.
  3. Trang cũ dài hơn 4000 ký tự → chỉ nối thêm phần mới (LLM trả về phần
     chưa có).
  4. Còn lại → LLM viết lại cả trang, "giữ cả hai và ghi rõ khi nguồn mâu
     thuẫn".
- **Cascade khi xoá nguồn.** Trang chỉ đến từ nguồn bị xoá thì bị xoá theo.
  Trang dùng chung nhiều nguồn thì chỉ bỏ tên nguồn đó khỏi `sources`. Các
  `[[link]]` trỏ tới trang đã xoá được đổi thành chữ thường.
- **Các file cấu trúc.**
  - `index.md` và `log.md` dựng lại tự động, không cần LLM.
  - `overview.md` do LLM viết sau mỗi lần ingest.
  - `purpose.md` và `schema.md` do người dùng sửa để định hướng việc trích
    xuất.
- **Tìm kiếm.** BM25 (FTS5), trong đó tiêu đề có trọng số ×5. Sau đó có thể
  mở rộng theo BFS trên đồ thị wikilink: điểm của trang hàng xóm bằng điểm
  trang cha nhân `decay` (0.5), bỏ các trang dưới `minScore` (0.1), duyệt tối
  đa 200 node. **Không dùng embedding, không dùng vector.**
- **Tool cho agent.** `search`, `read_page`, `list_pages`, `get_graph`,
  `read_raw`, gọi qua `/v3/tools/list` và `/v3/tools/call`.

Những điểm còn hở đáng chú ý:

- Không lưu lịch sử từng trang: nội dung bị ghi đè là mất.
- Khi một nguồn thay đổi, chỉ có thêm thông tin mới; thông tin cũ của chính
  nguồn đó không bao giờ bị rút lại.
- Tool `search` qua `/tools/call` bỏ qua tham số `hop`.
- Chỉ đọc được `.md` và `.txt`; chưa chuyển PDF hay DOCX.

### 1.2 Memory Core và Proxy: điều cần học cho Wiki

- **Bộ nhớ hội thoại L0–L3.** Chuỗi xử lý: hội thoại → atom (L1) → scene (L2)
  → persona (L3). Phần này agent-tui đã port trong `internal/memory` và
  `internal/learn`.
- **Proxy không chèn kết quả tìm kiếm vào từng lượt.** Proxy từng có một
  injector chèn top-5 L1 trước mỗi câu hỏi, nhưng nó đã bị **gỡ bỏ** vì làm hỏng
  prompt cache. Hiện proxy chỉ chèn phần *ổn định* (persona, danh mục scene)
  vào system prompt và chỉ dựng một lần mỗi session. Phần còn lại là "tool"
  mà agent tự gọi khi cần (qua curl/Bash, vì proxy không thêm tool native).
- **Wiki và CodeGraph cũng vào agent theo cách đó.** Proxy chỉ chèn một khối
  `<knowledge_tools>` liệt kê tài nguyên và cách gọi. Agent tự gọi `tools/call`
  khi cần đọc trang.

Nguyên tắc rút ra cho agent-tui: **phần tổng quan đặt cố định trong prompt,
còn trang chi tiết chỉ đưa ra khi agent yêu cầu.**

## 2. Tích hợp vào agent-tui

### 2.1 Vì sao viết lại bằng Go thay vì chạy service của TencentDB

agent-tui là một binary Go duy nhất, toàn bộ dữ liệu nằm trong data dir.
Chạy thêm ba container Node cộng một proxy sẽ trái với kiến trúc đó. Thêm nữa,
thiết kế Wiki của TencentDB vốn đã "file-first" và không dùng embedding, nên
port sang Go khá gọn: không cần SQLite (BM25 tính trực tiếp trong bộ nhớ, dùng
lại tokenizer của `internal/memory`), và không cần thêm dependency nào.

### 2.2 Bố cục trên đĩa

Wiki của mỗi project nằm cạnh memory của project đó, trong data dir, không
nằm trong repo:

```
<data dir>/projects/<slug-của-project>/wiki/
  raw/                tài liệu gốc, giữ nguyên
  pages/<type>/<slug>.md
  index.md            mục lục theo type, dựng lại sau mỗi thay đổi
  log.md              nhật ký ingest/sửa, mới nhất ở trên
  overview.md         LLM viết; luôn nằm trong system prompt
  purpose.md          wiki dùng để làm gì (người dùng sửa)
  schema.md           trích xuất cái gì, như thế nào (người dùng sửa)
  history/<id>/…      bản cũ của mỗi trang trước khi bị ghi đè hoặc xoá
  state.json          sha256 + trạng thái + danh sách trang của từng file raw
  ingest.lock         khoá ingest, dùng chung cho TUI và gateway
  _debug/             output LLM không đọc được
```

Không có project thì dùng `<data dir>/wiki/`.

### 2.3 Map sang code

| Khái niệm | MemoryKnowledge | agent-tui |
|---|---|---|
| Trang, frontmatter, slug | `ingest-v2/frontmatter.ts`, `slug.ts` | `internal/wiki/page.go` |
| Lưu trữ, raw, state | `wiki-service.ts`, `index-db.ts` (SQLite) | `internal/wiki/wiki.go` (file + `state.json`) |
| Đồ thị liên kết, resolve | `manager.ts` `resolveTarget` | `internal/wiki/graph.go` |
| BM25 + BFS | FTS5 + `graph-search.ts` | `internal/wiki/search.go` |
| Ingest, chunk, FILE block, merge, cascade | `ingest-v2/*` | `internal/wiki/ingest.go` |
| Prompt | `ingest-v2/prompts.ts`, `merge.ts`, `overview.ts` | `internal/wiki/prompts.go` |
| index.md / log.md / lint | `index-builder.ts`, `log-writer.ts` | `internal/wiki/index.go` |
| Tool cho agent | `/v3/tools/call` | `internal/kit/wiki_tools.go` |
| HTTP API | `routes/wiki.ts` | `internal/server/api_wiki.go` |
| Panel | MemoryPanel | `web/admin/src/app/wiki/page.tsx` |
| LLM | Vercel AI SDK | `learn.NewLLM`: API key nếu có, không thì Claude Code CLI |

### 2.4 Những chỗ làm khác (và tốt hơn) bản gốc

- **Lịch sử trang.** Mọi lần ghi đè hoặc xoá đều chụp bản cũ vào `history/`.
- **Rút lại thông tin khi nguồn đổi.** Trang *chỉ* đến từ một file vừa thay
  đổi (và không bị khoá) sẽ bị xoá rồi viết lại từ nội dung mới, thay vì cứ
  merge chồng lên. Trang dùng chung nhiều nguồn vẫn merge như bản gốc.
- **Tool search có `hops`.** Bản gốc bỏ qua `hop` khi gọi qua `/tools/call`.
- **Danh sách trang hiện có được chọn theo độ liên quan.** Khi wiki có hơn 300
  trang, prompt chỉ liệt kê 300 trang gần với đoạn tài liệu nhất (theo BM25)
  thay vì toàn bộ.
- **Overview nhận cả mô tả trang.** Bản gốc thu thập mô tả nhưng không đưa vào
  prompt.
- **Commit tất định.** Các trang ứng viên được sắp theo `(id, nguồn)` trước khi
  merge, nên kết quả không phụ thuộc goroutine nào chạy xong trước.
- **Slug giữ dấu tiếng Việt và chữ CJK.** Ví dụ `Đăng nhập` → `đăng-nhập`. Hai
  tiêu đề khác nhau thì luôn là hai trang.

### 2.5 Agent dùng Wiki như thế nào

Trong `kit.Extras`, khi wiki có ít nhất một trang:

- **System prompt** có thêm khối `<wiki pages="N">`. Khối này hướng dẫn khi nào
  nên tra wiki và chứa `overview.md` (tối đa 2000 ký tự). Nội dung giống hệt
  nhau ở mọi lượt nên không làm hỏng prompt cache.
- **Ba tool:**
  - `wiki_search` — tham số `query`, `limit`, `hops` (0–3). Mỗi kết quả kèm
    các trang liên kết vào/ra, để agent đi theo link thay vì tìm lại.
  - `wiki_read` — tham số `refs[]`: tiêu đề, id, `index`, `overview`, hoặc
    `raw:<file>`.
  - `wiki_write` — agent ghi lại một trang `synthesis` hoặc `comparison` rút ra
    khi trả lời câu hỏi. Đây là vòng "query → filed back" trong mô hình của
    Karpathy.
- **Với engine CLI** (Claude Code, Codex…), ba tool trên được phục vụ qua
  `agent-tui kit-mcp` với tên `mcp__agent-tui__wiki_*`, cùng đường với tool
  memory và skill.
- **Trace** của mỗi lượt có trường `wiki_pages`, nên admin thấy được lượt đó
  có Wiki hay không.

### 2.6 Cách dùng

```sh
agent-tui wiki add docs/ spec.md          # đưa tài liệu vào (.md .txt .rst .adoc .org; thư mục thì quét đệ quy)
agent-tui wiki ingest                     # đọc tài liệu mới/đổi → trang; -model để chọn model
agent-tui wiki status                     # số trang, tài liệu, còn chờ, lỗi
agent-tui wiki search -hops 1 login token # tìm như agent tìm
agent-tui wiki show "Auth API"            # in một trang; hoặc index | overview | log
agent-tui wiki lint                       # link gãy, trang mồ côi, tài liệu ingest lỗi
agent-tui wiki rm spec.md                 # bỏ tài liệu; lần ingest sau gỡ các trang chỉ đến từ nó
agent-tui wiki -C ~/code/x status         # project khác
```

Trên web admin, mở mục **Wiki** để:

- tải tài liệu lên và bấm **Ingest** (tiến trình hiện trực tiếp);
- duyệt hoặc tìm trang, bấm `[[link]]` để chuyển trang;
- sửa tay một trang (trang sửa tay bị khoá, ingest sau không merge vào);
- xem overview và kết quả lint;
- sửa `purpose.md` và `schema.md`.

API tương ứng nằm dưới `/api/wiki`:

- `GET /api/wiki`, `GET /api/wiki/page`, `GET /api/wiki/search`,
  `GET /api/wiki/graph`
- `PUT /api/wiki/page`, `DELETE /api/wiki/page`
- `POST /api/wiki/raw`, `GET /api/wiki/raw`, `DELETE /api/wiki/raw`
- `PUT /api/wiki/steer`
- `POST /api/wiki/ingest` — chạy nền; xem trạng thái qua `GET /api/wiki`

### 2.6b Đưa nội dung từ conversation vào wiki

Có bốn cách để đưa nội dung vào wiki. Cả bốn đều gọi cùng một endpoint
`POST /api/wiki/add`:

- **Nhờ agent**, ví dụ "đưa đoạn này vào wiki cho tôi". Agent gọi tool
  `wiki_add`. Tool này luôn có sẵn, kể cả khi wiki còn trống.
- **Nút Add to wiki** dưới mỗi message.
- **Nút Add to wiki** trên thanh nổi lên khi bôi đen văn bản.
- **Paste text** ở tab Documents của trang Wiki.

Nội dung được lưu thành tài liệu `raw/notes/<ngày-giờ>-<slug>.md`. Đầu tài
liệu ghi nguồn: conversation nào, message thứ mấy. Sau đó gateway chạy ingest
ngay:

- Nếu đã có trang cùng chủ đề, ingest merge nội dung mới vào trang đó và nói
  rõ chỗ nào nguồn cũ và nguồn mới khác nhau.
- Nếu đang có một ingest khác chạy, tài liệu mới được xếp hàng và ingest
  chạy lại ngay khi lần trước xong.
- Nếu không có gateway, tài liệu vẫn được lưu, chờ lần `agent-tui wiki ingest`
  sau.

### 2.7 Kết quả chạy thử

Thử ingest hai README của TencentDB (MemoryKnowledge và MemoryProxy) qua
Claude Code CLI với Sonnet:

- **Kết quả:** 46 trang (2 source, 23 entity, 21 concept) trong 1 phút 39
  giây.
- **Lint sạch:** không có link gãy, không có trang mồ côi.
- **Ngôn ngữ:** trang giữ đúng ngôn ngữ của nguồn (trang từ phần tiếng Trung
  thì viết bằng tiếng Trung).
- **Tìm kiếm:** `wiki search -hops 1 memory bridge identity` trả về
  "Memory Bridge" với điểm 1.00, rồi các trang liên kết với điểm 0.50, có ghi
  "← Memory Bridge" để biết đi tới từ trang nào.

### 2.8 Chưa làm / hướng tiếp

- **Chuyển PDF, DOCX, XLSX sang Markdown khi nạp.** Hiện phải tự chuyển trước,
  ví dụ bằng docling như vault second-brain đang làm.
- **CodeGraph:** chỉ mục symbol và call graph của repo. Phần này là nửa còn lại
  của MemoryKnowledge, chưa làm.
- **Lint bằng LLM:** phát hiện các trang mâu thuẫn nhau hoặc gần trùng nhau.
  Lint hiện tại không dùng LLM.
- **Embedding + RRF:** chỉ cần khi wiki lớn tới mức BM25 không đủ. TencentDB
  mặc định cũng không bật.
- **Tự ingest khi file trong `docs/` thay đổi.** Có thể làm bằng watcher
  fsnotify có sẵn, hoặc bằng một schedule.
