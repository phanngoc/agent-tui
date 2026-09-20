# Mô hình vận hành quyết định format lưu trữ

Tài liệu này trả lời một câu hỏi đã quay lại nhiều lần: *"sao không dùng SQLite như
opencode / OpenClaw / Hermes?"* — và đặt sẵn điều kiện để lần sau không phải tranh
luận lại từ đầu.

Nó là tài liệu chị em của [cách-lưu-raw-message.md](cách-lưu-raw-message.md). Tài
liệu kia so sánh *hình dạng dữ liệu*; tài liệu này giải thích *vì sao* mỗi hệ có
hình dạng đó.

Trạng thái: phân tích. Chưa có thay đổi code nào.

---

## 1. Luận đề

Không ai ngồi xuống và chọn "JSONL hay SQLite". Bốn biến của mô hình vận hành quyết
định thay họ, và khi cả bốn đã cố định thì chỉ còn đúng một đáp án hợp lý.

| # | Biến | Câu hỏi |
|---|---|---|
| 1 | Writer cardinality | Bao nhiêu process được phép ghi cùng lúc? |
| 2 | Lifetime ratio | Process sống lâu hơn session, hay session sống lâu hơn process? |
| 3 | Reader locality | Reader đứng trong process, ngoài process, hay ngoài máy? |
| 4 | Bản chất transcript | Log của quá khứ, hay state hiện tại nhiều bên truy vấn? |

Claude Code và OpenClaw khác nhau ở cả bốn. Họ không bất đồng quan điểm — họ đang
giải hai bài toán khác nhau.

---

## 2. Claude Code: transcript là journal của một process

```
  người dùng ở terminal
          │  (đồng bộ, một người, đang nhìn)
          ▼
  ┌───────────────────────┐
  │  claude  (1 process)  │
  │                       │
  │   Message[]  ◄── nguồn sự thật, sống trong RAM
  │      │                │
  │      │ append         │
  │      ▼                │
  └──────┼────────────────┘
         │ 1 writer, độc quyền
         ▼
   <session-id>.jsonl   ◄── KHÔNG phải nguồn sự thật.
                            Là journal để dựng lại RAM.
         │
         │ đọc đúng 1 lần, lúc --resume
         ▼
   process MỚI dựng lại Message[]
```

| Biến | Giá trị |
|---|---|
| Writer cardinality | **1**, bảo đảm bởi vật lý — process nào giữ terminal thì process đó ghi |
| Lifetime ratio | process ⊇ một đoạn session; session nghỉ khi process chết |
| Reader locality | **in-process** — không ai đọc file khi session đang chạy, kể cả chính nó |
| Bản chất | **log của quá khứ** |

### Vì sao JSONL là bắt buộc, không phải sở thích

Khi file chỉ là journal để dựng lại RAM, nó có đúng hai yêu cầu:

1. Ghi thêm phải rẻ và không hỏng cái đã ghi → **append-only**
2. Đọc lại phải dựng được `Message[]` → **sequential scan, một lần**

Append-only JSONL đáp ứng cả hai ở mức tối ưu lý thuyết. Ngược lại, **một journal mà
bạn ghi đè lại toàn bộ là mâu thuẫn về khái niệm** — bạn đang phá chính thứ mà
journal tồn tại để bảo vệ.

Nên "quét cả file" trong bảng so sánh dễ bị đọc sai. Nó không phải thao tác truy vấn
tồi; nó là **thao tác khôi phục**, chạy một lần. Với khôi phục thì `O(n)` sequential
là nhanh nhất có thể — bạn cần mọi byte, đọc tuần tự, không có gì nhanh hơn.

### Reader thứ hai mà Claude Code cố ý phục vụ

Có một reader không nằm trong sơ đồ: **hệ sinh thái**. Con người với `grep`,
dashboard, tool phân tích, script CI. Với họ, format file là **API công khai**.

Đây là quyết định sản phẩm, không phải kỹ thuật, và nó khoá chặt lựa chọn: một khi
transcript đọc được bằng `jq`, mọi thứ mọc lên xung quanh nó, và đổi format trở thành
breaking change với những người bạn chưa từng gặp. Claude Code trả cho quyền đó bằng
việc từ bỏ index.

---

## 3. OpenClaw: transcript là state đang sống

