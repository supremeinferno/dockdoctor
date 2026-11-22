//go:build !linux && !darwin

package doctor

import "errors"

func diskUsage(string) (uint64, uint64, error) {
	return 0, 0, errors.New("disk usage is not supported on this platform")
}
