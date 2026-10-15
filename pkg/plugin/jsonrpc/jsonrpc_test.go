package jsonrpc

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// pair returns two connected Conns.
func pair(t *testing.T, ha, hb Handler) (a, b *Conn) {
	t.Helper()
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	a = NewConn(ar, aw, ha, nil)
	b = NewConn(br, bw, hb, nil)
	go a.Run()
	go b.Run()
	t.Cleanup(func() { aw.Close(); bw.Close(); <-a.Done(); <-b.Done() })
	return a, b
}

func TestCallRoundTripAndCallback(t *testing.T) {
	var a, b *Conn
	a, b = pair(t,
		func(ctx context.Context, method string, params jsontext.Value, notify bool) (any, error) {
			return "pong:" + string(params), nil
		},
		func(ctx context.Context, method string, params jsontext.Value, notify bool) (any, error) {
			// A handler may call back into the other side while serving.
			var s string
			if err := b.Call(ctx, "ping", 1, &s); err != nil {
				return nil, err
			}
			return map[string]string{"method": method, "inner": s}, nil
		})
	var got map[string]string
	if err := a.Call(t.Context(), "outer", nil, &got); err != nil {
		t.Fatal(err)
	}
	if got["method"] != "outer" || got["inner"] != "pong:1" {
		t.Fatalf("got %v", got)
	}
}

func TestErrorsAndPanics(t *testing.T) {
	a, _ := pair(t, nil, func(ctx context.Context, method string, params jsontext.Value, notify bool) (any, error) {
		switch method {
		case "denied":
			return nil, Errorf(CodePermissionDenied, "no")
		case "fail":
			return nil, errors.New("plain")
		case "panic":
			panic("boom")
		}
		return nil, nil
	})
	check := func(method string, code int64, msg string) {
		t.Helper()
		err := a.Call(t.Context(), method, nil, nil)
		e, ok := errors.AsType[*Error](err)
		if !ok || e.Code != code || !strings.Contains(e.Message, msg) {
			t.Errorf("%s: %#v", method, err)
		}
	}
	check("denied", CodePermissionDenied, "no")
	check("fail", CodeInternalError, "plain")
	check("panic", CodeInternalError, "boom")
	if err := a.Call(t.Context(), "nil", nil, nil); err != nil {
		t.Errorf("null result: %v", err)
	}
}

func TestCancelRequest(t *testing.T) {
	cancelled := make(chan struct{})
	a, _ := pair(t, nil, func(ctx context.Context, method string, params jsontext.Value, notify bool) (any, error) {
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := a.Call(ctx, "slow", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("handler was not cancelled")
	}
}

// syncBuffer is a goroutine-safe bytes.Buffer.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestFramingAndStrayOutput(t *testing.T) {
	var notes []string
	var logs, out syncBuffer
	in := strings.Join([]string{
		"hello from print()",
		`{"jsonrpc":"2.0","method":"note","params":"a"}` + "\r",
		"",
		`{broken`,
		`{"jsonrpc":"2.0","method":"note","params":"b"}`,
		`{"jsonrpc":"2.0","id":"x1","method":"echo","params":"é"}`,
	}, "\n")
	done := make(chan struct{})
	c := NewConn(strings.NewReader(in), &out, func(ctx context.Context, method string, params jsontext.Value, notify bool) (any, error) {
		if notify {
			notes = append(notes, string(params))
			return nil, nil
		}
		defer close(done)
		return params, nil
	}, slog.New(slog.NewTextHandler(&logs, nil)))
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	<-done
	if strings.Join(notes, ",") != `"a","b"` {
		t.Fatalf("notifications = %v", notes)
	}
	if n := strings.Count(logs.String(), "non-protocol output"); n != 2 {
		t.Fatalf("%d warnings, want 2:\n%s", n, logs.String())
	}
	eventually := time.Now().Add(5 * time.Second)
	for !strings.HasSuffix(out.String(), "\n") && time.Now().Before(eventually) {
		time.Sleep(5 * time.Millisecond)
	}
	var resp struct {
		ID     string `json:"id"`
		Result string `json:"result"`
	}
	line := strings.TrimSpace(out.String())
	if strings.Count(line, "\n") != 0 || json.Unmarshal([]byte(line), &resp) != nil || resp.ID != "x1" || resp.Result != "é" {
		t.Fatalf("response = %q", out.String())
	}
}

func TestCallFailsWhenClosed(t *testing.T) {
	r, w := io.Pipe()
	c := NewConn(r, io.Discard, nil, nil)
	go c.Run()
	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "x", nil, nil) }()
	time.Sleep(20 * time.Millisecond)
	w.Close()
	if err := <-errc; !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v", err)
	}
	if err := c.Call(context.Background(), "x", nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("after close: %v", err)
	}
}
