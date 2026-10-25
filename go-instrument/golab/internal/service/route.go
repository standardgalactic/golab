package service

import (
	"net/http"
	"sort"
	"strings"
)

// Route is one entry of the routing table: a method and an exact path, the
// handler for them, and what the caller must be to use it.
type Route struct {
	Method  string
	Path    string
	Need    Need
	Handler http.Handler
}

// Router chooses a route from a method and a path. Match takes those two
// strings and nothing else, so it cannot consult headers, credentials, or the
// body, and routing cannot come to depend on who is asking.
type Router struct {
	routes []Route
}

// NewRouter builds a router over a table of routes.
func NewRouter(routes []Route) *Router { return &Router{routes: routes} }

// Match returns the route for the request line, or a status code explaining
// why there is none: 404 when no route has the path, 405 when some route has
// the path but none with the method, together with the allowed methods.
func (rt *Router) Match(method, path string) (route *Route, status int, allow []string) {
	for i := range rt.routes {
		r := &rt.routes[i]
		if r.Path != path {
			continue
		}
		if r.Method == method {
			return r, http.StatusOK, nil
		}
		allow = append(allow, r.Method)
	}
	if len(allow) > 0 {
		sort.Strings(allow)
		return nil, http.StatusMethodNotAllowed, allow
	}
	return nil, http.StatusNotFound, nil
}

func allowHeader(methods []string) string { return strings.Join(methods, ", ") }
