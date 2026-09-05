package unpack

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// SevenZipRunner extracts archives with a 7z-compatible binary (7-Zip,
// p7zip): zip plus rar including multi-part sets. The binary must be on PATH
// (bundled as p7zip in the container image; a documented host dependency for
// native installs: p7zip-full on Debian/Ubuntu, p7zip on Alpine, sevenzip on
// macOS Homebrew). Missing binary disables the feature with a clear message.
type SevenZipRunner struct {
	// Bin overrides binary detection with one explicit name or path.
	Bin string
	Log *slog.Logger
}

// candidateBinaries are probed in order: 7z (p7zip, 7-Zip), 7zz (Homebrew
// sevenzip v24+), 7za (standalone p7zip).
var candidateBinaries = []string{"7z", "7zz", "7za"}

// isExecutable reports whether path is an executable file (so an explicit
// Bin path works without a PATH lookup).
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

func (r *SevenZipRunner) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

// Available implements Runner.
func (r *SevenZipRunner) Available() (string, bool) {
	if r.Bin != "" {
		if path, err := exec.LookPath(r.Bin); err == nil {
			return path, true
		}
		if isExecutable(r.Bin) {
			return r.Bin, true
		}
		return "", false
	}
	for _, name := range candidateBinaries {
		if path, err := exec.LookPath(name); err == nil {
			return path, true
		}
	}
	return "", false
}

// resolvedBinary returns the concrete binary for List/Extract, honouring the
// override and the candidate probe.
func (r *SevenZipRunner) resolvedBinary() string {
	if r.Bin != "" {
		return r.Bin
	}
	for _, name := range candidateBinaries {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return candidateBinaries[0]
}

// parseSltList extracts entry paths from `7z l -slt` output. The first
// "Path = " block always describes the archive itself, so it is skipped.
func parseSltList(output string) []string {
	var out []string
	first := true
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "Path = ") {
			continue
		}
		if first {
			first = false
			continue
		}
		if path := strings.TrimSpace(strings.TrimPrefix(trimmed, "Path = ")); path != "" {
			out = append(out, path)
		}
	}
	return out
}

// List implements Runner.
func (r *SevenZipRunner) List(ctx context.Context, archive string) ([]string, error) {
	cmd := exec.CommandContext(ctx, r.resolvedBinary(), "l", "-slt", archive)
	var stdout, stderr cappedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("7z list: %w: %s", err, stderr.tail())
	}
	return parseSltList(stdout.String()), nil
}

var progressRe = regexp.MustCompile(`(\d{1,3})%`)

// Extract implements Runner. Progress percentages are parsed from 7z's
// -bsp1 progress stream; stdin stays closed so password-protected archives
// fail fast instead of prompting.
func (r *SevenZipRunner) Extract(ctx context.Context, archive, dest string, progress func(pct int)) error {
	cmd := exec.CommandContext(ctx, r.resolvedBinary(), "x", "-y", "-bsp1", "-o"+dest, archive)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr cappedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	if cmd.Process != nil {
		if err := reniceProcess(cmd.Process.Pid); err != nil {
			r.log().Debug("unpack: could not lower extractor priority", "err", err)
		}
	}
	// Drain the progress stream while 7z runs; percentages arrive as
	// "\r 12%" updates without newlines, so scan raw chunks.
	last := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		reader := bufio.NewReader(stdout)
		for {
			chunk, err := reader.ReadString('%')
			for _, m := range progressRe.FindAllStringSubmatch(chunk, -1) {
				var pct int
				fmt.Sscanf(m[1], "%d", &pct)
				if pct > 100 {
					pct = 100
				}
				if pct > last {
					last = pct
					progress(pct)
				}
			}
			if err != nil {
				return
			}
		}
	}()
	waitErr := cmd.Wait()
	<-done
	if waitErr != nil {
		return fmt.Errorf("7z extract: %w: %s", waitErr, stderr.tail())
	}
	if last < 100 {
		progress(100)
	}
	return nil
}

// cappedBuffer bounds captured subprocess output.
type cappedBuffer struct {
	buf bytes.Buffer
	cap int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.cap <= 0 {
		c.cap = 64 << 10
	}
	room := c.cap - c.buf.Len()
	if room <= 0 {
		return len(p), nil
	}
	if len(p) > room {
		p = p[:room]
	}
	return c.buf.Write(p)
}

func (c *cappedBuffer) String() string { return c.buf.String() }

func (c *cappedBuffer) tail() string {
	s := strings.TrimSpace(c.buf.String())
	if len(s) > 2048 {
		s = s[len(s)-2048:]
	}
	return s
}
