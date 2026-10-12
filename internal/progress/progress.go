// Package progress lets long calls (connecting, testing) report the step
// they're at, so the UI can show what's happening as it happens.
package progress

import "context"

type key struct{}

// With has Report call f for each step reported under ctx.
func With(ctx context.Context, f func(step string)) context.Context {
	return context.WithValue(ctx, key{}, f)
}

// Report tells ctx's listener, if any, that step starts.
func Report(ctx context.Context, step string) {
	if f, ok := ctx.Value(key{}).(func(string)); ok {
		f(step)
	}
}
