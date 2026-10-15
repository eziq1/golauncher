# @raycast.schemaVersion 1
# @raycast.title Greet
# @raycast.mode compact
# @raycast.packageName Examples
# @raycast.icon 👋
# @raycast.argument1 { "type": "text", "placeholder": "Name", "optional": true }
# @raycast.description Says hello.

param([string]$Name)
if (-not $Name) { $Name = "World" }
Write-Output "Hello, $Name!"
