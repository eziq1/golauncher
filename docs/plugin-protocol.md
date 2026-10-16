# golauncher external plugin protocol (version 1)

This is the wire protocol between golauncher and an external plugin: a
program in any language that runs in its own process. Implement it to write
a plugin without the Go SDK. Go plugins can use
[`pkg/plugin/rpcserve`](../pkg/plugin/rpcserve/) instead, and
[`examples/plugins/python-hello`](../examples/plugins/python-hello/) has a
complete implementation in about 130 lines of standard-library Python
(`golauncher_rpc.py`, comments included).

The message types are defined in Go in
[`pkg/plugin/protocol`](../pkg/plugin/protocol/protocol.go); the data types
(`Item`, `View`, `ActionDo`, `Result`, …) are the JSON form of
[`pkg/plugin`](../pkg/plugin/types.go), described in the
[plugin guide](plugin-guide.md).

## 1. Installation and manifest

A plugin is a directory with a `plugin.json` manifest:

```json
{
  "id": "example.hello-python",
  "name": "%plugin.name%",
  "description": "%plugin.description%",
  "version": "0.1.0",
  "icon": { "name": "command" },
  "protocol": 1,
  "run": { "command": "python", "args": ["-u", "main.py"] },
  "permissions": ["storage", "clipboard"],
  "commands": [
    { "name": "greet", "title": "%command.greet.title%", "mode": "view" }
  ],
  "preferences": [
    { "name": "greeting", "type": "text", "title": "%pref.greeting%", "default": "Hello" }
  ]
}
```

- All fields except `protocol` and `run` are those of `plugin.Manifest`.
- `protocol` is the protocol version, `1`.
- `run.command` is either a path relative to the plugin directory
  (`hello.exe`; `.exe` may be left out) or a program on `PATH` (`python`,
  `node`). The process starts with the plugin directory as its working
  directory and these extra environment variables: `GOLAUNCHER=1`,
  `GOLAUNCHER_PLUGIN_ID`, `GOLAUNCHER_PROTOCOL`, `PYTHONUNBUFFERED=1`,
  `PYTHONIOENCODING=utf-8`.
- Strings written as `%key%` are translated by the host from
  `locales/<lang>.json` (`en.json` is required when you use keys). A catalog
  maps keys to strings or to plural forms
  (`{"one": "…", "other": "{{.Count}} …"}`).

