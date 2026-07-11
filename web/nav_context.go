package web

import (
	"context"
	"io"

	"github.com/a-h/templ"

	"github.com/dobbo-ca/lynceus/internal/scope"
)

// navStateKey is the private context key under which a shell (ly-ae6.2) stores
// the resolved NavState for the current request.
type navStateKey struct{}

// NavState is everything the sidebar needs, resolved per request by the shell:
// the current scope, its resolved display label (scope.Scope carries only ids),
// which engines are enabled, and the active screen id.
type NavState struct {
	Scope   scope.Scope
	Label   string
	Engines EngineFlags
	Active  string
}

// WithNavState returns a context carrying ns. ly-ae6.2's resolver/middleware
// (or a page handler that knows its own scope) calls this before rendering.
func WithNavState(ctx context.Context, ns NavState) context.Context {
	return context.WithValue(ctx, navStateKey{}, ns)
}

// NavStateFromContext returns the NavState placed on ctx, or a safe default
// (fleet scope, DefaultEngines, no active) when none is present — so the
// sidebar renders correctly even before ly-ae6.2's middleware exists.
func NavStateFromContext(ctx context.Context) NavState {
	if ns, ok := ctx.Value(navStateKey{}).(NavState); ok {
		return ns
	}
	return NavState{Scope: FleetScope(), Engines: DefaultEngines()}
}

// SidebarFromContext renders the sidebar for the NavState on the render-time
// context. This is the one-liner a shell mounts: `@web.SidebarFromContext()`.
// It reads the context at Render time (not construction), so the resolved
// scope threads through correctly.
func SidebarFromContext() templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		ns := NavStateFromContext(ctx)
		return Sidebar(ns.Scope, ns.Label, ns.Engines, ns.Active).Render(ctx, w)
	})
}
