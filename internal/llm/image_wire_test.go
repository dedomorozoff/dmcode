package llm

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/genai"
)

// onePixelPNG is a real 1x1 PNG, so these tests exercise the same bytes a user's
// screenshot would travel as rather than a hand-written approximation.
var onePixelPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
	0xde, 0x00, 0x00, 0x00, 0x0c, 'I', 'D', 'A', 'T',
	0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00, 0x00,
	0x03, 0x01, 0x01, 0x00, 0x18, 0xdd, 0x8d, 0xb0,
	0x00, 0x00, 0x00, 0x00, 'I', 'E', 'N', 'D',
	0xae, 0x42, 0x60, 0x82,
}

func imagePart_(mime string, data []byte) *genai.Part {
	return &genai.Part{InlineData: &genai.Blob{MIMEType: mime, Data: data}}
}

// TestTextOnlyTurnStaysAString is the compatibility guard. Every request that
// never mentions an image must serialise exactly as it did before images were
// supported, because an endpoint that only implements the string form of
// `content` is the majority of what dmcode talks to.
func TestTextOnlyTurnStaysAString(t *testing.T) {
	msgs, err := contentsToChat([]*genai.Content{
		{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "привет"}}},
	})
	if err != nil {
		t.Fatalf("contentsToChat: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if _, ok := msgs[0].Content.(string); !ok {
		t.Errorf("Content is %T, want a plain string for a text-only turn", msgs[0].Content)
	}

	raw, err := json.Marshal(chatRequest{Model: "m", Messages: msgs})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"content":"привет"`) {
		t.Errorf("the wire body does not carry a string content: %s", raw)
	}
	if strings.Contains(string(raw), `"image_url"`) {
		t.Errorf("a text-only turn mentions an image: %s", raw)
	}
}

// TestImageBecomesADataURL pins the whole point of the change: a picture attached
// to a turn must survive into the request rather than being dropped in silence.
func TestImageBecomesADataURL(t *testing.T) {
	msgs, err := contentsToChat([]*genai.Content{
		{Role: genai.RoleUser, Parts: []*genai.Part{
			{Text: "что здесь?"},
			imagePart_("image/png", onePixelPNG),
		}},
	})
	if err != nil {
		t.Fatalf("contentsToChat: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	parts, ok := msgs[0].Content.([]chatContentPart)
	if !ok {
		t.Fatalf("Content is %T, want a part array", msgs[0].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("got %d parts, want a text and an image: %+v", len(parts), parts)
	}
	// Text first: a model reads the question before it looks at what it is about.
	if parts[0].Type != "text" || parts[0].Text != "что здесь?" {
		t.Errorf("part 0 is %+v, want the question", parts[0])
	}
	img := parts[1]
	if img.Type != "image_url" || img.ImageURL == nil {
		t.Fatalf("part 1 is %+v, want an image_url part", img)
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(onePixelPNG)
	if img.ImageURL.URL != want {
		t.Errorf("url = %q, want %q", img.ImageURL.URL, want)
	}
}

// TestImageOnlyTurnIsStillAMessage: a prompt that is nothing but a picture is a
// real turn, and the old "text or calls or nothing" guard would have dropped it.
func TestImageOnlyTurnIsStillAMessage(t *testing.T) {
	msgs, err := contentsToChat([]*genai.Content{
		{Role: genai.RoleUser, Parts: []*genai.Part{imagePart_("image/png", onePixelPNG)}},
	})
	if err != nil {
		t.Fatalf("contentsToChat: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want the image to count as one: %+v", len(msgs), msgs)
	}
	parts, ok := msgs[0].Content.([]chatContentPart)
	if !ok || len(parts) != 1 {
		t.Fatalf("Content is %#v, want one image part", msgs[0].Content)
	}
	if parts[0].Type != "image_url" {
		t.Errorf("the only part is %+v, want the image", parts[0])
	}
}

// TestSeveralImagesStayInOrder guards against a loop that keeps only the last
// one, which would be invisible with a single attachment.
func TestSeveralImagesStayInOrder(t *testing.T) {
	msgs, err := contentsToChat([]*genai.Content{
		{Role: genai.RoleUser, Parts: []*genai.Part{
			{Text: "сравни"},
			imagePart_("image/png", []byte{1, 2, 3}),
			imagePart_("image/jpeg", []byte{4, 5, 6}),
		}},
	})
	if err != nil {
		t.Fatalf("contentsToChat: %v", err)
	}
	parts, _ := msgs[0].Content.([]chatContentPart)
	if len(parts) != 3 {
		t.Fatalf("got %d parts, want text plus two images: %+v", len(parts), parts)
	}
	if !strings.Contains(parts[1].ImageURL.URL, "image/png") ||
		!strings.Contains(parts[2].ImageURL.URL, "image/jpeg") {
		t.Errorf("the images lost their order or their media types: %+v", parts[1:])
	}
}

// TestUnnamedBlobGetsAMediaType: the API rejects a data URL with no media type,
// and an absent one means a caller built the blob by hand.
func TestUnnamedBlobGetsAMediaType(t *testing.T) {
	msgs, err := contentsToChat([]*genai.Content{
		{Role: genai.RoleUser, Parts: []*genai.Part{
			{InlineData: &genai.Blob{Data: []byte{9, 9}}},
		}},
	})
	if err != nil {
		t.Fatalf("contentsToChat: %v", err)
	}
	parts, _ := msgs[0].Content.([]chatContentPart)
	if !strings.HasPrefix(parts[0].ImageURL.URL, "data:image/") {
		t.Errorf("url = %q, want a named media type", parts[0].ImageURL.URL)
	}
}

// TestEmptyBlobIsRefusedNotDropped keeps the failure legible. An empty image that
// vanishes produces a turn the model answers without the picture, and nothing on
// screen says so.
func TestEmptyBlobIsRefusedNotDropped(t *testing.T) {
	_, err := contentsToChat([]*genai.Content{
		{Role: genai.RoleUser, Parts: []*genai.Part{
			{Text: "посмотри"},
			{InlineData: &genai.Blob{MIMEType: "image/png"}},
		}},
	})
	if err == nil {
		t.Fatal("an empty image was accepted; it would be silently missing from the turn")
	}
	if !strings.Contains(err.Error(), "image") {
		t.Errorf("error = %v, want one naming the image", err)
	}
}

// TestTextIsReadableThroughTheUnion: Content is a union, and every reader wants
// the prose, so the accessor has to work for both shapes.
func TestTextIsReadableThroughTheUnion(t *testing.T) {
	msgs, err := contentsToChat([]*genai.Content{
		{Role: genai.RoleUser, Parts: []*genai.Part{
			{Text: "первая"}, {Text: " вторая"},
			imagePart_("image/png", []byte{7}),
		}},
		{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "обычный"}}},
	})
	if err != nil {
		t.Fatalf("contentsToChat: %v", err)
	}
	if got := msgs[0].Text(); got != "первая вторая" {
		t.Errorf("Text() = %q, want the joined prose of a part-array message", got)
	}
	if got := msgs[1].Text(); got != "обычный" {
		t.Errorf("Text() = %q, want the string form to pass through", got)
	}
	empty := chatMessage{Role: "tool"}
	if got := empty.Text(); got != "" {
		t.Errorf("Text() on a message with no content = %q, want empty", got)
	}
}

// TestToolLoopStillRoundTrips: adding images must not disturb the shape the tool
// loop depends on, since that path is exercised on every turn.
func TestToolLoopStillRoundTrips(t *testing.T) {
	msgs, err := contentsToChat([]*genai.Content{
		{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "сделай"}}},
		{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{
			ID: "c1", Name: "read_file", Args: map[string]any{"path": "a.go"},
		}}}},
		{Role: genai.RoleUser, Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{
			ID: "c1", Name: "read_file", Response: map[string]any{"output": "ok"},
		}}}},
	})
	if err != nil {
		t.Fatalf("contentsToChat: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3", len(msgs))
	}
	if msgs[1].Role != "assistant" || len(msgs[1].ToolCalls) != 1 {
		t.Fatalf("the assistant tool_calls message is malformed: %+v", msgs[1])
	}
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != "c1" {
		t.Fatalf("the tool result message is malformed: %+v", msgs[2])
	}
}

// TestAnImageAndAToolCallShareATurn: a model may ask for a tool call and refer to
// the picture in the same turn, so both have to come out.
func TestAnImageAndAToolCallShareATurn(t *testing.T) {
	msgs, err := contentsToChat([]*genai.Content{
		{Role: genai.RoleModel, Parts: []*genai.Part{
			{Text: "смотрю"},
			{FunctionCall: &genai.FunctionCall{ID: "c1", Name: "read_file"}},
		}},
		{Role: genai.RoleUser, Parts: []*genai.Part{
			{Text: "вот"},
			imagePart_("image/png", []byte{1}),
		}},
	})
	if err != nil {
		t.Fatalf("contentsToChat: %v", err)
	}
	if len(msgs[0].ToolCalls) != 1 || msgs[0].Text() != "смотрю" {
		t.Errorf("the assistant message lost something: %+v", msgs[0])
	}
	if _, ok := msgs[1].Content.([]chatContentPart); !ok {
		t.Errorf("the user message is %T, want a part array", msgs[1].Content)
	}
}

// TestUnsupportedPartsAreNamed rather than skipped. ADK's own client refuses a
// part it cannot send instead of passing over it, and the reason is worth copying:
// the failure has to happen where it can still be reported.
func TestUnsupportedPartsAreNamed(t *testing.T) {
	_, err := contentsToChat([]*genai.Content{
		{Role: genai.RoleUser, Parts: []*genai.Part{
			{ExecutableCode: &genai.ExecutableCode{Code: "print(1)"}},
		}},
	})
	// Executable code rides on a part that also carries no text, so the message
	// ends up empty and nothing is emitted. What must not happen is a silent
	// success that looks like the code was sent.
	if err == nil && len(mustChat(t, []*genai.Content{
		{Role: genai.RoleUser, Parts: []*genai.Part{{ExecutableCode: &genai.ExecutableCode{Code: "x"}}}},
	})) != 0 {
		t.Error("an unsendable part produced a message")
	}
}

func mustChat(t *testing.T, c []*genai.Content) []chatMessage {
	t.Helper()
	msgs, err := contentsToChat(c)
	if err != nil {
		t.Fatalf("contentsToChat: %v", err)
	}
	return msgs
}

// TestNilContentsAreSkipped: the stream carries nil events and a nil content has
// always been skipped rather than panicking.
func TestNilContentsAreSkipped(t *testing.T) {
	msgs, err := contentsToChat([]*genai.Content{nil, {Role: genai.RoleUser, Parts: []*genai.Part{{Text: "x"}}}})
	if err != nil {
		t.Fatalf("contentsToChat: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("got %d messages, want the nil content skipped", len(msgs))
	}
}

// TestImagePartRejectsEmptyBlob covers imagePart directly, since it is the only
// place a blob becomes a URL and its contract should not depend on the caller.
func TestImagePartRejectsEmptyBlob(t *testing.T) {
	if _, err := imagePart(nil); err == nil {
		t.Error("a nil blob was accepted")
	}
	if _, err := imagePart(&genai.Blob{MIMEType: "image/png"}); err == nil {
		t.Error("an empty blob was accepted")
	}
	p, err := imagePart(&genai.Blob{MIMEType: "image/webp", Data: []byte{1}})
	if err != nil {
		t.Fatalf("imagePart: %v", err)
	}
	if !strings.HasPrefix(p.ImageURL.URL, "data:image/webp;base64,") {
		t.Errorf("url = %q, want the caller's media type preserved", p.ImageURL.URL)
	}
}
