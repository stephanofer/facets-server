package mail

import (
	"bytes"
	"errors"
	htmltemplate "html/template"
	"io"
	texttemplate "text/template"
)

// Template is parsed once from trusted application-owned source and may be
// rendered concurrently. Never construct template source from user input.
type Template struct {
	subject, text *texttemplate.Template
	html          *htmltemplate.Template
}

func NewTemplate(subject, text, html string) (*Template, error) {
	if subject == "" || (text == "" && html == "") {
		return nil, errors.New("mail template requires subject and body")
	}
	t := &Template{}
	var err error
	t.subject, err = texttemplate.New("subject").Option("missingkey=error").Parse(subject)
	if err != nil {
		return nil, errors.New("invalid mail subject template")
	}
	t.text, err = texttemplate.New("text").Option("missingkey=error").Parse(text)
	if err != nil {
		return nil, errors.New("invalid mail text template")
	}
	t.html, err = htmltemplate.New("html").Option("missingkey=error").Parse(html)
	if err != nil {
		return nil, errors.New("invalid mail html template")
	}
	return t, nil
}

// Render returns content only; the authorized caller supplies recipients.
// HTML variables are automatically escaped. Do not pass template.HTML values
// derived from user input, which would bypass that protection.
func (t *Template) Render(data any) (Message, error) {
	var m Message
	for _, part := range []struct {
		template interface{ Execute(io.Writer, any) error }
		out      *string
		limit    int
	}{{t.subject, &m.Subject, 200}, {t.text, &m.Body, 60000}, {t.html, &m.HTML, 60000}} {
		w := &limitedBuffer{remaining: part.limit}
		if err := part.template.Execute(w, data); err != nil {
			// Template execution errors can contain sensitive dynamic values.
			return Message{}, errors.New("mail template rendering failed")
		}
		*part.out = w.String()
	}
	return m, nil
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	remaining int
}

func (w *limitedBuffer) String() string { return w.buffer.String() }

func (w *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, errors.New("mail template output too large")
	}
	w.remaining -= len(p)
	return w.buffer.Write(p)
}
