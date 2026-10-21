package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// ScanResult is what ScanBackupTarget found.
type ScanResult struct {
	// Adopted are the backups added to the history, Orphans of them have no
	// service here with the same project, name and kind (restore them into
	// one picked by hand).
	Adopted []store.Backup `json:"adopted"`
	Orphans int            `json:"orphans"`
	// Known backups were already in the history.
	Known int `json:"known"`
	// NoRecord counts files without a sidecar: older backups, manager
	// snapshots and anything else stored there. They're left alone.
	NoRecord int `json:"noRecord"`
	// Unreadable are sidecars that couldn't be used, with why.
	Unreadable []ScanProblem `json:"unreadable"`
}

type ScanProblem struct {
	Key   string `json:"key"`
	Error string `json:"error"`
}

// ScanBackupTarget adopts the backups another manager left on a target
// (moved project, rebuilt server): each one with a sidecar this manager has
// no record of is added to the history, attached to the service of the same
// project and name when there is one. Adopted backups belong to no
// schedule, so retention never prunes them.
func (c *Core) ScanBackupTarget(ctx context.Context, id string) (ScanResult, error) {
	target, err := c.store.GetBackupTarget(ctx, id)
	if err != nil {
		return ScanResult{}, err
	}
	st, err := c.openStorage(target)
	if err != nil {
		return ScanResult{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// shortcut: holds the whole listing in memory; a few hundred bytes per
	// object, so fine until targets hold hundreds of thousands of them.
	sizes := map[string]int64{}
	for obj, err := range st.List(ctx, "") {
		if err != nil {
			return ScanResult{}, fmt.Errorf("%w: cannot list %s: %v", ErrInvalid, target.Name, err)
		}
		sizes[obj.Key] = obj.Size
	}
	known := map[string]bool{}
	all, err := c.store.ListBackups(ctx, store.BackupFilter{})
	if err != nil {
		return ScanResult{}, err
	}
	for _, b := range all {
		if b.TargetID == target.ID {
			known[b.ObjectKey] = true
		}
	}
	services, err := c.servicesByName(ctx)
	if err != nil {
		return ScanResult{}, err
	}

	res := ScanResult{Adopted: []store.Backup{}, Unreadable: []ScanProblem{}}
	for key := range sizes {
		object, isSidecar := strings.CutSuffix(key, sidecarExt)
		if !isSidecar {
			if _, ok := sizes[key+sidecarExt]; !ok && !known[key] {
				res.NoRecord++
			}
			continue
		}
		if _, ok := sizes[object]; !ok {
			continue // a sidecar left behind by a deleted backup
		}
		if known[object] {
			res.Known++
			continue
		}
		if strings.HasSuffix(object, ".age") && !target.Encrypted() {
			res.Unreadable = append(res.Unreadable, ScanProblem{Key: key,
				Error: "encrypted: encrypt this storage with the key of the manager that made it first"})
			continue
		}
		b, err := readSidecar(ctx, st, key, target.AgeIdentity)
		if err == nil && b.Status != store.OpSucceeded {
			err = fmt.Errorf("backup did not succeed")
		}
		if err == nil && b.SizeBytes != sizes[object] {
			err = fmt.Errorf("backup is %d bytes, its record says %d", sizes[object], b.SizeBytes)
		}
		if err != nil {
			res.Unreadable = append(res.Unreadable, ScanProblem{Key: key, Error: err.Error()})
			continue
		}
		// The other manager's key may sit under another prefix: keep ours.
		b.ObjectKey, b.TargetID = object, target.ID
		if b.FinishedAt == nil {
			f := b.CreatedAt.Add(time.Duration(b.DurationMS) * time.Millisecond)
			b.FinishedAt = &f
		}
		if svc, ok := services[b.ProjectName+"/"+b.ServiceName]; ok && backupFits(svc, b) {
			b.ServiceID, b.ProjectID = svc.ID, svc.ProjectID
		} else {
			res.Orphans++
		}
		if b, err = c.store.CreateBackup(ctx, b); err != nil {
			return res, err
		}
		res.Adopted = append(res.Adopted, b)
	}
	return res, nil
}

// servicesByName indexes every service as project/service.
func (c *Core) servicesByName(ctx context.Context) (map[string]store.Service, error) {
	projects, err := c.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, p := range projects {
		names[p.ID] = p.Name
	}
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]store.Service{}
	for _, s := range svcs {
		out[names[s.ProjectID]+"/"+s.Name] = s
	}
	return out, nil
}

// backupFits reports whether b restores into svc.
func backupFits(svc store.Service, b store.Backup) bool {
	kind, err := backupKind(svc)
	return err == nil && kind == b.Kind && (kind != store.BackupKindDump || svc.Kind == b.ServiceKind)
}
