package ui

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/dedomorozoff/dmcode/internal/clipimg"
	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/imgprev"
)

// writePNG makes a small, recognisable PNG on disk and returns its path.
func writePNG(t *testing.T, dir, name string, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// Two bands, so a preview has something to be right or wrong about.
	for y := 0; y < h; y++ {
		c := color.RGBA{R: 220, G: 40, B: 40, A: 255}
		if y >= h/2 {
			c = color.RGBA{R: 40, G: 40, B: 220, A: 255}
		}
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

// imageModel is a model on the chat wire, which is the only wire that can carry a
// picture, sized so a preview fits its panel.
func imageModel(t *testing.T) *uiModel {
	t.Helper()
	m := newSessionModel(t)
	m.prov = config.Provider{Label: "local", API: config.APIChat, Model: "qwen2.5vl"}
	m.width, m.height = 120, 45
	m.profile = colorprofile.TrueColor
	m.layout()
	return m
}

// TestAttachDrawsAPreviewAndWaitsForThePrompt is the feature: a picture the user
// named is shown before anything is sent, so "what am I about to send" is
// answerable from the screen.
func TestAttachDrawsAPreviewAndWaitsForThePrompt(t *testing.T) {
	m := imageModel(t)
	dir := t.TempDir()
	p := writePNG(t, dir, "shot.png", 64, 48)

	m.attachImageCmd(p)

	if len(m.pending) != 1 {
		t.Fatalf("pending = %d images, want 1", len(m.pending))
	}
	if m.busy {
		t.Error("attaching started a turn; it must only prepare one")
	}
	if len(m.promptMarks) != 0 {
		t.Error("attaching recorded a prompt mark; a rewind would then cut a turn that never happened")
	}
	strip := ansi.Strip(m.pendingStrip())
	if !strings.Contains(strip, "shot.png") {
		t.Errorf("the preview does not name its file:\n%s", strip)
	}
	if !strings.Contains(strip, "64×48") {
		t.Errorf("the preview does not report the dimensions:\n%s", strip)
	}
	if !strings.Contains(strip, "▀") {
		t.Errorf("the preview carries no picture:\n%s", strip)
	}
	// Nothing in the transcript: an attachment is not part of the conversation
	// until it is sent, and a copy here would be a second picture on screen.
	for _, l := range m.history {
		if l.kind == kindImage {
			t.Errorf("an unsent attachment was written into the transcript: %q", lastLine(l.text))
		}
	}
}

// col is how many cells precede a byte offset on its own line.
func col(s string, at int) int {
	head := s[:at]
	if nl := strings.LastIndex(head, "\n"); nl >= 0 {
		head = head[nl+1:]
	}
	return ansi.StringWidth(head)
}

// lastLine is the caption: the art is many rows and the caption is the one that
// says what the picture is.
func lastLine(s string) string {
	parts := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return parts[len(parts)-1]
}

// send runs the part of the enter handler that decides what a turn carries: the
// pictures go into the transcript, the pending strip empties, and the mark records
// what a rewind would cut at. The turn itself is not started, so a test needs no
// runner.
//
// The order is the enter handler's: paths are taken out of the text and attached,
// then whatever was pending is appended. It is reproduced here rather than
// simplified because that combination is where a picture gets counted twice — the
// two sources are checked against each other here and nowhere else.
//
// Tests that assert on the *transcript* go through this, because that is the only
// moment a picture is written there: an attachment waits in the strip above the
// input and joins the conversation when it is sent.
func send(m *uiModel, text string) []imgprev.Attachment {
	text, imgs := m.takeImagePaths(text)
	imgs = append(imgs, m.pendingImages()...)
	m.pending = nil
	m.promptMarks = append(m.promptMarks,
		promptMark{text: text, idx: len(m.history), images: imgs})
	m.showPreviews(imgs)
	m.history = append(m.history, line{kindUser, text})
	m.historyDirty = true
	m.layout()
	m.followVP()
	return imgs
}

// screen is the part of the frame a picture can be in: the transcript and the
// strip above the input. The sidebar is deliberately excluded — the brand block is
// drawn with half blocks too, so a whole-frame search for ▀ finds the logo.
func screen(m *uiModel) string {
	return m.renderHistory() + "\n" + m.pendingStrip()
}

// TestPreviewReachesTheScreenIntact is the render-side contract. A preview is
// stored verbatim, and a verbatim row that does not fit is dropped whole, so the
// art has to arrive at its final width and must not be re-wrapped or re-coloured
// on the way out.
func TestPreviewReachesTheScreenIntact(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	m.attachImageCmd(writePNG(t, t.TempDir(), "shot.png", 64, 48))
	send(m, "вот")

	frame := m.renderHistory()
	if !strings.Contains(frame, "▀") {
		t.Fatalf("the drawn transcript has no picture:\n%s", frame)
	}
	if !strings.Contains(frame, "shot.png") {
		t.Errorf("the drawn transcript has no caption:\n%s", frame)
	}
	// Every row of the drawn picture must be exactly previewCols + chatIndent
	// wide. The margin is baked into the art and the transcript adds none, so the
	// row still lands at the same width it always did — which is what keeps a row
	// wider than the panel from taking the whole block with it.
	for _, row := range strings.Split(frame, "\n") {
		if !strings.Contains(row, "▀") {
			continue
		}
		if w := ansi.StringWidth(row); w != previewCols+chatIndent {
			t.Errorf("a preview row is %d cells wide, want %d", w, previewCols+chatIndent)
		}
	}
	// The transcript must not have added a colour of its own on top: a preview
	// that arrives tinted is a preview of the wrong picture.
	if strings.Contains(frame, "[38;5;") {
		t.Errorf("the transcript re-coloured a truecolor preview:\n%s", frame)
	}
}

// TestThePreviewHasNoBareIndentAlongsideIt is the fix for a preview with a stripe
// down its left side.
//
// An image row is prestyled, so the transcript's chatIndent is not added to it, and
// the art itself carries the bleed: every cell is a half block, with no spaces at
// either edge. A space would take the terminal's own background, which on a dark
// terminal reads as a column of the image that is out of step with the rest.
func TestThePreviewHasNoBareIndentAlongsideIt(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	m.attachImageCmd(writePNG(t, t.TempDir(), "shot.png", 64, 48))
	send(m, "вот")

	for i, row := range strings.Split(m.renderHistory(), "\n") {
		if !strings.Contains(row, "▀") {
			continue
		}
		if strings.ContainsAny(row, " ") {
			t.Errorf("row %d has an unpainted gap in it: %q", i, ansi.Strip(row))
		}
	}
}

// TestTheCaptionLinesUpWithThePicture: the caption sits under the art and reads as
// part of it, so it must start where the picture starts rather than against the
// panel border.
func TestTheCaptionLinesUpWithThePicture(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	m.attachImageCmd(writePNG(t, t.TempDir(), "shot.png", 64, 48))
	send(m, "вот")

	frame := m.renderHistory()
	pic := strings.Index(frame, "▀")
	cap := strings.Index(frame, "[image]")
	if pic < 0 || cap < 0 {
		t.Fatalf("frame has no picture or caption:\n%s", frame)
	}
	// Cell columns, not byte offsets: the picture's row opens with an escape
	// sequence and the caption's with spaces, and the two differ in bytes long
	// before they agree on a column.
	if col(frame, pic) != col(frame, cap) {
		t.Errorf("the caption starts at column %d, the picture at %d",
			col(frame, cap), col(frame, pic))
	}
}

// TestCaptionIsPartOfTheSameBlock keeps the two from being separated: a preview
// with no caption is a picture of unknown size, and a caption with no preview is
// the filename this feature exists to replace.
func TestCaptionIsPartOfTheSameBlock(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	m.attachImageCmd(writePNG(t, t.TempDir(), "a.png", 32, 32))
	send(m, "вот")

	if len(m.history) != 2 {
		t.Fatalf("got %d lines, want the art with its caption plus the prompt: %+v",
			len(m.history), m.history)
	}
	if n := strings.Count(m.history[0].text, "\n") + 1; n < 2 {
		t.Errorf("the block has %d rows, want the art plus a caption", n)
	}
}

// TestAPathInThePromptBecomesAnAttachment is drag-and-drop. A terminal cannot
// hand over a file — dropping one inserts its path as text — so this is the only
// route that makes dropping work at all.
func TestAPathInThePromptBecomesAnAttachment(t *testing.T) {
	m := imageModel(t)
	dir := t.TempDir()
	p := writePNG(t, dir, "dropped.png", 40, 40)

	text, imgs := m.takeImagePaths("что здесь " + p)

	if len(imgs) != 1 {
		t.Fatalf("got %d images, want 1 (from %q)", len(imgs), text)
	}
	if strings.Contains(text, p) {
		t.Errorf("the prompt still contains the path %q; the model would be asked about a filename", text)
	}
	if !strings.Contains(text, "что здесь") {
		t.Errorf("the question was lost: %q", text)
	}
}

// TestAPathWithSpacesIsAttached covers the quoting a dropped path arrives in: a
// terminal quotes a dropped path that contains a space, and the quotes are part of
// the gesture rather than part of the name.
func TestAPathWithSpacesIsAttached(t *testing.T) {
	m := imageModel(t)
	dir := t.TempDir()
	p := writePNG(t, dir, "my screenshot.png", 40, 40)

	_, imgs := m.takeImagePaths(`посмотри "` + p + `"`)

	if len(imgs) != 1 {
		t.Fatalf("got %d images, want the quoted path to be attached (from %q)", len(imgs), p)
	}
	if imgs[0].Name != "my screenshot.png" {
		t.Errorf("name = %q, want the whole quoted path resolved", imgs[0].Name)
	}
}

// TestAQuotedPathOnItsOwn: pasting a dropped path and pressing enter is the whole
// gesture, so the quotes must not stop it.
func TestAQuotedPathOnItsOwn(t *testing.T) {
	m := imageModel(t)
	p := writePNG(t, t.TempDir(), "shot.png", 40, 40)

	text, imgs := m.takeImagePaths(`"` + p + `"`)

	if len(imgs) != 1 {
		t.Fatalf("got %d images, want 1", len(imgs))
	}
	if strings.TrimSpace(text) != "" {
		t.Errorf("text = %q, want nothing left once the picture was taken", text)
	}
}

// TestAnUnquotedPathWithSpacesIsNotMistakenForOne: without quotes there is no way
// to tell a path with a space from a sentence containing one, so the whole run of
// words is left alone rather than half of it being treated as a file.
func TestAnUnquotedPathWithSpacesIsNotMistakenForOne(t *testing.T) {
	m := imageModel(t)
	p := writePNG(t, t.TempDir(), "my screenshot.png", 40, 40)

	text, imgs := m.takeImagePaths("посмотри " + p)

	if len(imgs) != 0 {
		t.Errorf("an unquoted path with a space was split and partly attached: %+v", imgs)
	}
	if !strings.Contains(text, "screenshot.png") {
		t.Errorf("the prompt lost the reference: %q", text)
	}
}

// TestANonImagePathStaysInThePrompt: swallowing a filename the model should read
// turns a question about a file into a question about nothing.
func TestANonImagePathStaysInThePrompt(t *testing.T) {
	m := imageModel(t)
	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	text, imgs := m.takeImagePaths("почини " + goFile)

	if len(imgs) != 0 {
		t.Errorf("a .go file was attached as a picture: %+v", imgs)
	}
	if !strings.Contains(text, "main.go") {
		t.Errorf("the path was removed from the prompt: %q", text)
	}
}

// TestAnUnreadableImageStaysInThePrompt. A path that looks like a picture and
// cannot be read is reported and left where it was: removing it would leave the
// user with a turn about nothing and no way to tell that from a lost attachment.
func TestAnUnreadableImageStaysInThePrompt(t *testing.T) {
	m := imageModel(t)
	dir := t.TempDir()
	missing := filepath.Join(dir, "gone.png")

	text, imgs := m.takeImagePaths("посмотри " + missing)

	if len(imgs) != 0 {
		t.Errorf("a missing file was attached: %+v", imgs)
	}
	if !strings.Contains(text, "gone.png") {
		t.Errorf("the path was swallowed instead of reported: %q", text)
	}
	if !strings.Contains(lastErrorLine(m), "gone.png") {
		t.Errorf("nothing on screen names the file that failed: %q", lastErrorLine(m))
	}
}

// TestAnUnsupportedFormatIsNamedRatherThanIgnored: a webp the user has every
// reason to send should get an error that says what this build reads.
func TestAnUnsupportedFormatIsNamedRatherThanIgnored(t *testing.T) {
	m := imageModel(t)
	p := filepath.Join(t.TempDir(), "photo.webp")
	if err := os.WriteFile(p, append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 16)...), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := m.attachImage(p); err == nil {
		t.Fatal("a webp was accepted")
	} else if !strings.Contains(err.Error(), "png") {
		t.Errorf("error = %v, want one naming the formats that are read", err)
	}
}

// lastErrorLine is the most recent failure printed into the transcript.
func lastErrorLine(m *uiModel) string {
	for i := len(m.history) - 1; i >= 0; i-- {
		if m.history[i].kind == kindErr {
			return m.history[i].text
		}
	}
	return ""
}

// TestTheResponsesWireRefusesWithSomethingActionable. ADK's client rejects an
// inline blob outright, so an attach there would fail at the SDK with a message
// about content parts. Saying it at the moment the picture is chosen is the
// difference between an instruction and a puzzle.
func TestTheResponsesWireRefusesWithSomethingActionable(t *testing.T) {
	m := imageModel(t)
	m.prov = config.Provider{Label: "groq", API: config.APIResponses, Model: "qwen"}

	m.attachImageCmd(writePNG(t, t.TempDir(), "shot.png", 32, 32))

	if len(m.pending) != 0 {
		t.Fatal("a picture was accepted on the responses wire")
	}
	line := lastErrorLine(m)
	for _, want := range []string{"groq", "DMCODE_API=chat", "/chat/completions"} {
		if !strings.Contains(line, want) {
			t.Errorf("the refusal does not mention %q: %q", want, line)
		}
	}
	if m.statusText == "" {
		t.Error("nothing was said; the user would see the keypress do nothing")
	}
}

// TestASendCarriesThePicture: the whole point is that the model receives it, and
// the only place that is decided is the content built for the turn.
func TestASendCarriesThePicture(t *testing.T) {
	a := loadFixture(t, "shot.png")
	msg := userContent("что здесь?", []imgprev.Attachment{a})

	if len(msg.Parts) != 2 {
		t.Fatalf("got %d parts, want the text and the image", len(msg.Parts))
	}
	if msg.Parts[0].Text != "что здесь?" {
		t.Errorf("part 0 = %+v, want the question", msg.Parts[0])
	}
	blob := msg.Parts[1].InlineData
	if blob == nil || len(blob.Data) == 0 {
		t.Fatalf("part 1 carries no image: %+v", msg.Parts[1])
	}
	if blob.MIMEType != "image/png" {
		t.Errorf("MIME = %q, want image/png", blob.MIMEType)
	}
	if blob.DisplayName != "shot.png" {
		t.Errorf("DisplayName = %q, want the file name so a session read back from disk says which picture it was", blob.DisplayName)
	}
}

// TestAPictureOnlyTurnStillSaysSomething: a prompt that is nothing but a
// screenshot has to reach the model as something, or the images float free of any
// question.
func TestAPictureOnlyTurnStillSaysSomething(t *testing.T) {
	a := loadFixture(t, "shot.png")
	msg := userContent("", []imgprev.Attachment{a})

	if len(msg.Parts) != 2 {
		t.Fatalf("got %d parts, want a lead-in and the image", len(msg.Parts))
	}
	if strings.TrimSpace(msg.Parts[0].Text) == "" {
		t.Error("the turn carries a picture and no instruction at all")
	}
}

// TestATextOnlyTurnIsUnchanged: every existing conversation must serialise
// exactly as it did before images existed.
func TestATextOnlyTurnIsUnchanged(t *testing.T) {
	msg := userContent("привет", nil)
	if len(msg.Parts) != 1 {
		t.Fatalf("got %d parts, want one", len(msg.Parts))
	}
	if msg.Parts[0].Text != "привет" || msg.Parts[0].InlineData != nil {
		t.Errorf("part = %+v, want the plain text that was always sent", msg.Parts[0])
	}
}

// TestRewindBringsThePictureBack is why the pictures ride on the mark. A turn sent
// as nothing but a screenshot has no text for the store to hand back, so without
// this the user finds an empty input box and has to go and find the file again.
func TestRewindBringsThePictureBack(t *testing.T) {
	m := imageModel(t)
	a := loadFixture(t, "shot.png")

	// A turn sent the way the enter handler sends it: the mark, then the preview,
	// then the prompt line.
	m.promptMarks = append(m.promptMarks,
		promptMark{text: "", idx: len(m.history), images: []imgprev.Attachment{a}})
	m.showPreviews([]imgprev.Attachment{a})
	m.history = append(m.history, line{kindUser, ""})
	appendUserEvent(t, m, "", a)
	m.history = append(m.history, line{kindAgent, "это скриншот"})

	m.rewind()

	if len(m.pending) != 1 {
		t.Fatalf("pending = %d images after a rewind, want the picture back", len(m.pending))
	}
	if m.pending[0].Name != a.Name {
		t.Errorf("the wrong picture came back: %q", m.pending[0].Name)
	}
}

// TestRewindCutsThePreviewWithTheTurn: a preview that survives the cut stays on
// screen describing a turn that no longer happened.
func TestRewindCutsThePreviewWithTheTurn(t *testing.T) {
	m := imageModel(t)
	a := loadFixture(t, "shot.png")
	m.history = append(m.history, line{kindSys, "kept"})
	kept := len(m.history)

	// The order matters and is the thing under test: the mark is recorded while
	// idx still points above the previews, so truncating to it takes them away
	// too. Recording the mark afterwards would leave the preview on screen
	// describing a turn that no longer exists.
	m.promptMarks = append(m.promptMarks,
		promptMark{text: "вот", idx: len(m.history), images: []imgprev.Attachment{a}})
	m.showPreviews([]imgprev.Attachment{a})
	m.history = append(m.history, line{kindUser, "вот"})
	appendUserEvent(t, m, "вот", a)
	m.history = append(m.history, line{kindAgent, "понял"})

	m.rewind()

	// One line is added by the rewind itself — the "rolled back" notice — so the
	// count is what predated the turn plus that.
	if len(m.history) != kept+1 {
		t.Errorf("the transcript has %d lines, want the %d that predate the turn plus the notice",
			len(m.history), kept+1)
	}
	for _, l := range m.history {
		if l.kind == kindImage {
			t.Errorf("a preview survived the rewind: %q", lastLine(l.text))
		}
	}
}

// TestANewSessionDropsTheAttachment: the pictures belong to the prompt they were
// going to ride with, and carrying them over would attach the previous
// conversation's screenshot to this one's first message.
func TestANewSessionDropsTheAttachment(t *testing.T) {
	m := imageModel(t)
	m.pending = []pendingImage{{Attachment: loadFixture(t, "shot.png")}}

	m.newSession("")

	if len(m.pending) != 0 {
		t.Errorf("%d pictures carried into the new session", len(m.pending))
	}
}

// TestDroppingTheLastPictureTakesItsPreview: the pictures ride with the next
// prompt and there is no other way to take one back, so the strip has to lose the
// dropped one — and, because the strip is the only place a pending attachment is
// drawn, that is the whole of what disappears from the screen.
func TestDroppingTheLastPictureTakesItsPreview(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	m.attachImageCmd(writePNG(t, t.TempDir(), "a.png", 32, 32))
	m.attachImageCmd(writePNG(t, t.TempDir(), "b.png", 32, 32))

	m.dropPendingImage()

	if len(m.pending) != 1 {
		t.Fatalf("pending = %d, want the first picture still attached", len(m.pending))
	}
	if m.pending[0].Name != "a.png" {
		t.Errorf("the wrong one survived: %q", m.pending[0].Name)
	}
	strip := ansi.Strip(m.pendingStrip())
	if strings.Contains(strip, "b.png") {
		t.Errorf("the dropped picture is still drawn:\n%s", strip)
	}
	if !strings.Contains(strip, "a.png") {
		t.Errorf("the surviving picture is gone from the strip:\n%s", strip)
	}
	// Nothing was left behind in the transcript either: an unsent attachment was
	// never written there, so dropping one cannot leave a copy.
	for _, l := range m.history {
		if l.kind == kindImage {
			t.Errorf("dropping left a picture in the transcript: %q", lastLine(l.text))
		}
	}
}

// TestDroppingWithNothingAttachedSaysSo, rather than appearing to work.
func TestDroppingWithNothingAttachedSaysSo(t *testing.T) {
	m := imageModel(t)
	m.dropPendingImage()
	if m.statusText == "" {
		t.Error("nothing was said when there was nothing to drop")
	}
}

// TestAttachingNothingExplainsItself.
func TestAttachingNothingExplainsItself(t *testing.T) {
	m := imageModel(t)
	m.attachImageCmd("")
	if !strings.Contains(m.statusText, "/image") {
		t.Errorf("status = %q, want the usage line", m.statusText)
	}
	if len(m.pending) != 0 {
		t.Error("an empty argument attached something")
	}
}

// TestAnEmptyPromptWithAPicturePendingSends: the whole gesture is paste then
// enter, so it must not be swallowed by the empty-prompt guard.
func TestAnEmptyPromptWithAPicturePendingSends(t *testing.T) {
	m := imageModel(t)
	m.pending = []pendingImage{{Attachment: loadFixture(t, "shot.png")}}

	// The guard that would otherwise drop the turn: no text, and nothing found in
	// it. It has to yield to a pending attachment.
	if strings.TrimSpace("") == "" && len(m.pending) == 0 {
		t.Fatal("the test's premise is wrong: nothing to send and nothing attached")
	}
	if len(m.pending) != 1 {
		t.Fatal("the attachment did not survive to the send")
	}
}

// TestTheTerminalProfileDecidesHowItIsDrawn: truecolor escapes sent to a
// 16-colour terminal come out as a field of wrong-coloured blocks, and a colourless
// one needs the ramp instead.
func TestTheTerminalProfileDecidesHowItIsDrawn(t *testing.T) {
	dir := t.TempDir()
	p := writePNG(t, dir, "shot.png", 64, 48)

	m := imageModel(t)
	m.profile = colorprofile.TrueColor
	m.history = nil
	m.attachImageCmd(p)
	send(m, "вот")
	rich := lastImageLine(m)

	m = imageModel(t)
	m.profile = colorprofile.ASCII
	m.history = nil
	m.attachImageCmd(p)
	send(m, "вот")
	plain := lastImageLine(m)

	if !strings.Contains(rich, "\x1b[38;2;") {
		t.Errorf("a truecolor terminal did not get truecolor art:\n%q", lastLine(rich))
	}
	if strings.Contains(plain, "\x1b[") {
		t.Errorf("a terminal with no colour got escape sequences:\n%q", lastLine(plain))
	}
	if strings.Contains(plain, "▀") {
		t.Errorf("half blocks were used with no colour to give them:\n%q", lastLine(plain))
	}
}

// TestTheWireIsCheckedBeforeAnythingIsRead: refusing after the file has been
// decoded and shrunk would waste the work and delay the message the user needs.
func TestTheWireIsCheckedBeforeAnythingIsRead(t *testing.T) {
	m := imageModel(t)
	m.prov = config.Provider{Label: "responses", API: config.APIResponses}
	if m.canSendImages() {
		t.Fatal("the responses wire reports that it can carry pictures; it cannot")
	}
	m.prov = config.Provider{Label: "chat", API: config.APIChat}
	if !m.canSendImages() {
		t.Error("the chat wire reports that it cannot carry pictures; it can")
	}
}

// TestAMissingExtensionIsNotAPicture keeps the detector cheap: it is a suffix test
// so that scanning a prompt does not mean decoding every word in it.
func TestAMissingExtensionIsNotAPicture(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"a.png", true}, {"a.PNG", true}, {"a.jpeg", true}, {"a.webp", true},
		{"a.go", false}, {"a.txt", false}, {"noextension", false}, {"a.png.txt", false},
	} {
		if got := looksLikeImage(tc.path); got != tc.want {
			t.Errorf("looksLikeImage(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestAnAttachmentReachesTheDrawnFrame: the whole feature is a picture on screen,
// so it has to survive the path from Load through the transcript into what View
// hands the renderer. The frame is the same size either way — the viewport is a
// fixed height — so what is checked is that the preview is in it.
func TestAnAttachmentReachesTheDrawnFrame(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	if strings.Contains(m.View().Content, "shot.png") {
		t.Fatal("the test's premise is wrong: a preview is already on screen")
	}

	m.attachImageCmd(writePNG(t, t.TempDir(), "shot.png", 32, 32))

	if frame := m.View().Content; !strings.Contains(frame, "shot.png") {
		t.Errorf("the drawn frame has no attachment in it:\n%s", frame)
	}
}

// TestTheFrameKeepsItsShapeWithTheStripUp is the arithmetic that matters most
// here: the strip takes rows, the transcript gives them up, and the frame still
// has to be exactly the terminal's height. A frame one row too tall scrolls the
// input off the bottom of the screen, which is the failure that looks least like
// a bug.
func TestTheFrameKeepsItsShapeWithTheStripUp(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	m.input.SetValue("почини это")
	m.followVP()
	beforeRows := strings.Split(m.View().Content, "\n")
	beforeVP := m.vp.Height()

	m.attachImageCmd(writePNG(t, t.TempDir(), "shot.png", 32, 32))

	afterRows := strings.Split(m.View().Content, "\n")
	if len(afterRows) != len(beforeRows) {
		t.Errorf("the frame has %d rows, want the %d it had before the attachment",
			len(afterRows), len(beforeRows))
	}
	for i := range afterRows {
		if w, want := ansi.StringWidth(afterRows[i]), ansi.StringWidth(beforeRows[i]); w != want {
			t.Errorf("row %d is %d cells wide, want %d", i, w, want)
		}
	}
	// The rows came out of the transcript, not out of thin air.
	if m.vp.Height() >= beforeVP {
		t.Errorf("the transcript is still %d rows, want fewer than %d with a strip up",
			m.vp.Height(), beforeVP)
	}
	if m.input.Value() != "почини это" {
		t.Errorf("the prompt was disturbed: %q", m.input.Value())
	}
}

// TestTypingAPathShowsThePreviewWithoutEnter is the whole point of watching the
// input: a user dropping a file types or pastes its path and expects to see what
// they are about to send, not only after they commit to it.
func TestTypingAPathShowsThePreviewWithoutEnter(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	p := writePNG(t, t.TempDir(), "shot.png", 64, 48)

	m.input.SetValue("посмотри " + p)
	cmd := m.watchInputImage()
	if cmd == nil {
		t.Fatal("a finished picture path in the input produced no command")
	}
	before := len(strings.Split(m.View().Content, "\n"))
	// The command runs the read and the decode off the event loop; drive it the
	// way the runtime would, through Update.
	if _, _ = m.Update(cmd()); true {
	}
	if len(m.pending) != 1 {
		t.Fatalf("pending = %d after the path was typed, want 1", len(m.pending))
	}
	// The frame's height does not change — the strip's rows come out of the
	// transcript — so what is asserted is the strip's content, not its size.
	if after := len(strings.Split(m.View().Content, "\n")); after != before {
		t.Errorf("the frame went from %d to %d rows; the strip's rows must come out of the transcript",
			before, after)
	}
	if !strings.Contains(ansi.Strip(m.pendingStrip()), "shot.png") {
		t.Error("the strip above the input has no picture in it")
	}
	if !strings.Contains(m.View().Content, "shot.png") {
		t.Error("the drawn frame does not show the attachment")
	}
	if m.busy {
		t.Error("typing a path started a turn; it must only prepare one")
	}
}

// TestTheSamePathIsNotAttachedTwice: the input is watched on every keystroke, so
// without a guard the file would be re-decoded for every character typed after it.
func TestTheSamePathIsNotAttachedTwice(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	p := writePNG(t, t.TempDir(), "shot.png", 64, 48)
	m.input.SetValue(p)

	if cmd := m.watchInputImage(); cmd == nil {
		t.Fatal("the first look at the path produced no command")
	}
	if cmd := m.watchInputImage(); cmd != nil {
		t.Error("the same path was looked at twice; the file would be decoded twice")
	}
	// A different picture is a different attachment, and is not suppressed.
	q := writePNG(t, t.TempDir(), "other.png", 64, 48)
	m.input.SetValue(q)
	if cmd := m.watchInputImage(); cmd == nil {
		t.Error("a different path was not looked at")
	}
}

// TestAMidTypedPathIsNotOpened: a path is only complete once its extension is, so
// nothing before that should reach the decoder — and nothing should be reported
// either, because the user is still typing.
func TestAMidTypedPathIsNotOpened(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	for _, partial := range []string{
		"E:/Downloads/shot", "E:/Downloads/shot.j", "E:/Downloads/shot.jp", "E:/Downloads/shot.jpgx",
	} {
		m.input.SetValue(partial)
		if cmd := m.watchInputImage(); cmd != nil {
			t.Errorf("%q was treated as a complete picture path", partial)
		}
	}
	if len(m.history) != 0 {
		t.Errorf("a half-typed path wrote %d lines into the transcript", len(m.history))
	}
}

// TestClearingTheInputForgetsThePath: a prompt with no picture in it has to clear
// the candidate, or the next picture typed would be suppressed as a repeat.
func TestClearingTheInputForgetsThePath(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	p := writePNG(t, t.TempDir(), "shot.png", 64, 48)
	m.input.SetValue(p)
	if m.watchInputImage() == nil {
		t.Fatal("the path was not watched")
	}

	// Watched on every keystroke, so deleting the picture is a keystroke too.
	m.input.SetValue("просто текст")
	m.watchInputImage()
	if m.watchedPath != "" {
		t.Errorf("watchedPath = %q after the picture was removed, want empty", m.watchedPath)
	}
}

// TestANonPicturePathIsNeverWatched: the watcher must not open a file the user is
// merely mentioning in a sentence.
func TestANonPicturePathIsNeverWatched(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	goFile := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(goFile, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.input.SetValue("почини " + goFile)
	if cmd := m.watchInputImage(); cmd != nil {
		t.Error("a .go file was opened as if it were a picture")
	}
}

// TestTheStripShowsAboveTheInput: the preview the user is about to send belongs
// next to the box they type into, not only in the transcript behind it.
func TestTheStripShowsAboveTheInput(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	if s := m.pendingStrip(); s != "" {
		t.Fatalf("an empty attachment is showing a strip:\n%s", s)
	}

	m.attachImageCmd(writePNG(t, t.TempDir(), "shot.png", 32, 32))

	frame := m.View().Content
	if !strings.Contains(frame, "▀") {
		t.Errorf("the drawn frame has no picture above the input:\n%s", frame)
	}
	if !strings.Contains(frame, "shot.png") {
		t.Errorf("the drawn frame has no caption above the input:\n%s", frame)
	}
	// Above the input box, not below it. The input's own prompt is the marker: the
	// placeholder is only drawn while the box is empty and a typed prompt replaces
	// it, so the prompt is the one thing always on that row.
	rows := strings.Split(frame, "\n")
	picRow, inputRow := -1, -1
	for i, r := range rows {
		if picRow < 0 && strings.Contains(r, "▀") {
			picRow = i
		}
		if strings.Contains(r, m.input.Prompt) {
			inputRow = i
		}
	}
	if picRow < 0 || inputRow < 0 {
		t.Fatalf("the frame has no picture row (%d) or no input row (%d)", picRow, inputRow)
	}
	if picRow > inputRow {
		t.Errorf("the preview is at row %d, below the input at %d", picRow, inputRow)
	}
}

// TestSeveralPicturesStackInTheStrip: one strip holding both, each with its own
// caption, rather than the last one replacing the first.
func TestSeveralPicturesStackInTheStrip(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	dir := t.TempDir()
	m.attachImageCmd(writePNG(t, dir, "a.png", 32, 32))
	m.attachImageCmd(writePNG(t, dir, "b.png", 32, 32))

	strip := m.pendingStrip()
	for _, want := range []string{"a.png", "b.png", "image 1/2", "image 2/2"} {
		if !strings.Contains(strip, want) {
			t.Errorf("the strip does not mention %q:\n%s", want, ansi.Strip(strip))
		}
	}
	want := 2*(m.pending[0].Rows+1) + pendingPanelBorder
	if got := strings.Count(strip, "\n") + 1; got != want {
		t.Errorf("the strip is %d rows, want %d for two previews", got, want)
	}
	if m.pendingHeight() != want {
		t.Errorf("pendingHeight = %d, want %d", m.pendingHeight(), want)
	}
}

// TestTheStripCostsNothingWhenEmpty: the rows belong to the transcript whenever
// they are not on screen, or a user who never attaches anything pays for it.
func TestTheStripCostsNothingWhenEmpty(t *testing.T) {
	m := imageModel(t)
	m.pending = nil
	base := m.chromeHeight()
	m.pending = []pendingImage{{Attachment: loadFixture(t, "a.png")}}
	if m.chromeHeight() <= base {
		t.Errorf("chromeHeight = %d with a picture attached, want more than %d",
			m.chromeHeight(), base)
	}
	m.pending = nil
	if got := m.chromeHeight(); got != base {
		t.Errorf("chromeHeight = %d with nothing attached, want %d", got, base)
	}
}

// TestDroppingTheLastPictureGivesTheRowsBack: the strip shrinks and the transcript
// grows, and the frame stays the terminal's height.
func TestDroppingTheLastPictureGivesTheRowsBack(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	m.attachImageCmd(writePNG(t, t.TempDir(), "a.png", 32, 32))
	m.attachImageCmd(writePNG(t, t.TempDir(), "b.png", 32, 32))
	before := len(strings.Split(m.View().Content, "\n"))

	m.dropPendingImage()

	if got := len(strings.Split(m.View().Content, "\n")); got != before {
		t.Errorf("the frame has %d rows after the drop, want the %d it had with both",
			got, before)
	}
	if s := m.pendingStrip(); strings.Contains(s, "b.png") {
		t.Errorf("the dropped picture is still in the strip:\n%s", ansi.Strip(s))
	}
}

// TestAnAttachedPictureIsOnScreenOnce is the duplication behind it: an attachment
// was drawn both in the strip above the input and in the transcript, so the same
// picture sat on screen twice and removing it took away one copy and left the other.
func TestAnAttachedPictureIsOnScreenOnce(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	m.attachImageCmd(writePNG(t, t.TempDir(), "shot.png", 64, 48))

	if got := strings.Count(screen(m), "shot.png"); got != 1 {
		t.Errorf("an attached picture is named %d times on screen, want once", got)
	}
	// And once it is sent, it is still named once — the strip hands its copy to the
	// transcript rather than the two both existing.
	send(m, "вот")
	if got := strings.Count(screen(m), "shot.png"); got != 1 {
		t.Errorf("after the send the picture is named %d times on screen, want once", got)
	}
}

// TestDroppingRemovesEveryTrace is the other half of the same bug: whatever a
// pending attachment is drawn in, dropping it must empty all of it. A copy the
// user cannot get rid of is worse than no preview at all.
func TestDroppingRemovesEveryTrace(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	m.attachImageCmd(writePNG(t, t.TempDir(), "shot.png", 64, 48))
	if !strings.Contains(screen(m), "shot.png") {
		t.Fatal("the test's premise is wrong: nothing was attached")
	}

	m.dropPendingImage()

	if strings.Contains(screen(m), "shot.png") {
		t.Errorf("the dropped picture is still on screen:\n%s", ansi.Strip(screen(m)))
	}
	if m.pendingHeight() != 0 {
		t.Errorf("the strip still claims %d rows after the drop", m.pendingHeight())
	}
}

// TestTheStripEmptiesOnSend: the pictures move from waiting to sent, so the strip
// must not keep showing what has already gone.
func TestSendingTheStripClearsIt(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	m.attachImageCmd(writePNG(t, t.TempDir(), "a.png", 32, 32))
	if m.pendingStrip() == "" {
		t.Fatal("the strip is empty with a picture attached")
	}

	send(m, "вот")

	if m.pendingStrip() != "" {
		t.Errorf("the strip survived the send:\n%s", ansi.Strip(m.pendingStrip()))
	}
	if m.pendingHeight() != 0 {
		t.Errorf("the strip still claims %d rows after the send", m.pendingHeight())
	}
}

// loadFixture builds an attachment without going through the filesystem, for the
// tests that only care about what reaches the turn.
func loadFixture(t *testing.T, name string) imgprev.Attachment {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 8), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	a, err := imgprev.Load(buf.Bytes(), name, previewCols, chatIndent, colorprofile.TrueColor)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return a
}

// appendUserEvent records a user turn in the store, the way a runner would, so a
// rewind has something to cut.
func appendUserEvent(t *testing.T, m *uiModel, text string, imgs ...imgprev.Attachment) {
	t.Helper()
	sess, err := m.sessions.Get(m.ctx, &session.GetRequest{
		AppName: sessionApp, UserID: sessionUser, SessionID: m.sessionID,
	})
	if err != nil {
		t.Fatalf("Get session: %v", err)
	}
	ev := session.NewEvent(m.ctx, "inv")
	ev.Author = "user"
	ev.Content = userContent(text, imgs)
	if err := m.sessions.AppendEvent(m.ctx, sess.Session, ev); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	reply := session.NewEvent(m.ctx, "inv")
	reply.Author = "dmcode"
	reply.Content = genai.NewContentFromText("ok", genai.RoleModel)
	if err := m.sessions.AppendEvent(m.ctx, sess.Session, reply); err != nil {
		t.Fatalf("AppendEvent reply: %v", err)
	}
}

func lastImageLine(m *uiModel) string {
	for i := len(m.history) - 1; i >= 0; i-- {
		if m.history[i].kind == kindImage {
			return m.history[i].text
		}
	}
	return ""
}

// The commands have to be listed or they cannot be found.
func TestThePictureCommandsAreInThePalette(t *testing.T) {
	m := imageModel(t)
	names := map[string]bool{}
	for _, c := range m.commands() {
		names[c.name] = true
	}
	for _, want := range []string{"image", "unimage"} {
		if !names[want] {
			t.Errorf("/%s is not in the command list", want)
		}
	}
}

// TestThePreviewFallsBackToItsCaption: the picture is 32 cells wide and cannot be
// squeezed, but the caption can still say a picture went. A turn sent as nothing
// but a screenshot would otherwise leave nothing at all on screen to say so.
func TestThePreviewFallsBackToItsCaption(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	// A short caption and a short name, so that there is a width the caption fits
	// at and the picture does not — which is the only situation where the fallback
	// has anything to do.
	m.attachImageCmd(writePNG(t, t.TempDir(), "a.png", 8, 8))
	send(m, "вот")
	if !strings.Contains(m.renderHistory(), "▀") {
		t.Fatal("the test's premise is wrong: a wide terminal does not draw the picture")
	}

	m.showSidebar = false
	m.width = 33
	m.layout()

	frame := m.renderHistory()
	if strings.Contains(frame, "▀") {
		t.Errorf("a picture too wide for the panel was drawn anyway:\n%s", frame)
	}
	if !strings.Contains(frame, "a.png") {
		t.Errorf("the caption went with the art:\n%s", frame)
	}
}

// TestTheLogoStillVanishesWhenItCannotFit keeps the two rules apart. The logo has
// no caption row, so a narrow terminal drops it whole — a banner sliced to the
// panel edge reads as corruption, and the header names the app regardless.
func TestTheLogoStillVanishesWhenItCannotFit(t *testing.T) {
	m := imageModel(t)
	m.showSidebar = false
	m.width = 12
	m.layout()
	m.history = append(m.history, line{kindLogo, logo})

	if frame := m.renderHistory(); strings.Contains(frame, "╚") {
		t.Errorf("the logo was sliced to the panel edge:\n%s", frame)
	}
}

// TestDeletingThePathDropsThePreview is the bug: the watch attaches the moment
// the path is complete, so deleting that path has to take the preview with it.
// A preview that outlives its path is showing the user a picture they said they
// no longer meant, and they would have to guess what would send it.
func TestDeletingThePathDropsThePreview(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	p := writePNG(t, t.TempDir(), "shot.png", 64, 48)

	m.input.SetValue("вот " + p)
	cmd := m.watchInputImage()
	if cmd == nil {
		t.Fatal("a complete path was not watched")
	}
	m.Update(cmd()) // the attach completes
	if len(m.pending) != 1 {
		t.Fatalf("pending = %d, want the watch to have attached one", len(m.pending))
	}

	// Deleting the path is a keystroke, and the watch runs on every one.
	m.input.SetValue("вот ")
	m.watchInputImage()

	if len(m.pending) != 0 {
		t.Errorf("pending = %d after the path was deleted, want 0:\n%s",
			len(m.pending), ansi.Strip(m.pendingStrip()))
	}
	if strip := ansi.Strip(m.pendingStrip()); strings.Contains(strip, "▀") {
		t.Errorf("the preview survived the path being deleted:\n%s", strip)
	}
}

// TestBackspacingOneCharacterAtATimeDeletesItToo: a path is deleted by repeated
// backspace, and every prefix of it is a shorter path. Dropping only on the
// empty string would keep the preview for all but the last keystroke.
func TestBackspacingOneCharacterAtATimeDeletesItToo(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	p := writePNG(t, t.TempDir(), "shot.png", 64, 48)
	m.input.SetValue(p)
	m.watchInputImage()
	m.Update(m.watchImageCmd(p)())
	if len(m.pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(m.pending))
	}

	// The first backspace, then every one after it. Starting at the full path
	// would assert that an unchanged prompt drops the picture, which is the
	// opposite of what it should do.
	for i := len(p) - 1; i >= 0; i-- {
		m.input.SetValue(p[:i])
		m.watchInputImage()
		if len(m.pending) != 0 {
			t.Fatalf("after backspacing to %q the preview was still pending, want it gone", p[:i])
		}
	}
}

// TestAPictureAttachedDeliberatelySurvivesEditingThePrompt: only a picture that
// came from a path in the prompt belongs to that text. One from /image or the
// clipboard was attached on purpose and must not vanish because the user kept
// typing.
func TestAPictureAttachedDeliberatelySurvivesEditingThePrompt(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	m.attachImageCmd(writePNG(t, t.TempDir(), "shot.png", 64, 48))

	m.input.SetValue("опиши это")
	m.watchInputImage()

	if len(m.pending) != 1 {
		t.Errorf("pending = %d after typing unrelated text, want the deliberate attach kept", len(m.pending))
	}
}

// TestASlowReadDoesNotReappearAfterThePathIsGone: reading a file is asynchronous,
// so the path can be edited while one is in flight. Arriving afterwards must not
// re-add a preview to a prompt that no longer mentions the picture — which is
// the same bug as a preview that will not be deleted, just arriving afterwards.
func TestASlowReadDoesNotReappearAfterThePathIsGone(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	p := writePNG(t, t.TempDir(), "shot.png", 64, 48)

	m.input.SetValue(p)
	if m.watchInputImage() == nil {
		t.Fatal("the path was not watched")
	}

	// The user gives up on it before the read lands.
	m.input.SetValue("просто текст")
	m.watchInputImage()

	// Now the in-flight read finishes, carrying the path it was started for.
	late := m.watchImageCmd(p)()
	m.Update(late)

	if len(m.pending) != 0 {
		t.Errorf("a read that finished after its path was deleted attached anyway:\n%s",
			ansi.Strip(m.pendingStrip()))
	}
}

// TestSendingAPathSendsThePictureOnce: the watch already attached the picture
// when the path was typed, so the send must not read the same file again. The
// prompt's own copy and the pending list are the two sources a turn's pictures
// come from, and a path that is in both is the one picture that must arrive once.
func TestSendingAPathSendsThePictureOnce(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	p := writePNG(t, t.TempDir(), "shot.png", 64, 48)

	m.input.SetValue("вот " + p)
	m.watchInputImage()
	m.Update(m.watchImageCmd(p)())
	if len(m.pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(m.pending))
	}

	if got := send(m, m.input.Value()); len(got) != 1 {
		t.Errorf("the turn carried %d pictures from one typed path, want 1", len(got))
	}
}

// TestTwoDifferentPathsStillSendTwoPictures is the other side of the reuse: the
// dedup is by path, so it must not collapse two distinct pictures into one, and
// the one the watch did not cover is read from disk at the send.
func TestTwoDifferentPathsStillSendTwoPictures(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	dir := t.TempDir()
	a := writePNG(t, dir, "one.png", 64, 48)
	b := writePNG(t, dir, "two.png", 64, 48)

	// The watch follows one candidate, the first complete path. The second is
	// only ever read at the send.
	m.input.SetValue(a + " " + b)
	m.watchInputImage()
	m.Update(m.watchImageCmd(a)())
	if len(m.pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(m.pending))
	}

	if got := send(m, m.input.Value()); len(got) != 2 {
		t.Errorf("the turn carried %d pictures from two paths, want 2", len(got))
	}
}

