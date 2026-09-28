package i18n

// catalog maps an English key to its Russian translation, per language.
//
// Keys are the exact English literals used at the call sites. Anything absent
// falls through to English, so adding a new string is a one-line change in the
// UI and translation can follow separately.
var catalog = map[Lang]map[string]string{
	Russian: {
		// Command palette descriptions.
		"choose a provider (free, no key needed)": "выбрать провайдера (бесплатно, без ключа)",
		"list models":                        "список моделей",
		"copy the agent's reply (ctrl+y)":    "скопировать ответ агента (ctrl+y)",
		"toggle the sidebar (ctrl+b)":        "боковая панель (ctrl+b)",
		"start a new session":                "новая сессия",
		"clear the screen":                   "очистить экран",
		"recent prompts (up/down to recall)": "последние промпты (↑/↓ — вызвать)",
		"show the hotkeys":                   "подсказки",
		"list the available tools":           "список инструментов",
		"interface language":                 "язык интерфейса",
		"quit":                               "выход",

		// Help and status line.
		"ctrl+p — commands · ctrl+b — panel · ctrl+y — copy reply":                      "ctrl+p — команды · ctrl+b — панель · ctrl+y — копировать ответ",
		"esc — stop the current turn · up/down — prompt history · pgup/pgdown — scroll": "esc — прервать текущий ход · ↑/↓ — история промптов · pgup/pgdown — скролл",
		"mouse — select and copy text right in the terminal":                            "мышь — выделение и копирование текста прямо в терминале",
		"ctrl+p commands · ctrl+b panel · ctrl+y copy · up/down history · esc stop":     "ctrl+p команды · ctrl+b панель · ctrl+y копировать · ↑/↓ история · esc отмена",
		"describe the task… (/help for commands, esc to cancel)":                        "опиши задачу… (/help — команды, esc — отмена)",

		// Session and turn state.
		"— session reset —":                   "— сессия сброшена —",
		"turn stopped":                        "ход прерван",
		"stopped":                             "прервано",
		"error":                               "ошибка",
		"ready":                               "готов",
		"cancelled":                           "отменено",
		"generating a reply…":                 "генерация ответа...",
		"running…":                            "выполнение...",
		"waiting for a task":                  "ожидание задачи",
		"⏹ turn stopped by the user (Esc)":    "⏹ ход прерван пользователем (Esc)",
		"⏹ turn stopped by the user (Ctrl+C)": "⏹ ход прерван пользователем (Ctrl+C)",
		"⏹ turn stopped":                      "⏹ ход прерван",

		// Provider status.
		"provider without tools": "провайдер без tools",
		"⚠ %s (%s) cannot call tools: tasks will stay prose with no file edits. /setup — pick another.": "⚠ %s (%s) не умеет вызывать инструменты: задачи останутся текстом без правок файлов. /setup — выбрать другой.",
		"failover: ": "запасной: ",
		"⚡ %s is unavailable (%s) — %s (%s) answered": "⚡ %s недоступен (%s) — ответил %s (%s)",
		"provider: ":               "провайдер: ",
		"provider saved to .env: ": "провайдер сохранён в .env: ",
		"unavailable":              "недоступен",
		"stream broke":             "обрыв потока",
		"failed":                   "ошибка",

		// Tool calls.
		"calling: ":         "вызов: ",
		"returned: ":        "ответ: ",
		"model activated: ": "модель активирована: ",
		" (context kept)":   " (контекст сохранён)",
		"· model → ":        "· модель → ",
		"   ↓ more ":        "   ↓ ещё ",

		// Setup wizard.
		"setup cancelled":                                  "настройка отменена",
		"no key entered":                                   "ключ не введён",
		"no base URL entered":                              "base URL не введён",
		"no model entered":                                 "модель не введена",
		"error reading .env":                               "ошибка чтения .env",
		"could not write .env":                             "не удалось записать .env",
		"checking the key…":                                "проверяю ключ...",
		"key rejected":                                     "ключ отклонён",
		"the endpoint rejected the key: ":                  "endpoint отклонил ключ: ",
		".env was left unchanged.":                         ".env остался без изменений.",
		"press enter on an empty field to save it anyway":  "нажми enter на пустом поле, чтобы всё равно сохранить",
		"Custom endpoint":                                  "Свой endpoint",
		"key (input hidden, paste or ctrl+v, then enter):": "ключ (ввод скрыт, вставь или ctrl+v, затем enter):",
		"base URL, e.g. http://localhost:1234/v1":          "base URL, напр. http://localhost:1234/v1",
		"model id on this endpoint":                        "идентификатор модели на этом endpoint",
		"? provider key":                                   "? ключ провайдера",
		"? custom endpoint":                                "? свой endpoint",
		"? model":                                          "? модель",
		"? provider  (* — current, enter — select, esc — cancel)": "? провайдер  (* — текущий, enter — выбрать, esc — отмена)",

		// Clipboard.
		"nothing to copy":                              "нет ответа для копирования",
		"clipboard error: ":                            "ошибка буфера: ",
		"reply copied to the clipboard!":               "ответ скопирован в буфер обмена!",
		"📋 the last reply was copied to the clipboard": "📋 последний ответ скопирован в буфер обмена",

		// Sidebar.
		"MODEL":               "МОДЕЛЬ",
		"SESSION":             "СЕССИЯ",
		"FOLDER":              "ПАПКА",
		"LAST TOOL":           "ПОСЛЕДНИЙ ТУЛ",
		"TOOLS":               "ИНСТРУМЕНТЫ",
		"HOTKEYS":             "ГОРЯЧИЕ КЛАВИШИ",
		"turns: %d":           " ходов: %d",
		"tools: %d":           " тулов: %d",
		" ctrl+p  commands":   " ctrl+p  команды",
		" ctrl+b  hide panel": " ctrl+b  скрыть панель",
		" ctrl+y  copy reply": " ctrl+y  копировать ответ",
		" esc     stop turn":  " esc     отмена хода",
		" pgup/dn scroll":     " pgup/dn скролл",

		// Status badges.
		"⏳ WORKING": "⏳ РАБОТАЕТ",
		"⏹ STOPPED": "⏹ ПРЕРВАНО",
		"● READY":   "● ГОТОВ",

		// Floating panels.
		"esc — close":      "esc — закрыть",
		"   nothing found": "   ничего не найдено",
		"⌘ commands":       "⌘ команды",
		"↑↓ · enter · esc": "↑↓ выбор · enter запуск · esc закрыть",
		"⌘ models":         "⌘ модели",

		// Boot.
		"dmcode is starting…": "dmcode загружается…",

		// Modes, folder and mouse.
		"ACT":                      "РАБОТА",
		"PLAN":                     "ПЛАН",
		"mode":                     "режим",
		"already in plan mode":     "уже в режиме плана",
		"already in act mode":      "уже в режиме работы",
		"already in ":              "уже в ",
		" mode":                    " режиме",
		"usage: /mode plan|act":    "использование: /mode plan|act",
		"plan mode is unavailable": "режим плана недоступен",
		"wait for the turn to finish before switching mode": "дождитесь окончания хода перед сменой режима",
		"switch plan/act mode (tab)":                        "переключить режим план/работа (tab)",
		"change the working folder":                         "сменить рабочую папку",
		"toggle mouse wheel scrolling":                      "включить/выключить прокрутку колесом",
		"current folder: ":                                  "текущая папка: ",
		"folder changed to: ":                               "рабочая папка изменена: ",
		"cannot enter that folder: ":                        "не удалось перейти в папку: ",
		"mouse on — the wheel scrolls":                      "мышь включена — колесо прокручивает",
		"mouse off — the wheel is ignored":                  "мышь выключена — колесо не работает",
		" tab     plan/act":                                 " tab    план/работа",
		"tab — plan/act mode · wheel — scroll · /mouse — toggle the wheel": "tab — режим план/работа · колесо — скролл · /mouse — переключить мышь",

		// Provider options.
		"No key — Pollinations (OpenAI-compatible, anonymous)":                       "Без ключа — Pollinations (OpenAI-совместимый, анонимно)",
		"Local — Ollama (http://127.0.0.1:11434/v1)":                                 "Локально — Ollama (http://127.0.0.1:11434/v1)",
		"Unsloth (local) — key from Settings → API, URL and model from your console": "Unsloth (локально) — ключ из Settings → API, URL и модель из консоли",
		"OpenRouter — free models (deepseek and others)":                             "OpenRouter — бесплатные модели (deepseek и др.)",
		"Kilo — gateway with free models (kilo-auto/free, account key)":              "Kilo — шлюз с бесплатными моделями (kilo-auto/free, ключ аккаунта)",
		"OpenCode Zen — free models (nemotron, mimo, big-pickle)":                    "OpenCode Zen — бесплатные модели (nemotron, mimo, big-pickle)",
		"Groq — free, fast, tool calling works":                                      "Groq — бесплатно, быстро, tool calling работает",
		"GitHub Models — free with a GitHub token":                                   "GitHub Models — бесплатно по токену GitHub",
		"Mistral — codestral, paid tier has a free slice":                            "Mistral — codestral, у платного тарифа есть бесплатная часть",
		"Cerebras — free tier, no card, very fast":                                   "Cerebras — бесплатный тариф, без карты, очень быстро",
		"NVIDIA NIM — free credits, many coding models":                              "NVIDIA NIM — бесплатные кредиты, много кодовых моделей",
		"SambaNova — free key, fast OpenAI-compatible":                               "SambaNova — бесплатный ключ, быстрый OpenAI-совместимый",
		"Hugging Face — free credits, OpenAI-compatible router":                      "Hugging Face — бесплатные кредиты, OpenAI-совместимый роутер",
		"Your own OpenAI-compatible endpoint":                                        "Свой OpenAI-совместимый endpoint",

		// Setup wizard (stdin).
		"\nNo free provider is configured. Pick one:\n": "\nБесплатный провайдер не настроен. Выбери:\n",
		"number [1]: ": "номер [1]: ",
		"could not read the choice (stdin not a terminal?)": "не удалось прочитать выбор (stdin не терминал?)",
		"Get a key here: ": "Возьми ключ тут: ",
		"key: ":            "ключ: ",
		"base URL (e.g. http://localhost:1234/v1): ": "base URL (напр. http://localhost:1234/v1): ",
		"model: ": "модель: ",
		"run Ollama locally (ollama serve) or LM Studio — dmcode picks it up on its own; or run /setup inside the app and choose a provider": "подними локально Ollama (ollama serve) или LM Studio — dmcode подхватит её сам; либо выполни /setup в приложении и выбери провайдера",

		// Errors and provider labels.
		"OpenAI-compatible":       "OpenAI-совместимый",
		"prompt history is empty": "история промптов пуста",
		"empty provider pool":     "пустой пул провайдеров",

		// Language picker.
		"? language  (* — current, enter — select, esc — cancel)": "? язык  (* — текущий, enter — выбрать, esc — отмена)",
		"language: ":                         "язык: ",
		"could not save the language choice": "не удалось сохранить выбор языка",
	},
}
