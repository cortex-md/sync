package server_test

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/apidocs"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

var undocumentedRoutes = []string{"/docs", "/docs/", "/docs/init.js", "/docs/openapi.yaml", "/metrics", "/assets/v1/*", "/share-assets/*"}

func fullFeatureStack(t *testing.T) *stackClient {
	return newProtectedStack(t, map[string]string{
		"CORTEX_SUBSCRIPTION_ENABLED":               "true",
		"CORTEX_SUBSCRIPTION_STRIPE_SECRET_KEY":     "sk_test_spec",
		"CORTEX_SUBSCRIPTION_STRIPE_PRICE_ID":       "price_spec",
		"CORTEX_SUBSCRIPTION_STRIPE_WEBHOOK_SECRET": "whsec_spec",
		"CORTEX_FEATURES":                           "collab,avatars,guests,public_links,metrics",
		"CORTEX_MAIL_DRIVER":                        "log",
	})
}

func TestOpenAPISpecDocumentsEveryRoute(t *testing.T) {
	stack := fullFeatureStack(t)
	routes := map[string]bool{}
	require.NoError(t, chi.Walk(stack.handler.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		for _, skip := range undocumentedRoutes {
			if route == skip {
				return nil
			}
		}
		routes[strings.ToLower(method)+" "+route] = true
		return nil
	}))

	var spec struct {
		OpenAPI string                            `yaml:"openapi"`
		Paths   map[string]map[string]interface{} `yaml:"paths"`
	}
	require.NoError(t, yaml.Unmarshal(apidocs.Spec, &spec))
	require.Equal(t, "3.1.0", spec.OpenAPI)
	documented := map[string]bool{}
	for path, item := range spec.Paths {
		for method := range item {
			switch method {
			case "get", "post", "put", "patch", "delete":
				documented[method+" "+path] = true
			}
		}
	}

	var missing, stale []string
	for route := range routes {
		if !documented[route] {
			missing = append(missing, route)
		}
	}
	for route := range documented {
		if !routes[route] {
			stale = append(stale, route)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	assert.Empty(t, missing, "routes missing from internal/apidocs/openapi.yaml")
	assert.Empty(t, stale, "documented paths that the router does not serve")
}

func TestDocsPageIsServedWithStrictPolicy(t *testing.T) {
	stack := fullFeatureStack(t)

	page := stack.do(http.MethodGet, "/docs", nil, nil)
	require.Equal(t, http.StatusOK, page.Code)
	assert.Contains(t, page.Body.String(), "swagger-ui")
	assert.Contains(t, page.Body.String(), `integrity="sha384-`)
	assert.Contains(t, page.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'")

	spec := stack.do(http.MethodGet, "/docs/openapi.yaml", nil, nil)
	require.Equal(t, http.StatusOK, spec.Code)
	assert.Contains(t, spec.Body.String(), "openapi: 3.1.0")

	cached := stack.do(http.MethodGet, "/docs/openapi.yaml", nil, map[string]string{"If-None-Match": spec.Header().Get("ETag")})
	assert.Equal(t, http.StatusNotModified, cached.Code)
}
