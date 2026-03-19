package localexecutor

import (
	"io"
	"sync"
)

type synchronizedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func newSynchronizedWriter(w io.Writer) *synchronizedWriter {
	return &synchronizedWriter{w: w}
}

func (w *synchronizedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}