golauncher loads plugins from `%APPDATA%\golauncher\plugins\<dir>\` and from
the directories in `GOLAUNCHER_PLUGIN_PATH` (separated by `;`). A directory
on that path may be a plugin directory itself, which is handy during
development. `glctl plugin validate <dir>` checks a plugin without starting
it, and `glctl query … --plugins <dir>` runs it in an isolated environment.

## 2. Transport

- JSON-RPC 2.0 over the process's **stdin** (host → plugin) and **stdout**
  (plugin → host).
- **Framing**: one JSON object per line, UTF-8, terminated by `\n` (a
  trailing `\r` is ignored). Do not pretty-print.
- Lines on stdout that are not JSON objects are logged by the host as
  warnings and otherwise ignored, but keep stdout for the protocol.
- **stderr** is free-form: each line is written to the golauncher log with
  the plugin ID. Use it for logging.
- Both sides send requests, responses and notifications. Request IDs may be
  numbers or strings. Requests may be answered out of order, and the host
  serves the plugin's requests concurrently, so a plugin may call the host
  (for example `host/storage.get`) while it is handling a host request.

## 3. Lifecycle

1. The host starts the process **lazily**, on the first call that needs it,
   and sends `initialize`. Calls arriving meanwhile wait.
2. The plugin answers `initialize` within **5 seconds** with its protocol
   version and capabilities. A different protocol version makes the plugin
   unavailable.
3. Requests follow. The host gives up on a request after the time the user
   waits for it: **150 ms** for `rootItems` and `contextItems`, **10 s** for `view`, **30 s**
   for `handleAction`, and sends `$/cancelRequest`.
4. After **5 minutes** without calls and with no open views, the host sends
   `shutdown`, closes stdin, and kills the process if it has not exited
   within **2 seconds**. Exit when stdin closes.

Failures:

| Situation | What the host does |
|-----------|--------------------|
| The process exits unexpectedly | Pending calls fail ("the plugin crashed"). The next call restarts it after a backoff of 1 s, 2 s, 4 s, … up to 30 s. |
| 5 crashes within 2 minutes | The plugin is turned off; Settings shows why and offers to turn it on again. |
| 3 consecutive calls time out | The process counts as hung: it is killed, which counts as a crash. |
| `initialize` fails with code `-32002` | The plugin is unavailable; the error message is shown as the reason (e.g. "Everything is not installed"). |
| `run.command` cannot be found | The plugin is unavailable ("Cannot find python…"). |

On Windows the process and everything it starts belong to a Job Object
that is closed with golauncher, so plugin processes never outlive it, and
they are created without a console window.

## 4. Host → plugin requests

| Method | Params | Result |
|--------|--------|--------|
| `initialize` | `{protocol, hostVersion, pluginId, locale, dataDir, cacheDir, preferences}` | `{protocol: 1, capabilities: {rootItems, views, actions, preferenceOptions, render, contextItems}}` |
| `rootItems` | `{query, raw?, context?}` (`raw`: the text as typed, untrimmed) | `Item[]` |
| `contextItems` | `{context: Context}` | `Item[]` |
| `view` | `{request: ViewRequest}` | `View` |
| `handleAction` | `{action: ActionDo}` | `Result` |
| `preferenceOptions` | `{name}` | `Option[]` |
| `shutdown` | `{}` | `null` |

- `preferences` maps each declared preference to its effective value (the
  user's value or the default), including password preferences.
- `dataDir` and `cacheDir` are private directories the plugin may write to.
- `capabilities` are booleans; the host does not call methods whose
  capability is false or missing. A plugin without `rootItems` is not
  started by typing in the root search.
- `rootItems` is called for every keystroke in the root search and must be
  fast. Items without a `score` are fuzzy-matched by the host. `context` is
  the context of the current show (see below), left out when there is none.
- `contextItems` is called for every root search while the launcher shows
  with a context: what the user was doing in the window that was active when
  it opened. `Context` is `{app: {name, path?, pid?, package?}, kind?, folder?,
  selected?, title?, extra?}`; `kind` is `explorer`, `desktop` or
  `path-in-title` (`folder` and `selected` are absolute paths), or missing
  when only the app and the title are known. Return the items to show in the
  "In <folder>" section at the top of the root (`[]` when nothing applies);
  they are also searchable. It must be fast (150 ms, like `rootItems`). Until
  a plugin has been initialized once the host does not know its
  capabilities, so the first show with a context starts it; afterwards a
  plugin without `contextItems` is not called. The context is never stored;
  put what an action needs into the action.
- `view` is called when a view opens and again for every change of the
  search text, unless the view sets `filterLocally`.
  `request.instance` identifies the open view for `ui/render`.
- `handleAction` runs `RunCommand` actions (no-view commands) and
  `Callback` actions. For a form submit, `action.values` holds the field
  values; for an item or command with arguments, the argument values. A
  `Result` may carry `do`, an action the host carries out afterwards (one of
  the host's own kinds, e.g. `{"kind": "ShowTask", "target": "<task id>"}`
  to open a task started with `host/task.runProcess`).
- Optional fields added to the data types travel in the same JSON, with no
  new capability or protocol version; an older host ignores them. For
  example an `Item` may carry `"accessories": [{"text": "3.2%", "meter":
  0.032, "load": true}]` (values at the right edge with a small bar) and
  `"subtitleSensitive": true` (a subtitle never given to an AI), a
  `Metadata` row `"sensitive": true` (a detail row never given to an AI),
  and an `Action` `"confirmMessage": "3 processes will be ended."` (the text
  of its confirmation, also shown when an AI asks to run it).

## 5. Host → plugin notifications

| Method | Params | Meaning |
|--------|--------|---------|
| `$/cancelRequest` | `{id}` | The host no longer waits for request `id`. Stop working on it; a response is ignored. |
| `localeChanged` | `{locale}` | The UI language changed. Strings you return should follow it. |
| `preferencesChanged` | `{preferences}` | The user changed preferences (same form as in `initialize`). |
| `event` | `{name}` | A host event you subscribed to with `host/subscribe`. |
| `viewClosed` | `{instance}` | The view instance was closed; release its state. |
| `taskFinished` | `{result: {id, state, exitCode, error?, lastLine?}}` | A background task you started with `host/task.runProcess` ended. `state` is `succeeded`, `failed` or `cancelled`. Sent only to the process that is running then: a plugin process that was restarted or stopped while idle misses it. |

## 6. Plugin → host requests

| Method | Params | Result | Permission |
|--------|--------|--------|------------|
| `host/storage.get` | `{key}` | `{found, value?, base64?}` | `storage` |
| `host/storage.set` | `{key, value}` or `{key, base64}` | `null` | `storage` |
| `host/storage.delete` | `{key}` | `null` | `storage` |
| `host/clipboard.read` | `{}` | `{text?, image?, files?, sourceApp?, exclude?}` | `clipboard` |
| `host/clipboard.writeText` | `{text, secret?}`（`secret: true` 时不记入剪贴板历史，用于密码） | `null` | `clipboard` |
| `host/clipboard.writeImage` | `{png}` (base64) | `null` | `clipboard` |
| `host/clipboard.writeFiles` | `{paths}` | `null` | `clipboard` |
| `host/subscribe` | `{event}` | `null` | — |
| `host/log` | `{level, message}` | `null` | — |
| `host/task.runProcess` | `{spec: TaskSpec, cmd: ProcessSpec}` | `{id}` | `exec` |
| `host/toast` | `{toast: {style, title, message?, silent?}}` | `null` | — |

- Storage is a private key/value store. Text values travel in `value`;
  values that are not valid UTF-8 travel base64-encoded in `base64`.
- `image` in a clipboard read is a base64 PNG.
- Events: `clipboard-changed`, `window-shown`, `window-hidden`, `locale-changed`,
  `config-changed` (the last two also arrive as `localeChanged` and
  `preferencesChanged`).
- `level` is `debug`, `info`, `warn` or `error`. `host/log` may also be
  sent as a notification.
- `host/task.runProcess` queues a **background task** of the host
  ([docs/background-tasks.md](background-tasks.md)) and returns its ID at
  once. `spec` is `{title, subtitle?, icon?, queue?, notify?}`: tasks with
  the same non-empty `queue` run one after the other (also across
  plugins), others in parallel (at most 4 at once); `notify` is `""`
  (notify on failure, the default), `"always"` or `"never"`. `cmd` is
  `{argv, dir?, env?, commandLine?}`: the program runs without a window in
  a Job Object, `env` entries (`NAME=value`) are added to the host's
  environment, and `commandLine` is what the user sees instead of `argv`.
  Its output appears live in Background Tasks (`Ctrl+J`); the outcome comes
  back as the `taskFinished` notification. Tasks run in Go code (the SDK's
  `Host.RunTask`) have no protocol method: `rpcserve` runs them in the
  plugin process, unseen by the launcher.
- `host/toast` shows a message in the launcher's footer when it is visible,
  else as a Windows notification. It may also be sent as a notification.
- The event `task-finished` (no payload) is published after every
  background task, of any plugin; prefer `taskFinished` for your own tasks.
- Calling a method whose permission the manifest does not declare fails
  with code `-32001`. The host enforces `exec` for its own process API
  (`host/task.runProcess`), but neither `network` nor `exec` can be
  enforced otherwise: the plugin is an ordinary program running with the
  user's rights. Declare them anyway; Settings shows them.

## 7. Plugin → host notifications

| Method | Params | Meaning |
|--------|--------|---------|
| `host/invalidate` | `{viewId}` | Data you show changed: the host pulls your root items and your open views with this view ID (`""` for all) again. Bursts are coalesced. |
| `ui/render` | `{instance, version, view}` | Replace the content of the open view `instance` with `view`. The host keeps only renders whose `version` is greater than the last one for that instance, and drops renders for closed views. |

## 8. Errors

Error responses use the JSON-RPC error object `{code, message}`. The
`message` of an error from `view` or `handleAction` is shown to the user,
so write it for people.

| Code | Meaning |
|------|---------|
| `-32700`, `-32600`, `-32601`, `-32602`, `-32603` | JSON-RPC standard errors (parse error, invalid request, method not found, invalid params, internal error) |
| `-32001` | Permission denied (host API not declared in `permissions`) |
| `-32002` | Unavailable: from `initialize`, the plugin cannot run on this system; the message is the reason |
| `-32800` | Request cancelled (`$/cancelRequest`) |

## 9. Example exchange

```text
→ {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocol":1,"hostVersion":"0.2.0","pluginId":"example.python-hello","locale":"en","dataDir":"…","cacheDir":"…","preferences":{}}}
← {"jsonrpc":"2.0","id":1,"result":{"protocol":1,"capabilities":{"rootItems":true,"views":true,"actions":true}}}
→ {"jsonrpc":"2.0","id":2,"method":"rootItems","params":{"query":"hel"}}
← {"jsonrpc":"2.0","id":"py1","method":"host/storage.get","params":{"key":"lastName"}}
→ {"jsonrpc":"2.0","id":"py1","result":{"found":false}}
← {"jsonrpc":"2.0","id":2,"result":[{"id":"greet","title":"Say Hello from Python","actions":[{"id":"open","title":"Open Form","do":{"kind":"Push","target":"greet"}}]}]}
→ {"jsonrpc":"2.0","id":3,"method":"handleAction","params":{"action":{"kind":"Callback","target":"submit","values":{"name":"Ann"}}}}
← {"jsonrpc":"2.0","id":"py2","method":"host/clipboard.writeText","params":{"text":"Hello, Ann."}}
→ {"jsonrpc":"2.0","id":"py2","result":null}
← {"jsonrpc":"2.0","id":3,"result":{"toast":{"style":"success","title":"Greeting copied"},"popToRoot":true}}
→ {"jsonrpc":"2.0","id":4,"method":"shutdown","params":{}}
← {"jsonrpc":"2.0","id":4,"result":null}
```
