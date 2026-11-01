// Package ginx mounts net/http handlers in a gin engine.
//
// bizkit is net/http-native; gin hosts store request state (the
// authenticated user, the workspace) in gin's per-request key map, which a
// plain http.Handler cannot see. Wrap bridges the two: it snapshots the gin
// keys into the request context, where the wrapped handler and any host
// callback it invokes read them back with Value or Keys.
package ginx

import (
	"context"
	"maps"
	"net/http"

	"github.com/gin-gonic/gin"
)

type keysCtxKey struct{}

// Wrap returns a gin handler that serves h with c.Request, whose context
// carries a snapshot of the string keys set on c (c.Set). Non-string keys are
// not copied. The snapshot is taken when the gin handler runs, so keys set by
// earlier middleware are visible and later c.Set calls are not. c.Request
// itself is left unchanged; h writes through c.Writer, so its status,
// headers and body reach the gin response. Like any gin handler, it does not
// run when an earlier middleware aborted the chain.
func Wrap(h http.Handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		r := c.Request
		if keys := snapshot(c); len(keys) > 0 {
			r = r.WithContext(context.WithValue(r.Context(), keysCtxKey{}, keys))
		}
		h.ServeHTTP(c.Writer, r)
	}
}

// WrapFunc is Wrap for an http.HandlerFunc.
func WrapFunc(f http.HandlerFunc) gin.HandlerFunc {
	return Wrap(f)
}

// Value returns the gin key stored by Wrap under key, and whether it was set.
// It reports false for a context that did not pass through Wrap.
func Value(ctx context.Context, key string) (any, bool) {
	keys, _ := ctx.Value(keysCtxKey{}).(map[string]any)
	v, ok := keys[key]
	return v, ok
}

// Keys returns a copy of every gin key stored by Wrap, or nil for a context
// that did not pass through Wrap. Modifying the result affects neither the
// context nor the gin context.
func Keys(ctx context.Context) map[string]any {
	keys, _ := ctx.Value(keysCtxKey{}).(map[string]any)
	return maps.Clone(keys)
}

// snapshot copies the string keys of c. c.Copy clones c.Keys under gin's
// own mutex, which is unexported; reading c.Keys directly would race.
func snapshot(c *gin.Context) map[string]any {
	src := c.Copy().Keys
	if len(src) == 0 {
		return nil
	}
	keys := make(map[string]any, len(src))
	for k, v := range src {
		if s, ok := k.(string); ok {
			keys[s] = v
		}
	}
	return keys
}