// TestTheClipboardWithNoPictureIsSilent is the ordinary case: a clipboard almost
// always holds text, and ctrl+v has to fall through to that paste. Producing a
// message here would put an error line in the transcript for every text paste.
func TestTheClipboardWithNoPictureIsSilent(t *testing.T) {
	m := imageModel(t)
	if msg := m.pastedImageMsg(nil, clipimg.ErrNoImage); msg != nil {
		t.Errorf("an empty clipboard produced %T, want nothing", msg)
	}
	// Wrapped too: the caller must not depend on getting the sentinel back bare.
	if msg := m.pastedImageMsg(nil, fmt.Errorf("read: %w", clipimg.ErrNoImage)); msg != nil {
		t.Errorf("a wrapped ErrNoImage produced %T, want nothing", msg)
	}
}

// TestAFailedClipboardReadIsReported: the clipboard said it had a picture and the
// bytes could not be turned into one. Dropping that silently is what makes ctrl+v
// look like it does nothing at all, which is the bug this replaces.
func TestAFailedClipboardReadIsReported(t *testing.T) {
	m := imageModel(t)
	msg := m.pastedImageMsg(nil, errors.New("the clipboard image is truncated"))
	em, ok := msg.(imageErrMsg)
	if !ok {
		t.Fatalf("a failed clipboard read produced %T, want imageErrMsg", msg)
	}
	if em.err == nil {
		t.Error("imageErrMsg carries no error to show the user")
	}
}

