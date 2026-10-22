//go:build !windows && !darwin

package wincontext

import (
	"context"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Detect reports nothing on platforms other than Windows.
func Detect(ctx context.Context, target uintptr) (plugin.Context, bool) {
	return DetectAt(ctx, target, nil)
}

// DetectAt reports nothing either.
func DetectAt(context.Context, uintptr, *Pointer) (plugin.Context, bool) {
	return plugin.Context{}, false
}
