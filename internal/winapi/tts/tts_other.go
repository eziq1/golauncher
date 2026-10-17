//go:build !windows && !darwin

package tts

import "context"

type unsupported struct{}

// New returns the system's speaker: SAPI on Windows, the `say` command on macOS; elsewhere one returning ErrUnsupported.
func New() Speaker { return unsupported{} }

func (unsupported) Speak(context.Context, string, string) error { return ErrUnsupported }