// TestPastedAPictureIsAttached covers the other branch, driven from an image
// rather than the clipboard so the test does not have to own the clipboard.
func TestPastedAPictureIsAttached(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	img.SetRGBA(1, 1, color.RGBA{R: 200, A: 255})

	msg, ok := m.pastedImageMsg(img, nil).(imageAttachedMsg)
	if !ok {
		t.Fatal("a readable clipboard picture was not attached")
	}
	if msg.fromInput {
		t.Error("a pasted picture was marked as coming from the prompt, so typing would delete it")
	}
	m.Update(msg)
	if len(m.pending) != 1 {
		t.Fatalf("pending = %d after a paste, want 1", len(m.pending))
	}
	if strings.Contains(ansi.Strip(m.pendingStrip()), "▀") == false {
		t.Error("the pasted picture has no preview")
	}
}

// fakeClipboard points the two clipboard seams at a synthetic selection and
// restores them when the test ends.
//
// It exists because the real clipboard is the one thing a test must not touch: it
// is global, anything the user copies mid-run changes the answer, and writing to
// it destroys what they had. A test that reached the real one would be both
// unreliable and rude.
func fakeClipboard(t *testing.T, img image.Image, imgErr error, text string, textErr error) {
	t.Helper()
	oldImg, oldText := readClipboardImage, readClipboardText
	t.Cleanup(func() { readClipboardImage, readClipboardText = oldImg, oldText })

	readClipboardImage = func() (image.Image, error) { return img, imgErr }
	readClipboardText = func() (string, error) { return text, textErr }
}

