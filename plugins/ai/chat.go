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

	conv        backend.Conversation
	convOpts    backend.OpenOptions
	convBackend string // the ID of conv's backend

	// pending is the first message of a chat opened with AI off; it is sent
	// once AI is turned on.
	pending string
	// title replaces the start of the first message as the chat's title
	// (a selection action: "Translate: Let's move…").
	title string
	// editable marks a chat about an editable selection: pasting the answer
	// replaces it. It is not stored.
	editable bool
	// toRoot goes back to the root search once AI is turned on from this
	// chat's prompt (opened from the selected text).
	toRoot bool

	// The answer being written.
	busy    bool
	partial strings.Builder
	steps   []ToolStep
	cancel  context.CancelFunc
	usage   *backend.Usage

	// idle stops the chat's process when no message follows for a while.
	idle    *time.Timer
	idleGen int // which timer is current
}

// stopIdle stops the chat's idle timer. Plugin.mu must be held.
func (ch *chat) stopIdle() {
	if ch.idle != nil {
		ch.idle.Stop()
		ch.idle = nil
	}
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
		ch = &chat{sess: Session{ID: p.o.NewID(), Backend: p.be.Name()}, pending: strings.TrimSpace(pl.Text), toRoot: pl.ToRoot}
		if strings.TrimSpace(pl.Quote) != "" {
			ch.pending = selectionMessage(pl, req.Arguments)
			ch.title = titleOf(pl.Title)
			ch.editable = pl.Editable
		}
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
		if ch.title != "" {
			ch.sess.Title = ch.title
		}
	}
	ch.sess.Updated = now
	ch.msgs = append(ch.msgs, Message{Role: RoleUser, Text: text, Time: now})
	p.save(ch)
	ch.stopIdle()
	ch.busy = true
	ch.partial.Reset()
	ch.steps = nil
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
		case backend.EventTool, backend.EventToolResult:
			p.mu.Lock()
			ch.addStep(ev)
			p.mu.Unlock()
			p.push(ch)
		case backend.EventPermission:
			// The chat cannot ask yet (phase 2 shows a confirmation), so it
			// rejects. Claude Code never asks (docs/ai.md §20).
			if ev.Permission != nil && ev.Permission.Respond != nil {
				ev.Permission.Respond("")
			}
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
	m := Message{Role: RoleAssistant, Text: ch.partial.String(), Time: p.h.Now(), Steps: ch.steps}
	p.h.Logger().Info("answer", "kind", ev.Kind, "chars", len(m.Text), "steps", len(m.Steps), "took", time.Since(start).Round(time.Millisecond))
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
	ch.steps = nil
	ch.cancel = nil
	if !p.closed && p.chats[ch.sess.ID] == ch { // not deleted meanwhile
		p.save(ch)
		p.armIdle(ch)
	}
	p.mu.Unlock()
	p.push(ch)
}

// armIdle (re)starts the timer that stops the chat's process once no
// message has followed its answer for Options.ChatIdleRelease (R59).
// Plugin.mu must be held.
func (p *Plugin) armIdle(ch *chat) {
	ch.stopIdle()
	ch.idleGen++
	gen := ch.idleGen
	ch.idle = time.AfterFunc(p.o.ChatIdleRelease, func() { p.h.Go(func() { p.releaseChat(ch, gen) }) })
}

// releaseChat stops the process of an idle chat (the timer of generation
// gen fired); the conversation stays, and the next message resumes the
// session.
func (p *Plugin) releaseChat(ch *chat, gen int) {
	p.mu.Lock()
	if ch.idle == nil || ch.idleGen != gen || ch.busy || ch.conv == nil || p.closed {
		p.mu.Unlock()
		return
	}
	ch.idle = nil
	conv := ch.conv
	p.mu.Unlock()
	conv.Release()
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
	id, be := p.be.current()
	p.mu.Lock()
	conv, old, oldID := ch.conv, ch.convOpts, ch.convBackend
	checked := p.checkedID == id && !p.checked.IsZero() && time.Since(p.checked) < checkTTL
	p.mu.Unlock()
	// Resume changes after the first answer; the conversation knows that.
	old.Resume = opts.Resume
	if conv != nil && old == opts && oldID == id {
		return conv, nil
	}
	if !checked {
		st, err := be.Check(ctx)
		if err != nil {
			return nil, err
		}
		p.mu.Lock()
		p.checked, p.checkedID, p.status = time.Now(), id, st
		p.mu.Unlock()
	}
	if conv != nil {
		_ = conv.Close() // the preferences (or the backend) changed
	}
	conv, err := be.Open(ctx, opts)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	ch.conv, ch.convOpts, ch.convBackend = conv, opts, id
	p.mu.Unlock()
	return conv, nil
}

