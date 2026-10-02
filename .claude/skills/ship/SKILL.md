---
name: ship
description: Đưa thay đổi đang làm lên main và dùng được ngay trên máy — kiểm tra (gofmt/vet/test), tạo branch, commit, push, mở PR, merge, kéo main về, rồi build và cài lại `tui` global qua skill deploy. Trigger - "ship", "ship it", "new pr, merge, build cài lại", "tạo PR rồi merge", "đẩy lên main rồi cài lại".
---

# ship — từ thay đổi trên máy tới `tui` bản mới

Người dùng gọi `ship` là đã đồng ý cả chuỗi: commit, push, PR, **merge vào main**, cài
lại. Không hỏi lại từng bước; chỉ dừng khi một bước hỏng (xem "Dừng khi nào").

Mọi lệnh chạy ở gốc repo. Trên Windows, `go` thường **không** có trên PATH — dùng
`$env:LOCALAPPDATA\Programs\Go\bin\go.exe` (Git Bash:
`/c/Users/$USERNAME/AppData/Local/Programs/go/bin/go.exe`). "go: command not found"
không có nghĩa là chưa cài Go.

## 1. Xem lại cái sẽ ship

- `git status --short` và `git diff` (cộng `git log origin/main..HEAD` nếu đã có commit).
  Không có gì để ship thì nói vậy và dừng.
- Chỉ stage file thuộc thay đổi này, gọi đích danh — không `git add -A`. File lạ
  (binary test, log, `.env`, credential) thì bỏ ra và nhắc người dùng.

## 2. Kiểm tra

```sh
# gofmt trên file đã đổi, bỏ CR trước: repo checkout với core.autocrlf=true nên
# `gofmt -l .` liệt kê MỌI file — đó là CRLF, không phải lỗi format.
for f in $(git diff --name-only --diff-filter=d origin/main -- '*.go'; git ls-files -o --exclude-standard '*.go'); do
  [ -n "$(tr -d '\r' < "$f" | gofmt -l)" ] && echo "unformatted: $f"
done
go vet ./...
go test ./...
```

Hỏng thì dừng, đưa nguyên văn lỗi. Không ship code đỏ, không bỏ test để cho qua.

## 3. Branch + commit + push

- Đang ở `main` → `git checkout -b <loại>/<slug-ngắn>` (`fix/…`, `feat/…`, `perf/…`,
  `chore/…`). Đang ở branch khác `main` → dùng luôn branch đó.
- Message theo kiểu repo: tiêu đề là một câu mệnh lệnh tiếng Anh, không prefix
  conventional (vd "Let auto run tools that name no path"); thân bài nói vì sao,
  ngắt dòng ~72 cột; kết thúc bằng dòng attribution mà system-reminder đưa.
- `git push -u origin <branch>`.

## 4. PR + merge

```sh
gh pr create --base main --title "<tiêu đề commit>" --body "<Problem / Fix / Tests>"
gh pr merge <số> --merge --delete-branch     # repo dùng merge commit
git checkout main && git pull
```

- Body: **Problem**, **Fix**, **Tests** (lệnh đã chạy ở bước 2 và kết quả), kết thúc
  bằng dòng attribution cho PR.
- **GitHub hay trả 5xx/timeout dù PR đã được tạo.** Trước khi thử lại
  `gh pr create`, luôn kiểm tra
  `gh pr list --head <branch> --state all --json number,state,url` — có rồi thì
  dùng số đó, đừng tạo PR thứ hai. Merge cũng vậy: kiểm tra
  `gh pr view <số> --json state` trước khi thử lại.

## 5. Build + cài lại

Gọi đúng script của skill deploy, không tự làm lại các bước của nó:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .claude/skills/deploy/install.ps1
```

macOS/Linux: `make install` (symlink `tui` đã có từ lần deploy đầu thì không cần làm lại).

Dòng `binary : … (commit <sha>)` phải trùng `git rev-parse --short HEAD` của main
sau khi pull. Lệch nghĩa là cài nhầm bản — báo, đừng nói là xong.

## Dừng khi nào

Dừng, báo nguyên văn, không tự "sửa cho qua":
- test/vet/gofmt hỏng;
- push bị từ chối, PR có conflict, hoặc merge bị chặn bởi check/review bắt buộc
  (không dùng `--admin`, không force push);
- script cài lỗi hoặc dòng `verify` không OK.

## Báo lại

Gọn, một khối:
- PR: link + trạng thái MERGED;
- main: commit merge;
- đã cài: commit + đường dẫn binary, dòng `verify`;
- nhắc: phiên `tui` đang mở vẫn chạy bản cũ — thoát và mở lại để dùng bản mới.