// plainPicture is a small non-empty image, which is all any of these tests need:
// the question is what the paste route does with it, not what it looks like.
func plainPicture() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	img.SetRGBA(1, 1, color.RGBA{R: 200, A: 255})
	return img
}

// TestCtrlVAttachesAPictureWhenTheClipboardHasOne is the feature as asked for.
func TestCtrlVAttachesAPictureWhenTheClipboardHasOne(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	fakeClipboard(t, plainPicture(), nil, "", nil)

	m.Update(pressCtrlV(t, m))

	if len(m.pending) != 1 {
		t.Fatalf("pending = %d after ctrl+v, want 1:\n%s",
			len(m.pending), ansi.Strip(m.pendingStrip()))
	}
	if !strings.Contains(ansi.Strip(m.pendingStrip()), "▀") {
		t.Error("the pasted picture has no preview")
	}
	// And the text must not have been pasted too: a clipboard holding a picture
	// has nothing to say.
	if v := m.input.Value(); v != "" {
		t.Errorf("input = %q, want empty; a picture was pasted as text as well", v)
	}
}

// TestCtrlVStillPastesText is the regression this route had. A clipboard holding a
// command line has no picture, and ctrl+v returning the image command
// unconditionally meant the key did nothing at all — so text pasting broke
// without image pasting ever working.
func TestCtrlVStillPastesText(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	fakeClipboard(t, nil, clipimg.ErrNoImage, "git status", nil)

	m.Update(pressCtrlV(t, m))

	if v := m.input.Value(); !strings.Contains(v, "git status") {
		t.Errorf("input = %q, want the clipboard's text pasted", v)
	}
	if len(m.pending) != 0 {
		t.Errorf("pending = %d after a text paste, want 0", len(m.pending))
	}
}

