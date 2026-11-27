package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
)

// toolNeed is what a member needs to call a tool: admin, or view on the
// project plus perm and feature when set.
type toolNeed struct {
	admin   bool
	perm    identity.Perm
	feature identity.Feature
}

var (
	adminOnly = toolNeed{admin: true}
	viewOnly  = toolNeed{}
)

func onProject(p identity.Perm, f ...identity.Feature) toolNeed {
	n := toolNeed{perm: p}
	if len(f) > 0 {
		n.feature = f[0]
	}
	return n
}

func feature(f identity.Feature) toolNeed { return toolNeed{feature: f} }

// toolAccess is every MCP tool and what calling it takes. A new tool fails
// TestToolAccess until it is listed here.
var toolAccess = map[string]toolNeed{
	"anonymize_now":          onProject(identity.PermEnv),
	"apply_project_compose":  onProject(identity.PermSettings),
	"backup_database":        onProject(identity.PermBackups),
	"backup_project":         onProject(identity.PermBackups),
	"close_preview":          onProject(identity.PermDeploy),
	"create_database":        onProject(identity.PermDataEdit),
	"create_network":         adminOnly,
	"create_project":         feature(identity.FeatureCreateProjects),
	"create_tracker":         adminOnly,
	"delete_backup":          onProject(identity.PermBackups),
	"delete_backup_schedule": onProject(identity.PermBackups),
	"delete_job":             onProject(identity.PermSettings),
	"delete_network":         adminOnly,
	"delete_project":         onProject(identity.PermDelete),
	"delete_service":         onProject(identity.PermDelete),
	"delete_tracker":         adminOnly,
	"delete_uptime_check":    onProject(identity.PermSettings),
	"delete_volume_paths":    onProject(identity.PermFilesEdit),
	"deploy":                 onProject(identity.PermDeploy),
	"deploy_image":           onProject(identity.PermSettings),
	"deploy_preview":         onProject(identity.PermDeploy),
	"export_analytics":       viewOnly,
	"find_documents":         onProject(identity.PermData),
	"get_analytics":          viewOnly,
	"get_app_status":         viewOnly,
	"get_audit_log":          adminOnly,
	"get_cleanup":            adminOnly,
	"get_connection":         onProject(identity.PermEnv),
	"get_deployment_log":     viewOnly,
	"get_funnel":             viewOnly,
	"get_issue":              viewOnly,
	"get_logs":               viewOnly,
	"get_manager_status":     viewOnly,
	"get_preview_seed":       viewOnly,
	"get_preview_settings":   viewOnly,
	"get_project_compose":    viewOnly,
	"get_project_git":        onProject(identity.PermGit),
	"get_redis_key":          onProject(identity.PermData),
	"get_server_status":      feature(identity.FeatureServers),
	"get_topology":           viewOnly,
	"get_visit":              viewOnly,
	"install_server_updates": adminOnly,
	"install_template":       onProject(identity.PermSettings),
	"link_project_git":       onProject(identity.PermGit),
	"list_backup_schedules":  viewOnly,
	"list_backups":           viewOnly,
	"list_databases":         onProject(identity.PermData),
	"list_deployments":       viewOnly,
	"list_domains":           feature(identity.FeatureDomains),
	"list_git_branches":      feature(identity.FeatureGitProviders),
	"list_git_repos":         feature(identity.FeatureGitProviders),
	"list_issues":            viewOnly,
	"list_job_runs":          viewOnly,
	"list_jobs":              viewOnly,
	"list_networks":          feature(identity.FeatureNetworks),
	"list_previews":          viewOnly,
	"list_projects":          viewOnly,
	"list_restores":          viewOnly,
	"list_services":          viewOnly,
	"list_storage":           feature(identity.FeatureStorage),
	"list_tables":            onProject(identity.PermData),
	"list_templates":         viewOnly,
	"list_trackers":          adminOnly,
	"list_visits":            viewOnly,
	"list_volume_files":      onProject(identity.PermFiles),
	"make_volume_dir":        onProject(identity.PermFilesEdit),
	"move_volume_path":       onProject(identity.PermFilesEdit),
	"query_database":         onProject(identity.PermData),
	"read_table":             onProject(identity.PermData),
	"read_volume_file":       onProject(identity.PermFiles),
	"reboot_server":          adminOnly,
	"rename_project":         onProject(identity.PermSettings),
	"rename_service":         onProject(identity.PermSettings),
	"restore_database":       onProject(identity.PermRestore),
	"rollback":               onProject(identity.PermDeploy),
	"run_cleanup":            adminOnly,
	"run_job":                onProject(identity.PermDeploy),
	"scan_redis_keys":        onProject(identity.PermData),
	"scan_storage":           adminOnly,
	"service_action":         onProject(identity.PermDeploy),
	"set_anonymize_rules":    onProject(identity.PermEnv),
	"set_backup_schedule":    onProject(identity.PermBackups),
	"set_issue_status":       onProject(identity.PermDeploy),
	"set_job":                onProject(identity.PermSettings),
	"set_maintenance":        onProject(identity.PermDeploy),
	"set_preview_settings":   onProject(identity.PermEnv),
	"set_project_env":        onProject(identity.PermEnv),
	"set_project_tags":       onProject(identity.PermSettings),
	"set_service_networks":   onProject(identity.PermSettings),
	"set_service_tags":       onProject(identity.PermSettings),
	"set_sleep":              onProject(identity.PermSettings),
	"set_uptime_check":       onProject(identity.PermSettings),
	"sync_dns":               adminOnly,
	"sync_project_git":       onProject(identity.PermGit),
	"verify_backup":          onProject(identity.PermBackups),
	"write_volume_file":      onProject(identity.PermFilesEdit),
}

