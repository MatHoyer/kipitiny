package mail

import (
	"bytes"
	"html/template"
	"net/mail"
	"strings"
)

// Body is the content of an email in kipitiny's layout, rendered as both
// plain text and HTML.
type Body struct {
	Title string
	// Accent colours the bar above the title (#rrggbb).
	Accent     string
	Paragraphs []string
	// Fields are short facts shown as a table (Project: shop).
	Fields []Field
	// Button links to the subject, when there is one.
	Button *Link
	// Footer is small print under everything.
	Footer string
}

type Field struct{ Name, Value string }

type Link struct{ Label, URL string }

// Message renders b for to, with b's title as the subject unless one is
// given.
func (b Body) Message(to []*mail.Address, subject string) (Message, error) {
	if subject == "" {
		subject = b.Title
	}
	if b.Accent == "" {
		b.Accent = "#4f46e5"
	}
	var html bytes.Buffer
	if err := layout.Execute(&html, b); err != nil {
		return Message{}, err
	}
	return Message{To: to, Subject: subject, Text: b.text(), HTML: html.String()}, nil
}

func (b Body) text() string {
	var s strings.Builder
	s.WriteString(b.Title + "\n\n")
	for _, p := range b.Paragraphs {
		s.WriteString(p + "\n\n")
	}
	for _, f := range b.Fields {
		if f.Value != "" {
			s.WriteString(f.Name + ": " + f.Value + "\n")
		}
	}
	if len(b.Fields) > 0 {
		s.WriteString("\n")
	}
	if b.Button != nil {
		s.WriteString(b.Button.Label + ": " + b.Button.URL + "\n\n")
	}
	if b.Footer != "" {
		s.WriteString("-- \n" + b.Footer + "\n")
	}
	return s.String()
}

// layout uses tables and inline styles: most mail clients ignore the rest.
var layout = template.Must(template.New("mail").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"></head>
<body style="margin:0;padding:24px 12px;background:#f4f4f5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;color:#18181b">
<table role="presentation" width="100%" cellspacing="0" cellpadding="0" style="max-width:560px;margin:0 auto;background:#ffffff;border-radius:8px;overflow:hidden">
<tr><td style="height:4px;background:{{.Accent}}"></td></tr>
<tr><td style="padding:24px">
<h1 style="margin:0 0 16px;font-size:18px;line-height:1.4">{{.Title}}</h1>
{{range .Paragraphs}}<p style="margin:0 0 16px;font-size:14px;line-height:1.6;white-space:pre-wrap">{{.}}</p>
{{end}}{{if .Fields}}<table role="presentation" cellspacing="0" cellpadding="0" style="margin:0 0 16px;font-size:14px;line-height:1.6">
{{range .Fields}}{{if .Value}}<tr><td style="padding:2px 16px 2px 0;color:#71717a;vertical-align:top">{{.Name}}</td><td style="padding:2px 0;word-break:break-word">{{.Value}}</td></tr>
{{end}}{{end}}</table>
{{end}}{{with .Button}}<p style="margin:8px 0 16px"><a href="{{.URL}}" style="display:inline-block;padding:10px 16px;border-radius:6px;background:#18181b;color:#ffffff;font-size:14px;font-weight:600;text-decoration:none">{{.Label}}</a></p>
{{end}}</td></tr>
{{if .Footer}}<tr><td style="padding:12px 24px;border-top:1px solid #e4e4e7;font-size:12px;color:#71717a">{{.Footer}}</td></tr>
{{end}}</table>
</body></html>
`))
