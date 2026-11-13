//go:build !linux && !darwin && !freebsd && !windows

package diskusage

// Of is not available on this system.
func Of(string) (Usage, error) { return Usage{}, ErrUnsupported }
