// Package logs keeps the launch log and earlier Client.log files of the mod.
package logs

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	keepClientLogs = 10
)

// Launch returns the launch log, which opens on the first write and rotates at 1 MB.
func Launch(dir string) io.Writer {
	return &lumberjack.Logger{Filename: filepath.Join(dir, "launch.log"), MaxSize: 1, MaxBackups: 1}
}

// ArchiveClientLog keeps earlier Client.log files, because the mod replaces it on every start.
func ArchiveClientLog(prefixDir, dir string) error {
	src := filepath.Join(prefixDir, "drive_c", "ProgramData", "TrucklineMP", "Client.log")
	fi, err := os.Stat(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(dir, "Client-"+fi.ModTime().Format("20060102-150405")+".log")
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		return err
	}

	// The names sort by time, and Glob returns them sorted.
	old, err := filepath.Glob(filepath.Join(dir, "Client-*.log"))
	if err != nil {
		return err
	}
	var errs []error
	for ; len(old) > keepClientLogs; old = old[1:] {
		if err := os.Remove(old[0]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