> Chi tiết version lấy từ doc của OpenClaw (bản migration 2026-09-01). Cơ chế bên
> dưới suy ra từ nguyên lý — nếu số version sai thì lập luận vẫn đứng, chỉ là đứng
> cho một kiến trúc có hình dạng đó.

```
   client A        client B        restart / recovery
      │               │                    │
      └───────┬───────┘                    │
              ▼                            │
      ┌───────────────┐                    │
      │    gateway    │ ◄──────────────────┘
      └───────┬───────┘
              │  đọc: "trang đầu", "đếm token", "khôi phục"
              │  ── đều là truy vấn CÓ GIỚI HẠN, KHÔNG dựng cả transcript
              ▼
   ┌──────────────────────────────┐
   │  openclaw-agent.sqlite       │ ◄── NGUỒN SỰ THẬT nằm ở đây
   │                              │
   │  transcript_events (cây)     │◄─┐ agent process (sống lâu,
   │  transcript_archives         │  │ không người trông)
   └──────────────────────────────┘  │ ghi đồng thời với gateway
                                     │
                              ┌──────┴──────┐
                              │ agent proc  │
                              └─────────────┘
```

| Biến | Giá trị |
|---|---|
| Writer cardinality | **≥2** — gateway và agent process cùng chạm |
| Lifetime ratio | **session ⊋ process** — session sống qua nhiều lần agent chết/khởi động lại |
| Reader locality | **cross-process**, đọc **online** trong lúc session đang chạy |
| Bản chất | **state hiện tại**, nhiều bên truy vấn |

### Bốn thứ, mỗi thứ tự nó đã đủ phá vỡ file phẳng

| Yêu cầu vận hành | Phá vỡ điều gì |
|---|---|
| Nhiều process cùng ghi | append-only không có giao thức đồng thời — phải **tự viết** lock, và viết tệ hơn |
| "Đừng dựng cả transcript chỉ để đếm token" | đọc **chọn lọc** ⇒ bắt buộc có index ⇒ RAM hết làm index được |
| Compaction sửa tại chỗ (`active` ↔ `archives`) | **mâu thuẫn trực tiếp** với append-only |
| Fork/branch là tính năng | cần **tham chiếu**, không phải bản sao |

Trúng bốn trên bốn. Ở điểm này SQLite không còn là lựa chọn — nó là thứ duy nhất còn
lại. Giữ file phẳng nghĩa là tự viết lock, tự viết index, tự viết archive, tự viết
reference — tức là tự viết một database, bản chưa qua hai mươi năm kiểm nghiệm.

---

## 4. Nguyên lý trung tâm: RAM chính là index

Đây là câu trả lời gọn nhất.

Claude Code **có** index. Nó chỉ không nằm trong storage layer:

| Vai trò index | Đặt ở đâu |
|---|---|
| primary key | **tên file** (`<session-id>.jsonl`) |
| partition | **thư mục theo project** |
| working set / random access | **`Message[]` trong RAM của process** |

Process giữ trọn session trong RAM suốt đời nó, nên index trên đĩa sẽ **dư thừa** —
nó index lại đúng thứ đã nằm sẵn trong bộ nhớ, và còn phải giữ đồng bộ với nó. Đĩa
chỉ cần đủ khả năng *dựng lại* RAM một lần. Đó chính xác là việc của một journal.

Khi nào RAM thôi làm index được?

> Khi không còn **một** process nào giữ trọn session suốt đời nó.

Hai cách điều đó xảy ra:

- **session sống lâu hơn process** → không có RAM liên tục nào để mà index
- **nhiều process cần các lát cắt khác nhau** → RAM của process A vô hình với process B

Khi index buộc phải rời RAM, nó phải hạ cánh xuống một nơi nhiều process cùng nhìn
thấy, có kiểm soát đồng thời. Nơi đó có đúng một cái tên:

> **Một index trên đĩa, chia sẻ giữa nhiều process, có kiểm soát đồng thời — đó chính
> là định nghĩa của database.**

OpenClaw không "chọn SQLite". Họ chọn một mô hình vận hành có gateway và agent sống
lâu, và SQLite rơi ra từ lựa chọn đó. Cuộc tranh luận storage của họ kết thúc ngay
khi vẽ xong sơ đồ process, trước khi ai kịp bàn JSON hay SQL.

---

## 5. Hai trục độc lập, không phải một thang đo

Bảng so sánh năm hệ dễ gây cảm giác có một thang duy nhất từ "file" tới "DB". Thực ra
có hai trục:

