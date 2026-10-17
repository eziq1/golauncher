package ai

import (
	"context"
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// A chat's backend (R72): each chat stays on the backend it was started
// with, its texts name that backend, and its agent's permission requests
// are asked in the launcher's confirmation dialog.

// newSession is the session of a new chat, on the backend chosen now.
func (p *Plugin) newSession() Session {
	return Session{ID: p.o.NewID(), BackendID: p.be.id(), Backend: p.be.Name()}
}

// backendOf returns the backend of a chat: its own, or the one chosen in
// the preferences when that is the chat's and this version does not have
// it (explained as such).
func (p *Plugin) backendOf(sess Session) (string, backend.Backend) {
	if sess.BackendID != "" && sess.BackendID == p.be.id() {
		return p.be.current()
	}
	return p.be.of(sess)
}

// backendName is the display name of a chat's backend; the recorded name
// when this version does not have it.
func (p *Plugin) backendName(sess Session) string {
	id, b := p.backendOf(sess)
	if _, ok := p.be.option(id); !ok && sess.Backend != "" {
		return sess.Backend
	}
	return b.Name()
}

// model is the model preference of a chat's backend ("" for its default).
func (p *Plugin) model(sess Session) string {
	id, _ := p.backendOf(sess)
	o, ok := p.be.option(id)
	if !ok {
		return ""
	}
	return p.h.Preferences().String(modelPref(o))
}

// backendText returns the text that differs by backend, of kind (an
// ErrorKind, or "" for the heading of the answers): Codex's, Gemini CLI's,
// else Claude Code's.
func backendText(l plugin.Localizer, kind, backendID string) string {
	pick := func(claude, codex, gemini string) string {
		switch backendID {
		case "codex":
			return codex
		case "gemini":
			return gemini
		}
		return claude
	}
	switch backend.ErrorKind(kind) {
	case backend.ErrNotInstalled:
		return pick(l.T("error.notInstalled"), l.T("error.notInstalled.codex"), l.T("error.notInstalled.gemini"))
	case backend.ErrNotLoggedIn:
		return pick(l.T("error.notLoggedIn"), l.T("error.notLoggedIn.codex"), l.T("error.notLoggedIn.gemini"))
	case backend.ErrVersion:
		return pick(l.T("error.version"), l.T("error.version.codex"), l.T("error.version.gemini"))
	case backend.ErrQuota:
		return pick(l.T("error.quota"), l.T("error.quota.codex"), l.T("error.quota.gemini"))
	}
	return pick(l.T("chat.assistant"), l.T("chat.assistant.codex"), l.T("chat.assistant.gemini"))
}

// assistantName is the heading of a chat's answers ("Claude", "Codex"…).
func (p *Plugin) assistantName(sess Session) string {
	switch id, _ := p.backendOf(sess); id {
	case DefaultBackendID, "codex", "gemini":
		return backendText(p.h.I18n(), "", id)
	}
	return p.backendName(sess)
}

// askPermission asks the user about an agent's permission request in the
// launcher's confirmation dialog and answers it. Without an asker it
// rejects. The runtime rejects it on its own when the user stops the answer
// or does not answer in time (Done closes the dialog then).
func (p *Plugin) askPermission(ctx context.Context, ch *chat, req *backend.PermissionRequest) {
	if req == nil || req.Respond == nil {
		return
	}
	p.mu.Lock()
	ask := p.asker
	name := p.backendName(ch.sess)
	p.mu.Unlock()
	if ask == nil {
		req.Respond("")
		return
	}
	if req.Done != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		done := req.Done
		p.h.Go(func() {
			select {
			case <-done: // answered meanwhile (stop, timeout): close the dialog
				cancel()
			case <-ctx.Done():
			}
		})
	}
	l := p.h.I18n()
	action := shorten(strings.Join(strings.Fields(firstOf(req.Title, req.Tool)), " "), maxStepText)
	always := req.Option("allow_always")
	a := backend.PermissionAsk{
		Client:       l.T("permission.client", "backend", name),
		Action:       action,
		Tool:         firstOf(req.Tool, req.Title),
		Destructive:  req.Destructive(),
		AllowSession: always != "" && !req.Destructive(),
	}
	if req.Tool != "" && req.Tool != req.Title {
		a.Message = l.T("permission.tool", "tool", shorten(req.Tool, maxStepText))
	}
	answer := ""
	switch ask(ctx, a) {
	case backend.PermissionAllow:
		answer = firstOf(req.Option("allow_once"), always)
	case backend.PermissionAllowSession:
		answer = firstOf(always, req.Option("allow_once"))
	}
	req.Respond(answer)
}