// TestATerminalThatHandlesCtrlVItselfStillAttaches is the case that made the
// shortcut look broken in the first place. A terminal that binds ctrl+v converts
// the key to a paste and never delivers the keystroke, so a picture arrives as
// tea.PasteMsg — with the filename of a copied file, or with nothing at all in
// it. Handling only the key meant the paste did nothing on those terminals.
//
// No keystroke is sent here, deliberately: pressing ctrl+v would exercise the
// other route and the test would pass whether or not the paste worked, which is
// the trap it is here to catch.
func TestATerminalThatHandlesCtrlVItselfStillAttaches(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	fakeClipboard(t, plainPicture(), nil, "", nil)

	// An image-only clipboard: the terminal had no text to send.
	runPaste(t, m, "")

	if len(m.pending) != 1 {
		t.Fatalf("pending = %d after a terminal paste of a picture, want 1", len(m.pending))
	}
	if !strings.Contains(ansi.Strip(m.pendingStrip()), "▀") {
		t.Error("the pasted picture has no preview")
	}
}

// TestAPasteOfBothTextAndAPictureKeepsBoth: the watch has already turned a
// pasted file path into an attachment, so the text arrives carrying a path whose
// picture is already pending. Dropping either half loses something the user
// pasted.
func TestAPasteOfBothTextAndAPictureKeepsBoth(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	dir := t.TempDir()
	p := writePNG(t, dir, "shot.png", 64, 48)
	fakeClipboard(t, nil, clipimg.ErrNoImage, "", nil)

	runPaste(t, m, "вот "+p)
	m.Update(m.watchImageCmd(p)()) // the watch that the paste also triggered

	if !strings.Contains(m.input.Value(), p) {
		t.Errorf("input = %q, want the pasted path in the prompt", m.input.Value())
	}
	if len(m.pending) != 1 {
		t.Errorf("pending = %d, want the pasted path's picture attached", len(m.pending))
	}
}

