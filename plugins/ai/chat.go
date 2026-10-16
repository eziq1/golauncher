package ai

import (
	"context"
	"encoding/json/v2"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// chat is a chat in memory: its messages, its conversation with the
// backend and the answer being written. Its fields are guarded by Plugin.mu.
type chat struct {
	sess Session
	msgs []Message

	conv     backend.Conversation
	convOpts backend.OpenOptions

	// pending is the first message of a chat opened with AI off; it is sent
	// once AI is turned on.
	pending string

	// The answer being written.
	busy    bool
	partial strings.Builder
	tools   []string
	cancel  context.CancelFunc
	usage   *backend.Usage
}

// lastAnswer returns the text of the last answer, or "".
func (ch *chat) lastAnswer() string {
	for _, m := range slices.Backward(ch.msgs) {
		if m.Role == RoleAssistant && m.Text != "" {
			return m.Text
		}
	}
	return ""
}

// lastQuestion returns the last message of the user, or "".
func (ch *chat) lastQuestion() string {
	for _, m := range slices.Backward(ch.msgs) {
		if m.Role == RoleUser {
			return m.Text
		}
	}
	return ""
}

// chatOf returns the chat shown by the view instance, opening it from the
// payload the first time. p.mu must be held.
func (p *Plugin) chatOf(req plugin.ViewRequest) *chat {
	if id, ok := p.views[req.Instance]; ok {
		if ch, ok := p.chats[id]; ok {
			return ch
		}
	}
	var pl chatPayload
	_ = json.Unmarshal(req.Payload, &pl)
	var ch *chat
	if pl.Session != "" {
		ch = p.chats[pl.Session]
		if ch == nil {
			if sess, msgs, err := p.st.Get(pl.Session); err == nil {
				ch = &chat{sess: sess, msgs: msgs}
			} else {
				p.h.Logger().Warn("open chat", "err", err)
			}
		}
	}
	if ch == nil {
		ch = &chat{sess: Session{ID: p.o.NewID(), Backend: p.be.Name()}, pending: strings.TrimSpace(pl.Text)}
	}
	p.chats[ch.sess.ID] = ch
	if req.Instance != "" {
		p.views[req.Instance] = ch.sess.ID
	}
	return ch
}

// chatView renders a chat view, first sending the message the chat was
// opened with once AI is on.
func (p *Plugin) chatView(req plugin.ViewRequest) plugin.View {
	p.mu.Lock()
	ch := p.chatOf(req)
	if text := ch.pending; text != "" && p.enabled() && !ch.busy {
		ch.pending = ""
		run := p.startTurn(ch, text)
		p.mu.Unlock()
		p.runTurn(run)
		p.mu.Lock()
	}
	v := p.render(ch, req.Instance)
	p.mu.Unlock()
	return v
}

// send sends the message typed in the search field of a chat view.
func (p *Plugin) send(instance, text string) plugin.Result {
	text = strings.TrimSpace(text)
	if text == "" || !p.enabled() {
		return plugin.Result{}
	}
	p.mu.Lock()
	ch := p.chats[p.views[instance]]
	if ch == nil {
		p.mu.Unlock()
		return plugin.Result{}
	}
	if ch.busy {
		p.mu.Unlock()
		l := p.h.I18n()
		return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastInfo, Title: l.T("toast.busy")}}
	}
	run := p.startTurn(ch, text)
	p.mu.Unlock()
	p.runTurn(run)
	return plugin.Result{ClearText: true}
}

// startTurn records the user's message and marks the chat as answering. It
// returns the function writing the answer. p.mu must be held.
func (p *Plugin) startTurn(ch *chat, text string) func() {
	now := p.h.Now()
	if len(ch.msgs) == 0 {
		ch.sess.Created = now
		ch.sess.Title = titleOf(text)
	}
	ch.sess.Updated = now
	ch.msgs = append(ch.msgs, Message{Role: RoleUser, Text: text, Time: now})
	p.save(ch)
	ch.busy = true
	ch.partial.Reset()
	ch.tools = nil
	ctx, cancel := context.WithCancel(context.Background())
	ch.cancel = cancel
	p.turns.Add(1)
	return func() {
		defer p.turns.Done()
		defer cancel()
		p.turn(ctx, ch, text)
	}
}

