package gemini

import (
	"testing"

	"github.com/eziq1/golauncher/internal/ai/acp"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// TestAttachmentCaps: Gemini CLI and custom ACP agents send images and text
// files (whether an agent takes images is decided at the handshake).
func TestAttachmentCaps(t *testing.T) {
	b := New(Options{})
	defer b.Close()
	caps := backend.AttachmentsOf(b)
	for _, mt := range backend.ImageTypes {
		if !caps.Accepts(backend.AttachImage, mt) {
			t.Errorf("Gemini CLI: no %s", mt)
		}
	}
	if !caps.Accepts(backend.AttachText, "text/plain") {
		t.Error("Gemini CLI: no text files")
	}
	if c := acp.Custom("Agent", "agent", nil).Info.Attachments; !c.Images || !c.Text {
		t.Errorf("custom agent: %+v", c)
	}
}
