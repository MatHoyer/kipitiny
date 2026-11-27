package data

func verifyKey(backupID string) string   { return "verify/" + backupID }
func backupKey(backupID string) string   { return "backup/" + backupID }
func restoreKey(restoreID string) string { return "restore/" + restoreID }

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
