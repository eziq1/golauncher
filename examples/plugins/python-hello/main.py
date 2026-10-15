"""Hello (Python): an example golauncher external plugin.

A root item opens a form; submitting it copies a greeting to the clipboard
(host/clipboard.writeText) and remembers the name (host/storage).
"""
from golauncher_rpc import Plugin


class Hello(Plugin):
    capabilities = {"rootItems": True, "views": True, "actions": True}

    def last_name(self):
        r = self.call("host/storage.get", {"key": "lastName"})
        return r["value"] if r and r.get("found") else ""

    def root_items(self, query):
        last = self.last_name()
        return [{
            "id": "greet",
            "title": self.t("root.title"),
            "subtitle": self.t("root.subtitleLast").replace("{name}", last) if last else "",
            "keywords": ["hello", "python"],
            "group": self.t("group"),
            "actions": [{
                "id": "open",
                "title": self.t("action.open"),
                "do": {"kind": "Push", "target": "greet"},
            }],
        }]

    def view(self, request):
        return {
            "kind": "form",
            "title": self.t("form.title"),
            "form": {
                "fields": [
                    {"id": "name", "type": "text", "title": self.t("form.name"),
                     "placeholder": self.t("form.namePlaceholder"), "required": True,
                     "default": self.last_name()},
                    {"id": "excited", "type": "checkbox", "title": self.t("form.excited")},
                ],
                "submit": {"id": "submit", "title": self.t("form.submit"),
                           "do": {"kind": "Callback", "target": "submit"}},
            },
        }

    def handle_action(self, action):
        if action.get("target") != "submit":
            raise RuntimeError("unknown action")
        values = action.get("values") or {}
        name = (values.get("name") or "").strip()
        if not name:
            return {"fieldErrors": {"name": self.t("form.nameRequired")}}
        text = self.t("greeting").replace("{name}", name)
        if values.get("excited"):
            text = text.rstrip(".!") + "!!!"
        self.call("host/clipboard.writeText", {"text": text})
        self.call("host/storage.set", {"key": "lastName", "value": name})
        return {
            "toast": {"style": "success", "title": self.t("toast.copied"), "message": text},
            "popToRoot": True,
        }


if __name__ == "__main__":
    Hello().run()
