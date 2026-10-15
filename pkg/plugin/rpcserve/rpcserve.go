// Package rpcserve runs a Go plugin as an external golauncher plugin: a
// separate process that talks to the launcher over JSON-RPC on stdin and
// stdout (docs/plugin-protocol.md).
//
//	func main() {
//		if err := rpcserve.Serve(hello.New()); err != nil {
//			log.Fatal(err)
//		}
//	}
//
// The same plugin code can be compiled into the launcher or run this way.
// Inside the plugin process, Serve provides a plugin.Host that forwards to
// the launcher: Storage, Clipboard, Events, Invalidate and Render become
// JSON-RPC calls, and Preferences and I18n follow the values the launcher
// sends. Logging goes to stderr, which the launcher writes to its log.
package rpcserve

import (
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/pkg/plugin/jsonrpc"
	"github.com/eziq1/golauncher/pkg/plugin/protocol"
)

// Serve runs p on the process's stdin and stdout until the launcher closes
// stdin. Anything the plugin prints to os.Stdout is redirected to stderr so
// it cannot corrupt the protocol stream.
func Serve(p plugin.Plugin) error {
	out := os.Stdout
	os.Stdout = os.Stderr
	return ServeConn(p, os.Stdin, out, os.Stderr)
}

// ServeConn is Serve over explicit streams; logs go to logOut.
func ServeConn(p plugin.Plugin, in io.Reader, out io.Writer, logOut io.Writer) error {
	s := &server{p: p, log: slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	s.conn = jsonrpc.NewConn(in, out, s.handle, s.log)
	err := s.conn.Run()
	s.close()
	return err
}

type server struct {
	p    plugin.Plugin
	log  *slog.Logger
	conn *jsonrpc.Conn
	h    atomic.Pointer[host]

	closeOnce sync.Once
}

func (s *server) close() {
	s.closeOnce.Do(func() {
		if s.h.Load() != nil {
			s.guard("Close", func() error { return s.p.Close() })
		}
	})
}

func (s *server) guard(what string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("plugin panicked", "in", what, "panic", r, "stack", string(debug.Stack()))
			err = jsonrpc.Errorf(jsonrpc.CodeInternalError, "plugin panicked in %s: %v", what, r)
		}
	}()
	return fn()
}

func decode[T any](params jsontext.Value) (T, error) {
	var v T
	if len(params) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(params, &v); err != nil {
		return v, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "invalid params: %v", err)
	}
	return v, nil
}

func notFound(method string) error {
	return jsonrpc.Errorf(jsonrpc.CodeMethodNotFound, "method not found: %s", method)
}

func (s *server) handle(ctx context.Context, method string, params jsontext.Value, notify bool) (any, error) {
	h := s.h.Load()
	if method != protocol.MethodInitialize && h == nil {
		if notify {
			return nil, nil
		}
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidRequest, "not initialized")
	}
	switch method {
	case protocol.MethodInitialize:
		return s.initialize(ctx, params)
	case protocol.MethodRootItems:
		rp, ok := s.p.(plugin.RootProvider)
		if !ok {
			return nil, notFound(method)
		}
		a, err := decode[protocol.RootItemsParams](params)
		if err != nil {
			return nil, err
		}
		items, err := rp.RootItems(ctx, plugin.Query{Text: a.Query})
		if items == nil {
			items = []plugin.Item{}
		}
		return items, err
	case protocol.MethodView:
		vp, ok := s.p.(plugin.ViewProvider)
		if !ok {
			return nil, notFound(method)
		}
		a, err := decode[protocol.ViewParams](params)
		if err != nil {
			return nil, err
		}
		return vp.View(ctx, a.Request)
	case protocol.MethodHandleAction:
		ah, ok := s.p.(plugin.ActionHandler)
		if !ok {
			return nil, notFound(method)
		}
		a, err := decode[protocol.HandleActionParams](params)
		if err != nil {
			return nil, err
		}
		return ah.HandleAction(ctx, a.Action)
	case protocol.MethodPreferenceOptions:
		po, ok := s.p.(plugin.PreferenceOptionsProvider)
		if !ok {
			return nil, notFound(method)
		}
		a, err := decode[protocol.PreferenceOptionsParams](params)
		if err != nil {
			return nil, err
		}
		opts, err := po.PreferenceOptions(ctx, a.Name)
		if opts == nil {
			opts = []plugin.Option{}
		}
		return opts, err
	case protocol.MethodShutdown:
		s.close()
		return nil, nil
	case protocol.NotifyLocaleChanged:
		a, err := decode[protocol.LocaleChangedParams](params)
		if err == nil {
			h.i18n.SetLocale(a.Locale)
			h.events.publish(plugin.EventLocaleChanged)
		}
		return nil, nil
	case protocol.NotifyPreferencesChanged:
		a, err := decode[protocol.PreferencesChangedParams](params)
		if err == nil {
			h.prefs.set(a.Preferences)
			h.events.publish(plugin.EventConfigChanged)
		}
		return nil, nil
	case protocol.NotifyEvent:
		a, err := decode[protocol.EventParams](params)
		if err == nil {
			h.events.publish(a.Name)
		}
		return nil, nil
	case protocol.NotifyViewClosed:
		a, err := decode[protocol.ViewClosedParams](params)
		if vc, ok := s.p.(plugin.ViewCloser); ok && err == nil {
			s.guard("ViewClosed", func() error { vc.ViewClosed(a.Instance); return nil })
		}
		return nil, nil
	}
	if notify {
		return nil, nil
	}
	return nil, notFound(method)
}

