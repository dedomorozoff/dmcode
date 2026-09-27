# dmcode

Coding agent built on [google/adk-go](https://github.com/google/adk-go).

Tools: `read_file`, `write_file`, `list_dir`, `run_command` (shell with timeout).

## Setup

```bash
go mod tidy   # Go 1.25+; toolchain 1.26 downloads automatically
```

Модель выбирается автоматически при старте:

1. Ключи из env/`.env`: `OPENAI_*`, `GROQ_API_KEY`, `GITHUB_TOKEN`, `MISTRAL_API_KEY`
2. Ничего нет — интерактивный визард: выбираешь бесплатного провайдера
   (Groq / OpenRouter / Mistral / свой endpoint), вставляешь ключ — он пишется в `.env`.
   Рекомендуемый бесплатный вариант: **Groq** (быстрый, tool calling работает, щедрый free tier).

```bash
cp .env.example .env   # или просто запусти и следуй визарду
```

## Run

```bash
go run .    # TUI (bubbletea): стриминг, tool-calls, /new /help /quit
```

Инструменты: `read_file`, `write_file`, `edit_file`, `list_dir`, `grep`, `glob`, `run_command`.
