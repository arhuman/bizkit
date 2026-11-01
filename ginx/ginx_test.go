package ginx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type typedKey struct{}

func serve(t *testing.T, handlers ...gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", handlers...)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	return w
}

func setKeys(c *gin.Context) {
	c.Set("username", "alice")
	c.Set("workspace", 42)
	c.Set(typedKey{}, "typed")
	c.Next()
}

func TestWrap_CopiesGinKeys(t *testing.T) {
	var user, ws any
	var userOK, wsOK bool
	var all map[string]any
	h := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		user, userOK = Value(r.Context(), "username")
		ws, wsOK = Value(r.Context(), "workspace")
		all = Keys(r.Context())
	})

	serve(t, setKeys, Wrap(h))

	if !userOK || user != "alice" {
		t.Errorf("Value(username) = %v, %v; want alice, true", user, userOK)
	}
	if !wsOK || ws != 42 {
		t.Errorf("Value(workspace) = %v, %v; want 42, true", ws, wsOK)
	}
	if len(all) != 2 || all["username"] != "alice" || all["workspace"] != 42 {
		t.Errorf("Keys = %v; want only username=alice workspace=42", all)
	}
}

func TestWrap_HandlerWritesReachGinResponse(t *testing.T) {
	h := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Test", "yes")
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "brewed")
	}

	w := serve(t, WrapFunc(h))

	if w.Code != http.StatusTeapot {
		t.Errorf("status = %d; want %d", w.Code, http.StatusTeapot)
	}
	if got := w.Header().Get("X-Test"); got != "yes" {
		t.Errorf("X-Test = %q; want yes", got)
	}
	if got := w.Body.String(); got != "brewed" {
		t.Errorf("body = %q; want brewed", got)
	}
}

func TestWrap_NoKeysNotFound(t *testing.T) {
	var ran, ok bool
	var all map[string]any
	h := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ran = true
		_, ok = Value(r.Context(), "username")
		all = Keys(r.Context())
	})

	serve(t, Wrap(h))

	if !ran {
		t.Fatal("handler did not run")
	}
	if ok {
		t.Error("Value(username) found on a request without keys")
	}
	if all != nil {
		t.Errorf("Keys = %v; want nil", all)
	}
	if _, ok := Value(context.Background(), "username"); ok {
		t.Error("Value found a key in a context that never passed through Wrap")
	}
}

func TestWrap_CopyMutationDoesNotLeak(t *testing.T) {
	var second map[string]any
	h := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		first := Keys(r.Context())
		first["username"] = "mallory"
		first["injected"] = true
		second = Keys(r.Context())
	})
	var after any
	check := func(c *gin.Context) { after = c.GetString("username") }

	serve(t, setKeys, Wrap(h), check)

	if second["username"] != "alice" || len(second) != 2 {
		t.Errorf("context keys after mutation = %v; want username=alice only", second)
	}
	if after != "alice" {
		t.Errorf("gin username after mutation = %v; want alice", after)
	}
}

func TestWrap_SnapshotAtWrapTime(t *testing.T) {
	var got any
	var ok bool
	h := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok = Value(r.Context(), "late")
	})
	late := func(c *gin.Context) { c.Set("late", "set-after") }

	serve(t, Wrap(h), late)

	if ok {
		t.Errorf("Value(late) = %v; want not found, it was set after Wrap ran", got)
	}
}

func TestWrap_AbortSkipsHandler(t *testing.T) {
	ran := false
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ran = true
		w.WriteHeader(http.StatusOK)
	})
	deny := func(c *gin.Context) { c.AbortWithStatus(http.StatusForbidden) }

	w := serve(t, deny, Wrap(h))

	if ran {
		t.Error("wrapped handler ran after abort")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d; want %d", w.Code, http.StatusForbidden)
	}
}
