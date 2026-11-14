package notifications

import (
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
	texttemplate "text/template"
)

// TemplateRenderer renders notification templates from a trusted directory.
type TemplateRenderer interface {
	Render(name string, data interface{}) (string, error)
}

// FileTemplateRenderer loads templates from a directory using os.OpenInRoot,
// so template names cannot escape the configured directory.
type FileTemplateRenderer struct {
	Dir string
}

func NewFileTemplateRenderer(dir string) FileTemplateRenderer {
	return FileTemplateRenderer{Dir: dir}
}

func (r FileTemplateRenderer) Render(name string, data interface{}) (string, error) {
	if strings.TrimSpace(r.Dir) == "" {
		return "", errors.New("email templates directory is empty")
	}
	if strings.TrimSpace(name) == "" {
		return "", errors.New("email template name is empty")
	}

	file, err := os.OpenInRoot(r.Dir, name)
	if err != nil {
		return "", fmt.Errorf("open email template %q: %w", name, err)
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("read email template %q: %w", name, err)
	}

	var output strings.Builder
	baseName := filepath.Base(name)

	if strings.HasSuffix(name, ".html.tmpl") {
		tmpl, err := htmltemplate.New(baseName).Parse(string(content))
		if err != nil {
			return "", fmt.Errorf("parse HTML email template %q: %w", name, err)
		}
		if err := tmpl.Execute(&output, data); err != nil {
			return "", fmt.Errorf("render HTML email template %q: %w", name, err)
		}
		return output.String(), nil
	}

	tmpl, err := texttemplate.New(baseName).Parse(string(content))
	if err != nil {
		return "", fmt.Errorf("parse text email template %q: %w", name, err)
	}
	if err := tmpl.Execute(&output, data); err != nil {
		return "", fmt.Errorf("render text email template %q: %w", name, err)
	}

	return output.String(), nil
}