- **Partitioning** — dữ liệu bị cắt theo ranh giới nào? (quyết định blast radius và
  sharding tự nhiên)
- **Indexing** — truy cập chọn lọc được phục vụ bởi cái gì?

|  | index = RAM | index = B-tree trên đĩa |
|---|---|---|
| **partition = per-session** | **Claude Code / Codex** | — |
| **partition = per-agent** | — | **OpenClaw** |
| **partition = không** | — | **Hermes** |

Mỗi ô kể một câu chuyện vận hành:

**OpenClaw giữ partitioning của trường phái file** (một DB mỗi agent) **và chỉ mua
index + concurrency của trường phái DB.** Họ không về một DB toàn cục. Đó là lựa chọn
lai có chủ đích: giữ blast radius nhỏ, giữ agent cô lập, chỉ trả cho thứ thật sự cần.
Cái giá là không query xuyên agent được.

**Hermes từ bỏ partitioning hoàn toàn**, và lý do lộ ra ở cột Search: FTS5 xuyên toàn
bộ lịch sử. Không thể có full-text search xuyên mọi session mà vẫn giữ dữ liệu chia
nhỏ — index toàn cục đòi một không gian toàn cục. **Search là áp lực duy nhất buộc bỏ
partitioning**, và nó đắt: khoá compaction xuyên process, retry có jitter, 10 bảng,
23 lần migration.

---

## 6. Hai câu hỏi vận hành không bảng nào ghi

### "Ai serialize các thao tác?"

| | Claude Code | OpenClaw |
|---|---|---|
| | **Con người.** Một người, một terminal, gõ tuần tự. Con người *là* lock. | **Không ai.** Gateway nhận request bất đồng bộ, agent chạy nền. Phải có giao thức. |

Đây là lý do sâu nhất và dễ bị bỏ qua nhất, vì nó vô hình: Claude Code được tặng miễn
phí một concurrency control hoàn hảo — sự tuần tự của một người ngồi trước bàn phím.
OpenClaw mất món quà đó ngay khoảnh khắc thêm gateway, và phải mua lại bằng WAL.

### "Ai khôi phục sau crash, và có ai đang nhìn không?"

| | Claude Code | OpenClaw |
|---|---|---|
| Crash nghĩa là | terminal đóng — người dùng **thấy** | agent chết lúc 3 giờ sáng — **không ai thấy** |
| Ai khôi phục | chính người đó, gõ `--resume` | một process khác, tự động, có thể giữa turn |
| Mất dòng cuối dở | chấp nhận được — người dùng biết mình vừa làm gì | **không chấp nhận được** — không ai biết đã commit tới đâu |

"Mất dòng cuối thì bỏ dòng cuối" là recovery model hợp lệ **khi có người chứng kiến**.
Nó sụp đổ khi không có. Và "biết chính xác cái gì đã commit" chính là định nghĩa của
transaction.

---

## 7. Cái giá mỗi bên tự nguyện trả

| Claude Code trả | OpenClaw trả |
|---|---|
| không search xuyên session | không query xuyên agent |
| compaction thủ công, đẻ segment mới | cây khó debug — phải duyệt, không "đọc" được |
| file phình vô hạn | schema version + migration |
| full scan lúc resume | **phá vỡ tool bên thứ ba** (2026-09-01) |

Dòng cuối là bài học đắt nhất và không có trên bảng độ phức tạp nào: `agentsview`,
`codeburn`, `llm-systems-manager` đều dashboard trống sau migration. Vì file phẳng là
**API công khai** còn SQLite là **cấu trúc nội bộ**.

> Chuyển trường phái không phải refactor storage. Nó là phá vỡ hợp đồng với những
> người dùng bạn chưa từng gặp.

Đó là chi phí chỉ mô hình vận hành mới nhìn thấy, và là lý do chính đáng nhất để trì
hoãn việc chuyển phe càng lâu càng tốt.

---

## 8. agent-tui đứng ở đâu

Đối chiếu bốn biến, kiểm chứng được trong code:

| Biến | agent-tui | Giống ai |
|---|---|---|
| Writer cardinality | **1** — mọi mutation trong vòng lặp `Update` của Bubble Tea; `Save` đưa clone cho **một** writer goroutine (`manager.go:265`) | Claude Code |
| Lifetime ratio | process ⊇ đoạn session | Claude Code |
| Reader locality | **in-process** — `Restore` nạp hết vào `m.sessions`, UI render từ RAM | Claude Code |
| Bản chất | log của quá khứ | Claude Code |

