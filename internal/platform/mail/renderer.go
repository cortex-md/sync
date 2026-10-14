package mail

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	"strings"
	texttemplate "text/template"

	"github.com/cortexnotes/cortex-sync/internal/domain"
)

//go:embed templates
var templateFS embed.FS

type Rendered struct {
	Subject string
	Text    string
	HTML    string
}

type Renderer struct {
	text      map[string]*texttemplate.Template
	html      map[string]*htmltemplate.Template
	publicURL string
}

func (r *Renderer) WithPublicURL(publicURL string) *Renderer {
	r.publicURL = strings.TrimRight(publicURL, "/")
	return r
}

var locales = []string{"en", "pt-BR"}

var templateNames = []string{
	domain.EmailTemplateVerifyEmail,
	domain.EmailTemplateEmailChange,
	domain.EmailTemplateEmailChanged,
	domain.EmailTemplatePasswordReset,
	domain.EmailTemplatePasswordChanged,
	domain.EmailTemplateVaultInvite,
	domain.EmailTemplateInviteDeclined,
}

func NewRenderer() (*Renderer, error) {
	r := &Renderer{
		text: make(map[string]*texttemplate.Template),
		html: make(map[string]*htmltemplate.Template),
	}
	for _, locale := range locales {
		for _, name := range templateNames {
			path := fmt.Sprintf("templates/%s/%s.tmpl", locale, name)
			textTmpl, err := texttemplate.New(name).Option("missingkey=zero").ParseFS(templateFS, path)
			if err != nil {
				return nil, fmt.Errorf("parsing text template %s: %w", path, err)
			}
			htmlTmpl, err := htmltemplate.New(name).Option("missingkey=zero").ParseFS(templateFS, "templates/layout.html", path)
			if err != nil {
				return nil, fmt.Errorf("parsing html template %s: %w", path, err)
			}
			key := locale + "/" + name
			r.text[key] = textTmpl
			r.html[key] = htmlTmpl
		}
	}
	return r, nil
}

func (r *Renderer) Render(name string, locale string, data map[string]string) (*Rendered, error) {
	supportedLocale := domain.SupportedLocale(locale)
	key := supportedLocale + "/" + name
	textTmpl, ok := r.text[key]
	if !ok {
		return nil, fmt.Errorf("unknown email template %q", name)
	}

	htmlData := make(map[string]string, len(data)+1)
	for k, v := range data {
		htmlData[k] = v
	}
	htmlData["Locale"] = supportedLocale
	htmlData["PublicURL"] = r.publicURL

	var subject, text, html bytes.Buffer
	if err := textTmpl.ExecuteTemplate(&subject, "subject", data); err != nil {
		return nil, err
	}
	if err := textTmpl.ExecuteTemplate(&text, "text", data); err != nil {
		return nil, err
	}
	if err := r.html[key].ExecuteTemplate(&html, "html", htmlData); err != nil {
		return nil, err
	}

	return &Rendered{
		Subject: strings.TrimSpace(subject.String()),
		Text:    strings.TrimSpace(text.String()),
		HTML:    html.String(),
	}, nil
}
