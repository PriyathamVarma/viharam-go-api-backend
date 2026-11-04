package openapi

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

var pathParameterPattern = regexp.MustCompile(`:([A-Za-z0-9_]+)`)

// Register adds the generated OpenAPI document and a lightweight Swagger UI.
// The document is built from Gin's registered routes so Mongo compatibility
// and Supabase PostgreSQL deployments expose the routes they actually serve.
func Register(engine *gin.Engine) {
	engine.GET("/swagger.json", func(c *gin.Context) {
		c.JSON(http.StatusOK, Document(engine.Routes()))
	})
	engine.GET("/swagger", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(uiHTML))
	})
	engine.GET("/swagger/", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(uiHTML))
	})
}

// Document converts the routes registered by Gin into an OpenAPI 3 document.
// Handlers use flexible JSON envelopes, so request and response payloads are
// intentionally represented as free-form objects until domain schemas settle.
func Document(routes gin.RoutesInfo) map[string]any {
	paths := map[string]any{}
	for _, route := range routes {
		if route.Path == "/swagger" || route.Path == "/swagger/" || route.Path == "/swagger.json" {
			continue
		}
		path := openAPIPath(route.Path)
		operations, ok := paths[path].(map[string]any)
		if !ok {
			operations = map[string]any{}
			paths[path] = operations
		}
		method := strings.ToLower(route.Method)
		operation := map[string]any{
			"operationId": operationID(route.Method, path),
			"summary":     fmt.Sprintf("%s %s", route.Method, route.Path),
			"tags":        []string{tagFor(path)},
			"parameters":  pathParameters(route.Path),
			"responses": map[string]any{
				"200": map[string]any{"description": "Successful response"},
				"400": map[string]any{"description": "Invalid request"},
				"401": map[string]any{"description": "Authentication required"},
				"500": map[string]any{"description": "Server error"},
			},
		}
		if strings.HasPrefix(path, "/api/v1/") && !isPublic(path, method) {
			operation["security"] = []any{map[string]any{"bearerAuth": []any{}}}
		}
		if method == "post" || method == "put" || method == "patch" {
			operation["requestBody"] = map[string]any{
				"required": true,
				"content": map[string]any{
					"application/json": map[string]any{
						"schema": map[string]any{"$ref": "#/components/schemas/JSONPayload"},
					},
				},
			}
		}
		operations[method] = operation
	}

	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "Viharam Go API",
			"version":     "1.0.0",
			"description": "Viharam travel marketplace API. Route documentation is generated from the running Gin service.",
		},
		"servers": []any{
			map[string]any{"url": "/", "description": "Current server"},
		},
		"tags": []any{
			map[string]any{"name": "health", "description": "Service health and metadata"},
			map[string]any{"name": "auth", "description": "Authentication and current user"},
			map[string]any{"name": "api", "description": "Viharam API v1 resources"},
		},
		"paths": paths,
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]any{
					"type":         "http",
					"scheme":       "bearer",
					"bearerFormat": "JWT",
				},
			},
			"schemas": map[string]any{
				"JSONPayload": map[string]any{
					"type":                 "object",
					"additionalProperties": true,
				},
			},
		},
	}
}

func openAPIPath(path string) string {
	return pathParameterPattern.ReplaceAllString(path, `{$1}`)
}

func pathParameters(path string) []any {
	matches := pathParameterPattern.FindAllStringSubmatch(path, -1)
	parameters := make([]any, 0, len(matches))
	for _, match := range matches {
		parameters = append(parameters, map[string]any{
			"name":     match[1],
			"in":       "path",
			"required": true,
			"schema":   map[string]any{"type": "string"},
		})
	}
	return parameters
}

func operationID(method, path string) string {
	value := strings.NewReplacer("/", "_", "{", "", "}", "", "-", "_", ":", "").Replace(path)
	value = strings.Trim(value, "_")
	return strings.ToLower(method) + "_" + value
}

func tagFor(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "v1" {
		return parts[2]
	}
	if len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}
	return "health"
}

func isPublic(path, method string) bool {
	if method != "get" && method != "post" {
		return false
	}
	public := []string{
		"/api/v1/auth/register",
		"/api/v1/auth/login",
		"/api/v1/auth/demo-login",
		"/api/v1/categories",
		"/api/v1/experiences",
		"/api/v1/experiences/{id}",
		"/api/v1/search",
		"/api/v1/properties",
		"/api/v1/properties/{id}",
		"/api/v1/experience-slots",
		"/api/v1/experience-reviews",
		"/api/v1/experience-reviews/{id}",
		"/api/v1/reviews",
		"/api/v1/pricing-rules/calculate",
	}
	for _, candidate := range public {
		if path == candidate {
			return true
		}
	}
	return false
}

const uiHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Viharam Go API — Swagger UI</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css" />
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.ui = SwaggerUIBundle({ url: '/swagger.json', dom_id: '#swagger-ui', deepLinking: true, presets: [SwaggerUIBundle.presets.apis, SwaggerUIBundle.SwaggerUIStandalonePreset] });
  </script>
</body>
</html>`
