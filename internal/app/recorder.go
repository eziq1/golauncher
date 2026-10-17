package app

import (
	"slices"
	"sync"

	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Effect is a recorded side effect.
type Effect struct {
	Kind    plugin.ActionKind    `json:"kind"`
	Target  string               `json:"target,omitempty"`
	Open    *plugin.OpenOptions  `json:"open,omitempty"`
	Files   []string             `json:"files,omitempty"`
	Content *plugin.PasteContent `json:"content,omitempty"`
}

// Recorder is a core.System that records effects instead of performing
// them. Copies and pastes are applied to Clipboard when it is set, so a fake
// clipboard reflects them.
type Recorder struct {
	Clipboard plugin.Clipboard
	// PasteErr is returned by Paste after the content was copied, to
	// simulate a paste that fails (core.ErrNotPasted).
	PasteErr error

	mu      sync.Mutex
	effects []Effect
}

var _ core.System = (*Recorder)(nil)

func (r *Recorder) add(e Effect) {
	r.mu.Lock()
	r.effects = append(r.effects, e)
	r.mu.Unlock()
}

// Effects returns the recorded effects.
func (r *Recorder) Effects() []Effect {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.effects)
}

// Reset forgets the recorded effects.
func (r *Recorder) Reset() {
	r.mu.Lock()
	r.effects = nil
	r.mu.Unlock()
}

func (r *Recorder) Open(target string, opts plugin.OpenOptions) error {
	e := Effect{Kind: plugin.ActionOpen, Target: target}
	if opts != (plugin.OpenOptions{}) {
		e.Open = &opts
	}
	r.add(e)
	return nil
}

func (r *Recorder) CopyText(text string) error {
	r.add(Effect{Kind: plugin.ActionCopyText, Target: text})
	if r.Clipboard != nil {
		return r.Clipboard.WriteText(text)
	}
	return nil
}

func (r *Recorder) CopyImage(path string) error {
	r.add(Effect{Kind: plugin.ActionCopyImage, Target: path})
	return nil
}

func (r *Recorder) CopyFiles(paths []string) error {
	r.add(Effect{Kind: plugin.ActionCopyFiles, Files: paths})
	if r.Clipboard != nil {
		return r.Clipboard.WriteFiles(paths)
	}
	return nil
}

func (r *Recorder) Paste(text string, content plugin.PasteContent) error {
	e := Effect{Kind: plugin.ActionPaste, Target: text}
	if text == "" {
		e.Content = &content
	}
	r.add(e)
	if r.Clipboard != nil && text != "" {
		if err := r.Clipboard.WriteText(text); err != nil {
			return err
		}
	}
	return r.PasteErr
}
