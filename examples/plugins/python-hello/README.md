# Hello (Python)

An example external golauncher plugin written in Python 3.7+ with the
standard library only.

- `golauncher_rpc.py` implements the protocol
  ([docs/plugin-protocol.md](../../../docs/plugin-protocol.md)): JSON-RPC 2.0
  over stdin/stdout, one JSON object per line. Copy it into your own plugin.
- `main.py` is the plugin: a root item ("Say Hello from Python") opens a
  form; submitting it copies a greeting to the clipboard with
  `host/clipboard.writeText` and remembers the name with `host/storage.set`.
- `plugin.json` is the manifest; `locales/*.json` translate its `%key%`
  strings and the strings the plugin returns (`Plugin.t`).

Try it without touching your real setup (`glctl` runs an isolated
environment and only records side effects):

```powershell
glctl plugin validate examples/plugins/python-hello
glctl query "say hello" --plugins examples/plugins/python-hello --format text
glctl session new -s s.json --plugins examples/plugins/python-hello
glctl session type -s s.json "say hello"
glctl session action -s s.json example.python-hello/greet open
```

To use it in golauncher, copy the directory to
`%APPDATA%\golauncher\plugins\python-hello`, or point
`GOLAUNCHER_PLUGIN_PATH` at it, and restart golauncher. `python` must be on
`PATH`; otherwise Settings shows the plugin as unavailable.

Rules of thumb:

- Never print to stdout; it carries the protocol. Log with
  `print(..., file=sys.stderr)`: golauncher writes stderr to its log.
- `rootItems` runs on every keystroke and must answer within 150 ms.
- An exception in a handler becomes an error response; the process keeps
  running. If the process exits, golauncher restarts it on the next use.
