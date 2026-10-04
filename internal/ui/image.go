package ui

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"

	"github.com/dedomorozoff/dmcode/internal/clipimg"
	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/i18n"
	"github.com/dedomorozoff/dmcode/internal/imgprev"
	dmtools "github.com/dedomorozoff/dmcode/internal/tools"
)

// previewCols is how wide an image preview is drawn.
//
// It is fixed rather than derived from the panel width because the art is stored
// in the transcript verbatim, and a verbatim row that does not fit is dropped
// whole вЂ” a preview that vanished on a narrower terminal, or that had to be
// re-rendered on every resize, would both be worse than one that is simply a
// consistent size. Thirty-two columns fits every terminal dmcode runs on, and
// enough rows of half-blocks to make a screenshot recognisable.
const previewCols = 32

// maxPreviewBytes bounds what a preview is built from. The image is read in full
// and then reduced, so this is not a quality knob: it exists so a stray 200 MB
// file cannot be pulled into memory by a path in a prompt. The smaller limit
// imgprev applies afterwards is about the wire, not about memory.
const maxPreviewBytes = 32 << 20

// imageExtensions are the suffixes takeImagePaths recognises.
//
// It is a list rather than a MIME sniff because the tokens being examined are
// what the user typed, and the point is to decide whether to *look* at a path.
// Decoding every word in a prompt to find out would be absurd; the extension is
// the cheap first question, and the decoder is what actually decides.
var imageExtensions = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".webp": true,
}

// looksLikeImage reports whether a token could be a picture worth opening.
//
// webp is in the list even though imgprev cannot decode it: recognising it gets
// the user a named "this build reads png, jpeg and gif" instead of a filename
// being silently treated as prose.
func looksLikeImage(path string) bool {
	return imageExtensions[strings.ToLower(filepath.Ext(path))]
}

// takeImagePaths pulls the pictures out of a prompt and returns what is left.
//
// This is what makes drag-and-drop work. A terminal has no way to hand dmcode a
// file: dropping one on Windows Terminal, iTerm or kitty inserts its *path* as
// text, and there is nothing else on the wire to tell. So a path in the prompt
// is treated as an attachment, and the filename is removed from the question the
// model reads вЂ” otherwise the model is asked about "shot.png" and answers about
// the name rather than the picture.
//
// A token that looks like an image but cannot be loaded is reported and then left
// in the text. Removing it would turn a typo into a turn about nothing, and the
// user would have no way to tell the difference from a lost attachment.
func (m *uiModel) takeImagePaths(text string) (string, []imgprev.Attachment) {
	if !strings.ContainsAny(text, "/\\.") {
		// No path separator and no dot: nothing here can be a file path.
		return text, nil
	}
	var (
		kept []string
		imgs []imgprev.Attachment
	)
	for _, tok := range splitTokens(text) {
		if !tok.quoted && !looksLikeImage(tok.text) {
			kept = append(kept, tok.text)
			continue
		}
		if tok.quoted && !looksLikeImage(tok.text) {
			// A quoted run of words is kept intact and left alone: the quotes are
			// how a path with a space is marked, so a quoted token that is not an
			// image is not ours to break up.
			kept = append(kept, tok.raw)
			continue
		}
		if _, ok := m.pendingForPath(tok.text); ok {
			// Already attached when the path was typed, and the caller appends
			// the pending list to whatever this returns. The token is still taken
			// out of the text, but the picture must not be added here as well:
			// that would hand the model two copies of one screenshot, where the
			// second costs context and says nothing.
			continue
		}
		a, err := m.attachImage(tok.text)
		if err != nil {
			m.reportImage(tok.text, err)
			kept = append(kept, tok.raw)
			continue
		}
		imgs = append(imgs, a)
	}
	return strings.Join(kept, " "), imgs
}

// token is one whitespace-delimited run of the prompt, with whether it arrived
// inside quotes.
type token struct {
	text   string
	raw    string
	quoted bool
}

