package secrets

// Steps of connecting or testing a provider, reported with progress.Report.
const (
	// StepImage: pulling the provider's CLI image (first use only).
	StepImage = "image"
	// StepHelper: starting the helper container the CLI runs in.
	StepHelper = "helper"
	// StepLogin: signing in with the token.
	StepLogin = "login"
	// StepVerify: checking the account the token signed in to.
	StepVerify = "verify"
	// StepSession: checking the saved session still works.
	StepSession = "session"
	// StepVaults: listing vaults after signing in again.
	StepVaults = "vaults"
)
