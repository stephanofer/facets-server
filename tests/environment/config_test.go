package environment_test

import (
	"encoding/json/v2"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestEnvironmentLock(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../environment.json")
	if err != nil {
		t.Fatal(err)
	}
	var environment struct {
		Go            string `json:"go"`
		PostgresImage string `json:"postgresImage"`
		GoImage       string `json:"goImage"`
		RyukImage     string `json:"ryukImage"`
	}
	if err := json.Unmarshal(data, &environment); err != nil {
		t.Fatal(err)
	}
	images := []struct {
		name   string
		image  string
		prefix string
	}{
		{name: "postgres", image: environment.PostgresImage, prefix: "postgres:18.6-trixie@"},
		{name: "go", image: environment.GoImage, prefix: "golang:" + environment.Go + "-trixie@"},
		{name: "ryuk", image: environment.RyukImage, prefix: "testcontainers/ryuk:0.14.0@"},
	}
	digest := regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)
	for _, image := range images {
		t.Run(image.name, func(t *testing.T) {
			t.Parallel()
			if !strings.HasPrefix(image.image, image.prefix) || !digest.MatchString(image.image) {
				t.Errorf("image must include the approved version and a SHA-256 digest: %q", image.image)
			}
		})
	}
	compose, err := os.ReadFile("../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(compose), "image: "+environment.PostgresImage) {
		t.Error("Compose and Testcontainers must use the same locked PostgreSQL image")
	}
	module, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(module), "\ngo "+environment.Go+"\n") {
		t.Error("Go module and environment toolchain versions differ")
	}
}
