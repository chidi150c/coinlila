package util

import (
	"os"
	"path/filepath"
	"syscall"
)

// writeFileAtomic writes data to path atomically (tmp file + fsync + rename).
// On Unix it also fsyncs the parent directory to harden the rename durability.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil { return err }
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil { tmp.Close(); return err }
	if err := tmp.Sync(); err != nil { tmp.Close(); return err }
	if err := tmp.Close(); err != nil { return err }

	if err := os.Rename(tmpPath, path); err != nil { return err }

	// best-effort fsync parent dir (Unix)
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// optional advisory lock via exclusive-create
func acquirePidLock(lockPath string) (*os.File, error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil { return nil, err }
	_, _ = f.Write([]byte{})

	// mark close-on-exec
	if st := syscall.CloseOnExec(int(f.Fd())); st != nil { /* ignore */ }
	return f, nil
}

func releasePidLock(f *os.File) { if f != nil { path := f.Name(); f.Close(); _ = os.Remove(path) } }