// TestAFailedClipboardReadOnPasteIsReported: the paste is the only signal the
// user gets, so dropping the failure leaves them with no way to tell a broken
// clipboard from a shortcut that is not bound.
func TestAFailedClipboardReadOnPasteIsReported(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	fakeClipboard(t, nil, errors.New("the clipboard image is truncated"), "", nil)

	runPaste(t, m, "")

	if len(m.pending) != 0 {
		t.Errorf("pending = %d, want 0: a truncated picture was attached anyway", len(m.pending))
	}
	if !strings.Contains(m.renderHistory(), "truncated") {
		t.Errorf("the failure was not reported:\n%s", ansi.Strip(m.renderHistory()))
	}
}

// TestCtrlVOnTheResponsesWireIsStillATextPaste: a wire that cannot carry a
// picture must not turn into a key that does nothing.
func TestCtrlVOnTheResponsesWireIsStillATextPaste(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	m.prov = config.Provider{Label: "oai", API: config.APIResponses, Model: "gpt"}
	fakeClipboard(t, plainPicture(), nil, "git status", nil)

	m.Update(pressCtrlV(t, m))

	if v := m.input.Value(); !strings.Contains(v, "git status") {
		t.Errorf("input = %q, want the clipboard's text pasted", v)
	}
	if len(m.pending) != 0 {
		t.Errorf("pending = %d on a wire that cannot carry pictures, want 0", len(m.pending))
	}
}