// runTurn writes an answer: at once with Options.Sync, else in the
// background.
func (p *Plugin) runTurn(run func()) {
	if p.o.Sync {
		run()
		return
	}
	p.h.Go(run)
}

// save stores the chat. p.mu must be held.
func (p *Plugin) save(ch *chat) {
	if err := p.st.Save(ch.sess, ch.msgs); err != nil {
		p.h.Logger().Warn("save chat", "err", err)
	}
}

// turn asks the backend and follows the answer until it ends.
func (p *Plugin) turn(ctx context.Context, ch *chat, text string) {
	start := time.Now()
	conv, err := p.conversation(ctx, ch)
	var events <-chan backend.Event
	if err == nil {
		events, err = conv.Send(ctx, text)
	}
	if err != nil {
		p.finish(ch, backend.Event{Kind: backend.EventError, Err: err}, start)
		return
	}
	for ev := range events {
		switch ev.Kind {
		case backend.EventText:
			p.mu.Lock()
			ch.partial.WriteString(ev.Text)
			p.mu.Unlock()
			p.push(ch)
		case backend.EventTool:
			p.mu.Lock()
			ch.tools = append(ch.tools, ev.Tool)
			p.mu.Unlock()
			p.push(ch)
		case backend.EventDone, backend.EventError:
			p.finish(ch, ev, start)
			return
		}
	}
	// The channel closed without a final event.
	p.finish(ch, backend.Event{Kind: backend.EventError, Err: errors.New("ai: the answer ended unexpectedly")}, start)
}

// finish records the answer that ended with ev. Only lengths and times are
// logged, never the content (docs/ai.md §9).
func (p *Plugin) finish(ch *chat, ev backend.Event, start time.Time) {
	p.mu.Lock()
	m := Message{Role: RoleAssistant, Text: ch.partial.String(), Time: p.h.Now(), Tools: ch.tools}
	p.h.Logger().Info("answer", "kind", ev.Kind, "chars", len(m.Text), "took", time.Since(start).Round(time.Millisecond))
	switch ev.Kind {
	case backend.EventError:
		if ev.Err != nil {
			m.Error = string(backend.KindOf(ev.Err)) + ":" + errorText(ev.Err)
		}
	case backend.EventDone:
		m.Stopped = ev.Cancelled
		ch.usage = ev.Usage
		ch.sess.Started = true
	}
	if m.Text != "" {
		ch.sess.Started = true // the backend has the session
	}
	ch.msgs = append(ch.msgs, m)
	ch.sess.Updated = m.Time
	ch.busy = false
	ch.partial.Reset()
	ch.tools = nil
	ch.cancel = nil
	if !p.closed && p.chats[ch.sess.ID] == ch { // not deleted meanwhile
		p.save(ch)
	}
	p.mu.Unlock()
	p.push(ch)
}

// errorText is the backend's own message of err.
func errorText(err error) string {
	if e, ok := errors.AsType[*backend.Error](err); ok {
		return e.Message
	}
	return err.Error()
}

// conversation returns the chat's conversation with the backend, checking
// the backend first and opening the conversation with the current
// preferences.
func (p *Plugin) conversation(ctx context.Context, ch *chat) (backend.Conversation, error) {
	opts := p.openOptions(ch)
	p.mu.Lock()
	conv, old := ch.conv, ch.convOpts
	checked := !p.checked.IsZero() && time.Since(p.checked) < checkTTL
	p.mu.Unlock()
	// Resume changes after the first answer; the conversation knows that.
	old.Resume = opts.Resume
	if conv != nil && old == opts {
		return conv, nil
	}
	if !checked {
		st, err := p.be.Check(ctx)
		if err != nil {
			return nil, err
		}
		p.mu.Lock()
		p.checked, p.status = time.Now(), st
		p.mu.Unlock()
	}
	if conv != nil {
		_ = conv.Close() // the preferences changed
	}
	conv, err := p.be.Open(ctx, opts)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	ch.conv, ch.convOpts = conv, opts
	p.mu.Unlock()
	return conv, nil
}

// openOptions are the options of the chat's conversation.
func (p *Plugin) openOptions(ch *chat) backend.OpenOptions {
	prefs := p.h.Preferences()
	web := prefs.Bool(prefWebSearch)
	p.mu.Lock()
	resume := ch.sess.Started
	id := ch.sess.ID
	p.mu.Unlock()
	return backend.OpenOptions{
		SessionID:    id,
		Resume:       resume,
		Model:        prefs.String(prefModel),
		WebSearch:    web,
		SystemPrompt: systemPrompt(p.h.I18n().Locale(), web),
	}
}

