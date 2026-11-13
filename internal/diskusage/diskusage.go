// Package diskusage reports the size and the free space of the disk that holds a folder (the data
// volume of the server: database and screenshots).
package diskusage

import "errors"

// Usage of a disk, in bytes. Free is what the server's user can still write.
type Usage struct {
	Total uint64 `json:"total"`
	Free  uint64 `json:"free"`
}

// UsedPercent is the share of the disk in use (0-100).
func (u Usage) UsedPercent() float64 {
	if u.Total == 0 {
		return 0
	}
	return float64(u.Total-min(u.Free, u.Total)) * 100 / float64(u.Total)
}

// ErrUnsupported: this system cannot tell the disk's free space.
var ErrUnsupported = errors.New("disk usage is not available on this system")
