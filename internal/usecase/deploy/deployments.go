package deploy

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func (c *Deployer) ListDeployments(ctx context.Context, serviceID string) ([]domain.Deployment, error) {
	if _, err := c.store.GetService(ctx, serviceID); err != nil {
		return nil, err
	}
	return c.store.ListDeployments(ctx, serviceID, 50)
}

func (c *Deployer) GetDeployment(ctx context.Context, id string) (domain.Deployment, error) {
	return c.store.GetDeployment(ctx, id)
}

// DeploymentLog opens the deployment's log file. Missing logs yield an empty reader.
func (c *Deployer) DeploymentLog(ctx context.Context, id string) (io.ReadCloser, error) {
	dep, err := c.store.GetDeployment(ctx, id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(c.DeployLogPath(dep.ServiceID, dep.ID))
	if errors.Is(err, os.ErrNotExist) {
		return io.NopCloser(strings.NewReader("")), nil
	}
	return f, err
}

func (c *Deployer) deployLogDir(serviceID string) string {
	return filepath.Join(c.cfg.DataDir, "deploys", serviceID)
}

func (c *Deployer) DeployLogPath(serviceID, deployID string) string {
	return filepath.Join(c.deployLogDir(serviceID), deployID+".log")
}

func (c *Deployer) createDeployLog(serviceID, deployID string) (*os.File, error) {
	if err := os.MkdirAll(c.deployLogDir(serviceID), 0o700); err != nil {
		return nil, err
	}
	return os.Create(c.DeployLogPath(serviceID, deployID))
}

func (c *Deployer) RemoveDeployLogs(serviceID string) {
	if err := os.RemoveAll(c.deployLogDir(serviceID)); err != nil {
		c.log.Warn("cannot remove deploy logs", "service", serviceID, "err", err)
	}
}
