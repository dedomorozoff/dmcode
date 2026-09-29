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
		"ctrl+p — commands · ctrl+b — panel · ctrl+y — copy reply · ctrl+l — clear · ctrl+n — new session": "ctrl+p — команды · ctrl+b — панель · ctrl+y — копировать ответ · ctrl+l — очистить · ctrl+n — новая сессия",
		"esc — stop the current turn · up/down — prompt history · pgup/pgdown — scroll":                    "esc — прервать текущий ход · ↑/↓ — история промптов · pgup/pgdown — скролл",
		"mouse — select and copy text right in the terminal":                                               "мышь — выделение и копирование текста прямо в терминале",
		"ctrl+p commands · ctrl+b panel · ctrl+y copy · up/down history · esc stop":                        "ctrl+p команды · ctrl+b панель · ctrl+y копировать · ↑/↓ история · esc отмена",
		"describe the task… (/help for commands, esc to cancel)":                                           "опиши задачу… (/help — команды, esc — отмена)",

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
		"files: %d":           " файлов: %d",
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

		// Proxy.
		"show or set the HTTP proxy":              "показать или задать HTTP-прокси",
		"proxy: none (direct connection)":         "прокси: нет (прямое соединение)",
		"proxy: cleared, connecting directly":     "прокси: сброшен, прямое соединение",
		"proxy: bypass list cleared":              "прокси: список исключений очищен",
		"proxy: the provider answered through it": "прокси: провайдер ответил через него",
		"proxy: the request failed — ":            "прокси: запрос не удался — ",
		"set one with: /proxy <url> · clear with: /proxy off · bypass with: /proxy no <list>": "задать: /proxy <url> · отключить: /proxy off · исключения: /proxy no <список>",
		"checking the proxy…": "проверяю прокси…",

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

		// Rewind (ctrl+z).
		"wait for the turn to finish before rewinding":                 "дождитесь конца хода, потом откат",
		"rewind needs a session store, which this session has none of": "откату нужно хранилище сессий, которого здесь нет",
		"nothing to rewind to":                                         "откатывать нечего",
		"the current session is no longer on disk":                     "текущей сессии больше нет на диске",
		"rewind failed: ":                                              "откат не удался: ",
		"rolled back to the previous message":                          "откат к предыдущему сообщению",
		"the message is back in the input — edit it and send again":    "сообщение вернулось в поле ввода — отредактируйте и отправьте снова",

		// Sessions.
		"start a new session, the old one is kept (ctrl+n)":     "начать новую сессию, старая сохраняется (ctrl+n)",
		"clear the screen (ctrl+l)":                             "очистить экран (ctrl+l)",
		"switch between saved sessions":                         "переключение между сохранёнными сессиями",
		"open a session by id (/resume <id>)":                   "открыть сессию по id (/resume <id>)",
		"usage: /resume <id> — /sessions lists the ids":         "usage: /resume <id> — список id в /sessions",
		"undo the last message (ctrl+z)":                        "отменить последнее сообщение (ctrl+z)",
		"session":                                               "сессия",
		"new session":                                           "новая сессия",
		"is kept, see /sessions":                                "сохранена, смотрите /sessions",
		"session reset":                                         "сессия сброшена",
		"could not start a new session: ":                       "не удалось начать новую сессию: ",
		"switched session from ":                                "переключение с сессии ",
		"could not read that session: ":                         "не удалось прочитать сессию: ",
		"only the last events of that session were loaded":      "загружены только последние события сессии",
		"that session has no messages yet":                      "в этой сессии пока нет сообщений",
		"(no answer — that turn was cut short)":                 "(без ответа — ход был прерван)",
		"wait for the turn to finish before switching sessions": "дождитесь конца хода, потом переключение",
		"this session has no session store":                     "у этой сессии нет хранилища",
		"sessions are not being saved: ":                        "сессии не сохраняются: ",
		"this session could not be written to disk: ":           "сессию не удалось записать на диск: ",
		"no saved sessions yet":                                 "сохранённых сессий пока нет",
		"session deleted":                                       "сессия удалена",
		"could not delete that session: ":                       "не удалось удалить сессию: ",
		" ctrl+z  undo last message":                            " ctrl+z  откат сообщения",
		"(untitled)":                                            "(без названия)",
		"events":                                                "событий",
		"just now":                                              "только что",
		"min ago":                                               "мин назад",
		"h ago":                                                 "ч назад",
		"d ago":                                                 "дн назад",
		"unknown":                                               "неизвестно",
		"? sessions  (* — current, enter — switch, d — delete, esc — close)": "? сессии  (* — текущая, enter — переключить, d — удалить, esc — закрыть)",
		"? press d again to delete that session for good":                    "? нажмите d ещё раз, чтобы удалить сессию навсегда",

		// Retries.
		"retrying in %s — %s": "повтор через %s — %s",
		"attempt":             "попытка",
		"in":                  "через",

		// Plan (todo).
		"show the current plan": "показать текущий план",
		"plan":                  "план",
		"done":                  "готово",
		"the plan is empty":     "план пуст",
		"the plan is empty — the agent has not published one": "план пуст — агент его ещё не опубликовал",
		"usage: /todo, /todo clear":                           "usage: /todo, /todo clear",

		// Questions (ask_user).
		"waiting for your answer":                     "ждём вашего ответа",
		"question skipped — the agent will carry on":  "вопрос пропущен — агент продолжит сам",
		"no answer in time, chose":                    "ответа не было, выбрано",
		"? choose one":                                "? выберите один",
		"? choose one or more":                        "? выберите один или несколько",
		"enter — confirm · space — tick · esc — skip": "enter — подтвердить · space — отметить · esc — пропустить",
		"enter — confirm · space — tick · c — your own · esc — skip": "enter — подтвердить · space — отметить · c — свой вариант · esc — пропустить",
		"enter — send · esc — back":                                  "enter — отправить · esc — назад",
		"write your own answer":                                      "вписать свой вариант",
		"(recommended)":                                              "(рекомендуется)",
	},
}
