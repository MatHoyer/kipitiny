package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/core"
)

// toolNeed is what a member needs to call a tool: admin, or view on the
// project plus perm and feature when set.
type toolNeed struct {
	admin   bool
	perm    core.Perm
	feature core.Feature
}

var (
	adminOnly = toolNeed{admin: true}
	viewOnly  = toolNeed{}
)

func onProject(p core.Perm, f ...core.Feature) toolNeed {
	n := toolNeed{perm: p}
	if len(f) > 0 {
		n.feature = f[0]
	}
	return n
}

func feature(f core.Feature) toolNeed { return toolNeed{feature: f} }

// toolAccess is every MCP tool and what calling it takes. A new tool fails
// TestToolAccess until it is listed here.
var toolAccess = map[string]toolNeed{
	"anonymize_now":          onProject(core.PermEnv),
	"apply_project_compose":  onProject(core.PermSettings),
	"backup_database":        onProject(core.PermBackups),
	"backup_project":         onProject(core.PermBackups),
	"close_preview":          onProject(core.PermDeploy),
	"create_database":        onProject(core.PermDataEdit),
	"create_network":         adminOnly,
	"create_project":         feature(core.FeatureCreateProjects),
	"create_tracker":         adminOnly,
	"delete_backup":          onProject(core.PermBackups),
	"delete_backup_schedule": onProject(core.PermBackups),
	"delete_job":             onProject(core.PermSettings),
	"delete_network":         adminOnly,
	"delete_project":         onProject(core.PermDelete),
	"delete_service":         onProject(core.PermDelete),
	"delete_tracker":         adminOnly,
	"delete_uptime_check":    onProject(core.PermSettings),
	"delete_volume_paths":    onProject(core.PermFilesEdit),
	"deploy":                 onProject(core.PermDeploy),
	"deploy_image":           onProject(core.PermSettings),
	"deploy_preview":         onProject(core.PermDeploy),
	"export_analytics":       viewOnly,
	"find_documents":         onProject(core.PermData),
	"get_analytics":          viewOnly,
	"get_app_status":         viewOnly,
	"get_audit_log":          adminOnly,
	"get_cleanup":            adminOnly,
	"get_connection":         onProject(core.PermEnv),
	"get_deployment_log":     viewOnly,
	"get_funnel":             viewOnly,
	"get_issue":              viewOnly,
	"get_logs":               viewOnly,
	"get_manager_status":     viewOnly,
	"get_preview_seed":       viewOnly,
	"get_preview_settings":   viewOnly,
	"get_project_compose":    viewOnly,
	"get_project_git":        onProject(core.PermGit),
	"get_redis_key":          onProject(core.PermData),
	"get_server_status":      feature(core.FeatureServers),
	"get_topology":           viewOnly,
	"get_visit":              viewOnly,
	"install_server_updates": adminOnly,
	"install_template":       onProject(core.PermSettings),
	"link_project_git":       onProject(core.PermGit),
	"list_backup_schedules":  viewOnly,
	"list_backups":           viewOnly,
	"list_databases":         onProject(core.PermData),
	"list_deployments":       viewOnly,
	"list_domains":           feature(core.FeatureDomains),
	"list_git_branches":      feature(core.FeatureGitProviders),
	"list_git_repos":         feature(core.FeatureGitProviders),
	"list_issues":            viewOnly,
	"list_job_runs":          viewOnly,
	"list_jobs":              viewOnly,
	"list_networks":          feature(core.FeatureNetworks),
	"list_previews":          viewOnly,
	"list_projects":          viewOnly,
	"list_restores":          viewOnly,
	"list_services":          viewOnly,
	"list_storage":           feature(core.FeatureStorage),
	"list_tables":            onProject(core.PermData),
	"list_templates":         viewOnly,
	"list_trackers":          adminOnly,
	"list_visits":            viewOnly,
	"list_volume_files":      onProject(core.PermFiles),
	"make_volume_dir":        onProject(core.PermFilesEdit),
	"move_volume_path":       onProject(core.PermFilesEdit),
	"query_database":         onProject(core.PermData),
	"read_table":             onProject(core.PermData),
	"read_volume_file":       onProject(core.PermFiles),
	"reboot_server":          adminOnly,
	"rename_project":         onProject(core.PermSettings),
	"rename_service":         onProject(core.PermSettings),
	"restore_database":       onProject(core.PermRestore),
	"rollback":               onProject(core.PermDeploy),
	"run_cleanup":            adminOnly,
	"run_job":                onProject(core.PermDeploy),
	"scan_redis_keys":        onProject(core.PermData),
	"scan_storage":           adminOnly,
	"service_action":         onProject(core.PermDeploy),
	"set_anonymize_rules":    onProject(core.PermEnv),
	"set_backup_schedule":    onProject(core.PermBackups),
	"set_issue_status":       onProject(core.PermDeploy),
	"set_job":                onProject(core.PermSettings),
	"set_maintenance":        onProject(core.PermDeploy),
	"set_preview_settings":   onProject(core.PermEnv),
	"set_project_env":        onProject(core.PermEnv),
	"set_project_tags":       onProject(core.PermSettings),
	"set_service_networks":   onProject(core.PermSettings),
	"set_service_tags":       onProject(core.PermSettings),
	"set_sleep":              onProject(core.PermSettings),
	"set_uptime_check":       onProject(core.PermSettings),
	"sync_dns":               adminOnly,
	"sync_project_git":       onProject(core.PermGit),
	"verify_backup":          onProject(core.PermBackups),
	"write_volume_file":      onProject(core.PermFilesEdit),
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
			projectID, err := e.c.ProjectOf(ctx, core.IDBackup, s.backup)
			if err != nil {
				t.Fatal(err)
			}
			member := e.member(t, fmt.Sprintf("m%d", i), projectID)
			args := e.toolArgs(t, tool, s)
			if _, ok := args["repo_url"]; ok {
				args["repo_url"] = "https://kipitiny.invalid/x.git" // .invalid never resolves: no real network
			}

			if need.admin {
				out, _ := e.call(member(core.Perms, core.Features...), tool.Name, args)
				if !strings.Contains(out, "admins only") {
					t.Errorf("member with every permission and feature: %s", out)
				}
				return
			}
			if need.perm != "" {
				without := slices.DeleteFunc(slices.Clone(core.Perms), func(p core.Perm) bool { return p == need.perm })
				want := "needs the " + string(need.perm) + " permission"
				if out, _ := e.call(member(without, core.Features...), tool.Name, args); !strings.Contains(out, want) {
					t.Errorf("member without %s: %s", need.perm, out)
				}
				if out, _ := e.call(e.session(without...), tool.Name, args); !strings.Contains(out, want) {
					t.Errorf("admin token restricted without %s: %s", need.perm, out)
				}
			}
			if need.feature != "" {
				without := slices.DeleteFunc(slices.Clone(core.Features), func(f core.Feature) bool { return f == need.feature })
				if out, _ := e.call(member(core.Perms, without...), tool.Name, args); !strings.Contains(out, "needs access to "+string(need.feature)) {
					t.Errorf("member without %s: %s", need.feature, out)
				}
			}
			granted := []core.Perm{core.PermView}
			if need.perm != "" {
				granted = append(granted, need.perm)
			}
			var features []core.Feature
			if need.feature != "" {
				features = []core.Feature{need.feature}
			}
			if out, _ := e.call(member(granted, features...), tool.Name, args); strings.Contains(out, "forbidden") || strings.Contains(out, "not found: project") {
				t.Errorf("member with exactly %v %v refused: %s", granted, features, out)
			}
		})
	}
}
