// Package ai is the builtin.ai plugin (docs/ai.md, phase 2): chatting with
// Claude in the launcher through the user's installed Claude Code CLI and
// its subscription. It offers the "AI Chat" and "AI Chat History" commands,
// opens a chat when Tab is pressed in the root search with text
// (plugin.TabProvider), offers "Ask AI: <text>" when the root search finds
// nothing good (plugin.FallbackProvider), and hands a question over to the
// Claude or ChatGPT apps. With the "tools" preference (on by default) Claude
// may use the launcher's plugins through golauncher's MCP tools (R47); each
// call is shown as a step line of the answer.
//
// Answers come from a backend.Backend: internal/ai/claudecode for real, a
// backend.Fake in tests and glctl. Nothing is sent while the master switch
// (the "enabled" preference) is off, which is the default.
package ai

import (
	"context"
	"embed"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io/fs"
	"strconv"
	"sync"
	"time"

	"github.com/eziq1/golauncher/internal/winapi/shell"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// ID is the plugin ID.
const ID = "builtin.ai"

// Preference names.
const (
	prefEnabled   = "enabled"
	prefModel     = "model"
	prefWebSearch = "webSearch"
	prefTools     = "tools"
	prefRetention = "retentionDays"
	prefFallback  = "askFallback"
)

// View IDs (also the command names).
const (
	viewChat    = "chat"
	viewHistory = "history"
)

const (
	// defaultIdleRelease is how long after the window hides the CLI
	// processes are stopped (docs/ai.md §6.1); the chats resume later.
	defaultIdleRelease = 10 * time.Minute
	// defaultRetentionDays is how long chats are kept (docs/ai.md §14).
	defaultRetentionDays = 30
	// checkTTL is how long a successful backend check is trusted.
	checkTTL = 5 * time.Minute
)

//go:embed locales/*.json
var locales embed.FS

// Options configure the plugin.
type Options struct {
	// Backend answers the questions. Required.
	Backend backend.Backend
	// Sync writes each answer before the action (or view) that asked for it
	// returns, so that results are deterministic (glctl, tests).
	Sync bool
	// HasProtocol reports whether an app handles links of a URL scheme
	// (nil: the Windows registry); it decides between claude:// links and
	// claude.ai.
	HasProtocol func(scheme string) bool
	// IdleRelease overrides defaultIdleRelease.
	IdleRelease time.Duration
	// NewID returns the ID of a new chat (nil: a random UUID). glctl numbers
	// them so that its output is reproducible.
	NewID func() string
}

// Plugin is the AI chat plugin.
type Plugin struct {
	o  Options
	be backend.Backend
	h  plugin.Host
	st *store

	mu      sync.Mutex
	chats   map[string]*chat  // live chats by session ID
	views   map[string]string // open chat view instance -> session ID
	idle    *time.Timer       // releases the processes after the window hid
	unsubs  []func()
	checked time.Time // when the backend was last found usable
	status  backend.Status
	closed  bool
	turns   sync.WaitGroup // answers being written
}

var (
	_ plugin.Plugin           = (*Plugin)(nil)
	_ plugin.ViewProvider     = (*Plugin)(nil)
	_ plugin.ActionHandler    = (*Plugin)(nil)
	_ plugin.ViewCloser       = (*Plugin)(nil)
	_ plugin.LocaleProvider   = (*Plugin)(nil)
	_ plugin.FallbackProvider = (*Plugin)(nil)
	_ plugin.TabProvider      = (*Plugin)(nil)
)

// New returns the plugin answering with be.
func New(be backend.Backend) *Plugin { return NewWithOptions(Options{Backend: be}) }

// NewWithOptions returns the plugin configured by o.
func NewWithOptions(o Options) *Plugin {
	if o.HasProtocol == nil {
		o.HasProtocol = shell.ProtocolRegistered
	}
	if o.IdleRelease == 0 {
		o.IdleRelease = defaultIdleRelease
	}
	if o.NewID == nil {
		o.NewID = newSessionID
	}
	return &Plugin{o: o, be: o.Backend, chats: map[string]*chat{}, views: map[string]string{}}
}

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:          ID,
		Name:        "%plugin.name%",
		Description: "%plugin.description%",
		Version:     "1.0.0",
		Icon:        plugin.Icon{Name: "sparkles"},
		Permissions: []plugin.Permission{plugin.PermStorage, plugin.PermClipboard, plugin.PermNetwork, plugin.PermExec},
		Commands: []plugin.Command{
			{Name: viewChat, Title: "%command.chat.title%", Mode: plugin.ModeView, Icon: plugin.Icon{Name: "sparkles"},
				Keywords: []string{"%command.chat.keywords%"}},
			{Name: viewHistory, Title: "%command.history.title%", Mode: plugin.ModeView, Icon: plugin.Icon{Name: "history"},
				Keywords: []string{"%command.history.keywords%"}},
		},
		Preferences: []plugin.Preference{
			{Name: prefEnabled, Type: plugin.PrefBool, Title: "%pref.enabled.title%", Description: "%pref.enabled.description%",
				Default: plugin.MustPayload(false)},
			{Name: prefModel, Type: plugin.PrefSelect, Title: "%pref.model.title%", Description: "%pref.model.description%",
				Default: plugin.MustPayload(""), Options: []plugin.Option{
					{Value: "", Title: "%pref.model.default%"}, {Value: "sonnet", Title: "%pref.model.sonnet%"},
					{Value: "opus", Title: "%pref.model.opus%"}, {Value: "haiku", Title: "%pref.model.haiku%"},
				}},
			{Name: prefWebSearch, Type: plugin.PrefBool, Title: "%pref.webSearch.title%", Description: "%pref.webSearch.description%",
				Default: plugin.MustPayload(false)},
			{Name: prefTools, Type: plugin.PrefBool, Title: "%pref.tools.title%", Description: "%pref.tools.description%",
				Default: plugin.MustPayload(true)},
			{Name: prefRetention, Type: plugin.PrefSelect, Title: "%pref.retention.title%", Description: "%pref.retention.description%",
				Default: plugin.MustPayload(strconv.Itoa(defaultRetentionDays)), Options: []plugin.Option{
					{Value: "7", Title: "%pref.retention.7%"}, {Value: "30", Title: "%pref.retention.30%"},
					{Value: "90", Title: "%pref.retention.90%"}, {Value: "365", Title: "%pref.retention.365%"},
				}},
			{Name: prefFallback, Type: plugin.PrefBool, Title: "%pref.askFallback.title%", Description: "%pref.askFallback.description%",
				Default: plugin.MustPayload(true)},
		},
	}
}