// systemPrompt is appended to the backend's system prompt. It is written for
// the model, not shown, so it is not translated; it names the language to
// answer in.
func systemPrompt(locale string, web bool) string {
	lang := map[string]string{"en": "English", "zh-CN": "Simplified Chinese"}[locale]
	if lang == "" {
		lang = "the language of the locale " + locale
	}
	s := "You are the AI assistant built into golauncher, a keyboard launcher for Windows; the user opened you from its search box. " +
		"Answer in " + lang + " unless the user writes in another language. " +
		"Be concise and direct, and format answers in Markdown (short paragraphs, lists, fenced code blocks). " +
		"You cannot see or change the user's computer or files."
	if web {
		s += " You may search the web and read web pages when it helps."
	}
	return s
}

// push renders the chat to every view instance showing it.
func (p *Plugin) push(ch *chat) {
	type pushed struct {
		instance string
		view     plugin.View
	}
	p.mu.Lock()
	var out []pushed
	for inst, id := range p.views {
		if id == ch.sess.ID {
			out = append(out, pushed{inst, p.render(ch, inst)})
		}
	}
	p.mu.Unlock()
	for _, x := range out {
		p.h.Render(x.instance, x.view)
	}
}

// stopInstance stops the answer of the chat in a view.
func (p *Plugin) stopInstance(instance string) {
	p.mu.Lock()
	ch := p.chats[p.views[instance]]
	p.mu.Unlock()
	if ch != nil {
		p.stopChat(ch)
	}
}

// stopChat stops the answer being written in ch, if any.
func (p *Plugin) stopChat(ch *chat) {
	p.mu.Lock()
	busy, cancel, conv := ch.busy, ch.cancel, ch.conv
	p.mu.Unlock()
	if !busy {
		return
	}
	if conv != nil {
		conv.Cancel()
	}
	if cancel != nil {
		cancel()
	}
}

// newChat replaces the chat of a view by a new one (Ctrl+N).
func (p *Plugin) newChat(instance string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ch := &chat{sess: Session{ID: p.o.NewID(), Backend: p.be.Name()}}
	p.chats[ch.sess.ID] = ch
	p.views[instance] = ch.sess.ID
}

// render builds the chat view of ch for a view instance. p.mu must be held.
func (p *Plugin) render(ch *chat, instance string) plugin.View {
	l := p.h.I18n()
	title := ch.sess.Title
	if title == "" {
		title = l.T("chat.title")
	}
	if !p.enabled() {
		return p.offView(ch, instance)
	}
	inst := plugin.MustPayload(instancePayload{Instance: instance})
	cb := func(id, title, shortcut string) plugin.Action {
		return plugin.Action{ID: id, Title: title, Shortcut: shortcut, Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: id, Payload: inst}}
	}
	actions := []plugin.Action{cb("send", l.T("action.send"), "")}
	if ch.busy {
		actions = append(actions, cb("stop", l.T("action.stop"), ""))
	}
	if answer := ch.lastAnswer(); answer != "" {
		actions = append(actions,
			plugin.Action{ID: "copy", Title: l.T("action.copy"), Shortcut: "Ctrl+Shift+C", Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: answer}},
			plugin.Action{ID: "paste", Title: l.T("action.paste"), Shortcut: "Ctrl+Shift+V", Do: plugin.ActionDo{Kind: plugin.ActionPaste, Target: answer}},
		)
	}
	actions = append(actions,
		cb("claude", l.T("action.claude"), ""),
		cb("chatgpt", l.T("action.chatgpt"), ""),
		cb("new", l.T("action.new"), "Ctrl+N"),
		plugin.Action{ID: "history", Title: l.T("command.history.title"), Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: viewHistory}},
	)
	if ch.sess.Started {
		actions = append(actions, plugin.Action{ID: "copy-id", Title: l.T("action.copyID"), Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: ch.sess.ID}})
	}
	v := plugin.View{
		Kind:        plugin.ViewDetail,
		Title:       title,
		Placeholder: l.T("chat.placeholder"),
		Detail:      &plugin.Detail{Markdown: p.transcript(ch), Metadata: p.metadata(ch)},
		Sections:    []plugin.Section{{Items: []plugin.Item{{ID: "chat", Title: title, Icon: plugin.Icon{Name: "sparkles"}, Actions: actions}}}},
	}
	if len(ch.msgs) > 0 {
		v.Placeholder = l.T("chat.placeholderFollowUp")
	}
	if ch.busy {
		stop := cb("stop", l.T("action.stop"), "")
		v.Escape = &stop
	}
	return v
}

