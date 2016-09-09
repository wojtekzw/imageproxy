package imageproxy

import (
	"fmt"
	"io"
)

type LimitedReadCloser struct {
	io.ReadCloser
	N     int64
	Limit int64
}

func NewLimitedReadCloser(rc io.ReadCloser, l int64) *LimitedReadCloser {
	return &LimitedReadCloser{rc, l, l}
}

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

func (l *LimitedReadCloser) Close() error {
	return l.ReadCloser.Close()
}
