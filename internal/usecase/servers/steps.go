package servers

// cleanupKey names the running cleanup: one at a time.
const cleanupKey = "cleanup"

// Steps of a cleanup, as CleanupView.Step reports them. All but
// CleanupDeployments run on each server in turn (CleanupView.StepServer).
const (
	// CleanupConnect: reaching the server's Docker.
	CleanupConnect = "connect"
	// CleanupContainers: removing stopped containers kipitiny didn't create.
	CleanupContainers = "containers"
	// CleanupImages: removing dangling or unused images.
	CleanupImages = "images"
	// CleanupBuildCache: pruning the build cache.
	CleanupBuildCache = "buildcache"
	// CleanupVolumes: removing volumes no container mounts.
	CleanupVolumes = "volumes"
	// CleanupNetworks: removing unused networks kipitiny didn't create.
	CleanupNetworks = "networks"
	// CleanupDeployments: trimming old deployment records and their logs.
	CleanupDeployments = "deployments"
)