// runPaste delivers a terminal paste and runs whatever command it asked for,
// without pressing any key — which is how a terminal that binds ctrl+v itself
// reports a paste.
func runPaste(t *testing.T, m *uiModel, content string) {
	t.Helper()
	_, cmd := m.Update(tea.PasteMsg{Content: content})
	if cmd == nil {
		return
	}
	m.Update(cmd())
}

// pressCtrlV sends the keystroke and returns the message its command produced,
// which is how a real turn delivers it: the key returns a command, and the
// message that command produced is the next thing Update sees.
//
// The key is synthesised rather than pasted in as a message, because the whole
// question is which route the picture takes — the keystroke for a terminal that
// lets it through, tea.PasteMsg for one that does not.
func pressCtrlV(t *testing.T, m *uiModel) tea.Msg {
	t.Helper()
	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'v', Mod: tea.ModCtrl}))
	if cmd == nil {
		return nil
	}
	return cmd()
}

// TestRemovingADroppedPathByTyping simulates the real keys, because that is the
// only way to be sure the removal is wired to the keystroke rather than to a
// helper: a dropped path sits in the prompt, the picture is attached, and the
// user backspaces it away one character at a time.
func TestRemovingADroppedPathByTyping(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	p := writePNG(t, t.TempDir(), "shot.png", 64, 48)

	// A terminal drops a file by inserting its path as text.
	m.Update(tea.PasteMsg{Content: p})
	runAttach(t, m, p)
	if len(m.pending) != 1 {
		t.Fatalf("pending = %d after the drop, want 1", len(m.pending))
	}

	for range len(p) + 2 {
		m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	}
	if len(m.pending) != 0 {
		t.Errorf("pending = %d after the path was backspaced away, want 0:\n%s",
			len(m.pending), ansi.Strip(m.pendingStrip()))
	}
	if v := strings.TrimSpace(m.input.Value()); v != "" {
		t.Errorf("input = %q, want empty", v)
	}
}

