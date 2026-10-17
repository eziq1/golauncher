//go:build !windows && !darwin

package winselection

// Outside Windows there is no UI Automation: the defaults in selection.go
// describe no window, so Begin always decides methodNone and Read reports
// nothing.