// splitTokens cuts a prompt into tokens, keeping a quoted run together.
//
// This is the difference between drag-and-drop working and not. A dropped path
// with a space in its name arrives wrapped in quotes, and splitting on whitespace
// anyway produces "my" and "screenshot.png" вЂ” the second of which is a plausible
// looking path that does not exist, so the user gets an error about a file they
// never named and the picture is not attached.
//
// An *unquoted* path with a space is left alone. There is no way to tell it from
// a sentence, and guessing would mean half a sentence being treated as a filename.
func splitTokens(text string) []token {
	var (
		out   []token
		cur   strings.Builder
		quote rune
		open  = -1 // index in out of the token the builder is extending
	)
	flush := func() {
		s := cur.String()
		cur.Reset()
		if open >= 0 {
			// raw keeps the quotes, text drops them: the file name is what has to
			// be opened, and a token still carrying its quotes is not a path
			// anything will resolve.
			out[open].raw = s
			out[open].text = strings.Trim(s, `"'`)
			open = -1
			return
		}
		if s != "" {
			out = append(out, token{text: s, raw: s})
		}
	}
	for _, r := range text {
		switch {
		case quote != 0:
			// Both quote characters are written into the raw form, so a token that
			// fails to load is put back exactly as it was typed rather than
			// re-quoted differently.
			cur.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			flush()
			quote = r
			open = len(out)
			out = append(out, token{quoted: true})
			cur.WriteRune(r)
		case unicode.IsSpace(r):
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	// An unterminated quote means the prompt ended mid-path. The token keeps its
	// quoted flag either way: a half-typed name is not prose, and treating it as
	// prose would leave a real path buried in a sentence.
	flush()
	return out
}

// attachImage reads one picture from disk and prepares it for a turn.
//
// Everything expensive and everything that can fail happens here, on the caller's
// goroutine, so that the failure is reported against the token the user typed
// rather than surfacing later as a turn that mysteriously had no image in it.
func (m *uiModel) attachImage(path string) (imgprev.Attachment, error) {
	if !m.canSendImages() {
		return imgprev.Attachment{}, errNoImageWire
	}
	abs, err := dmtools.ResolveExisting(path, true)
	if err != nil {
		return imgprev.Attachment{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return imgprev.Attachment{}, err
	}
	if info.Size() > maxPreviewBytes {
		return imgprev.Attachment{}, fmt.Errorf("%s is %s вЂ” too large to open",
			path, imgprev.Bytes(int(min(info.Size(), maxPreviewBytes))))
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return imgprev.Attachment{}, err
	}
	return imgprev.Load(data, filepath.Base(abs), previewCols, chatIndent, m.profile)
}

// errNoImageWire is what an attachment on the responses wire reports.
//
// It is checked here rather than left to fail at the provider: ADK's own client
// rejects an inline blob outright ("unsupported content part"), so on this wire
// the turn would die with a message about the SDK rather than about the user's
// choice. Saying it at the moment they attach is the difference between an
// actionable sentence and a puzzle.
var errNoImageWire = errors.New("this endpoint cannot be sent images")

// canSendImages reports whether the active provider's wire carries a picture.
func (m *uiModel) canSendImages() bool {
	return m.prov.Wire() == config.APIChat
}

// reportImage puts an attachment failure where the user can act on it.
func (m *uiModel) reportImage(path string, err error) {
	var msg string
	switch {
	case errors.Is(err, errNoImageWire):
		msg = i18n.T("images need the chat wire: ") + m.prov.Label +
			" " + i18n.T("uses /v1/responses. Set DMCODE_API=chat, or pick a provider on /setup that speaks /chat/completions.")
	case errors.Is(err, imgprev.ErrUnsupportedImage):
		msg = i18n.T("cannot read that image: ") + err.Error()
	case errors.Is(err, dmtools.ErrOutsideRoot):
		msg = err.Error()
	default:
		msg = i18n.T("cannot attach that image: ") + err.Error()
	}
	m.statusText = msg
	m.history = append(m.history, line{kindErr, path + " вЂ” " + msg})
	m.historyDirty = true
	m.followVP()
}

// showPreviews prints each picture into the transcript, above the prompt that is
// about to carry it.
//
// The caption is part of the same verbatim block as the art rather than a line of
// its own, so the two cannot be separated by anything: a preview without its
// caption is a picture of unknown size, and a caption without its preview is a
// filename, which is what this feature exists to replace.
func (m *uiModel) showPreviews(imgs []imgprev.Attachment) {
	// The caption carries no indent of its own: the art bleeds to the panel
	// border, and a caption two cells in from the picture above it reads as a
	// separate line of prose rather than as part of the block.
	for i, a := range imgs {
		m.history = append(m.history,
			line{kindImage, a.Art + "\n" + a.Caption(i+1, len(imgs), a.Name)})
	}
	if len(imgs) > 0 {
		m.historyDirty = true
	}
}

// The clipboard is read through these indirections rather than called
// directly, because it is a global, shared, destructive resource: a test that
// read the real one would both be unreliable (anything the user copies mid-run
// changes the answer) and destructive (it is the only clipboard there is). Every
// clipboard test swaps these instead, so the behaviour is driven by a synthetic
// selection and the real one is left alone.
//
// The write side is here for the same reason: ctrl+y and a drag-selection both
// land in the user's clipboard, and a test of either used to leave its own text
// there.
var (
	readClipboardImage = clipimg.Read
	readClipboardText  = clipboard.ReadAll
	writeClipboardText = clipboard.WriteAll
)

// pasteImageCmd attaches the clipboard's picture, if it holds one.
//
// It returns nil only for clipimg.ErrNoImage, which is the common case: a
// clipboard almost always holds text, and ctrl+v has to keep pasting that. Every
// other error is a failure — the clipboard said it had a picture and the bytes
// could not be turned into one — and is reported, because silently swallowing it
// is what makes ctrl+v look like it does nothing at all.
//
// The work runs as a command rather than inline because reading the clipboard
// locks an OS thread and waits on another process, and doing that inside Update
// would freeze the interface for as long as the wait lasted.
func (m *uiModel) pasteImageCmd() tea.Cmd {
	if !m.canSendImages() {
		// Asked on the chat wire only. On the responses wire every attach fails
		// anyway, and refusing here keeps ctrl+v purely a text paste instead of
		// spending a command on a clipboard read that cannot succeed.
		return nil
	}
	return func() tea.Msg {
		img, err := readClipboardImage()
		return m.pastedImageMsg(img, err)
	}
}

// ctrlPasteCmd is what one ctrl+v does, for a terminal that lets the keystroke
// through: the picture if the clipboard holds one, the text if it does not.
//
// The fallback is the part that was missing. Returning the image command
// unconditionally meant a clipboard with only text produced no message at all, so
// the key did nothing at all — ctrl+v stopped working for the case it had always
// worked for, in exchange for a case it did not work for either. Reading the
// clipboard does not consume it, so asking for the picture first costs the text
// paste nothing.
func (m *uiModel) ctrlPasteCmd() tea.Cmd {
	return func() tea.Msg {
		if m.canSendImages() {
			img, err := readClipboardImage()
			if msg := m.pastedImageMsg(img, err); msg != nil {
				return msg
			}
		}
		// No picture, or a wire that cannot carry one. Either way this is a text
		// paste, and it is delivered as the message the terminal would have sent
		// so that there is exactly one code path for pasted text.
		text, err := readClipboardText()
		if err != nil {
			return imageErrMsg{err}
		}
		return tea.PasteMsg{Content: text}
	}
}

// pastedImageMsg turns a clipboard read into a message.
//
// Only clipimg.ErrNoImage produces no message: the selection holds no picture,
// so ctrl+v falls through to the text paste and nothing is wrong. Every other
// error is reported, because silently dropping it is what makes ctrl+v look like
// it does nothing at all.
//
// It is a separate function so the one decision can be tested against a synthetic
// error. clipimg.Read is the real clipboard — a global, shared, destructive
// resource — and a test that has to reach it to check a branch is a test that
// destroys whatever the user had copied.
func (m *uiModel) pastedImageMsg(img image.Image, err error) tea.Msg {
	if err != nil {
		if errors.Is(err, clipimg.ErrNoImage) {
			return nil
		}
		return imageErrMsg{err}
	}
	var buf bytes.Buffer
	if err := imgprev.Encode(&buf, img, "image/png"); err != nil {
		return imageErrMsg{err}
	}
	a, err := imgprev.Load(buf.Bytes(), i18n.T("clipboard"), previewCols, chatIndent, m.profile)
	if err != nil {
		return imageErrMsg{err}
	}
	return imageAttachedMsg{a: a, fromInput: false}
}

// imageAttachedMsg is a picture that was read off the clipboard or out of the
// input, already rendered. err is carried alongside rather than as its own message
// because both routes end in the same place, and a caller that had to handle two
// messages would eventually handle only the one it expected.
type imageAttachedMsg struct {
	a imgprev.Attachment
	// fromInput travels with the picture rather than being filled in on arrival,
	// because it is the route that decided it: an attachment from a path in the
	// prompt belongs to that text and goes when the text changes.
	fromInput bool
	// path is the input the watch read, and it is what makes a slow read safe.
	// Reading and decoding a file is asynchronous, so the path can be edited
	// while it is in flight: type a path, delete it, and the decode that started
	// on the old one still finishes. Without carrying the path the arrival cannot
	// be matched against what the prompt now says, and the picture appears on a
	// prompt that no longer mentions it — the same bug as a preview that will not
	// be deleted, arriving after the deletion instead of before it.
	path string
	err  error
}

// imageErrMsg is a clipboard image that could not be used.
type imageErrMsg struct{ err error }

// attachImageCmd is /image and the paste path: it attaches a picture without
// sending anything, so the user can look at the preview and decide.
func (m *uiModel) attachImageCmd(args string) tea.Cmd {
	// The argument goes through splitTokens because a path with a space in it
	// arrives quoted — dropped that way by a terminal, and typed that way by a
	// user copying the form it printed. A token still carrying its quotes is not
	// a path anything will resolve, and the error that comes back names the quote
	// rather than the file.
	arg := ""
	for _, tok := range splitTokens(args) {
		arg = tok.text
		break
	}
	if arg == "" {
		m.statusText = i18n.T("usage: /image <path> — or drop a picture into the prompt")
		return nil
	}
	a, err := m.attachImage(arg)
	if err != nil {
		m.reportImage(arg, err)
		return nil
	}
	// Not from the input: /image is an explicit attachment, and the prompt's text
	// has nothing to do with it.
	m.addPending(a, false)
	return nil
}

// watchImageCmd attaches a picture the user is still typing, as a command rather
// than inline: reading and decoding a file is I/O, and doing it inside Update
// would freeze the interface on the keystroke that completed the path.
//
// It goes through the same addPending as /image, so the preview above the input is
// the same one either route produced.
func (m *uiModel) watchImageCmd(path string) tea.Cmd {
	return func() tea.Msg {
		a, err := m.attachImage(path)
		return imageAttachedMsg{a: a, fromInput: true, path: path, err: err}
	}
}

// pendingImage is one picture waiting for the next message, with where it came
// from.
//
// fromInput is what makes deleting the path work. A picture that arrived as a path
// in the prompt is a statement the user is still editing: remove the path and the
// picture must go with it. One from /image or the clipboard is not — it was
// attached deliberately and is not going anywhere because the prompt no longer
// mentions a file.
type pendingImage struct {
	imgprev.Attachment
	fromInput bool
	// path is the prompt token this came from, and it is how the send does not
	// attach the same picture twice. A path in the prompt is already attached the
	// moment it is typed, so takeImagePaths finds it on its way past; reading it
	// again would put two copies of one screenshot in front of the model, where
	// the second costs context and means nothing.
	path string
}

// addPending records an attachment. The strip above the input is the only place
// it is drawn from here on — nothing goes into the transcript until the turn is
// actually sent.
//
// It used to do both, and the two copies were a bug rather than a redundancy: the
// same picture appeared twice on screen, once as a record of the conversation and
// once as something still to be decided, and removing it took away one of them and
// left the other. A pending attachment is one thing in one place; the transcript
// entry is written by the send, which is the moment it becomes part of the
// conversation.
//
// layout() before followVP, and the order matters. The strip takes rows away from
// the transcript, so the viewport has to be resized to the height that is left;
// re-syncing first would size it against a height that is about to change and leave
// the frame a strip taller than the terminal.
func (m *uiModel) addPending(a imgprev.Attachment, fromInput bool) {
	m.addPendingFrom(a, fromInput, "")
}

// addPendingFrom is addPending with the prompt token the picture came from, for
// the watch route. A /image or clipboard attachment has no token, which is what
// the empty path means.
func (m *uiModel) addPendingFrom(a imgprev.Attachment, fromInput bool, path string) {
	m.pending = append(m.pending, pendingImage{Attachment: a, fromInput: fromInput, path: path})
	m.layout()
	m.followVP()
	m.statusText = fmt.Sprintf(i18n.T("attached (%d waiting to send)"), len(m.pending))
}

// pendingForPath returns the attachment already made from path, if any.
func (m *uiModel) pendingForPath(path string) (imgprev.Attachment, bool) {
	for _, p := range m.pending {
		if p.path != "" && p.path == path {
			return p.Attachment, true
		}
	}
	return imgprev.Attachment{}, false
}

// pendingImages is the pending attachments as the turn wants them: plain values,
// with the origin dropped because a turn does not care how a picture arrived.
func (m *uiModel) pendingImages() []imgprev.Attachment {
	if len(m.pending) == 0 {
		return nil
	}
	out := make([]imgprev.Attachment, 0, len(m.pending))
	for _, p := range m.pending {
		out = append(out, p.Attachment)
	}
	return out
}

// dropInputPending removes every attachment that came from a path in the input.
//
// It runs when the candidate changes, which includes becoming nothing: a user who
// deletes the picture's path is un-saying it, and a preview that stayed would be
// showing something they have taken back. It reports whether anything went, so the
// caller only re-lays-out when the screen actually changes.
func (m *uiModel) dropInputPending() bool {
	kept := m.pending[:0]
	dropped := false
	for _, p := range m.pending {
		if p.fromInput {
			dropped = true
			continue
		}
		kept = append(kept, p)
	}
	m.pending = kept
	return dropped
}

// dropPendingImage removes the last attached picture, which is what a mistake
// needs: the pictures ride with the next prompt and there is no other way to take
// one back before sending.
func (m *uiModel) dropPendingImage() {
	if len(m.pending) == 0 {
		m.statusText = i18n.T("no image is attached")
		return
	}
	dropped := m.pending[len(m.pending)-1]
	m.pending = m.pending[:len(m.pending)-1]
	if dropped.fromInput {
		// The path that put it there goes with it, and the watch stops following
		// it. Leaving either behind means the removal does not hold: the prompt
		// still names the file, so the send attaches the picture again, and the
		// next keystroke that moves the candidate re-attaches it too. The user
		// would press /unimage and watch the picture come straight back.
		m.dropTokenFromInput(dropped.path)
		m.watchedPath = ""
	}
	// The strip's rows go back to the transcript, so the viewport is resized before
	// it is re-synced — the reverse of the attach, and for the same reason.
	m.layout()
	m.followVP()
	m.statusText = i18n.T("removed ") + dropped.Name
}

// dropTokenFromInput removes one token from the prompt, leaving the rest as it
// was.
//
// A quoted token is matched with its quotes and replaced without them, so a path
// with a space in its name is the one that goes and the sentence around it keeps
// the quoting style it was typed in.
func (m *uiModel) dropTokenFromInput(path string) {
	if path == "" {
		return
	}
	var kept []string
	for _, tok := range splitTokens(m.input.Value()) {
		if tok.text != path {
			kept = append(kept, tok.raw)
		}
	}
	m.input.SetValue(strings.Join(kept, " "))
	m.input.CursorEnd()
}

// cutCommandToken reports whether text ends with cmd as a token of its own, and
// returns text without it.
//
// It exists for /unimage, which is a command about an attachment and so is most
// often needed in a prompt that already holds a dropped path. Typing it appends
// to that path, so matching the whole line misses every time — and missing it
// does not merely fail to help: the line stops being a command and is sent to the
// model as a question about a file called "/unimage".
//
// Requiring the token to stand alone is what keeps a file whose name ends in the
// same letters, "holiday.png" style, from being read as the command. Only the
// separator in front of it is checked, so a path with a space in it still works.
func cutCommandToken(text, cmd string) (string, bool) {
	trimmed := strings.TrimRight(text, " \t")
	if !strings.HasSuffix(trimmed, cmd) {
		return text, false
	}
	at := len(trimmed) - len(cmd)
	if at > 0 {
		if c := trimmed[at-1]; c != ' ' && c != '\t' {
			return text, false
		}
	}
	rest := strings.TrimRight(trimmed[:at], " \t")
	if rest == text {
		return text, false
	}
	return rest, true
}