func (s *server) initialize(ctx context.Context, params jsontext.Value) (any, error) {
	if s.h.Load() != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidRequest, "already initialized")
	}
	a, err := decode[protocol.InitializeParams](params)
	if err != nil {
		return nil, err
	}
	m := s.p.Manifest()
	id := m.ID
	if id == "" {
		id = a.PluginID
	}
	mgr := i18n.NewManager(i18n.Source, nil)
	if err := mgr.Register(i18n.CoreNamespace, i18n.CoreLocales()); err != nil {
		return nil, err
	}
	var locales fs.FS
	if lp, ok := s.p.(plugin.LocaleProvider); ok {
		locales = lp.Locales()
	} else if _, err := os.Stat("locales/" + i18n.Source + ".json"); err == nil {
		locales = os.DirFS("locales")
	}
	if locales != nil {
		if err := mgr.Register(id, locales); err != nil {
			s.log.Warn("loading locales", "err", err)
		}
	}
	mgr.SetLocale(a.Locale)
	for _, d := range []string{a.DataDir, a.CacheDir} {
		if d != "" {
			_ = os.MkdirAll(d, 0o755)
		}
	}
	h := &host{
		s: s, id: id, i18n: mgr, dataDir: a.DataDir, cacheDir: a.CacheDir,
		prefs:  newPrefs(m.Preferences, a.Preferences),
		events: &events{subs: map[string]map[int]func(){}},
	}
	h.events.h = h
	if err := s.guard("Init", func() error { return s.p.Init(ctx, h) }); err != nil {
		if errors.Is(err, plugin.ErrUnavailable) {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeUnavailable, Message: unavailableReason(err)}
		}
		return nil, err
	}
	s.h.Store(h)
	_, root := s.p.(plugin.RootProvider)
	_, views := s.p.(plugin.ViewProvider)
	_, actions := s.p.(plugin.ActionHandler)
	_, opts := s.p.(plugin.PreferenceOptionsProvider)
	return protocol.InitializeResult{
		Protocol: protocol.Version,
		Capabilities: protocol.Capabilities{
			RootItems: root, Views: views, Actions: actions, PreferenceOptions: opts, Render: true,
		},
	}, nil
}

func unavailableReason(err error) string {
	s := err.Error()
	if r, ok := strings.CutSuffix(s, ": "+plugin.ErrUnavailable.Error()); ok {
		return r
	}
	return s
}

// callTimeout bounds host calls made through the Host.
const callTimeout = 10 * time.Second

// host is the plugin.Host inside the plugin process.
type host struct {
	s        *server
	id       string
	i18n     *i18n.Manager
	prefs    *prefs
	events   *events
	dataDir  string
	cacheDir string
	version  atomic.Int64
}

var _ plugin.Host = (*host)(nil)

func (h *host) call(method string, params, result any) error {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return h.s.conn.Call(ctx, method, params, result)
}

func (h *host) Logger() *slog.Logger            { return h.s.log.With("plugin", h.id) }
func (h *host) Now() time.Time                  { return time.Now() }
func (h *host) Storage() plugin.KV              { return kv{h} }
func (h *host) Preferences() plugin.Preferences { return h.prefs }
func (h *host) Clipboard() plugin.Clipboard     { return clipboard{h} }
func (h *host) Events() plugin.Events           { return h.events }
func (h *host) I18n() plugin.Localizer          { return h.i18n.Localizer(h.id) }
func (h *host) DataDir() string                 { return h.dataDir }
func (h *host) CacheDir() string                { return h.cacheDir }

// Go implements plugin.Host: a panic in fn is logged instead of ending the
// plugin process.
func (h *host) Go(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				h.s.log.Error("panic contained", "in", "Host.Go", "panic", r, "stack", string(debug.Stack()))
			}
		}()
		fn()
	}()
}

// Invalidate implements plugin.Host.
func (h *host) Invalidate(viewID string) {
	if err := h.s.conn.Notify(protocol.NotifyInvalidate, protocol.InvalidateParams{ViewID: viewID}); err != nil {
		h.s.log.Warn("invalidate", "err", err)
	}
}

// Render implements plugin.Host. Versions increase with every call.
func (h *host) Render(instance string, v plugin.View) {
	p := protocol.RenderParams{Instance: instance, Version: h.version.Add(1), View: v}
	if err := h.s.conn.Notify(protocol.NotifyRender, p); err != nil {
		h.s.log.Warn("render", "err", err)
	}
}

type kv struct{ h *host }

