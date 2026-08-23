Core Editor Polish: Выделение и Буфер обмена, Палитра команд, Сессии и LSP
Откладываем AI-фичи (M3–M4) на финал и фокусируемся на развитии возможностей самого редактора (из блока M5 — полировка и базовых фич полноценного редактора).

План ключевых улучшений редактора
1. Выделение текста и Буфер обмена (Selection & Clipboard)
Визуальное выделение: Shift+стрелки / Shift+Home / Shift+End / Shift+PgUp / Shift+PgDn выделяет текст.
Действия с выделением:
Ctrl+C (или Alt+W / Ctrl+Y) — копирование в системный/внутренний буфер обмена.
Ctrl+V — вставка из буфера обмена (с заменой выделения, если оно активно).
Ctrl+X — вырезание выделенного диапазона.
Ввод символа / Backspace / Delete при активном выделении заменяет/удаляет весь выделенный блок с одним шагом undo.
Подсветка диапазона выделения в view.go.
2. Палитра команд (Command Palette Ctrl+Shift+P / F2)
Интерактивный fuzzy-поиск по всем действиям редактора:
Save File, Close Tab, Split Vertical, Split Horizontal, Git Commit, Toggle Tree, Find & Replace, Format File, Undo, Redo, Switch Theme, Quit.
3. Сохранение и восстановление сессий (Session Restore)
Автоматическое сохранение состояния (.dmed/session.json или в ~/.config/dmed/session.json):
Открытые вкладки, путь к проекту, текущая вкладка, позиции курсора и скролла, сплиты.
При запуске dmed без аргументов — автоматическое продолжение последней сессии.
4. LSP Клиент (Language Server Protocol)
Базовый LSP клиент через stdin/stdout (например для gopls, pyright, rust-analyzer):
Диагностика (ошибки и ворнинги в gutter и под строками).
Go to definition (gd / Ctrl+]).
Форматирование документа при сохранении или по команде.
Proposed Changes
Buffer & Editor Layer (internal/buffer, internal/editor)
Добавление Selection (startLine, startCol, endLine, endCol) в буфер.
Методы: DeleteSelection(), GetSelectedText(), InsertText().
Интеграция системного clipboard через чистый Go (golang.design/x/clipboard или fallback).
Добавление режима Command Palette и сохранение сессий в internal/session.
Verification Plan
Automated Tests
Юнит-тесты на выделение и операции замены в буфере (internal/buffer/buffer_test.go).
Тесты на палитру команд и восстановление сессий (internal/editor/session_test.go, internal/editor/palette_test.go).
make vet && make test && make build.
Manual Verification
Выделение через Shift+стрелки, вырезание Ctrl+X, вставка Ctrl+V.
Вызов палитры команд и исполнение действий.
Перезапуск редактора с проверкой восстановления табов и сплитов.
Agent
dmed
Model quota reached
Your plan's baseline quota will refresh on 8/31/2026, 12:05:03 AM.



AI may make mistakes. Double-check all generated code.