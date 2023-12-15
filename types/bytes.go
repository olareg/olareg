package types

import "bytes"

// BytesReadCloser wraps a [bytes.Reader] to make it an [io.ReadSeekCloser]
type BytesReadCloser struct {
	*bytes.Reader
}

// Close is a noop
func (brc BytesReadCloser) Close() error {
	return nil
}
