//go:build linux

package agent

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// watchAuthLog feeds sshd log lines into the counter until ctx ends. It
// prefers a classic log file (auth.log / secure) and falls back to journald.
func watchAuthLog(ctx context.Context, fc *failureCounter, log *slog.Logger) string {
	for _, p := range []string{"/var/log/auth.log", "/var/log/secure"} {
		if fi, err := os.Stat(p); err == nil && time.Since(fi.ModTime()) < 30*24*time.Hour {
			if f, err := os.Open(p); err == nil {
				f.Close()
				go tailFile(ctx, p, fc, log)
				return p
			}
		}
	}
	if _, err := exec.LookPath("journalctl"); err == nil {
		go followJournal(ctx, fc, log)
		return "journald"
	}
	return ""
}

func tailFile(ctx context.Context, path string, fc *failureCounter, log *slog.Logger) {
	var f *os.File
	var ino uint64
	var off int64
	open := func(seekEnd bool) {
		if f != nil {
			f.Close()
			f = nil
		}
		nf, err := os.Open(path)
		if err != nil {
			return
		}
		fi, _ := nf.Stat()
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			ino = st.Ino
		}
		if seekEnd {
			off, _ = nf.Seek(0, io.SeekEnd)
		} else {
			off = 0
		}
		f = nf
	}
	open(true)
	buf := make([]byte, 0, 64*1024)
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			if f != nil {
				f.Close()
			}
			return
		case <-t.C:
		}
		// Rotation: inode changed or file shrank.
		if fi, err := os.Stat(path); err == nil {
			st, _ := fi.Sys().(*syscall.Stat_t)
			if f == nil || (st != nil && st.Ino != ino) || fi.Size() < off {
				open(false)
			}
		}
		if f == nil {
			continue
		}
		chunk := make([]byte, 256*1024)
		for {
			n, err := f.Read(chunk)
			if n > 0 {
				off += int64(n)
				buf = append(buf, chunk[:n]...)
				for {
					i := indexByte(buf, '\n')
					if i < 0 {
						break
					}
					fc.ParseAuthLine(string(buf[:i]))
					buf = buf[i+1:]
				}
				if len(buf) > 1<<20 {
					buf = buf[:0]
				}
			}
			if err != nil || n == 0 {
				break
			}
		}
	}
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

func followJournal(ctx context.Context, fc *failureCounter, log *slog.Logger) {
	for ctx.Err() == nil {
		cmd := exec.CommandContext(ctx, "journalctl", "-f", "-n", "0", "-o", "cat", "-t", "sshd", "-t", "sshd-session", "-t", "sshd-auth")
		out, err := cmd.StdoutPipe()
		if err != nil {
			return
		}
		if err := cmd.Start(); err != nil {
			log.Warn("journalctl", "err", err)
			return
		}
		s := bufio.NewScanner(out)
		s.Buffer(make([]byte, 64*1024), 1<<20)
		for s.Scan() {
			fc.ParseAuthLine("sshd: " + s.Text())
		}
		_ = cmd.Wait()
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
	}
}

// watchWindowsSecurity is a no-op on Linux.
func watchWindowsSecurity(ctx context.Context, fc *failureCounter, log *slog.Logger) string {
	return ""
}