**Bốn trên bốn giống Claude Code.** Trường phái đã được mô hình vận hành quyết định
xong từ lâu — chỉ là chưa ai viết nó ra thành format.

`/btw` và side chat **không** lật biến nào: chúng chạy trong cùng process, cùng vòng
lặp `Update`; `m.runs` (`model.go:202`) chỉ là map các `CancelFunc`. Vẫn một writer.

### Phát hiện: với engine = CLI, agent-tui đã là hệ hai nguồn sự thật

```go
"--output-format", "stream-json"
a = append(a, "--resume", t.ExternalID)
```
— `internal/engine/claude.go:27,35`

Khi `Engine == "claude"`, một cuộc hội thoại được lưu **hai lần, bởi hai hệ thống
khác nhau**:

```
  agent-tui  ──spawn──►  claude --resume <ExternalID>
      │                        │
      ▼                        ▼
  <id>.json                <ExternalID>.jsonl
  (rewrite cả file)        (append-only)
      │                        │
      └──── cùng một hội thoại ┘
           nối bằng ExternalID
```

Hệ quả kiến trúc: với session chạy engine ngoài, **`.json` của agent-tui không phải
nguồn sự thật — nó là một view/cache dựng lại được.** Nguồn sự thật nằm trong
transcript của CLI kia, và `ExternalID` là con trỏ tới nó.

Một cache có tiêu chuẩn durability thấp hơn nhiều so với một system of record. Nó cho
phép những đánh đổi mà nguồn sự thật không cho phép. Nhưng điều đó **không** áp dụng
cho nhánh `engine == "api"`, nơi `.json` là nguồn sự thật duy nhất.

> Hai nhánh này có yêu cầu durability khác nhau nhưng đang dùng chung một đường ghi.
> Đáng ghi nhận độc lập với chuyện JSONL hay không.

---

## 9. Điều kiện kích hoạt

Đừng tranh luận SQLite theo cảm tính. Đặt trigger và chờ nó nổ.

| Nếu điều này xảy ra | Biến bị lật | Kết luận |
|---|---|---|
| `agent-tui serve` / daemon / bất kỳ process thứ hai nào ghi | writer cardinality → ≥2 | **SQLite, hết bàn.** Tự viết lock luôn là quyết định sai. |
| Session cần sống và chạy tiếp khi TUI đóng | lifetime ratio đảo | SQLite |
| Cần search xuyên >200 session | index phải rời RAM | index rời (§5, kiểu Hermes) — nhưng *chỉ* index; data vẫn là file |
| Chỉ cần fork rẻ | — | **không** cần DB. `parent_id` + offset trong file là đủ. |

Chú ý dòng cuối. Fork **không lật biến nào cả**: vẫn một writer, vẫn một process, vẫn
RAM làm index. OpenClaw dùng cây id/parentId vì họ *đã* ở trong DB rồi nên cây là miễn
phí — không phải vì fork đòi hỏi DB.

> Lấy cấu trúc dữ liệu của OpenClaw mà bỏ lại điều kiện vận hành khiến nó đúng là
> cargo cult.

---

## 10. Việc cần làm, suy ra từ mô hình

Mô hình đã chốt: **đĩa là journal, RAM là index.** Hai chỗ trong code hiện vi phạm nó.

### Vi phạm 1 — rewrite toàn bộ file (vế "đĩa là journal")

`Save` → `clone()` → `json.Marshal` cả session → ghi lại cả file
(`manager.go:239, 272`). Sau message thứ `k`, file dài `k·m̄`, nên tổng bytes ghi
trong đời session là `m̄·n(n+1)/2` — **bậc hai**, hệ số khuếch đại `(n+1)/2`.

Đo thật (`~/.local/share/agent-tui/sessions`, 8 file, 1.7 MB):

```
lớn nhất:  496 578 B / 164 msgs  →  m̄ ≈ 3.0 KB
Σ ghi    =  3.0 KB × 164×165/2  ≈  41 MB   (khuếch đại ≈ 82×)
```

41 MB rải trên nhiều giờ là con số không đáng gì trên NVMe. Vấn đề không nằm ở giá
trị mà ở **đạo hàm** — session dài gấp đôi thì chi phí gấp bốn:

