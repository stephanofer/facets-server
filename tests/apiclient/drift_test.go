package apiclient_test

import (
	"encoding/json/v2"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func copyCollection(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	root := openTestRoot(t, directory)
	source := openTestRoot(t, "../..")
	for _, folder := range []string{"api-client", "docs"} {
		err := fs.WalkDir(source.FS(), folder, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if folder == "docs" && path != "docs/openapi.json" {
				return nil
			}
			if folder == "api-client" && filepath.Ext(path) != ".yml" {
				return nil
			}
			data, err := source.ReadFile(path)
			if err != nil {
				return err
			}
			if err := root.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				return err
			}
			return root.WriteFile(path, data, 0o600)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

func openTestRoot(t *testing.T, directory string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	return root
}

func replaceFile(t *testing.T, directory, path, old, new string) {
	t.Helper()
	root := openTestRoot(t, directory)
	data, err := root.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("fixture does not contain %q", old)
	}
	if err := root.WriteFile(path, []byte(strings.Replace(string(data), old, new, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionRejectsDrift(t *testing.T) {
	for _, tc := range []struct {
		name, file, old, new, want string
	}{
		{"method", "api-client/spaces/edit-space.yml", "method: PUT", "method: PATCH", "expected PUT"},
		{"path", "api-client/spaces/edit-space.yml", "/v1/spaces/", "/v1/space/", "expected PUT"},
		{"literal path parameter", "api-client/spaces/edit-space.yml", "{{spaceId}}", "fixed-id", "expected PUT"},
		{"public auth", "api-client/auth/customer-login.yml", "method: POST", "method: POST\n  auth: inherit", "public request"},
		{"protected auth", "api-client/spaces/edit-space.yml", "auth: inherit", "auth: none", "protected request"},
		{"header", "api-client/spaces/edit-space.yml", "name: Idempotency-Key", "name: Wrong", "missing required header"},
		{"query", "api-client/spaces/list-spaces.yml", "method: GET", "method: GET\n  params:\n    - name: wrong\n      value: example\n      type: query", "undocumented query parameter"},
		{"body field", "api-client/spaces/edit-space.yml", `"version": {{spaceVersion}}`, `"wrong": {{spaceVersion}}`, "missing required field version"},
		{"null body", "api-client/spaces/edit-space.yml", `"version": {{spaceVersion}}`, `"version": null`, "must be a number"},
		{"enum", "api-client/spaces/edit-space.yml", `"purpose": "family"`, `"purpose": "unknown"`, "enum value"},
		{"staff realm", "api-client/internal/folder.yml", "{{staffToken}}", "{{accessToken}}", "must use bearer"},
		{"customer realm", "api-client/opencollection.yml", "token: \"{{accessToken}}\"", "token: \"{{staffToken}}\"", "must use bearer"},
		{"nested field", "api-client/spaces/edit-space.yml", `"currency": "USD",`, `"wrong": "USD",`, "missing required field currency"},
		{"JSON", "api-client/spaces/edit-space.yml", `"version": {{spaceVersion}}`, `"version": broken`, "invalid JSON"},
		{"status", "api-client/spaces/edit-space.yml", `value: "200"`, `value: "201"`, "success"},
		{"name", "api-client/spaces/edit-space.yml", "name: edit-space", "name: wrong", "operationId"},
		{"YAML", "api-client/spaces/edit-space.yml", "info:", "info: [", "invalid YAML"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := copyCollection(t)
			replaceFile(t, root, tc.file, tc.old, tc.new)
			assertProblem(t, checkCollection(root), tc.want)
		})
	}
}

func assertProblem(t *testing.T, problems []error, want string) {
	t.Helper()
	for _, err := range problems {
		if strings.Contains(err.Error(), want) {
			return
		}
	}
	t.Fatalf("expected %q, got %v", want, problems)
}

func TestCollectionEndpointLifecycle(t *testing.T) {
	for _, name := range []string{"missing", "orphan", "duplicate", "added endpoint", "deleted endpoint"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := copyCollection(t)
			files := openTestRoot(t, root)
			file := "api-client/health/liveness.yml"
			want := ""
			switch name {
			case "missing":
				if err := files.Remove(file); err != nil {
					t.Fatal(err)
				}
				want = "missing request"
			case "orphan", "duplicate":
				data, err := files.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				dest := "api-client/health/unknown.yml"
				if name == "duplicate" {
					dest = "api-client/other/liveness.yml"
					if err := files.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
						t.Fatal(err)
					}
				}
				if err := files.WriteFile(dest, data, 0o600); err != nil {
					t.Fatal(err)
				}
				want = "orphan/duplicate"
			default:
				file := "docs/openapi.json"
				data, err := files.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				var doc object
				if err := json.Unmarshal(data, &doc); err != nil {
					t.Fatal(err)
				}
				paths := obj(doc["paths"])
				switch name {
				case "added endpoint":
					paths["/new"] = object{"get": object{"operationId": "new-endpoint"}}
					want = "missing request for new-endpoint"
				case "deleted endpoint":
					delete(paths, "/v1/health/live")
					want = "orphan/duplicate"
				}
				data, err = json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				if err := files.WriteFile(file, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			assertProblem(t, checkCollection(root), want)
		})
	}
}
