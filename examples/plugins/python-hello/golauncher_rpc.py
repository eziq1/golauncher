"""A minimal implementation of the golauncher external plugin protocol.

JSON-RPC 2.0 over stdin/stdout, one JSON object per line (see
docs/plugin-protocol.md). Standard library only. Log to stderr, never print
to stdout: stdout carries the protocol.
"""
import json
import os
import sys


class Plugin:
    """Subclass and override the methods you support; register them in
    `capabilities`. Call `self.call(...)` for host APIs."""

    capabilities = {}

    def __init__(self):
        sys.stdin.reconfigure(encoding="utf-8")
        sys.stdout.reconfigure(encoding="utf-8", newline="\n")
        self._next_id = 0
        self._queue = []  # messages read while waiting for a response
        self.locale = "en"
        self.preferences = {}
        self._messages = {}

    # --- protocol plumbing -------------------------------------------------
    def _send(self, msg):
        msg["jsonrpc"] = "2.0"
        sys.stdout.write(json.dumps(msg, ensure_ascii=False) + "\n")
        sys.stdout.flush()

    def _read(self):
        if self._queue:
            return self._queue.pop(0)
        while True:
            line = sys.stdin.readline()
            if not line:
                return None
            if line.strip():
                return json.loads(line)

    def call(self, method, params=None):
        """Calls a host method (e.g. "host/storage.get") and returns its result."""
        self._next_id += 1
        rid = "py%d" % self._next_id
        self._send({"id": rid, "method": method, "params": params or {}})
        while True:
            msg = self._read()
            if msg is None:
                raise EOFError("host closed the connection")
            if "method" not in msg and msg.get("id") == rid:
                if "error" in msg:
                    raise RuntimeError(msg["error"]["message"])
                return msg.get("result")
            self._queue.append(msg)

    def notify(self, method, params):
        self._send({"method": method, "params": params})

    def run(self):
        while True:
            msg = self._read()
            if msg is None:
                return
            method, params = msg.get("method"), msg.get("params") or {}
            if method is None:
                continue  # a late response
            try:
                result = self._dispatch(method, params)
                error = None
            except Exception as e:  # report instead of crashing
                print("error in %s: %r" % (method, e), file=sys.stderr)
                result, error = None, {"code": -32603, "message": str(e)}
            if "id" in msg:
                reply = {"id": msg["id"]}
                if error:
                    reply["error"] = error
                else:
                    reply["result"] = result
                self._send(reply)
            if method == "shutdown":
                return

    def _dispatch(self, method, params):
        if method == "initialize":
            self.locale = params.get("locale", "en")
            self.preferences = params.get("preferences") or {}
            return {"protocol": 1, "capabilities": self.capabilities}
        if method == "localeChanged":
            self.locale = params["locale"]
            return None
        if method == "preferencesChanged":
            self.preferences = params["preferences"]
            return None
        handler = {
            "rootItems": lambda: self.root_items(params["query"]),
            "view": lambda: self.view(params["request"]),
            "handleAction": lambda: self.handle_action(params["action"]),
            "shutdown": lambda: None,
        }.get(method)
        if handler is None:
            if method.startswith("$/") or "/" in method or method in ("event", "viewClosed"):
                return None
            raise RuntimeError("method not found: " + method)
        return handler()

    # --- helpers ---------------------------------------------------------------
    def t(self, key):
        """Translates key with locales/<locale>.json, falling back to en.json."""
        for loc in (self.locale, "en"):
            if loc not in self._messages:
                path = os.path.join("locales", loc + ".json")
                try:
                    with open(path, encoding="utf-8") as f:
                        self._messages[loc] = json.load(f)
                except OSError:
                    self._messages[loc] = {}
            if key in self._messages[loc]:
                return self._messages[loc][key]
        return key

    def root_items(self, query):
        return []

    def view(self, request):
        raise RuntimeError("no views")

    def handle_action(self, action):
        raise RuntimeError("no actions")
