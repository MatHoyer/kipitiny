package core

import (
	"sync"
	"sync/atomic"
)

// opSteps holds the step each running operation (a backup, a restore, a
// restore test) is at, for the UI to show while it runs. Nothing is stored:
// a restarted manager has no running operation.
type opSteps struct {
	mu sync.Mutex
	m  map[string]*opStep
}

// opStep is an operation's current step, and the bytes it has moved so far
// when it streams.
type opStep struct {
	name  string
	bytes atomic.Int64
}

// Write counts p, so an opStep can tee a stream.
func (s *opStep) Write(p []byte) (int, error) {
	s.bytes.Add(int64(len(p)))
	return len(p), nil
}

func verifyKey(backupID string) string   { return "verify/" + backupID }
func backupKey(backupID string) string   { return "backup/" + backupID }
func restoreKey(restoreID string) string { return "restore/" + restoreID }

// set moves the operation key to step, returning it to count bytes on.
func (o *opSteps) set(key, step string) *opStep {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.m == nil {
		o.m = map[string]*opStep{}
	}
	s := &opStep{name: step}
	o.m[key] = s
	return s
}

func (o *opSteps) done(key string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.m, key)
}

// get returns the operation's step and bytes, "" when it isn't running.
func (o *opSteps) get(key string) (string, int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if s, ok := o.m[key]; ok {
		return s.name, s.bytes.Load()
	}
	return "", 0
}

// Steps of a backup, as Backup.Step reports them.
const (
	// BackupPreBackup: running the service's pre-backup command.
	BackupPreBackup = "prebackup"
	// BackupPrepare: reading the server's version and extensions.
	BackupPrepare = "prepare"
	// BackupSnapshot: taking a consistent copy of the manager's database.
	BackupSnapshot = "snapshot"
	// BackupHelper: starting the container that reads the volumes.
	BackupHelper = "helper"
	// BackupUpload: dumping, compressing, encrypting and uploading, as one
	// stream; StepBytes counts what reached the storage.
	BackupUpload = "upload"
)

// Steps of a restore, as Restore.Step reports them.
const (
	// RestoreStopApps: stopping the apps using the database.
	RestoreStopApps = "stop"
	// RestoreCheck: reading the dump through (checksum, decryption).
	RestoreCheck = "check"
	// RestoreScratch: loading it into a scratch database first.
	RestoreScratch = "scratch"
	// RestoreLoad: loading it for real (the live database, or the scratch
	// one PostgreSQL swaps in).
	RestoreLoad = "load"
	// RestoreSwap: putting the restored database in place of the live one.
	RestoreSwap = "swap"
	// RestoreExtract: extracting a volume archive into a staging volume.
	RestoreExtract = "extract"
	// RestoreStopService: stopping the service whose volumes are replaced.
	RestoreStopService = "stopservice"
	// RestoreReplace: replacing the volumes' content with the staged copy.
	RestoreReplace = "replace"
	// RestoreStart: starting what was stopped again.
	RestoreStart = "start"
)