// offView is the first-use prompt shown while AI is off.
func (p *Plugin) offView(ch *chat, instance string) plugin.View {
	l := p.h.I18n()
	md := "## " + l.T("off.heading") + "\n\n" + l.T("off.body", "backend", p.be.Name()) + "\n\n" + l.T("off.privacy")
	if ch.pending != "" {
		md += "\n\n" + l.T("off.pending") + "\n\n> " + strings.ReplaceAll(ch.pending, "\n", "\n> ")
	}
	enable := plugin.Action{ID: "enable", Title: l.T("action.enable"), Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "enable"}}
	return plugin.View{
		Kind:        plugin.ViewDetail,
		Title:       l.T("chat.title"),
		Placeholder: l.T("off.placeholder"),
		Detail:      &plugin.Detail{Markdown: md},
		Sections:    []plugin.Section{{Items: []plugin.Item{{ID: "chat", Title: l.T("action.enable"), Icon: plugin.Icon{Name: "sparkles"}, Actions: []plugin.Action{enable}}}}},
	}
}

// transcript is the Markdown of the chat's messages.
func (p *Plugin) transcript(ch *chat) string {
	l := p.h.I18n()
	if len(ch.msgs) == 0 {
		return l.T("chat.empty", "backend", p.be.Name())
	}
	var b strings.Builder
	answer := func(text string, tools []string) {
		b.WriteString("**" + l.T("chat.assistant") + "**\n\n")
		for _, t := range tools {
			b.WriteString("_" + p.toolLine(t) + "_\n\n")
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
			answer(m.Text, m.Tools)
			if m.Stopped {
				b.WriteString("_" + l.T("chat.stopped") + "_\n\n")
			}
			if m.Error != "" {
				b.WriteString(p.errorMarkdown(m.Error) + "\n\n")
			}
		}
	}
	if ch.busy {
		text := ch.partial.String()
		if text == "" && len(ch.tools) == 0 {
			text = "_" + l.T("chat.thinking") + "_"
		} else {
			text += "▍"
		}
		answer(text, ch.tools)
	}
	return strings.TrimSpace(b.String())
}

// toolLine describes a tool the model used.
func (p *Plugin) toolLine(tool string) string {
	l := p.h.I18n()
	switch tool {
	case "WebSearch":
		return l.T("tool.webSearch")
	case "WebFetch":
		return l.T("tool.webFetch")
	}
	return l.T("tool.other", "name", tool)
}

// errorMarkdown explains a recorded error ("kind:message").
func (p *Plugin) errorMarkdown(recorded string) string {
	l := p.h.I18n()
	kind, msg, _ := strings.Cut(recorded, ":")
	var text string
	switch backend.ErrorKind(kind) {
	case backend.ErrNotInstalled:
		text = l.T("error.notInstalled")
	case backend.ErrNotLoggedIn:
		text = l.T("error.notLoggedIn")
	case backend.ErrVersion:
		text = l.T("error.version")
	case backend.ErrQuota:
		text = l.T("error.quota")
	default:
		text = l.T("error.failed", "backend", p.be.Name())
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
	model := p.h.Preferences().String(prefModel)
	if ch.usage != nil && ch.usage.Model != "" {
		model = ch.usage.Model
	}
	if model == "" {
		model = l.T("pref.model.default")
	}
	md := []plugin.Metadata{{Label: p.be.Name(), Value: model}}
	if p.status.Untested {
		md[0].Value += " · " + l.T("meta.untested", "version", p.status.Version)
	}
	if u := ch.usage; u != nil && (u.InputTokens > 0 || u.OutputTokens > 0) {
		md = append(md, plugin.Metadata{Label: l.T("meta.usage"),
			Value: l.T("meta.tokens", "in", l.Number(float64(u.InputTokens)), "out", l.Number(float64(u.OutputTokens)))})
	}
	return md
}
