package agent

import "time"

// SetSettle shortens the wait for views to settle in tests.
func SetSettle(quiet time.Duration) func() {
	old := settleQuiet
	settleQuiet = quiet
	return func() { settleQuiet = old }
}

// SetHelloTimeout changes the wait for a pipe connection's hello.
func SetHelloTimeout(d time.Duration) func() {
	old := helloTimeout
	helloTimeout = d
	return func() { helloTimeout = old }
}
