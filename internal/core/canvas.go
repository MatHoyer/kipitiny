package core

import (
	"context"
	"fmt"
	"math"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// The map canvas lays nodes out by itself; where someone drags one is kept
// here, shared by every user. Nodes are "<kind>:<id>": a server's own nodes
// (server, internet, tunnel, traefik, manager) take the server ID, the
// others their project, service or network ID.

// Point is a node's position, relative to its parent frame if any.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

const maxCanvasNodes = 5000

var serverNodeKinds = []string{"server", "internet", "tunnel", "traefik", "manager"}

// canvasNodes is the set of node keys that exist now.
func (c *Core) canvasNodes(ctx context.Context) (map[string]bool, error) {
	known := map[string]bool{}
	servers, err := c.store.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	for _, sv := range servers {
		for _, k := range serverNodeKinds {
			known[k+":"+sv.ID] = true
		}
	}
	projects, err := c.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		known["project:"+p.ID] = true
	}
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range svcs {
		known["svc:"+s.ID] = true
	}
	nets, err := c.store.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}
	for _, n := range nets {
		known["net:"+n.ID] = true
	}
	return known, nil
}

// CanvasLayout returns the saved positions of the nodes that still exist.
func (c *Core) CanvasLayout(ctx context.Context) (map[string]Point, error) {
	known, err := c.canvasNodes(ctx)
	if err != nil {
		return nil, err
	}
	ps, err := c.store.ListCanvasPositions(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Point, len(ps))
	for _, p := range ps {
		if known[p.Node] {
			out[p.Node] = Point{X: p.X, Y: p.Y}
		}
	}
	return out, nil
}

// SaveCanvasLayout records where nodes were dragged to, and forgets the
// positions of nodes that no longer exist.
func (c *Core) SaveCanvasLayout(ctx context.Context, positions map[string]Point) error {
	if len(positions) > maxCanvasNodes {
		return fmt.Errorf("%w: too many nodes", ErrInvalid)
	}
	known, err := c.canvasNodes(ctx)
	if err != nil {
		return err
	}
	ps := make([]store.CanvasPosition, 0, len(positions))
	for node, p := range positions {
		if !known[node] {
			return fmt.Errorf("%w: unknown node %q", ErrInvalid, node)
		}
		if !finite(p.X) || !finite(p.Y) || math.Abs(p.X) > 1e6 || math.Abs(p.Y) > 1e6 {
			return fmt.Errorf("%w: position of %s out of range", ErrInvalid, node)
		}
		ps = append(ps, store.CanvasPosition{Node: node, X: math.Round(p.X), Y: math.Round(p.Y)})
	}
	stale, err := c.staleCanvasNodes(ctx, known)
	if err != nil {
		return err
	}
	return c.store.SaveCanvasPositions(ctx, ps, stale)
}

// ResetCanvasLayout forgets every position, so the canvas lays all nodes
// out again.
func (c *Core) ResetCanvasLayout(ctx context.Context) error {
	ps, err := c.store.ListCanvasPositions(ctx)
	if err != nil {
		return err
	}
	remove := make([]string, len(ps))
	for i, p := range ps {
		remove[i] = p.Node
	}
	return c.store.SaveCanvasPositions(ctx, nil, remove)
}

func (c *Core) staleCanvasNodes(ctx context.Context, known map[string]bool) ([]string, error) {
	ps, err := c.store.ListCanvasPositions(ctx)
	if err != nil {
		return nil, err
	}
	var stale []string
	for _, p := range ps {
		if !known[p.Node] {
			stale = append(stale, p.Node)
		}
	}
	return stale, nil
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