| n | Σ ghi |
|---|---|
| 164 | 41 MB |
| 500 | 375 MB |
| 1 000 | 1.5 GB |
| 2 000 | 6.0 GB |

Hệ không hỏng dần mà hỏng đột ngột, và không có cảnh báo ở giữa.

**Van áp suất đã được lắp sẵn**, và nó nói lên điều gì:

```go
select {
case m.saves <- snapshot:
default:                    // ← rơi im lặng
}
```

Đây là load shedding, có mặt vì (đúng đắn) không muốn đĩa chậm làm nghẽn phím gõ.
Nhưng sự tồn tại của van là lời thừa nhận rằng **chi phí ghi không bị chặn trên** —
với `W = O(1)` thì không cần van. Hệ quả là invariant durability thật sự không phải
"message đã lưu" mà là "message sẽ được lưu, trừ khi writer đang bận, và khi đó nó
biến mất không dấu vết". Van càng dễ mở khi `n` càng lớn: **session càng dài, xác
suất mất message càng cao.**

Ghi chú: `write()` **có** temp + rename (`manager.go:272-282`), nên ghi là atomic.
Mất điện giữa chừng không mất session. Đó không phải vấn đề ở đây.

### Vi phạm 2 — `Restore` parse 500 KB để lấy 150 byte (vế "RAM là index")

```go
func (m *Manager) Restore(limit int) {
    entries, _ := os.ReadDir(m.dir)      // dir TOÀN CỤC: config.DataDir()
    for _, e := range entries {
        b, _ := os.ReadFile(...)          // đọc TOÀN BỘ file
        json.Unmarshal(b, &s)             // parse TOÀN BỘ messages
        if s.Root != m.root { continue }  // rồi mới vứt đi
    }
    sort.Slice(...)                       // sort cần Updated của tất cả
    loaded = loaded[:limit]               // cắt còn 20 — SAU khi đã trả tiền
}
```

Gọi từ `main.go:90` là `Restore(20)`. Ba khuyết tật chồng lên nhau:

1. **Thư mục toàn cục.** Session của mọi project nằm chung, nên `Restore` là
   `O(B_toàn_hệ_thống)`, không phải `O(B_project_này)`. Mở project 3 file vẫn phải
   parse session của project 200 file.
2. **Header và body bị hợp nhất.** Danh sách session cần đúng
   `{ID, Title, Root, Updated, Engine}` ≈ **150 B**. Để lấy chúng, code parse cả
   `Messages []Message` ≈ **500 000 B**. Khuếch đại đọc ≈ **3 300×**.
3. **`limit` áp dụng sau.** Không short-circuit được, vì `sort` theo `Updated` cần
   trường đó từ tất cả — mà nó nằm sau body trong cùng file.

Và vì UI render từ RAM, **toàn bộ chi phí đọc đĩa dồn vào đúng một thời điểm:
startup**, đồng bộ, blocking, ngay trước khung hình đầu tiên.

| S | B_total | `Restore` (json ≈ 150 MB/s) | alloc |
|---|---|---|---|
| 8 (hôm nay) | 1.7 MB | ~11 ms | ~5 MB |
| 50 | 25 MB | ~170 ms | ~75 MB |
| 200 | 100 MB | **~670 ms** | **~300 MB** |
| 500 | 250 MB | **~1.7 s** | ~750 MB |

670 ms blocking trước khung hình đầu của một TUI là hỏng sản phẩm, không phải chậm.

> **Write `O(n²)` là khoản nợ tương lai. Read `O(B_total)` là hoá đơn đang đến.**

---

## 11. Hình dạng đích, và cái giá phải trả tỉnh táo

```
sessions/<project-hash>/<id>.meta.json    ~200 B   rewrite toàn bộ — O(1) vì kích thước hằng
sessions/<project-hash>/<id>.jsonl        ~500 KB  append-only, không bao giờ rewrite
```

Rewrite-toàn-bộ **không sai** — nó chỉ sai khi áp lên thứ tăng trưởng theo `n`. Áp
lên header kích thước hằng, nó là thao tác rẻ nhất có thể và giữ nguyên temp+rename
đang có.

Consistency giữa hai file **không cần giao dịch**, vì `.meta` là dữ liệu **dẫn xuất**:
ghi `.jsonl` trước, `.meta` sau. Crash ở giữa → meta cũ hơn body → lần mở sau scan
body để tái tạo. Sai lệch tự lành.