// Locales implements plugin.LocaleProvider.
func (p *Plugin) Locales() fs.FS {
	sub, _ := fs.Sub(locales, "locales")
	return sub
}

// Init implements plugin.Plugin.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.h = h
	p.st = &store{kv: h.Storage()}
	ev := h.Events()
	p.unsubs = append(p.unsubs,
		ev.Subscribe(plugin.EventWindowHidden, p.windowHidden),
		ev.Subscribe(plugin.EventWindowShown, p.windowShown),
		ev.Subscribe(plugin.EventConfigChanged, p.configChanged),
	)
	p.prune()
	return nil
}

// Close implements plugin.Plugin: the answers being written are stopped and
// the CLI processes end.
func (p *Plugin) Close() error {
	p.mu.Lock()
	p.closed = true
	for _, u := range p.unsubs {
		u()
	}
	if p.idle != nil {
		p.idle.Stop()
	}
	chats := p.chats
	p.chats = map[string]*chat{}
	p.mu.Unlock()
	for _, ch := range chats {
		p.stopChat(ch)
	}
	p.turns.Wait()
	for _, ch := range chats {
		if ch.conv != nil {
			_ = ch.conv.Close()
		}
	}
	if c, ok := p.be.(interface{ Close() error }); ok {
		return c.Close()
	}
	return nil
}

func (p *Plugin) enabled() bool { return p.h.Preferences().Bool(prefEnabled) }

// retention is how long chats are kept.
func (p *Plugin) retention() time.Duration {
	days, err := strconv.Atoi(p.h.Preferences().String(prefRetention))
	if err != nil || days <= 0 {
		days = defaultRetentionDays
	}
	return time.Duration(days) * 24 * time.Hour
}

// prune deletes the chats older than the retention period, except open ones.
func (p *Plugin) prune() {
	now := p.h.Now()
	n, err := p.st.Prune(now.Add(-p.retention()), func(id string) bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		_, live := p.chats[id]
		return live
	})
	if err != nil {
		p.h.Logger().Warn("prune chats", "err", err)
	} else if n > 0 {
		p.h.Logger().Info("pruned chats", "count", n)
	}
}

// windowHidden starts the timer that stops the CLI processes.
func (p *Plugin) windowHidden() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	if p.idle != nil {
		p.idle.Stop()
	}
	p.idle = time.AfterFunc(p.o.IdleRelease, func() { p.h.Go(p.releaseIdle) })
}

