//go:build !windows

package wincontext

import (
	"context"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Detect reports nothing on platforms other than Windows.
func Detect(context.Context, uintptr) (plugin.Context, bool) { return plugin.Context{}, false }
