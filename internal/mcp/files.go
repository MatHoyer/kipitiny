package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/core"
)

// Volume files: browsing (read), writes (admin). The core checks the scopes
// and audits writes itself.

// maxWriteBase64 caps a binary file written through MCP (decoded size).
const maxWriteBase64 = 16 << 20

type volumePathIn struct {
	Service string `json:"service" jsonschema:"the service as project/service, or its ID"`
	Path    string `json:"path,omitempty" jsonschema:"<volume>/<path inside it> (e.g. data/mods) or @container/<absolute path> (e.g. @container/etc/nginx); empty lists the volumes and @container"`
}

func (t *tools) listVolumeFiles(ctx context.Context, _ *mcp.CallToolRequest, in volumePathIn) (*mcp.CallToolResult, core.VolumeListing, error) {
	svc, err := t.c.ResolveService(ctx, in.Service)
	if err != nil {
		return nil, core.VolumeListing{}, friendly(err)
	}
	l, err := t.c.ListVolumeFiles(ctx, svc.ID, in.Path)
	return nil, l, friendly(err)
}

func (t *tools) readVolumeFile(ctx context.Context, _ *mcp.CallToolRequest, in volumePathIn) (*mcp.CallToolResult, core.VolumeFile, error) {
	svc, err := t.c.ResolveService(ctx, in.Service)
	if err != nil {
		return nil, core.VolumeFile{}, friendly(err)
	}
	f, err := t.c.ReadVolumeFile(ctx, svc.ID, in.Path)
	return nil, f, friendly(err)
}

type writeVolumeFileIn struct {
	Service       string `json:"service" jsonschema:"the app as project/service, or its ID"`
	Path          string `json:"path" jsonschema:"<volume>/<path inside it>; missing folders are created"`
	Content       string `json:"content,omitempty" jsonschema:"the file's text; or content_base64 for binary files"`
	ContentBase64 string `json:"content_base64,omitempty" jsonschema:"the file's bytes, base64 (up to 16 MiB), e.g. a .jar"`
	Overwrite     bool   `json:"overwrite,omitempty" jsonschema:"replace an existing file (it keeps its owner and mode)"`
	Modified      string `json:"modified,omitempty" jsonschema:"the modified time read_volume_file returned: refuse the write if the file changed since"`
}

func (t *tools) writeVolumeFile(ctx context.Context, _ *mcp.CallToolRequest, in writeVolumeFileIn) (*mcp.CallToolResult, core.VolumeEntry, error) {
	svc, err := t.c.ResolveService(ctx, in.Service)
	if err != nil {
		return nil, core.VolumeEntry{}, friendly(err)
	}
	var body io.Reader = strings.NewReader(in.Content)
	if in.ContentBase64 != "" {
		if in.Content != "" {
			return nil, core.VolumeEntry{}, errors.New("give content or content_base64, not both")
		}
		if base64.StdEncoding.DecodedLen(len(in.ContentBase64)) > maxWriteBase64 {
			return nil, core.VolumeEntry{}, fmt.Errorf("content_base64 is limited to %d MiB; upload bigger files from the UI", maxWriteBase64>>20)
		}
		b, err := base64.StdEncoding.DecodeString(in.ContentBase64)
		if err != nil {
			return nil, core.VolumeEntry{}, fmt.Errorf("content_base64: %w", err)
		}
		body = strings.NewReader(string(b))
	}
	opts := core.WriteOptions{Overwrite: in.Overwrite}
	if in.Modified != "" {
		if opts.Modified, err = time.Parse(time.RFC3339, in.Modified); err != nil {
			return nil, core.VolumeEntry{}, errors.New("modified must be an RFC 3339 time, as read_volume_file returns it")
		}
	}
	e, err := t.c.WriteVolumeFile(ctx, svc.ID, in.Path, body, opts)
	return nil, e, friendly(err)
}

// volumeDone says what a write did.
type volumeDone struct {
	Done string `json:"done"`
}

type makeVolumeDirIn struct {
	Service string `json:"service" jsonschema:"the app as project/service, or its ID"`
	Path    string `json:"path" jsonschema:"<volume>/<folder>; missing parents are created"`
}

func (t *tools) makeVolumeDir(ctx context.Context, _ *mcp.CallToolRequest, in makeVolumeDirIn) (*mcp.CallToolResult, volumeDone, error) {
	svc, err := t.c.ResolveService(ctx, in.Service)
	if err != nil {
		return nil, volumeDone{}, friendly(err)
	}
	if err := t.c.MakeVolumeDir(ctx, svc.ID, in.Path); err != nil {
		return nil, volumeDone{}, friendly(err)
	}
	return nil, volumeDone{Done: "created " + in.Path}, nil
}

type moveVolumePathIn struct {
	Service string `json:"service" jsonschema:"the app as project/service, or its ID"`
	From    string `json:"from" jsonschema:"<volume>/<path> of the file or folder"`
	To      string `json:"to" jsonschema:"its new <volume>/<path>, in any volume of the same service; its folder must exist and it must not"`
	Copy    bool   `json:"copy,omitempty" jsonschema:"copy instead of moving (folders recursively, owners and modes kept)"`
}

func (t *tools) moveVolumePath(ctx context.Context, _ *mcp.CallToolRequest, in moveVolumePathIn) (*mcp.CallToolResult, volumeDone, error) {
	svc, err := t.c.ResolveService(ctx, in.Service)
	if err != nil {
		return nil, volumeDone{}, friendly(err)
	}
	op, verb := t.c.MoveVolumePath, "moved"
	if in.Copy {
		op, verb = t.c.CopyVolumePath, "copied"
	}
	if err := op(ctx, svc.ID, in.From, in.To); err != nil {
		return nil, volumeDone{}, friendly(err)
	}
	return nil, volumeDone{Done: verb + " " + in.From + " to " + in.To}, nil
}

type deleteVolumePathsIn struct {
	Service string   `json:"service" jsonschema:"the app as project/service, or its ID"`
	Paths   []string `json:"paths" jsonschema:"<volume>/<path> of each file or folder to delete (folders with their content)"`
}

func (t *tools) deleteVolumePaths(ctx context.Context, _ *mcp.CallToolRequest, in deleteVolumePathsIn) (*mcp.CallToolResult, volumeDone, error) {
	svc, err := t.c.ResolveService(ctx, in.Service)
	if err != nil {
		return nil, volumeDone{}, friendly(err)
	}
	if err := t.c.DeleteVolumePaths(ctx, svc.ID, in.Paths); err != nil {
		return nil, volumeDone{}, friendly(err)
	}
	return nil, volumeDone{Done: "deleted " + strings.Join(in.Paths, ", ")}, nil
}