// transcript is the Markdown of the chat's messages.
func (p *Plugin) transcript(ch *chat) string {
	l := p.h.I18n()
	if len(ch.msgs) == 0 {
		return l.T("chat.empty", "backend", p.be.Name())
	}
	var b strings.Builder
	heading := p.assistantName(ch.sess)
	answer := func(text, notice string, tools []string, steps []ToolStep) {
		b.WriteString("**" + heading + "**\n\n")
		if notice != "" {
			b.WriteString("_" + p.noticeLine(ch.sess, notice) + "_\n\n")
		}
		for _, t := range tools {
			b.WriteString("_" + p.toolLine(t) + "_\n\n")
		}
		for _, s := range steps {
			b.WriteString("_" + p.stepLine(s) + "_\n\n")
		}
		if text != "" {
			b.WriteString(text + "\n\n")
		}
	}
	for i, m := range ch.msgs {
		if i > 0 && m.Role == RoleUser {
			b.WriteString("---\n\n")
		}
		switch m.Role {
		case RoleUser:
			b.WriteString("**" + l.T("chat.you") + "**\n\n" + m.Text + "\n\n")
		case RoleAssistant:
			answer(m.Text, m.Notice, m.Tools, m.Steps)
			if m.Stopped {
				b.WriteString("_" + l.T("chat.stopped") + "_\n\n")
			}
			if m.Error != "" {
				b.WriteString(p.errorMarkdown(ch.sess, m.Error) + "\n\n")
			}
		}
	}
	if ch.busy {
		text := ch.partial.String()
		if text == "" && len(ch.steps) == 0 {
			text = "_" + l.T("chat.thinking") + "_"
		} else {
			text += "▍"
		}
		answer(text, ch.notice, nil, ch.steps)
	} else if id, _ := p.backendOf(ch.sess); !p.hasBackend(id) {
		b.WriteString("> " + l.T("chat.backendGone", "backend", p.backendName(ch.sess)) + "\n\n")
	}
	return strings.TrimSpace(b.String())
}

// hasBackend reports whether this version has the backend with id.
func (p *Plugin) hasBackend(id string) bool {
	_, ok := p.be.option(id)
	return ok
}

// noticeLine words a backend notice.
func (p *Plugin) noticeLine(sess Session, code string) string {
	l := p.h.I18n()
	switch code {
	case backend.NoticeNewSession:
		return l.T("notice.newSession", "backend", p.backendName(sess))
	}
	return code
}

// errorMarkdown explains a recorded error ("kind:message") of a chat.
func (p *Plugin) errorMarkdown(sess Session, recorded string) string {
	l := p.h.I18n()
	kind, msg, _ := strings.Cut(recorded, ":")
	id, _ := p.backendOf(sess)
	var text string
	switch kind {
	case string(backend.ErrNotInstalled), string(backend.ErrNotLoggedIn), string(backend.ErrVersion), string(backend.ErrQuota):
		text = backendText(l, kind, id)
	case string(backend.ErrUnavailable):
		text = l.T("error.unavailable")
	case recordedChatUnavailable:
		text, msg = l.T("error.chatUnavailable", "backend", p.backendName(sess)), ""
	default:
		text = l.T("error.failed", "backend", p.backendName(sess))
	}
	out := "> **" + l.T("error.heading") + "** " + text
	if msg = strings.TrimSpace(msg); msg != "" {
		out += "\n>\n> " + strings.ReplaceAll(msg, "\n", "\n> ")
	}
	return out
}

// metadata is the footer line of a chat: backend, model and the last turn's
// usage.
func (p *Plugin) metadata(ch *chat) []plugin.Metadata {
	l := p.h.I18n()
	sess := ch.sess
	if len(ch.msgs) == 0 {
		sess.BackendID = p.be.id() // it takes the chosen one with its first message
	}
	id, _ := p.backendOf(sess)
	model := p.model(sess)
	modelSet := model != ""
	if ch.usage != nil && ch.usage.Model != "" {
		model = ch.usage.Model
	}
	if model == "" {
		model = l.T("pref.model.default")
	}
	md := []plugin.Metadata{{Label: p.backendName(sess), Value: model}}
	if c, ok := p.checks[id]; ok && c.status.Untested {
		md[0].Value += " · " + l.T("meta.untested", "version", c.status.Version)
	}
	if c, ok := p.checks[id]; ok {
		if f := p.reducedFeatures(c.status, modelSet); f != "" {
			md[0].Value += " · " + l.T("meta.reduced", "version", c.status.Version, "features", f)
		}
	}
	if u := ch.usage; u != nil && (u.InputTokens > 0 || u.OutputTokens > 0) {
		md = append(md, plugin.Metadata{Label: l.T("meta.usage"),
			Value: l.T("meta.tokens", "in", l.Number(float64(u.InputTokens)), "out", l.Number(float64(u.OutputTokens)))})
	}
	return md
}
