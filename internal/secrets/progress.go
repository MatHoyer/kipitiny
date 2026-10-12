package secrets

import "context"

// Step names a stage of connecting or testing a provider, reported as it
// starts so the UI can show what's happening.
type Step string

const (
	// StepImage: pulling the provider's CLI image (first use only).
	StepImage Step = "image"
	// StepHelper: starting the helper container the CLI runs in.
	StepHelper Step = "helper"
	// StepLogin: signing in with the token.
	StepLogin Step = "login"
	// StepVerify: checking the account the token signed in to.
	StepVerify Step = "verify"
	// StepSession: checking the saved session still works.
	StepSession Step = "session"
	// StepVaults: listing vaults after signing in again.
	StepVaults Step = "vaults"
)

type progressKey struct{}

// WithProgress has Report call f for each step reported under ctx.
func WithProgress(ctx context.Context, f func(Step)) context.Context {
	return context.WithValue(ctx, progressKey{}, f)
}

// Report tells ctx's listener, if any, that step starts.
func Report(ctx context.Context, step Step) {
	if f, ok := ctx.Value(progressKey{}).(func(Step)); ok {
		f(step)
	}
}
