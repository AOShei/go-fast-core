//go:build race

package xmlscan

// raceDetector is set when the tests run under the race detector, which
// slows code ten times and more: timings mean nothing there.
const raceDetector = true
