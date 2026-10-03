package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"securewrite/wipe"
)

const (
	exitOK          = 0
	exitError       = 1
	exitUsage       = 2
	exitInterrupted = 130
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	fs := flag.NewFlagSet("securewrite", flag.ContinueOnError)
	passes := fs.Int("passes", 1, "number of overwrite passes")
	pattern := fs.String("pattern", "random", "zero, one, random, or dod (cycles zero, one, random)")
	chunkFlag := fs.String("chunk", "1GiB", "size of each fill file (keep below the filesystem's max file size)")
	reserveFlag := fs.String("reserve", "64MiB", "free space left untouched for other processes")
	verify := fs.Bool("verify", false, "read back samples of every chunk before deletion")
	yes := fs.Bool("yes", false, "skip confirmation prompt")
	quiet := fs.Bool("quiet", false, "suppress progress output")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: securewrite [flags] <directory>\n\nOverwrites the free space of the filesystem containing <directory>.\n\nFlags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return exitUsage
	}

	patterns, err := wipe.Patterns(*pattern, *passes)
	if err != nil {
		return usageErr(err)
	}
	chunk, err := wipe.ParseSize(*chunkFlag)
	if err != nil || chunk == 0 {
		return usageErr(fmt.Errorf("-chunk: invalid size %q", *chunkFlag))
	}
	reserve, err := wipe.ParseSize(*reserveFlag)
	if err != nil {
		return usageErr(fmt.Errorf("-reserve: %w", err))
	}
	dir := fs.Arg(0)
	if err := wipe.CheckDir(dir); err != nil {
		return usageErr(err)
	}

	total, free, err := wipe.Space(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "securewrite:", err)
		return exitError
	}
	fmt.Fprintf(os.Stderr, "Filesystem containing %s: %s total, %s free\n",
		dir, wipe.FormatBytes(int64(total)), wipe.FormatBytes(int64(free)))
	fmt.Fprintf(os.Stderr, "Will overwrite free space with %d pass(es), leaving %s in reserve.\n",
		len(patterns), wipe.FormatBytes(reserve))
	if !*yes && !confirm(os.Stdin) {
		fmt.Fprintln(os.Stderr, "Aborted.")
		return exitError
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opts := wipe.Options{Dir: dir, ChunkSize: chunk, Reserve: reserve, Patterns: patterns, Verify: *verify}
	if !*quiet {
		opts.Progress = os.Stderr
	}
	err = wipe.Run(ctx, opts)
	switch {
	case err == nil:
		fmt.Fprintln(os.Stderr, "Done. Temporary files removed.")
		return exitOK
	case errors.Is(err, context.Canceled):
		fmt.Fprintln(os.Stderr, "\nInterrupted. Temporary files removed.")
		return exitInterrupted
	default:
		fmt.Fprintln(os.Stderr, "\nsecurewrite:", err)
		return exitError
	}
}

func usageErr(err error) int {
	fmt.Fprintln(os.Stderr, "securewrite:", err)
	return exitUsage
}

func confirm(in io.Reader) bool {
	fmt.Fprint(os.Stderr, "Continue? [y/N] ")
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}
