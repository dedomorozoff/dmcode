# Unsloth in /setup: key survives, URL pinned, model picked from the server

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The /setup Unsloth row asks for a key once, knows its own endpoint, and offers the served models as a picker instead of a blind text prompt.

**Architecture:** The TUI wizard's key lives in a state field instead of the input buffer; the Unsloth preset row pins the documented local URL `http://localhost:8888/v1` and its own key variable; the key probe in `applySetup` (already a `ListModels` call) hands its ids to a new picker stage when the option has no preset model, with typed input as the fallback.

**Tech Stack:** Go, Bubble Tea v2, no new dependencies.

**Spec:** `docs/superpowers/specs/2026-11-17-unsloth-setup-design.md`

## Global Constraints

- No new dependencies; `config.ListModels` (existing, `internal/config/config.go:233`, returns `([]string, error)`) is the only fetch.
- Every user-facing string goes through `i18n.T`; new strings get a Russian entry in `internal/i18n/catalog_ru.go`.
- The stdin wizard and the TUI wizard share `config.SetupOptions()`; flow changes land in both (`internal/config/setup.go`, `internal/ui/ui.go`).
- A model-less commit is never written: `commitSetup` refuses `opt.Model == ""` unless `opt.GGUF`.
- Existing conventions: atomic writes, 0600 secrets, tests never touch a live endpoint — network answers arrive as hand-built `setupCheckMsg` in `internal/ui/setup_test.go`, and the stdin wizard's fetch goes through an injectable seam.

## Review Focus

1. A local server that is up but serves an empty `/models` (nothing downloaded) — expected: typed model prompt, never an empty picker or a model-less commit. Pinned by `TestAnEmptyModelListFallsBackToTyping` (Task 3).
2. A rejected key on a model-less row must lead to a *typed* model stage (server off, key fine — the user still needs a way through), not back to the key prompt forever. Pinned by `TestARejectedKeyOnAModelLessRowLeadsToTyping` (Task 3).
3. The custom endpoint row keeps its URL stage — pinning Unsloth must not eat the custom row's URL. Pinned by `TestTheCustomRowStillAsksForTheURL` (Task 2).
4. The key must never reach the transcript or status text (it is shown masked only). No new test — `maskedKey` and the existing rejection path already own this; reviewer checks no new string prints `m.setup.key`.

---

### Task 1: The key survives the wizard chain (TUI bug)

**Files:**
- Modify: `internal/ui/ui.go` (`setupState` ~line 718, `setupKey` handler ~line 3601, `applySetup` ~line 3815)
- Test: `internal/ui/setup_test.go`

**Interfaces:**
- Produces: `setupState.key string` — the key typed at the key stage, read by `applySetup` in this and later tasks.

- [ ] **Step 1: Write the failing test**

In `internal/ui/setup_test.go`, next to `TestUnslothAsksForURLAndModelInTheWizard`:

```go
// TestTheWizardCarriesTheKeyToTheProbe drives the chained stages with the keys
// a model would type and inspects the vars the probe is actually built from.
// The key used to live only in the input buffer: the URL stage cleared it, the
// model stage filled it with the model text, and the probe went out with the
// model id in the key position — which is why the key had to be typed twice.
func TestTheWizardCarriesTheKeyToTheProbe(t *testing.T) {
	dir := inTempDir(t)
	t.Setenv("DMCODE_SETTINGS_PATH", filepath.Join(dir, "settings.json"))

	m := newSetupModel(t)
	row := mustRow(t, func(o config.SetupOption) bool { return strings.Contains(o.Label, "Unsloth") })
	d := setupKeys{m: m}.type_("/setup").enter().walkTo(row).enter().
		type_("sk-unsloth-abc").enter().
		type_("http://127.0.0.1:8888/v1").enter().
		type_("test-model").enter()

	msg := d.cmd()
	check, ok := msg.(setupCheckMsg)
	if !ok {
		t.Fatalf("the model stage produced %T, want setupCheckMsg", msg)
	}
	if got := check.vars["OPENAI_API_KEY"]; got != "sk-unsloth-abc" {
		t.Errorf("the probe would send the key %q, want sk-unsloth-abc", got)
	}
}
```

