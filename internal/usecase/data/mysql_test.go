package data

import (
	"slices"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func mysqlService(kind domain.ServiceKind) domain.Service {
	return domain.Service{
		ID: "01SQL", ProjectID: "P1", Name: "sql", Kind: kind,
		Image: MySQLImageFor(kind), Replicas: 1, MemoryMB: 512, Env: NewMySQLEnv(),
	}
}

func mongoService() domain.Service {
	return domain.Service{
		ID: "01DOCS", ProjectID: "P1", Name: "docs", Kind: domain.ServiceKindMongoDB,
		Image: DefaultMongoImage, Replicas: 1, MemoryMB: 1024, Env: NewMongoEnv(),
	}
}

func TestMySQLDumpCmd(t *testing.T) {
	if cmd := mysqlDumpCmd(domain.ServiceKindMariaDB, "app"); cmd[0] != "mariadb-dump" || slices.Contains(cmd, "--set-gtid-purged=OFF") ||
		cmd[len(cmd)-1] != "app" || cmd[len(cmd)-2] != "--" {
		t.Errorf("mariadb = %v", cmd)
	}
	if cmd := mysqlDumpCmd(domain.ServiceKindMySQL, "app"); cmd[0] != "mysqldump" || !slices.Contains(cmd, "--set-gtid-purged=OFF") {
		t.Errorf("mysql = %v", cmd)
	}
}

// The backup and server flags of MySQL, MariaDB and MongoDB (core checks the
// container spec they go into).
func TestMySQLAndMongoBackupAndArgs(t *testing.T) {
	for _, svc := range []domain.Service{mysqlService(domain.ServiceKindMySQL), mysqlService(domain.ServiceKindMariaDB), mongoService()} {
		if k, err := backupKind(svc); k != domain.BackupKindDump || err != nil || volumeNames(svc) != nil {
			t.Errorf("%s: backed up as %q (%v), volumes %v", svc.Kind, k, err, volumeNames(svc))
		}
	}
	if cmd := mysqlArgs(512); !slices.Contains(cmd, "--innodb-buffer-pool-size=256M") {
		t.Errorf("mysql args = %v", cmd)
	}
	for mem, want := range map[int]string{512: "0.25", 1024: "0.25", 4096: "1.50"} {
		if got := mongoArgs(mem); got[1] != want {
			t.Errorf("mongo cache for %d MB = %s, want %s", mem, got[1], want)
		}
	}
}

// A MongoDB without a restart policy gets one for its first start (the
// image's init can lose the port, docker-library/mongo#730); a service's
// container keeps its own.
func TestMongoSpecRestart(t *testing.T) {
	spec := domain.ContainerSpec{Labels: map[string]string{}}
	ApplyDatabaseSpec(&spec, mongoService())
	if spec.Restart != (domain.RestartPolicy{Name: domain.RestartOnFailure, MaxRetries: 1}) {
		t.Errorf("scratch restart = %+v", spec.Restart)
	}
	spec = domain.ContainerSpec{Labels: map[string]string{}, Restart: domain.RestartPolicy{Name: domain.RestartUnlessStopped}}
	ApplyDatabaseSpec(&spec, mongoService())
	if spec.Restart.Name != domain.RestartUnlessStopped {
		t.Errorf("service restart = %+v", spec.Restart)
	}
}
