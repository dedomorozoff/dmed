// Package i18n provides UI string translation (en/ru).
//
// A Translator is an immutable value holding the active language; it is
// stored on the editor model and resolved against read-only catalogs, so
// there is no global mutable state.
package i18n

import (
	"fmt"
	"strings"
)

// Lang identifies a supported interface language.
type Lang string

const (
	En Lang = "en"
	Ru Lang = "ru"
)

// Resolve maps a config value to a supported language, defaulting to English.
func Resolve(name string) Lang {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "ru", "russian", "русский":
		return Ru
	default:
		return En
	}
}

// Translator resolves UI strings for a fixed language.
type Translator struct {
	lang Lang
}

// New returns a Translator for lang.
func New(lang Lang) Translator { return Translator{lang: lang} }

// Lang returns the active language.
func (t Translator) Lang() Lang { return t.lang }

// T returns the translated string for key, formatted with args.
// Unknown keys fall back to English, then to the key itself.
func (t Translator) T(key string, args ...any) string {
	cat := enCatalog
	if t.lang == Ru {
		cat = ruCatalog
	}
	s := cat[key]
	if s == "" {
		s = enCatalog[key]
	}
	if s == "" {
		s = key
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

var enCatalog = map[string]string{
	// Git status line / panel
	"git.no_repo":         "no repository",
	"git.init_hint":       "(i: init repo, esc/q: close)",
	"git.hints":           "(space: stage, a: all, c: commit, d: diff, l: log, b: branch, r: refresh, q: close)",
	"git.status_count":    "%d changed, %d staged",
	"git.norepo_msg":      "no git repo",
	"git.init_failed":     "git init failed: %s",
	"git.init_ok":         "initialized git repo at %s",
	"git.already_repo":    "already a git repo",
	"git.no_dir":          "no directory to init",
	"git.staged":          "staged: %s",
	"git.unstaged":        "unstaged: %s",
	"git.stage_error":     "stage error: %s",
	"git.unstage_error":   "unstage error: %s",
	"git.staged_all":      "staged all (%d files)",
	"git.refreshed":       "refreshed",
	"git.status_error":    "git status error: %s",
	"git.commit_failed":   "commit failed: %s",
	"git.committed":       "committed: %s",
	"git.log_error":       "git log error: %s",
	"git.branches_error":  "git branches error: %s",
	"git.switched":        "switched to %s",
	"git.switch_error":    "switch error: %s",
	"git.already_on":      "already on %s",
	"git.created":         "created branch %s",
	"git.create_error":    "create error: %s",
	"git.new_branch_name": "new branch name:",
	"git.diff_error":      "diff error: %s",
	"git.log_hint":        "(j/k: navigate, Tab: diff focus, esc/q: back, r: refresh)",
	"git.no_commits":      "no commits",
	"git.commit_count":    "(%d commits)",
	"git.branch_hint":     "(j/k: switch, Enter: checkout, n: new branch, esc/q: back, r: refresh)",
	"git.branch_new_hint": "(Enter: create, Esc: cancel)",

	// Git labels rendered in the status line
	"git.prefix_status":    " git ",
	"git.prefix_log":       " LOG ",
	"git.prefix_branch":    " BRANCH ",
	"git.new_branch_label": " new branch: ",

	// Help panel
	"help.title":             "dmed — keys",
	"help.close_hint":        "(F1/Esc closes)",
	"help.save":              "save active tab (untitled: Save As)",
	"help.palette":           "Command Palette (File: New, Save, ...)",
	"help.select":            "select text range",
	"help.clipboard":         "copy / cut / paste",
	"help.search":            "search in file (Enter/F3 next, Shift+F3 prev)",
	"help.replace":           "search & replace (Tab switch, Enter rep, Ctrl+A all)",
	"help.git_panel":         "Git panel; Ctrl+B switches back to tree",
	"help.git_diff":          "side-by-side diff vs HEAD",
	"help.hunk":              "jump to previous / next Git hunk",
	"help.finder":            "fuzzy file finder",
	"help.open":              "open file by path",
	"help.terminal":          "toggle bottom terminal (Esc closes)",
	"help.chat":              "AI chat panel (local Ollama, right side)",
	"help.inline":            "AI inline rewrite (select text, describe change)",
	"help.agent":             "background agent tasks panel (queue, progress, cancel)",
	"help.tree":              "project tree; Ctrl+G switches to Git",
	"help.tree_nav":          "navigate, open, fold",
	"help.tab_switch":        "switch tabs in active pane",
	"help.tab_jump":          "jump to tab N",
	"help.split_vert":        "split vertical (side by side)",
	"help.split_horiz":       "split horizontal (stacked)",
	"help.split_focus":       "focus other pane",
	"help.split_close":       "close pane (unsplit)",
	"help.tab_close":         "close tab (last quits)",
	"help.move":              "move cursor",
	"help.edit":              "edit text",
	"help.undo":              "undo / redo",
	"help.lines":             "delete line / duplicate line",
	"help.multicursor_word":  "multi-cursor: add cursor at next occurrence of word",
	"help.multicursor_click": "add cursor at click position",
	"help.multicursor_esc":   "exit multi-cursor mode",
	"help.move_line":         "move line up / down",
	"help.toggle_help":       "toggle this help",
	"help.quit":              "quit",
}

var ruCatalog = map[string]string{
	"git.no_repo":         "репозиторий не найден",
	"git.init_hint":       "(i: init, esc/q: закрыть)",
	"git.hints":           "(space: stage, a: всё, c: commit, d: diff, l: log, b: ветки, r: refresh, q: закрыть)",
	"git.status_count":    "изменено: %d, staged: %d",
	"git.norepo_msg":      "нет git-репозитория",
	"git.init_failed":     "git init не удался: %s",
	"git.init_ok":         "репозиторий инициализирован: %s",
	"git.already_repo":    "уже git-репозиторий",
	"git.no_dir":          "нет каталога для init",
	"git.staged":          "в индекс добавлено: %s",
	"git.unstaged":        "из индекса убрано: %s",
	"git.stage_error":     "ошибка stage: %s",
	"git.unstage_error":   "ошибка unstage: %s",
	"git.staged_all":      "в индекс добавлено файлов: %d",
	"git.refreshed":       "обновлено",
	"git.status_error":    "ошибка git status: %s",
	"git.commit_failed":   "коммит не удался: %s",
	"git.committed":       "закоммичено: %s",
	"git.log_error":       "ошибка git log: %s",
	"git.branches_error":  "ошибка списка веток: %s",
	"git.switched":        "переключено на %s",
	"git.switch_error":    "ошибка переключения: %s",
	"git.already_on":      "уже на %s",
	"git.created":         "создана ветка %s",
	"git.create_error":    "ошибка создания: %s",
	"git.new_branch_name": "имя новой ветки:",
	"git.diff_error":      "ошибка diff: %s",
	"git.log_hint":        "(j/k: навигация, Tab: diff, esc/q: назад, r: refresh)",
	"git.no_commits":      "нет коммитов",
	"git.commit_count":    "(%d коммитов)",
	"git.branch_hint":     "(j/k: switch, Enter: checkout, n: new, esc/q: назад, r: refresh)",
	"git.branch_new_hint": "(Enter: создать, Esc: отмена)",

	"git.prefix_status":    " git ",
	"git.prefix_log":       " ЛОГ ",
	"git.prefix_branch":    " ВЕТКИ ",
	"git.new_branch_label": " новая ветка: ",

	// Help panel
	"help.title":             "dmed — клавиши",
	"help.close_hint":        "(F1/Esc закрывает)",
	"help.save":              "сохранить активную вкладку (без имени: Сохранить как)",
	"help.palette":           "Палитра команд (Файл: новый, сохранить, ...)",
	"help.select":            "выделить текст",
	"help.clipboard":         "копировать / вырезать / вставить",
	"help.search":            "поиск в файле (Enter/F3 далее, Shift+F3 назад)",
	"help.replace":           "поиск и замена (Tab переключить, Enter заменить, Ctrl+A все)",
	"help.git_panel":         "Git-панель; Ctrl+B возврат к дереву",
	"help.git_diff":          "side-by-side diff против HEAD",
	"help.hunk":              "перейти к предыдущему / следующему хунку",
	"help.finder":            "быстрый поиск файлов",
	"help.open":              "открыть файл по пути",
	"help.terminal":          "включить/выключить нижний терминал (Esc закрыть)",
	"help.chat":              "AI чат-панель (локальный Ollama, справа)",
	"help.inline":            "AI inline-переписывание (выделите текст, опишите изменение)",
	"help.agent":             "панель фоновых агентов (очередь, прогресс, отмена)",
	"help.tree":              "дерево проекта; Ctrl+G переход к Git",
	"help.tree_nav":          "навигация, открыть, свернуть",
	"help.tab_switch":        "переключение вкладок в активной панели",
	"help.tab_jump":          "перейти к вкладке N",
	"help.split_vert":        "вертикальный сплит (рядом)",
	"help.split_horiz":       "горизонтальный сплит (друг над другом)",
	"help.split_focus":       "фокус на другой панели",
	"help.split_close":       "закрыть панель (без сплита)",
	"help.tab_close":         "закрыть вкладку (последняя закрывает редактор)",
	"help.move":              "двигать курсор",
	"help.edit":              "редактировать текст",
	"help.undo":              "отменить / повторить",
	"help.lines":             "удалить строку / дублировать строку",
	"help.multicursor_word":  "мультикурсор: добавить курсор на следующем вхождении слова",
	"help.multicursor_click": "добавить курсор по месту клика",
	"help.multicursor_esc":   "выход из мультикурсора",
	"help.move_line":         "переместить строку вверх / вниз",
	"help.toggle_help":       "переключить эту справку",
	"help.quit":              "выйти",
}
