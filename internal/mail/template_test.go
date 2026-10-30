package mail_test

import (
	"strings"
	"testing"

	"github.com/stephanofer/facets-server/internal/mail"
)

func TestNewTemplate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, subject, text, html string }{
		{"missing subject", "", "body", ""},
		{"missing body", "subject", "", ""},
		{"bad subject", "{{", "body", ""},
		{"bad text", "subject", "{{", ""},
		{"bad html", "subject", "", "{{"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := mail.NewTemplate(tc.subject, tc.text, tc.html); err == nil {
				t.Fatal("invalid template accepted")
			}
		})
	}
}

func TestTemplateRender(t *testing.T) {
	t.Parallel()
	tpl, err := mail.NewTemplate("Hello {{.Name}}", "Code {{.Code}}", "<p>{{.Code}}</p>")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		data    map[string]string
		invalid bool
	}{
		{"escaped", map[string]string{"Name": "Ana", "Code": "<script>"}, false},
		{"missing key", map[string]string{"Name": "Ana"}, true},
		{"bounded subject", map[string]string{"Name": strings.Repeat("x", 201), "Code": "123"}, true},
		{"bounded body", map[string]string{"Name": "Ana", "Code": strings.Repeat("x", 60001)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, err := tpl.Render(tc.data)
			if (err != nil) != tc.invalid {
				t.Fatal(err)
			}
			if !tc.invalid && (m.Subject != "Hello Ana" || m.Body != "Code <script>" || m.HTML != "<p>&lt;script&gt;</p>" || m.To != "") {
				t.Fatal("incorrect rendering")
			}
		})
	}
}
