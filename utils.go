package imageproxy

import (
	"fmt"
	"io"
)

// LimitedReadCloser - ReadCloser with Read to Limit bytes
type LimitedReadCloser struct {
	io.ReadCloser
	N     int64
	Limit int64
}

// NewLimitedReadCloser - can be used anywhere where ReadCloser
func NewLimitedReadCloser(rc io.ReadCloser, l int64) *LimitedReadCloser {
	return &LimitedReadCloser{rc, l, l}
}

// Read - fulfill interface
func (l *LimitedReadCloser) Read(p []byte) (n int, err error) {
	if l.N <= 0 {
		return 0, fmt.Errorf("http: response body too large. Max allowed: %d bytes", l.Limit)
	}
	if int64(len(p)) > l.N {
		p = p[0:l.N]
	}
	n, err = l.ReadCloser.Read(p)
	l.N -= int64(n)
	return
}

// Close - fulfill interface
func (l *LimitedReadCloser) Close() error {
	return l.ReadCloser.Close()
}
