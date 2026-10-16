package email

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type VerificationEmailData struct {
	Username string
	Code     string
	Email    string
}

func (c *Client) LoadTemplate(name string, data interface{}) (string, error) {
	fileName := fmt.Sprintf("%s.html", name)
	file, err := os.OpenInRoot(c.cfg.TemplatesDir, fileName)
	if err != nil {
		return "", fmt.Errorf("open template: %w", err)
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}

	tmpl, err := template.New(filepath.Base(fileName)).Parse(string(content))
	if err != nil {
		return "", err
	}

	var result strings.Builder
	if err := tmpl.Execute(&result, data); err != nil {
		return "", err
	}

	return result.String(), nil
}

func (c *Client) SendVerificationEmail(ctx context.Context, target_email, code, username string) error {
	templateData := VerificationEmailData{
		Username: username,
		Code:     code,
		Email:    target_email,
	}

	subject, err := c.LoadTemplate("verification/subject.txt.tmpl", templateData)
	if err != nil {
		return fmt.Errorf("load subject template: %w", err)
	}
	http_body, err := c.LoadTemplate("verification/body.html.tmpl", templateData)
	if err != nil {
		return fmt.Errorf("load http template: %w", err)
	}
	text_body, err := c.LoadTemplate("verification/body.txt.tmpl", templateData)
	if err != nil {
		return fmt.Errorf("load template: %w", err)
	}

	return c.SendEmail(ctx, target_email, subject, http_body, text_body)
}
