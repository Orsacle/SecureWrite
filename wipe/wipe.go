package wipe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const bufSize = 1 << 20

var ErrNoShrink = errors.New("free space is not shrinking; the filesystem likely compresses or deduplicates writes")

type Options struct {
	Dir       string
	ChunkSize int64
	Reserve   int64
	Patterns  []Pattern
	Verify    bool
	Progress  io.Writer // nil disables progress output
}

type chunk struct {
	path  string
	index int
	size  int64
}

type wiper struct {
	opts   Options
	gen    *generator
	dir    string
	buf    []byte
	chunks []chunk
}

func CheckDir(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	probe, err := os.MkdirTemp(dir, ".securewrite-probe-")
	if err != nil {
		return fmt.Errorf("%s is not writable: %w", dir, err)
	}
	return os.Remove(probe)
}

func Run(ctx context.Context, o Options) (err error) {
	if o.ChunkSize <= 0 {
		return fmt.Errorf("chunk size must be positive")
	}
	gen, err := newGenerator()
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(o.Dir, ".securewrite-")
	if err != nil {
		return err
	}
	w := &wiper{opts: o, gen: gen, dir: tmp, buf: make([]byte, bufSize)}
	defer func() {
		cerr := w.removeChunks()
		if rerr := os.Remove(tmp); cerr == nil {
			cerr = rerr
		}
		if err == nil && cerr != nil {
			err = fmt.Errorf("cleanup: %w", cerr)
		}
	}()

	for pass, p := range o.Patterns {
		if err := w.pass(ctx, pass, p); err != nil {
			return err
		}
	}
	return nil
}

func (w *wiper) pass(ctx context.Context, pass int, p Pattern) error {
	_, free, err := Space(w.dir)
	if err != nil {
		return err
	}
	label := fmt.Sprintf("pass %d/%d (%s)", pass+1, len(w.opts.Patterns), p)
	prog := newProgress(w.opts.Progress, label, max(int64(free)-w.opts.Reserve, 0))

	var done int64
	for i := 0; ; i++ {
		_, free, err := Space(w.dir)
		if err != nil {
			return err
		}
		budget := int64(free) - w.opts.Reserve
		if budget <= 0 {
			break
		}
		n, full, err := w.writeChunk(ctx, pass, i, p, min(w.opts.ChunkSize, budget), func(n int64) { prog.update(done + n) })
		done += n
		if err != nil {
			return err
		}
		if full {
			break
		}
		_, after, err := Space(w.dir)
		if err != nil {
			return err
		}
		if int64(free)-int64(after) < n/2 {
			return ErrNoShrink
		}
	}
	prog.finish(done)

	if w.opts.Verify && len(w.chunks) > 0 {
		if err := w.verify(pass, p); err != nil {
			return err
		}
	}
	return w.removeChunks()
}

func (w *wiper) writeChunk(ctx context.Context, pass, idx int, p Pattern, size int64, report func(int64)) (written int64, full bool, err error) {
	path := filepath.Join(w.dir, fmt.Sprintf("chunk-%06d", idx))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if isNoSpace(err) {
			return 0, true, nil
		}
		return 0, false, err
	}
	w.chunks = append(w.chunks, chunk{path: path, index: idx})

	fill := func(b []byte, off int64) { w.gen.fill(b, p, pass, idx, off) }
	written, err = writeFull(ctx, f, w.buf, size, fill, report)
	if isNoSpace(err) {
		full, err = true, nil
	}
	if serr := f.Sync(); serr != nil && err == nil {
		if isNoSpace(serr) {
			full = true
		} else {
			err = serr
		}
	}
	if cerr := f.Close(); cerr != nil && err == nil {
		err = cerr
	}
	w.chunks[len(w.chunks)-1].size = written
	return written, full, err
}

func writeFull(ctx context.Context, w io.Writer, buf []byte, size int64, fill func([]byte, int64), report func(int64)) (int64, error) {
	var off int64
	for off < size {
		if err := ctx.Err(); err != nil {
			return off, err
		}
		b := buf[:min(int64(len(buf)), size-off)]
		fill(b, off)
		n, err := w.Write(b)
		off += int64(n)
		report(off)
		if err != nil {
			return off, err
		}
	}
	return off, nil
}

func (w *wiper) verify(pass int, p Pattern) error {
	got := make([]byte, bufSize)
	for _, c := range w.chunks {
		if c.size == 0 {
			continue
		}
		f, err := os.Open(c.path)
		if err != nil {
			return err
		}
		for _, off := range sampleOffsets(c.size) {
			n := min(int64(bufSize), c.size-off)
			if _, err := f.ReadAt(got[:n], off); err != nil {
				f.Close()
				return fmt.Errorf("verify %s: %w", c.path, err)
			}
			w.gen.fill(w.buf[:n], p, pass, c.index, off)
			if !bytes.Equal(got[:n], w.buf[:n]) {
				f.Close()
				return fmt.Errorf("verify %s: data mismatch near offset %d", c.path, off)
			}
		}
		f.Close()
	}
	return nil
}

func sampleOffsets(size int64) []int64 {
	last := (size - 1) / bufSize * bufSize
	mid := last / 2 / bufSize * bufSize
	offs := []int64{0}
	for _, o := range []int64{mid, last} {
		if o != offs[len(offs)-1] {
			offs = append(offs, o)
		}
	}
	return offs
}

func (w *wiper) removeChunks() error {
	var first error
	for _, c := range w.chunks {
		if err := os.Remove(c.path); err != nil && !errors.Is(err, fs.ErrNotExist) && first == nil {
			first = err
		}
	}
	w.chunks = w.chunks[:0]
	return first
}
