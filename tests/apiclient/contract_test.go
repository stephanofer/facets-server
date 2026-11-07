package apiclient_test

import (
	"encoding/json/v2"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type object = map[string]any

type assertion struct {
	Expression string `yaml:"expression"`
	Operator   string `yaml:"operator"`
	Value      string `yaml:"value"`
}

type request struct {
	Info struct {
		Name string `yaml:"name"`
		Type string `yaml:"type"`
	} `yaml:"info"`
	HTTP struct {
		Method  string      `yaml:"method"`
		URL     string      `yaml:"url"`
		Auth    any         `yaml:"auth"`
		Headers []parameter `yaml:"headers"`
		Params  []parameter `yaml:"params"`
		Body    struct {
			Type string `yaml:"type"`
			Data string `yaml:"data"`
		} `yaml:"body"`
	} `yaml:"http"`
	Runtime struct {
		Assertions []assertion `yaml:"assertions"`
	} `yaml:"runtime"`
}

type parameter struct {
	Name     string `yaml:"name"`
	Value    string `yaml:"value"`
	Type     string `yaml:"type"`
	Disabled bool   `yaml:"disabled"`
}

func obj(v any) object { m, _ := v.(map[string]any); return m }

func resolve(document object, ref string) (any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("unsupported external reference %q", ref)
	}
	var value any = document
	for part := range strings.SplitSeq(strings.TrimPrefix(ref, "#/"), "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		var ok bool
		value, ok = obj(value)[part]
		if !ok {
			return nil, fmt.Errorf("unresolved reference %q", ref)
		}
	}
	return value, nil
}

func schemaObject(document object, value any) object {
	m := obj(value)
	if ref, ok := m["$ref"].(string); ok {
		value, _ = resolve(document, ref)
		return obj(value)
	}
	return m
}

func checkRequest(document, operation, pathItem object, method, path, file string, data []byte) error {
	var r request
	if err := yaml.Unmarshal(data, &r); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}
	id, _ := operation["operationId"].(string)
	if r.Info.Name != id || r.Info.Type != "http" || strings.TrimSuffix(filepath.Base(file), filepath.Ext(file)) != id {
		return fmt.Errorf("filename and info.name must match operationId %q and info.type must be http", id)
	}
	tags, _ := operation["tags"].([]any)
	if len(tags) != 1 || !strings.EqualFold(filepath.Base(filepath.Dir(file)), fmt.Sprint(tags[0])) {
		return fmt.Errorf("request folder must match the operation tag")
	}
	// Parameter names in Bruno may be camelCase, but every segment must remain a variable.
	actualPath := regexp.MustCompile(`\{\{[^{}]+\}\}`).ReplaceAllString(strings.TrimPrefix(r.HTTP.URL, "{{baseUrl}}"), "{}")
	expectedPath := regexp.MustCompile(`\{[^{}]+\}`).ReplaceAllString(path, "{}")
	if r.HTTP.Method != strings.ToUpper(method) || !strings.HasPrefix(r.HTTP.URL, "{{baseUrl}}/") || actualPath != expectedPath {
		return fmt.Errorf("expected %s {{baseUrl}}%s", strings.ToUpper(method), path)
	}
	security, ok := operation["security"]
	if !ok {
		security = document["security"]
	}
	secured, _ := security.([]any)
	if len(secured) == 0 && r.HTTP.Auth != nil {
		return fmt.Errorf("public request must not inherit auth")
	}
	if len(secured) > 0 && r.HTTP.Auth != "inherit" {
		return fmt.Errorf("protected request must inherit realm auth")
	}
	parameters, _ := pathItem["parameters"].([]any)
	operationParameters, _ := operation["parameters"].([]any)
	parameters = append(slices.Clone(parameters), operationParameters...)
	for _, raw := range parameters {
		p := schemaObject(document, raw)
		if p["required"] != true || p["in"] == "path" {
			continue
		}
		values := r.HTTP.Headers
		if p["in"] == "query" {
			values = r.HTTP.Params
		}
		found := slices.ContainsFunc(values, func(v parameter) bool {
			return !v.Disabled && strings.EqualFold(v.Name, fmt.Sprint(p["name"])) && v.Value != "" && (p["in"] != "query" || v.Type == "query")
		})
		if !found {
			return fmt.Errorf("missing required %v parameter %v", p["in"], p["name"])
		}
	}
	for _, query := range r.HTTP.Params {
		if query.Disabled {
			continue
		}
		if query.Type != "query" || !slices.ContainsFunc(parameters, func(raw any) bool {
			p := schemaObject(document, raw)
			return p["in"] == "query" && p["name"] == query.Name
		}) {
			return fmt.Errorf("undocumented query parameter %q", query.Name)
		}
	}
	body := schemaObject(document, operation["requestBody"])
	if body == nil && r.HTTP.Body.Type != "" {
		return fmt.Errorf("unexpected request body")
	}
	if body != nil {
		content := obj(body["content"])
		if r.HTTP.Body.Type != "json" || content["application/json"] == nil {
			return fmt.Errorf("expected JSON request body")
		}
		// Unquoted runtime numbers are valid Bruno templates but not JSON literals.
		text := regexp.MustCompile(`(:\s*)\{\{[^{}]+\}\}`).ReplaceAllString(r.HTTP.Body.Data, "${1}\"{{dynamicNumber}}\"")
		var example any
		if err := json.Unmarshal([]byte(text), &example); err != nil {
			return fmt.Errorf("invalid JSON example: %w", err)
		}
		if err := checkExample(document, obj(content["application/json"])["schema"], example, "body"); err != nil {
			return err
		}
	}
	responses := obj(operation["responses"])
	if !slices.ContainsFunc(r.Runtime.Assertions, func(a assertion) bool {
		return a.Expression == "res.status" && a.Operator == "eq" && strings.HasPrefix(a.Value, "2") && responses[a.Value] != nil
	}) {
		return fmt.Errorf("missing successful res.status assertion matching OpenAPI")
	}
	return nil
}

