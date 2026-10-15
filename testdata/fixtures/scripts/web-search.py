#!/usr/bin/env python3
# @raycast.schemaVersion 1
# @raycast.title Search the Web
# @raycast.mode silent
# @raycast.packageName Examples
# @raycast.argument1 { "type": "text", "placeholder": "Query", "percentEncoded": true }
# @raycast.argument2 { "type": "dropdown", "placeholder": "Engine", "optional": true, "data": [{"title": "DuckDuckGo", "value": "https://duckduckgo.com/?q="}, {"title": "Bing", "value": "https://www.bing.com/search?q="}] }

import sys
import webbrowser

engine = sys.argv[2] if len(sys.argv) > 2 and sys.argv[2] else "https://duckduckgo.com/?q="
webbrowser.open(engine + sys.argv[1])