func (k kv) Get(key string) ([]byte, bool, error) {
	var r protocol.StorageGetResult
	if err := k.h.call(protocol.HostStorageGet, protocol.StorageKeyParams{Key: key}, &r); err != nil {
		return nil, false, err
	}
	if !r.Found {
		return nil, false, nil
	}
	if r.Base64 != "" {
		b, err := base64.StdEncoding.DecodeString(r.Base64)
		return b, err == nil, err
	}
	return []byte(r.Value), true, nil
}

func (k kv) Set(key string, value []byte) error {
	p := protocol.StorageSetParams{Key: key}
	if utf8.Valid(value) {
		p.Value = string(value)
	} else {
		p.Base64 = base64.StdEncoding.EncodeToString(value)
	}
	return k.h.call(protocol.HostStorageSet, p, nil)
}

func (k kv) Delete(key string) error {
	return k.h.call(protocol.HostStorageDelete, protocol.StorageKeyParams{Key: key}, nil)
}

type clipboard struct{ h *host }

func (c clipboard) Read() (plugin.ClipboardContent, error) {
	var r protocol.ClipboardContent
	if err := c.h.call(protocol.HostClipboardRead, struct{}{}, &r); err != nil {
		return plugin.ClipboardContent{}, err
	}
	out := plugin.ClipboardContent{Text: r.Text, Files: r.Files, SourceApp: r.SourceApp, Exclude: r.Exclude}
	if r.Image != "" {
		img, err := base64.StdEncoding.DecodeString(r.Image)
		if err != nil {
			return out, err
		}
		out.Image = img
	}
	return out, nil
}

func (c clipboard) WriteText(text string) error {
	return c.h.call(protocol.HostClipboardWriteText, protocol.WriteTextParams{Text: text}, nil)
}

func (c clipboard) WriteImage(png []byte) error {
	return c.h.call(protocol.HostClipboardWriteImage, protocol.WriteImageParams{PNG: base64.StdEncoding.EncodeToString(png)}, nil)
}

func (c clipboard) WriteFiles(paths []string) error {
	return c.h.call(protocol.HostClipboardWriteFiles, protocol.WriteFilesParams{Paths: paths}, nil)
}

// events implements plugin.Events: the first subscription to an event
// name subscribes with the launcher (host/subscribe); events then arrive as
// notifications and run the handlers on their own goroutines.
type events struct {
	h          *host
	mu         sync.Mutex
	next       int
	subs       map[string]map[int]func()
	subscribed map[string]bool
}

func (e *events) Subscribe(name string, fn func()) (unsubscribe func()) {
	e.mu.Lock()
	id := e.next
	e.next++
	if e.subs[name] == nil {
		e.subs[name] = map[int]func(){}
	}
	e.subs[name][id] = fn
	first := !e.subscribed[name]
	if first {
		if e.subscribed == nil {
			e.subscribed = map[string]bool{}
		}
		e.subscribed[name] = true
	}
	e.mu.Unlock()
	// Locale and preference changes come as their own notifications.
	if first && name != plugin.EventLocaleChanged && name != plugin.EventConfigChanged {
		e.h.Go(func() {
			if err := e.h.call(protocol.HostSubscribe, protocol.SubscribeParams{Event: name}, nil); err != nil {
				e.h.s.log.Warn("subscribe", "event", name, "err", err)
			}
		})
	}
	return func() {
		e.mu.Lock()
		delete(e.subs[name], id)
		e.mu.Unlock()
	}
}

func (e *events) publish(name string) {
	e.mu.Lock()
	var fns []func()
	for _, fn := range e.subs[name] {
		fns = append(fns, fn)
	}
	e.mu.Unlock()
	for _, fn := range fns {
		e.h.Go(fn)
	}
}

// prefs implements plugin.Preferences from the values sent by the launcher,
// falling back to the manifest defaults.
type prefs struct {
	mu       sync.RWMutex
	defaults map[string]jsontext.Value
	values   map[string]jsontext.Value
}

func newPrefs(declared []plugin.Preference, values map[string]jsontext.Value) *prefs {
	p := &prefs{defaults: map[string]jsontext.Value{}}
	for _, d := range declared {
		if len(d.Default) > 0 {
			p.defaults[d.Name] = d.Default
		}
	}
	p.set(values)
	return p
}

func (p *prefs) set(values map[string]jsontext.Value) {
	p.mu.Lock()
	p.values = values
	p.mu.Unlock()
}

func (p *prefs) Raw(name string) (jsontext.Value, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if v, ok := p.values[name]; ok {
		return v, true
	}
	v, ok := p.defaults[name]
	return v, ok
}

func get[T any](p *prefs, name string) T {
	var v T
	if raw, ok := p.Raw(name); ok {
		_ = json.Unmarshal(raw, &v)
	}
	return v
}

func (p *prefs) String(name string) string    { return get[string](p, name) }
func (p *prefs) Int(name string) int          { return int(get[float64](p, name)) }
func (p *prefs) Bool(name string) bool        { return get[bool](p, name) }
func (p *prefs) Strings(name string) []string { return get[[]string](p, name) }