func (p *Plugin) windowShown() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.idle != nil {
		p.idle.Stop()
		p.idle = nil
	}
}

// releaseIdle stops the processes of the chats not writing an answer; chats
// no longer shown are dropped from memory (they stay in the history).
func (p *Plugin) releaseIdle() {
	p.mu.Lock()
	var release, closeConv []backend.Conversation
	for id, ch := range p.chats {
		if ch.busy || ch.conv == nil {
			continue
		}
		if p.shown(id) {
			release = append(release, ch.conv)
			continue
		}
		closeConv = append(closeConv, ch.conv)
		delete(p.chats, id)
	}
	p.mu.Unlock()
	for _, c := range release {
		c.Release()
	}
	for _, c := range closeConv {
		_ = c.Close()
	}
}

// shown reports whether a view shows the chat with id. p.mu must be held.
func (p *Plugin) shown(id string) bool {
	for _, sid := range p.views {
		if sid == id {
			return true
		}
	}
	return false
}

// configChanged follows the preferences: turning AI off stops everything;
// open chat views show the new state.
func (p *Plugin) configChanged() {
	if !p.enabled() {
		p.mu.Lock()
		var chats []*chat
		for _, ch := range p.chats {
			chats = append(chats, ch)
		}
		p.mu.Unlock()
		for _, ch := range chats {
			p.stopChat(ch)
		}
		p.releaseIdle()
	}
	p.h.Invalidate(viewChat)
}

// chatPayload is the payload of the chat view.
type chatPayload struct {
	// Text is the first message of a new chat (Tab, Ask AI).
	Text string `json:"text,omitempty"`
	// Session opens a chat of the history.
	Session string `json:"session,omitempty"`
}

// instancePayload names the chat view instance of an action.
type instancePayload struct {
	Instance string `json:"instance"`
}

// View implements plugin.ViewProvider.
func (p *Plugin) View(_ context.Context, req plugin.ViewRequest) (plugin.View, error) {
	switch req.ID {
	case viewChat:
		return p.chatView(req), nil
	case viewHistory:
		return p.historyView(), nil
	}
	return plugin.View{}, nil
}

// ViewClosed implements plugin.ViewCloser.
func (p *Plugin) ViewClosed(instance string) {
	p.mu.Lock()
	delete(p.views, instance)
	p.mu.Unlock()
}

// FallbackItems implements plugin.FallbackProvider: "Ask AI: <text>".
func (p *Plugin) FallbackItems(_ context.Context, q plugin.Query) ([]plugin.Item, error) {
	if !p.enabled() || !p.h.Preferences().Bool(prefFallback) {
		return nil, nil
	}
	l := p.h.I18n()
	return []plugin.Item{{
		ID:       "ask",
		Title:    l.T("fallback.title", "text", q.Text),
		Subtitle: p.be.Name(),
		Icon:     plugin.Icon{Name: "sparkles"},
		Actions:  []plugin.Action{p.askAction(q.Text)},
	}}, nil
}

// TabAction implements plugin.TabProvider: Tab opens a new chat with the
// text. With AI off it opens the prompt to turn it on, which then sends it.
func (p *Plugin) TabAction(q plugin.Query) (plugin.Action, bool) {
	return p.askAction(q.Text), true
}

func (p *Plugin) askAction(text string) plugin.Action {
	return plugin.Action{ID: "ask", Title: p.h.I18n().T("action.ask"),
		Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: viewChat, Payload: plugin.MustPayload(chatPayload{Text: text})}}
}

// HandleAction implements plugin.ActionHandler.
func (p *Plugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	switch a.Target {
	case "enable":
		return plugin.Result{Preferences: map[string]jsontext.Value{prefEnabled: plugin.MustPayload(true)}, Refresh: true}, nil
	case "delete", "clear":
		return p.historyAction(a)
	}
	var ip instancePayload
	_ = json.Unmarshal(a.Payload, &ip)
	switch a.Target {
	case "send":
		return p.send(ip.Instance, a.Query), nil
	case "stop":
		p.stopInstance(ip.Instance)
		return plugin.Result{Refresh: true}, nil
	case "new":
		p.newChat(ip.Instance)
		return plugin.Result{ClearText: true}, nil
	case "claude", "chatgpt":
		return p.continueIn(a.Target, ip.Instance, a.Query), nil
	}
	return plugin.Result{}, nil
}