// Check example structure without pretending static templates are runtime values.
func checkExample(document object, raw, value any, path string) error {
	schema := schemaObject(document, raw)
	if text, ok := value.(string); ok && strings.Contains(text, "{{") {
		return nil
	}
	switch schema["type"] {
	case "object":
		fields, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		required, _ := schema["required"].([]any)
		for _, name := range required {
			if _, ok := fields[fmt.Sprint(name)]; !ok {
				return fmt.Errorf("%s missing required field %v", path, name)
			}
		}
		properties := obj(schema["properties"])
		for name, child := range fields {
			property, known := properties[name]
			if !known && schema["additionalProperties"] == false {
				return fmt.Errorf("%s unknown field %s", path, name)
			}
			if known {
				if err := checkExample(document, property, child, path+"."+name); err != nil {
					return err
				}
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be an array", path)
		}
		for _, item := range items {
			if err := checkExample(document, schema["items"], item, path+"[]"); err != nil {
				return err
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s must be a string", path)
		}
	case "integer", "number":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("%s must be a number", path)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be a boolean", path)
		}
	}
	if enum, ok := schema["enum"].([]any); ok && !slices.ContainsFunc(enum, func(candidate any) bool { return reflect.DeepEqual(candidate, value) }) {
		return fmt.Errorf("%s is not a documented enum value", path)
	}
	return nil
}

func checkAuthFiles(root *os.Root) []error {
	var problems []error
	for file, token := range map[string]string{"opencollection.yml": "{{accessToken}}", "internal/folder.yml": "{{staffToken}}"} {
		data, err := root.ReadFile(filepath.Join("api-client", file))
		if err != nil {
			problems = append(problems, err)
			continue
		}
		var config object
		if err := yaml.Unmarshal(data, &config); err != nil {
			problems = append(problems, err)
			continue
		}
		auth := obj(obj(config["request"])["auth"])
		if auth["type"] != "bearer" || auth["token"] != token {
			problems = append(problems, fmt.Errorf("%s must use bearer %s", file, token))
		}
	}
	return problems
}

func checkCollection(directory string) (problems []error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return []error{err}
	}
	defer func() {
		if err := root.Close(); err != nil {
			problems = append(problems, err)
		}
	}()
	data, err := root.ReadFile("docs/openapi.json")
	if err != nil {
		return []error{err}
	}
	var document object
	if err := json.Unmarshal(data, &document); err != nil {
		return []error{err}
	}
	type endpoint struct {
		method, path        string
		operation, pathItem object
	}
	endpoints := map[string]endpoint{}
	problems = checkAuthFiles(root)
	for path, raw := range obj(document["paths"]) {
		for method, operation := range obj(raw) {
			if !slices.Contains([]string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}, method) {
				continue
			}
			id, _ := obj(operation)["operationId"].(string)
			if _, exists := endpoints[id]; exists || id == "" {
				problems = append(problems, fmt.Errorf("missing/duplicate operationId %q", id))
			}
			endpoints[id] = endpoint{method, path, obj(operation), obj(raw)}
		}
	}
	seen := map[string]bool{}
	err = fs.WalkDir(root.FS(), "api-client", func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if slices.Contains([]string{"environments", "node_modules", ".git"}, entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() == "folder.yml" {
			data, err := root.ReadFile(file)
			if err != nil {
				return err
			}
			var folder object
			if err := yaml.Unmarshal(data, &folder); err != nil {
				return err
			}
			auth := obj(folder["request"])["auth"]
			if filepath.Base(filepath.Dir(file)) != "internal" && auth != nil && auth != "inherit" {
				problems = append(problems, fmt.Errorf("%s must inherit customer realm auth", file))
			}
			return nil
		}
		if entry.Name() == "opencollection.yml" {
			return nil
		}
		if !slices.Contains([]string{".yml", ".yaml", ".bru"}, filepath.Ext(file)) {
			return nil
		}
		id := strings.TrimSuffix(entry.Name(), filepath.Ext(file))
		e, exists := endpoints[id]
		if !exists || seen[id] {
			problems = append(problems, fmt.Errorf("%s: orphan/duplicate request", file))
			return nil
		}
		seen[id] = true
		data, err := root.ReadFile(file)
		if err != nil {
			return err
		}
		if err := checkRequest(document, e.operation, e.pathItem, e.method, e.path, file, data); err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", file, err))
		}
		return nil
	})
	if err != nil {
		problems = append(problems, err)
	}
	for id := range endpoints {
		if !seen[id] {
			problems = append(problems, fmt.Errorf("missing request for %s", id))
		}
	}
	return problems
}

func TestCollectionContract(t *testing.T) {
	for _, err := range checkCollection("../..") {
		t.Error(err)
	}
}