// openOptions are the options of the chat's conversation.
func (p *Plugin) openOptions(ch *chat) backend.OpenOptions {
	prefs := p.h.Preferences()
	web := prefs.Bool(prefWebSearch)
	tools := p.enabled() && prefs.Bool(prefTools)
	p.mu.Lock()
	resume := ch.sess.Started
	id := ch.sess.ID
	p.mu.Unlock()
	return backend.OpenOptions{
		SessionID:     id,
		Resume:        resume,
		Model:         prefs.String(prefModel),
		WebSearch:     web,
		LauncherTools: tools,
		SystemPrompt:  systemPrompt(p.h.I18n().Locale(), web, tools),
	}
}

// systemPrompt is appended to the backend's system prompt. It is written for
// the model, not shown, so it is not translated; it names the language to
// answer in. With tools, it explains how to use golauncher's tools.
func systemPrompt(locale string, web, tools bool) string {
	lang := map[string]string{"en": "English", "zh-CN": "Simplified Chinese"}[locale]
	if lang == "" {
		lang = "the language of the locale " + locale
	}
	s := "You are the AI assistant built into golauncher, a keyboard launcher for Windows; the user opened you from its search box. " +
		"Answer in " + lang + " unless the user writes in another language. " +
		"Be concise and direct, and format answers in Markdown (short paragraphs, lists, fenced code blocks). "
	if tools {
		s += "You can act on the user's computer only through golauncher's tools (list_plugins, search, run_command, get_detail, run_action), " +
			"which use the launcher's plugins. Search or list the plugins first, then act on an item by its handle with run_action. " +
			"Actions that change something are confirmed by the user in the launcher; if the user declines, do not retry or work around it. " +
			"Report only what the tool results say was done. You cannot read files or run commands directly."
	} else {
		s += "You cannot see or change the user's computer or files."
	}
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
		paste := l.T("action.paste")
		if ch.editable {
			paste = l.T("action.replaceSelection")
		}
		actions = append(actions,
			plugin.Action{ID: "copy", Title: l.T("action.copy"), Shortcut: "Ctrl+Shift+C", Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: answer}},
			plugin.Action{ID: "paste", Title: paste, Shortcut: "Ctrl+Shift+V", Do: plugin.ActionDo{Kind: plugin.ActionPaste, Target: answer}},
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
	if ch.toRoot {
		md += "\n\n" + l.T("off.selection")
		enable.Do.Payload = plugin.MustPayload(enablePayload{ToRoot: true})
	}
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
	answer := func(text string, tools []string, steps []ToolStep) {
		b.WriteString("**" + l.T("chat.assistant") + "**\n\n")
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
			answer(m.Text, m.Tools, m.Steps)
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
		if text == "" && len(ch.steps) == 0 {
			text = "_" + l.T("chat.thinking") + "_"
		} else {
			text += "▍"
		}
		answer(text, nil, ch.steps)
	}
	return strings.TrimSpace(b.String())
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
	case backend.ErrUnavailable:
		text = l.T("error.unavailable")
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
	if id, _ := p.be.current(); p.checkedID == id && p.status.Untested {
		md[0].Value += " · " + l.T("meta.untested", "version", p.status.Version)
	}
	if u := ch.usage; u != nil && (u.InputTokens > 0 || u.OutputTokens > 0) {
		md = append(md, plugin.Metadata{Label: l.T("meta.usage"),
			Value: l.T("meta.tokens", "in", l.Number(float64(u.InputTokens)), "out", l.Number(float64(u.OutputTokens)))})
	}
	return md
}
