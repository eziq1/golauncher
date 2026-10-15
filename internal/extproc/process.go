package extproc

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/pkg/plugin/jsonrpc"
	"github.com/eziq1/golauncher/pkg/plugin/protocol"
)

// process is one running plugin process.
type process struct {
	cmd   *exec.Cmd
	conn  *jsonrpc.Conn
	stdin io.WriteCloser
	job   job
	done  chan struct{} // closed after the process exited
	// expected is set before a deliberate stop, so the exit is not a crash.
	expected atomic.Bool
	killOnce sync.Once
	subs     map[string]func() // host event subscriptions; guarded by Proxy.mu
}

// kill ends the process and everything it started.
func (proc *process) kill() {
	proc.killOnce.Do(func() {
		proc.job.terminate()
		if proc.cmd.Process != nil {
			_ = proc.cmd.Process.Kill()
		}
	})
}

// launch starts the process and initializes it.
func (p *Proxy) launch() (*process, protocol.Capabilities, error) {
	var caps protocol.Capabilities
	path, err := resolveCommand(p.dir, p.man.Run.Command)
	if err != nil {
		return nil, caps, &CallError{ErrUnavailable, p.tr().T("extproc.commandNotFound", "command", p.man.Run.Command)}
	}
	cmd := exec.Command(path, p.man.Run.Args...)
	cmd.Dir = p.dir
	cmd.Env = append(os.Environ(),
		"GOLAUNCHER=1",
		"GOLAUNCHER_PLUGIN_ID="+p.man.ID,
		"GOLAUNCHER_PROTOCOL="+strconv.Itoa(protocol.Version),
		"PYTHONUNBUFFERED=1",
		"PYTHONIOENCODING=utf-8",
	)
	cmd.SysProcAttr = sysProcAttr()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, caps, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, caps, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, caps, err
	}
	if err := cmd.Start(); err != nil {
		return nil, caps, &CallError{ErrCrashed, p.tr().T("extproc.startFailed", "err", err)}
	}
	proc := &process{cmd: cmd, stdin: stdin, done: make(chan struct{}), subs: map[string]func(){}}
	if proc.job, err = newJob(cmd.Process.Pid); err != nil {
		p.log.Warn("job object", "err", err)
	}
	proc.conn = jsonrpc.NewConn(stdout, stdin, p.hostHandler(proc), p.log)

	var readers sync.WaitGroup
	readers.Go(func() {
		if err := proc.conn.Run(); err != nil {
			p.log.Warn("reading from external plugin", "err", err)
		}
	})
	readers.Go(func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			p.log.Info("plugin stderr", "line", sc.Text())
		}
	})
	go func() {
		readers.Wait()
		err := cmd.Wait()
		proc.job.close()
		close(proc.done)
		p.onExit(proc, err)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), p.opts.InitTimeout)
	defer cancel()
	params := protocol.InitializeParams{
		Protocol: protocol.Version, HostVersion: p.opts.HostVersion, PluginID: p.man.ID,
		Locale: p.h.I18n().Locale(), DataDir: p.h.DataDir(), CacheDir: p.h.CacheDir(),
		Preferences: p.preferences(),
	}
	var res protocol.InitializeResult
	err = proc.conn.Call(ctx, protocol.MethodInitialize, params, &res)
	if err == nil && res.Protocol != protocol.Version {
		err = &CallError{ErrUnavailable, p.tr().T("extproc.protocol", "version", res.Protocol, "supported", protocol.Version)}
	}
	if e, ok := errors.AsType[*jsonrpc.Error](err); ok && e.Code == jsonrpc.CodeUnavailable {
		err = &CallError{ErrUnavailable, e.Message}
	}
	if err != nil {
		proc.expected.Store(true)
		proc.kill()
		<-proc.done
		if _, ok := errors.AsType[*CallError](err); !ok {
			err = fmt.Errorf("initialize: %w", err)
		}
		return nil, caps, err
	}
	return proc, res.Capabilities, nil
}

func permissionDenied(perm plugin.Permission) error {
	return jsonrpc.Errorf(jsonrpc.CodePermissionDenied, "permission denied: the manifest does not declare %q", perm)
}