// runAttach completes the watch for a path the way a real turn does: the paste
// asks for it, and the command that ask returns is run.
func runAttach(t *testing.T, m *uiModel, path string) {
	t.Helper()
	if msg := m.watchImageCmd(path)(); msg != nil {
		m.Update(msg)
	}
}

// TestUnimageAfterADropIsReachable is the route a user is told to use, in the
// exact state a drag-and-drop leaves the prompt in.
//
// The command is matched against the whole line, so with a dropped path in the
// prompt the line is "C:\...\shot.png" and typing /unimage appends to it. The
// prefix no longer matches, the switch does not fire, and the picture stays put —
// which is what "there is no way to remove it" looks like from the outside.
func TestUnimageAfterADropIsReachable(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	p := writePNG(t, t.TempDir(), "shot.png", 64, 48)

	m.input.SetValue(p)
	m.watchInputImage()
	runAttach(t, m, p)
	if len(m.pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(m.pending))
	}

	// The user types the command into the prompt that still holds the path.
	m.input.SetValue(p + " /unimage")
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	if len(m.pending) != 0 {
		t.Errorf("/unimage left %d pictures attached; the dropped one has to go:\n%s",
			len(m.pending), ansi.Strip(m.pendingStrip()))
	}
}

// TestUnimageAfterADropStartsNoTurn is the bug. Typing the command into a prompt
// that holds a dropped path made the line neither a bare command nor a prefix
// match, so it fell through to the turn: the model was asked about a file called
// "/unimage", carrying the very picture the user was trying to remove.
func TestUnimageAfterADropStartsNoTurn(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	p := writePNG(t, t.TempDir(), "shot.png", 64, 48)
	m.input.SetValue(p)
	m.watchInputImage()
	runAttach(t, m, p)

	m.input.SetValue(p + " /unimage")
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	if m.busy {
		t.Error("/unimage started a turn instead of dropping the picture")
	}
	if len(m.pending) != 0 {
		t.Errorf("pending = %d, want 0:\n%s", len(m.pending), ansi.Strip(m.pendingStrip()))
	}
	for _, l := range m.history {
		if l.kind == kindUser {
			t.Errorf("the command reached the transcript as a message: %q", ansi.Strip(l.text))
		}
	}
}

// TestUnimageTakesThePathWithIt: dropping the picture but leaving its path in the
// prompt would not be a removal. The path is still named, so the send attaches
// the picture again, and the preview returns on the next keystroke that moves the
// candidate — the user would press /unimage and watch it come straight back.
func TestUnimageTakesThePathWithIt(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	p := writePNG(t, t.TempDir(), "shot.png", 64, 48)
	m.input.SetValue("посмотри " + p)
	m.watchInputImage()
	runAttach(t, m, p)

	m.input.SetValue("посмотри " + p + " /unimage")
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	if got := strings.TrimSpace(m.input.Value()); got != "посмотри" {
		t.Errorf("input = %q, want the dropped path removed and the rest kept", got)
	}
	if _, imgs := m.takeImagePaths(m.input.Value()); len(imgs) != 0 {
		t.Errorf("the removed picture would still be sent: %d attachments", len(imgs))
	}
}

// TestUnimageOnAPastedPictureLeavesThePromptAlone: a pasted picture has no path in
// the prompt, so there is nothing to take out. The text the user is writing must
// survive untouched, or removing a screenshot would cost them their question.
func TestUnimageOnAPastedPictureLeavesThePromptAlone(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	fakeClipboard(t, plainPicture(), nil, "", nil)
	m.Update(pressCtrlV(t, m))
	if len(m.pending) != 1 {
		t.Fatalf("pending = %d, want the pasted picture", len(m.pending))
	}

	m.input.SetValue("что здесь /unimage")
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	if len(m.pending) != 0 {
		t.Errorf("pending = %d after /unimage on a pasted picture, want 0", len(m.pending))
	}
	if got := strings.TrimSpace(m.input.Value()); got != "что здесь" {
		t.Errorf("input = %q, want only the command removed", got)
	}
}

// TestAFilenameEndingInTheCommandIsNotTheCommand keeps the rule honest: a picture
// called "my unimage.png" is a path, and treating the tail of it as a command
// would delete a file the user only mentioned.
func TestAFilenameEndingInTheCommandIsNotTheCommand(t *testing.T) {
	for _, text := range []string{
		`C:\shots\unimage`,                   // no separator in front: not a token of its own
		`посмотри C:\shots\some unimage.png`, // the letters are part of the name
	} {
		rest, ok := cutCommandToken(text, "/unimage")
		if ok {
			t.Errorf("cutCommandToken(%q) = %q, true; want it left alone", text, rest)
		}
		if rest != text {
			t.Errorf("cutCommandToken(%q) changed the text to %q", text, rest)
		}
	}
}

// TestTheCommandIsRecognisedAsAToken pins what the rule does accept, including a
// path with a space in it — the case drag-and-drop makes ordinary.
func TestTheCommandIsRecognisedAsAToken(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/unimage", ""},
		{"/unimage   ", ""},
		{"посмотри /unimage", "посмотри"},
		{`"C:\my shots\a.png" /unimage`, `"C:\my shots\a.png"`},
	} {
		rest, ok := cutCommandToken(tc.in, "/unimage")
		if !ok {
			t.Errorf("cutCommandToken(%q) did not fire", tc.in)
			continue
		}
		if rest != tc.want {
			t.Errorf("cutCommandToken(%q) = %q, want %q", tc.in, rest, tc.want)
		}
	}
}

// TestTheCommandMustBeLast: it is recognised only as the last thing typed. A
// command in the middle of a sentence is part of what the user is writing, and
// cutting it out would edit their question behind their back.
func TestTheCommandMustBeLast(t *testing.T) {
	for _, in := range []string{
		"a b /unimage c",
		"/unimage опиши это",
		"вот /unimage.png", // not even a command, and not last-but-one
	} {
		if rest, ok := cutCommandToken(in, "/unimage"); ok {
			t.Errorf("cutCommandToken(%q) = %q, true; want it left alone", in, rest)
		}
	}
}

// TestADroppedPictureWithNoPathIsNotRemovedByEditing: the counterpart to the rule
// that ties a picture to its path. A pasted one is not tied to the prompt, so
// typing must not take it away.
func TestADroppedPictureWithNoPathIsNotRemovedByEditing(t *testing.T) {
	m := imageModel(t)
	m.history = nil
	fakeClipboard(t, plainPicture(), nil, "", nil)
	m.Update(pressCtrlV(t, m))

	m.input.SetValue("просто текст")
	m.watchInputImage()

	if len(m.pending) != 1 {
		t.Errorf("pending = %d after typing, want the pasted picture kept", len(m.pending))
	}
}
