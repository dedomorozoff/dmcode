# План: C2 — кластер коробок в `dialogs.go`

Источник дизайна: `docs/superpowers/specs/2026-11-17-dialogs-split-design.md`.
Исполнение — native (executing-plans), работа в `main`.

## Задача 1: перенос диапазона

1. Вырезать из `internal/ui/ui.go` строки от комментария перед `suggestBox`
   до конца `langBox` включительно (сейчас 5173–5752; сверить по
   `grep -n "func (m \*uiModel) suggestBox" / "langBox" / "filteredCommands"`).
2. Создать `internal/ui/dialogs.go`: package-заголовок, импорты, скопированный
   диапазон без изменений.
3. Починить импорты обоих файлов (компилятор подскажет: убрать из `ui.go`
   ставшие неиспользуемыми, добавить в `dialogs.go` недостающие).
4. Проверка: `go build ./...`, `gofmt`, `go vet ./internal/ui/`.

## Задача 2: верификация переноса

1. Мультимножество строк: старый `ui.go` (из `git show HEAD:internal/ui/ui.go`)
   = новый `ui.go` + `dialogs.go` минус заголовки (package/import/var-блоки).
   Скрипт-проверка, не глазами.
2. Полный `go test ./...` — зелёный.
3. `go build -o dmcode.exe .`.
4. Коммит: «Move the overlay boxes out of ui.go into dialogs.go».
5. Ledger: `.superpowers/sdd/2026-11-17-dialogs-split/progress.md`.
