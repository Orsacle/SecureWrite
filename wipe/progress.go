package wipe

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

type progress struct {
	w           io.Writer
	label       string
	total       int64
	start, last time.Time
}

func newProgress(w io.Writer, label string, total int64) *progress {
	now := time.Now()
	return &progress{w: w, label: label, total: total, start: now, last: now}
}

func (p *progress) update(done int64) {
	if p.w == nil {
		return
	}
	now := time.Now()
	if now.Sub(p.last) < time.Second {
		return
	}
	p.last = now
	p.print(done, now)
}

func (p *progress) finish(done int64) {
	if p.w == nil {
		return
	}
	p.print(done, time.Now())
	fmt.Fprintln(p.w)
}

func (p *progress) print(done int64, now time.Time) {
	rate := float64(done) / max(now.Sub(p.start).Seconds(), 1e-9)
	eta := "--"
	if rate > 0 && p.total > done {
		eta = time.Duration(float64(p.total-done) / rate * float64(time.Second)).Round(time.Second).String()
	}
	fmt.Fprintf(p.w, "\r%s: %s / %s  %s/s  ETA %s   ",
		p.label, FormatBytes(done), FormatBytes(p.total), FormatBytes(int64(rate)), eta)
}

var units = []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}

func FormatBytes(n int64) string {
	f, i := float64(n), 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

func ParseSize(s string) (int64, error) {
	t := strings.ToUpper(strings.TrimSpace(s))
	t = strings.TrimSuffix(strings.TrimSuffix(t, "B"), "I")
	mult := int64(1)
	if i := strings.IndexAny(t, "KMGT"); i >= 0 && i == len(t)-1 {
		mult = int64(1) << (10 * (strings.IndexByte("KMGT", t[i]) + 1))
		t = t[:i]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
	if err != nil || n < 0 || n > (1<<63-1)/mult {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return n * mult, nil
}