> Không bao giờ để hai nguồn sự thật. Để một nguồn và một cache dựng lại được.

`Restore` khi đó là `O(S)` syscall × ~200 B — 200 session thành ~40 KB, ~2 ms, thay
vì ~670 ms.

### Khó khăn thật mà khuyến nghị "chuyển sang JSONL" hay bỏ qua

Chính codebase đã ghi lại nó:

```go
// The copy has to reach inside each message: a tool call is filled in
// after its message is already in the transcript...
```
— `manager.go:290-293`

**`ToolCall.Result` được điền SAU khi message đã vào transcript.** Append-only theo
định nghĩa không sửa được thứ đã ghi. Ba lời giải:

| | Cách | Ưu | Nhược |
|---|---|---|---|
| V1 | Ghi mỗi `ToolCall` thành record riêng (cách Claude Code làm) | append thuần, sống sót crash giữa turn | reader phải reassemble; đổi shape dữ liệu |
| V2 | Append patch record, replay last-wins | giữ nguyên `Message` struct | file phình; replay `O(records)`; khó debug bằng mắt |
| **V3** | **Chỉ append khi turn xong** — in-flight giữ trong RAM | đơn giản nhất, ~20 dòng, giữ nguyên mọi struct | mất turn dở nếu crash giữa chừng |

**Chọn V3**, vì lập luận về *baseline chứ không phải về lý tưởng*: state in-flight vốn
đã là runtime-only (`s.Calls`, `s.Output`, `s.RunAt` đều `json:"-"`), và van `default:`
vốn đã cho phép mất message im lặng. V3 không làm durability tệ đi so với hôm nay — nó
làm cho ranh giới mất mát trở nên **tường minh và xác định** thay vì phụ thuộc độ sâu
hàng đợi. Đổi một failure mode ngẫu nhiên lấy một failure mode phát biểu được là cải
thiện, kể cả khi tần suất không đổi.

Nâng lên V1 khi turn đủ dài để mất nó là đau thật (agent chạy 10 phút, 40 tool call).

### Thứ tự thi công

Xếp theo (thiệt hại × xác suất) ÷ chi phí:

| # | Việc | Sửa | Từ → đến | Công |
|---|---|---|---|---|
| **1** | Tách `.meta.json` khỏi body | `Restore` | `O(B_total)` → `O(S)` | ~1 giờ |
| **2** | Body sang `.jsonl` append (V3) | `Save` | `O(n²)` → `O(n)` | ~3 giờ |
| 3 | Shard dir theo project hash | `Restore` | hằng số ÷ số project | ~30 phút, kèm #1 |
| 4 | Bỏ van `default:`, coalesce theo session id | durability | mất ngẫu nhiên → không mất | ~1 giờ, sau #2 |
| 5 | Fork bằng `parent_id` + offset | `Fork` | `O(n)` → `O(1)` | ~4 giờ, khi fork được dùng thật |
| 6 | SQLite | tất cả | — | ~2 tuần + migration; **chỉ khi §9 nổ** |

Mục 4 cho thấy tính dây chuyền của một sửa chữa đúng tầng: van `default:` là *triệu
chứng* của `W = O(n)`. Chữa nguyên nhân thì triệu chứng tự mất, và durability về miễn
phí mà không phải đánh đổi latency. Dấu hiệu nhận biết sửa đúng tầng là nó **xoá bỏ**
những thứ khác thay vì cộng thêm vào.

Hai việc đầu là phần lớn giá trị với phần nhỏ chi phí, và cả hai đều không cần schema,
không migration, không mất `grep`/`jq`, không phá tool bên thứ ba — tức là không dính
bài học §7.

---

## 12. Tóm lại

Claude Code chọn file vì transcript của nó là **journal của một process có một người
ngồi trước nó**: một writer do vật lý bảo đảm, RAM đã làm index, đĩa chỉ cần dựng lại
được RAM một lần — và format file là hợp đồng công khai với cả một hệ sinh thái.

OpenClaw chọn SQLite vì transcript của nó là **state đang sống mà nhiều process cùng
chạm, không ai ngồi trông**: RAM không còn là index chung được, đọc phải có giới hạn,
compaction phải sửa tại chỗ, và không còn con người nào để serialize.

agent-tui có mô hình vận hành của Claude Code — bốn trên bốn. Trường phái đã được
quyết định sẵn. Việc còn lại không phải chọn phe mà là làm cho format khớp với mô hình
đã có.