(The chain's `d.cmd` holds the last command the handlers returned — here the probe; `d.cmd()` runs `ListModels` against a dead loopback port, which fails in milliseconds — only `vars` is asserted.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestTheWizardCarriesTheKeyToTheProbe -v`
Expected: FAIL — vars carry `"test-model"` in the key position.

- [ ] **Step 3: Implement**

In `internal/ui/ui.go`:
- `setupState`: add `key string` (comment: the key as typed, kept out of the buffer because the chained stages reuse it).
- `setupKey` case `setupKey`, `enter`: after the non-empty check, `m.setup.key = strings.TrimSpace(m.setup.buf)` before both the URL-chain branch and `applySetup`.
- `applySetup`: `vars := config.SetupVars(opt, m.setup.key)` (was `strings.TrimSpace(m.setup.buf)`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ui/ -run TestTheWizardCarriesTheKeyToTheProbe -v`
Expected: PASS. Also `go test ./internal/ui/` — the whole package stays green (the generic loop in `TestEverySetupOptionIsReachableAndPersists` builds its own `setupCheckMsg`, so it is unaffected here).

- [ ] **Step 5: Commit**

```bash
git add internal/ui/ui.go internal/ui/setup_test.go
git commit -m "Carry the setup key in state, not in the reused input buffer"
```

---

### Task 2: The Unsloth row knows its endpoint and its own key name

**Files:**
- Modify: `internal/config/setup.go:88` (the row), `internal/config/setup.go:306-319` (stdin prompt routing)
- Modify: `internal/ui/ui.go` (`setupKey` routing, ~line 3613)
- Test: `internal/config/setup_test.go`, `internal/ui/setup_test.go`
- Modify: `internal/i18n/catalog_ru.go` (label translation)

**Interfaces:**
- Consumes: `setupState.key` (Task 1).
- Produces: the Unsloth option with `BaseURL: "http://localhost:8888/v1"`, `EnvKey: "UNSLOTH_API_KEY"`, `Model: ""`; the wizards ask for whatever a chosen option is missing (URL when empty, model when empty) instead of only when the URL is empty.

- [ ] **Step 1: Write the failing tests**

`internal/config/setup_test.go`:
- In `TestSetupPinsTheMeasuredEndpoints`, add an Unsloth block: `o.BaseURL == "http://localhost:8888/v1"`, `o.EnvKey == "UNSLOTH_API_KEY"`, `o.Model == ""`.
- In `TestSetupOptionsAreWellFormed` (~line 182), replace the "baseURL but no model" error with: a preset URL without a model is allowed only when `isLoopback(o.BaseURL)` — a local server whose list the wizard fetches; a remote one still errors.
- Rewrite `TestUnslothPromptsForKeyAndEndpoint` as `TestUnslothAsksForKeyAndModel`: with the URL pinned, the stdin input `"idx\nsk-unsloth-abc\n\ntest-model\n"` — a blank line where the model prompt sits is rejected with a complaint about the model; `"idx\nsk-unsloth-abc\ntest-model\n"` persists `UNSLOTH_API_KEY=sk-unsloth-abc` and `Model: test-model`, and no URL is ever asked.

`internal/ui/setup_test.go`:
- Rewrite `TestUnslothAsksForURLAndModelInTheWizard` as `TestUnslothGoesFromKeyToTheModelStage`: walk to the Unsloth row, type the key, enter — `m.setup.stage == setupModel` (not `setupURL`).
- Add `TestTheCustomRowStillAsksForTheURL`: walk to the "Your own OpenAI-compatible endpoint" row, type a key, enter — `m.setup.stage == setupURL`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ ./internal/ui/ -run "TestSetupPinsTheMeasuredEndpoints|TestUnsloth|TestSetupOptionsAreWellFormed|TestTheCustomRow" -v`
Expected: FAIL (URL empty, EnvKey still OPENAI_API_KEY, stdin still asks the URL).

- [ ] **Step 3: Implement**

- `internal/config/setup.go` row: `{Label: i18n.T("Unsloth Studio (local) — key from Settings → API, the model comes from the server"), Signup: "https://unsloth.ai/docs/basics/api", BaseURL: "http://localhost:8888/v1", EnvKey: "UNSLOTH_API_KEY", API: APIChat}` — `Model` stays empty; comment notes the endpoint is the documented default (their quickstart runs `-p 8888`) and the catalogue is whatever the user downloaded.
- `internal/config/setup.go` stdin routing (the `else if opt.BaseURL == ""` block): ask the URL when `opt.BaseURL == ""`, then — outside that condition — ask the model when `opt.Model == ""` (same two prompts, same rejection texts as today).
- `internal/ui/ui.go` `setupKey` routing: `if m.setup.opt.BaseURL == ""` → `setupURL`; `else if m.setup.opt.Model == ""` → `setupModel`; else `applySetup`.
- `internal/i18n/catalog_ru.go`: `"Unsloth Studio (local) — key from Settings → API, the model comes from the server": "Unsloth Studio (локально) — ключ из Settings → API, модель придёт с сервера"`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go build ./...` then `go test ./internal/config/ ./internal/ui/ -v`
Expected: PASS. Note: `TestEverySetupOptionIsReachableAndPersists` needs its generic loop updated — after the key stage, type the URL only when `want.BaseURL == ""`, then type `"test-model"` when `m.setup.opt.Model == ""`; the expected URL for Unsloth becomes its preset, the expected model `"test-model"`.

- [ ] **Step 5: Commit**

```bash
git add internal/config/setup.go internal/config/setup_test.go internal/ui/ui.go internal/ui/setup_test.go internal/i18n/catalog_ru.go
git commit -m "Pin the Unsloth endpoint and key name; wizards ask for whatever is missing"
```

---

### Task 3: The model is picked from what the endpoint serves

**Files:**
- Modify: `internal/ui/ui.go` (stage const ~line 707, `setupState` ~line 718, `setupCheckMsg`, `applySetup` goroutine ~line 3840, `handleSetupCheck` ~line 3849, `setupKey` routing ~line 3613, `setupBox` ~line 5483)
- Modify: `internal/config/setup.go` (stdin model choice, seam), `internal/config/setup_test.go`
- Test: `internal/ui/setup_test.go`, `internal/config/setup_test.go`
- Modify: `internal/i18n/catalog_ru.go`

**Interfaces:**
- Consumes: `setupState.key` (Task 1), the pinned Unsloth row (Task 2).
- Produces: `setupCheckMsg.models []string`; stage `setupChoose`; `setupState.vars map[string]string` and `setupState.models []string`; config seam `fetchModels` (package var, `func(Provider) ([]string, error)`).

- [ ] **Step 1: Write the failing tests**

`internal/ui/setup_test.go`:

```go
// TestUnslothPicksAModelFromTheProbe simulates a successful key check that
// carried the served ids (the way the real probe does) and drives the picker.
func TestUnslothPicksAModelFromTheProbe(t *testing.T) {
	dir := inTempDir(t)
	t.Setenv("DMCODE_SETTINGS_PATH", filepath.Join(dir, "settings.json"))

	m := newSetupModel(t)
	row := mustRow(t, func(o config.SetupOption) bool { return strings.Contains(o.Label, "Unsloth") })
	setupKeys{m: m}.type_("/setup").enter().walkTo(row).enter().type_("sk-unsloth-abc").enter()

	vars := config.SetupVars(m.setup.opt, "sk-unsloth-abc")
	models := []string{"unsloth/Qwen3-Coder-30B", "unsloth/Qwen3-VL-32B"}
	d := setupKeys{m: m}
	_, _ = m.Update(setupCheckMsg{vars: vars, opt: m.setup.opt, models: models})
	if m.setup.stage != setupChoose {
		t.Fatalf("after the probe the wizard sits at stage %d, want the model picker", m.setup.stage)
	}
	d = d.down().enter()

	st, err := settings.Load()
	if err != nil { t.Fatalf("settings.Load: %v", err) }
	if st.Model != "unsloth/Qwen3-VL-32B" {
		t.Errorf("settings.Model = %q, want the row the picker highlighted", st.Model)
	}
}

// TestAnEmptyModelListFallsBackToTyping — a server that is up but serves
// nothing must not show an empty picker or commit a model-less config.
func TestAnEmptyModelListFallsBackToTyping(t *testing.T) { /* walk to Unsloth, key, enter; Update(setupCheckMsg{vars, opt, models: nil}); want stage setupModel and no settings.json written */ }

// TestARejectedKeyOnAModelLessRowLeadsToTyping — the probe error on a
// model-less row lands on the typed model stage, not back on the key prompt.
func TestARejectedKeyOnAModelLessRowLeadsToTyping(t *testing.T) { /* setupCheckMsg{vars, opt, err: errors.New("401")}; want stage setupModel */ }
```

`internal/config/setup_test.go` — `TestUnslothPicksFromTheServedList`, through the `fetchModels` seam (swap it, restore with `t.Cleanup`):
- fetch returns `["a", "b"]`, input `"idx\nkey\n2\n"` → settings model `b`; input `"idx\nkey\n\n"` → model `a` (enter takes the highlighted first row, as the TUI does);
- fetch returns an error → the typed model prompt is used (input supplies one; it persists).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ ./internal/ui/ -run "TestUnslothPicks|TestAnEmptyModelList|TestARejectedKey" -v`
Expected: FAIL — `setupChoose` undefined; `setupCheckMsg` has no `models` field.

- [ ] **Step 3: Implement**

- `internal/ui/ui.go`: add stage const `setupChoose` (after `setupModel`); `setupState` gains `vars map[string]string` and `models []string`; `setupCheckMsg` gains `models []string`.
- `applySetup` goroutine: `ids, err := config.ListModels(probe)` → `setupCheckMsg{vars: vars, opt: opt, models: ids, err: err}` (ids were discarded before).
- `setupKey` routing (Task 2's else-if chain): `Model == ""` no longer routes to `setupModel` — it goes to `applySetup`, whose probe carries the list.
- `handleSetupCheck` success branch:

```go
if msg.opt.Model == "" && !msg.opt.GGUF {
	if len(msg.models) > 0 {
		m.setup.vars, m.setup.models = msg.vars, msg.models
		m.setup.list = listView{}
		m.setup.stage = setupChoose
		return nil
	}
	// Up but empty: nothing downloaded yet — the typed prompt, not an empty picker.
	m.setup.stage = setupModel
	m.setup.buf = ""
	m.statusText = i18n.T("the endpoint serves no models yet")
	return nil
}
return m.commitSetup(msg.opt, msg.vars)
```

- `handleSetupCheck` error branch: when `msg.opt.Model == "" && !msg.opt.GGUF`, land on `setupModel` (typed) instead of `setupKey`; the key is already in `m.setup.key`, so the retry commits without retyping it.
- `commitSetup`: refuse `opt.Model == "" && !opt.GGUF` with a status line (defense in depth — no path may write a model-less config).
- `setupKey` case `setupChoose`: `up`/`down` move `m.setup.list.sel`; `enter` → `m.setup.opt.Model = m.setup.models[m.setup.list.sel]`, then `commitSetup(m.setup.opt, m.setup.vars)`; `esc` → `setupModel` with the buffer cleared (typed fallback).
- `setupBox` case `setupChoose`: rows with the `▸` marker on `m.setup.list.sel` (the same shape as `setupPick`'s rendering), title `i18n.T("? model on this endpoint")`, hint row `i18n.T("enter — choose, esc — type an id instead")`, panel bound to `&m.setup.list`.
- `internal/config/setup.go`: package var `var fetchModels = ListModels` (comment names the clipimg lesson: a seam plus a test that fails when the seam is bypassed); in the stdin model branch, when `opt.Model == ""` call `fetchModels(...)`; on ids, print the numbered list and read the choice — blank or `1` takes `ids[0]`, a valid number takes `ids[n-1]`, anything else falls through to the typed prompt; on error, typed prompt.
- `internal/i18n/catalog_ru.go`: the two new strings.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go build ./...` then `go test ./... -v`
Expected: PASS. The generic loop in `TestEverySetupOptionIsReachableAndPersists` is updated once more for model-less rows: after the key `enter`, run the returned probe cmd (a dead loopback port fails it in milliseconds), send `setupCheckMsg{vars, opt, models: []string{"unsloth/Qwen3-Coder-30B"}}`, drive the picker with `enter`, and expect `st.Model == "unsloth/Qwen3-Coder-30B"`.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/ui.go internal/ui/setup_test.go internal/config/setup.go internal/config/setup_test.go internal/i18n/catalog_ru.go
git commit -m "Offer the endpoint's served models as a picker in /setup"
```
