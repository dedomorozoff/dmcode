<div align="center">

# dmcode

**Кодинг-агент для терминала. Один бинарник, ноль обязательной настройки.**

Спроси — он прочитает файлы, поправит их и запустит тесты.
Построен на [google/adk-go](https://github.com/google/adk-go) и [Bubble Tea](https://github.com/charmbracelet/bubbletea).

`go install` · один файл · никаких зависимостей

</div>

---

## Установка

**macOS / Linux / BSD:**

```bash
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmcode/main/install.sh | bash
```

**Windows (PowerShell):**

```powershell
irm https://raw.githubusercontent.com/dedomorozoff/dmcode/main/install.ps1 | iex
```

**Из исходников** — нужен Go 1.26+:

```bash
go install github.com/dedomorozoff/dmcode@latest
```

Готово. Первый запуск — и всё:

```bash
dmcode
```

## Провайдер — подбирается сам

Ключ не нужен. При старте dmcode сам находит, на чём работать, и молча
переключается на другого провайдера, если первый отвалился прямо посреди
сессии.

| Что найдёт сам | Когда |
|---|---|
| **Ollama, LM Studio, llama.cpp, vLLM, Jan** | запущены локально — проверяются первыми |
| **Pollinations** | анонимный OpenAI-совместимый API, вообще без ключа |
| **Groq, OpenRouter, OpenCode Zen, Mistral, GitHub Models** | если ключ уже лежит в `.env` или окружении |

Ничего не нашёл — запускается мастер `/setup`: выбираешь провайдера, вставляешь
ключ, он пишется в `.env`. Есть варианты вообще без ключа, а есть свои
endpoint'ы (Unsloth, LM Studio, vLLM — что угодно, что говорит OpenAI-совместимый
API).

Свой endpoint — это три строки в `.env`:

```bash
OPENAI_BASE_URL=https://api.groq.com/openai/v1
OPENAI_API_KEY=gsk_...
DMCODE_MODEL=qwen/qwen3-32b
```

Полезно знать: `DMCODE_API=chat` заставляет говорить по `/chat/completions`,
`DMCODE_REASONING_EFFORT=low` ограничивает канал рассуждений.

## Инструменты

Агент работает файлами и шеллом, а не болтает:

`read_file` · `write_file` · `edit_file` · `list_dir` · `grep` · `glob` · `run_command`

## Клавиши

| | |
|---|---|
| `ctrl+p` | палитра команд |
| `ctrl+b` | скрыть боковую панель |
| `ctrl+y` | скопировать ответ |
| `esc` | прервать текущий ход |
| `↑` `↓` | история промптов |
| `pgup` `pgdn` | скролл |

Команды: `/setup` `/models` `/tools` `/history` `/new` `/clear` `/copy` `/sidebar` `/help` `/quit`

## Сборка из исходников

```bash
git clone https://github.com/dedomorozoff/dmcode
cd dmcode
make build              # -> dist/dmcode
make test               # тесты
make vet                # go vet
```

Кросс-компиляция — `make build-linux-amd64`, `build-darwin-arm64`,
`build-windows-amd64` и любые другие пары GOOS-GOARCH. Пакеты: `make deb`,
`make rpm`, `make pkg`.

Теги `v*` собирают релизы через GitHub Actions: бинари под восемь платформ,
`.deb`, `.rpm`, Arch-пакет и zip для Windows.

## Структура

```
main.go              только связывает пакеты между собой
internal/agent       сборка агента и системный промпт
internal/config      .env, endpoint'ы, мастер настройки
internal/discover    поиск провайдеров, которые реально отвечают
internal/llm         OpenAI-совместимый wire, failover между endpoint'ами
internal/tools       инструменты агента
internal/ui          терминальный интерфейс на Bubble Tea
```

## Дорожная карта

Планы — в [ROADMAP.md](ROADMAP.md): отмена хода, права на опасные команды,
персистентность сессий, MCP и LSP.