// TestToolAccess calls every tool as a member, each on a project of its
// own: allowed with exactly what toolAccess says, refused with everything
// but its permission (from the member's grants or a restricted token) or its
// feature, and admin tools refused to a member holding everything.
func TestToolAccess(t *testing.T) {
	// Allowed calls stop at Docker, past the checks; nothing reaches a daemon.
	t.Setenv("DOCKER_HOST", "unix:///nonexistent/docker.sock")
	ctx := context.Background()
	e := newEnv(t)
	tools, err := e.session().ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, tool := range tools.Tools {
		listed[tool.Name] = true
	}
	for name := range toolAccess {
		if !listed[name] {
			t.Errorf("%s is in toolAccess but is no tool", name)
		}
	}

	for i, tool := range tools.Tools {
		need, ok := toolAccess[tool.Name]
		if !ok {
			t.Errorf("%s: declare what it needs in toolAccess", tool.Name)
			continue
		}
		t.Run(tool.Name, func(t *testing.T) {
			s := e.seed(t, fmt.Sprintf("p%d", i))
			projectID, err := e.c.ProjectOf(ctx, identity.IDBackup, s.backup)
			if err != nil {
				t.Fatal(err)
			}
			member := e.member(t, fmt.Sprintf("m%d", i), projectID)
			args := e.toolArgs(t, tool, s)
			if _, ok := args["repo_url"]; ok {
				args["repo_url"] = "https://kipitiny.invalid/x.git" // .invalid never resolves: no real network
			}

			if need.admin {
				out, _ := e.call(member(identity.Perms, identity.Features...), tool.Name, args)
				if !strings.Contains(out, "admins only") {
					t.Errorf("member with every permission and feature: %s", out)
				}
				return
			}
			if need.perm != "" {
				without := slices.DeleteFunc(slices.Clone(identity.Perms), func(p identity.Perm) bool { return p == need.perm })
				want := "needs the " + string(need.perm) + " permission"
				if out, _ := e.call(member(without, identity.Features...), tool.Name, args); !strings.Contains(out, want) {
					t.Errorf("member without %s: %s", need.perm, out)
				}
				if out, _ := e.call(e.session(without...), tool.Name, args); !strings.Contains(out, want) {
					t.Errorf("admin token restricted without %s: %s", need.perm, out)
				}
			}
			if need.feature != "" {
				without := slices.DeleteFunc(slices.Clone(identity.Features), func(f identity.Feature) bool { return f == need.feature })
				if out, _ := e.call(member(identity.Perms, without...), tool.Name, args); !strings.Contains(out, "needs access to "+string(need.feature)) {
					t.Errorf("member without %s: %s", need.feature, out)
				}
			}
			granted := []identity.Perm{identity.PermView}
			if need.perm != "" {
				granted = append(granted, need.perm)
			}
			var features []identity.Feature
			if need.feature != "" {
				features = []identity.Feature{need.feature}
			}
			if out, _ := e.call(member(granted, features...), tool.Name, args); strings.Contains(out, "forbidden") || strings.Contains(out, "not found: project") {
				t.Errorf("member with exactly %v %v refused: %s", granted, features, out)
			}
		})
	}
}
