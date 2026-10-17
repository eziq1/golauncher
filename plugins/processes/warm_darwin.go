package processes

import "github.com/eziq1/golauncher/internal/winapi/process"

// warm starts NSWorkspace in the background at Init, so that the first
// "ps " is not slowed by it (process.Warm).
func warm() { process.Warm() }