func (p *Proxy) has(perm plugin.Permission) bool {
	return slices.Contains(p.man.Permissions, perm)
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

// hostHandler serves the plugin's calls to the host, checking permissions.
func (p *Proxy) hostHandler(proc *process) jsonrpc.Handler {
	return func(ctx context.Context, method string, params jsontext.Value, notify bool) (any, error) {
		switch method {
		case protocol.HostStorageGet, protocol.HostStorageSet, protocol.HostStorageDelete:
			if !p.has(plugin.PermStorage) {
				return nil, permissionDenied(plugin.PermStorage)
			}
			return p.storage(method, params)
		case protocol.HostClipboardRead, protocol.HostClipboardWriteText, protocol.HostClipboardWriteImage, protocol.HostClipboardWriteFiles:
			if !p.has(plugin.PermClipboard) {
				return nil, permissionDenied(plugin.PermClipboard)
			}
			return p.clipboard(method, params)
		case protocol.HostSubscribe:
			a, err := decode[protocol.SubscribeParams](params)
			if err != nil {
				return nil, err
			}
			p.subscribe(proc, a.Event)
			return nil, nil
		case protocol.HostLog:
			a, err := decode[protocol.LogParams](params)
			if err != nil {
				return nil, err
			}
			switch a.Level {
			case "debug":
				p.log.Debug(a.Message)
			case "warn", "warning":
				p.log.Warn(a.Message)
			case "error":
				p.log.Error(a.Message)
			default:
				p.log.Info(a.Message)
			}
			return nil, nil
		case protocol.HostTaskRunProcess:
			if !p.has(plugin.PermExec) {
				return nil, permissionDenied(plugin.PermExec)
			}
			a, err := decode[protocol.RunProcessParams](params)
			if err != nil {
				return nil, err
			}
			// The outcome goes back to the process running then (a restarted
			// process has forgotten the task; an idle-stopped one misses it).
			a.Spec.OnDone = func(r plugin.TaskResult) {
				p.notify(protocol.NotifyTaskFinished, protocol.TaskFinishedParams{Result: r})
			}
			return protocol.RunProcessResult{ID: p.h.RunProcess(a.Spec, a.Cmd)}, nil
		case protocol.HostToast:
			a, err := decode[protocol.ToastParams](params)
			if err != nil {
				return nil, err
			}
			p.h.Toast(a.Toast)
			return nil, nil
		case protocol.NotifyInvalidate:
			a, err := decode[protocol.InvalidateParams](params)
			if err == nil {
				p.h.Invalidate(a.ViewID)
			}
			return nil, err
		case protocol.NotifyRender:
			a, err := decode[protocol.RenderParams](params)
			if err != nil {
				return nil, err
			}
			p.mu.Lock()
			fresh := a.Version > p.renderVer[a.Instance]
			if fresh {
				p.renderVer[a.Instance] = a.Version
			}
			p.mu.Unlock()
			if fresh {
				p.h.Render(a.Instance, a.View)
			}
			return nil, nil
		}
		if notify {
			p.log.Debug("unknown notification from plugin", "method", method)
			return nil, nil
		}
		return nil, jsonrpc.Errorf(jsonrpc.CodeMethodNotFound, "method not found: %s", method)
	}
}

func (p *Proxy) storage(method string, params jsontext.Value) (any, error) {
	kv := p.h.Storage()
	switch method {
	case protocol.HostStorageGet:
		a, err := decode[protocol.StorageKeyParams](params)
		if err != nil {
			return nil, err
		}
		v, ok, err := kv.Get(a.Key)
		if err != nil || !ok {
			return protocol.StorageGetResult{}, err
		}
		return protocol.StorageGetResult{Found: true, StorageValue: storageValue(v)}, nil
	case protocol.HostStorageSet:
		a, err := decode[protocol.StorageSetParams](params)
		if err != nil {
			return nil, err
		}
		v := []byte(a.Value)
		if a.Base64 != "" {
			if v, err = base64.StdEncoding.DecodeString(a.Base64); err != nil {
				return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "base64: %v", err)
			}
		}
		return nil, kv.Set(a.Key, v)
	default:
		a, err := decode[protocol.StorageKeyParams](params)
		if err != nil {
			return nil, err
		}
		return nil, kv.Delete(a.Key)
	}
}

func storageValue(v []byte) protocol.StorageValue {
	if utf8.Valid(v) {
		return protocol.StorageValue{Value: string(v)}
	}
	return protocol.StorageValue{Base64: base64.StdEncoding.EncodeToString(v)}
}

func (p *Proxy) clipboard(method string, params jsontext.Value) (any, error) {
	cb := p.h.Clipboard()
	switch method {
	case protocol.HostClipboardRead:
		c, err := cb.Read()
		if err != nil {
			return nil, err
		}
		out := protocol.ClipboardContent{Text: c.Text, Files: c.Files, SourceApp: c.SourceApp, Exclude: c.Exclude}
		if len(c.Image) > 0 {
			out.Image = base64.StdEncoding.EncodeToString(c.Image)
		}
		return out, nil
	case protocol.HostClipboardWriteText:
		a, err := decode[protocol.WriteTextParams](params)
		if err != nil {
			return nil, err
		}
		return nil, cb.WriteText(a.Text)
	case protocol.HostClipboardWriteImage:
		a, err := decode[protocol.WriteImageParams](params)
		if err != nil {
			return nil, err
		}
		png, err := base64.StdEncoding.DecodeString(a.PNG)
		if err != nil {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "base64: %v", err)
		}
		return nil, cb.WriteImage(png)
	default:
		a, err := decode[protocol.WriteFilesParams](params)
		if err != nil {
			return nil, err
		}
		return nil, cb.WriteFiles(a.Paths)
	}
}

// subscribe forwards host event name to the process, once per name.
func (p *Proxy) subscribe(proc *process, name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if proc.subs == nil || proc.subs[name] != nil {
		return // exited or already subscribed
	}
	proc.subs[name] = p.h.Events().Subscribe(name, func() {
		if err := proc.conn.Notify(protocol.NotifyEvent, protocol.EventParams{Name: name}); err != nil {
			p.log.Debug("forwarding event", "event", name, "err", err)
		}
	})
}
