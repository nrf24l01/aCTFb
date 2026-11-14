package notifications

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFileTemplateRendererRendersRepositoryTemplates(t *testing.T) {
	renderer := NewFileTemplateRenderer(filepath.Join("..", "..", "email_templates"))
	data := VerificationEmailData{
		Username: "learner",
		Code:     "123456",
		Email:    "learner@example.test",
	}

	subject, err := renderer.Render("verification/subject.tmpl", data)
	if err != nil {
		t.Fatalf("render subject: %v", err)
	}
	if !strings.Contains(subject, "learner") {
		t.Errorf("subject = %q, want username", subject)
	}

	textBody, err := renderer.Render("verification/body.txt.tmpl", data)
	if err != nil {
		t.Fatalf("render text body: %v", err)
	}
	if !strings.Contains(textBody, "123456") || !strings.Contains(textBody, "learner@example.test") {
		t.Errorf("text body = %q, want code and email", textBody)
	}

	htmlBody, err := renderer.Render("verification/body.html.tmpl", data)
	if err != nil {
		t.Fatalf("render HTML body: %v", err)
	}
	if !strings.Contains(htmlBody, "123456") {
		t.Errorf("HTML body = %q, want code", htmlBody)
	}
}

func TestFileTemplateRendererEscapesHTML(t *testing.T) {
	renderer := NewFileTemplateRenderer(filepath.Join("..", "..", "email_templates"))
	data := VerificationEmailData{
		Username: `<script>alert("x")</script>`,
		Code:     "123456",
		Email:    "learner@example.test",
	}

	htmlBody, err := renderer.Render("verification/body.html.tmpl", data)
	if err != nil {
		t.Fatalf("render HTML body: %v", err)
	}
	if strings.Contains(htmlBody, "<script>") {
		t.Errorf("HTML body contains unescaped script: %q", htmlBody)
	}
	if !strings.Contains(htmlBody, "&lt;script&gt;") {
		t.Errorf("HTML body does not contain escaped username: %q", htmlBody)
	}
}

func TestFileTemplateRendererRendersPasswordResetTemplates(t *testing.T) {
	renderer := NewFileTemplateRenderer(filepath.Join("..", "..", "email_templates"))
	data := PasswordResetEmailData{
		Username: "learner",
		Token:    "opaque-reset-token",
		Email:    "learner@example.test",
	}

	subject, err := renderer.Render("password_reset/subject.tmpl", data)
	if err != nil {
		t.Fatalf("render subject: %v", err)
	}
	if !strings.Contains(subject, "learner") {
		t.Errorf("subject = %q, want username", subject)
	}

	textBody, err := renderer.Render("password_reset/body.txt.tmpl", data)
	if err != nil {
		t.Fatalf("render text body: %v", err)
	}
	if !strings.Contains(textBody, data.Token) {
		t.Errorf("text body = %q, want reset token", textBody)
	}
	if strings.Contains(textBody, "http") {
		t.Errorf("text body contains a URL: %q", textBody)
	}

	htmlBody, err := renderer.Render("password_reset/body.html.tmpl", data)
	if err != nil {
		t.Fatalf("render HTML body: %v", err)
	}
	if !strings.Contains(htmlBody, data.Token) {
		t.Errorf("HTML body = %q, want reset token", htmlBody)
	}
	if strings.Contains(htmlBody, "http") {
		t.Errorf("HTML body contains a URL: %q", htmlBody)
	}
}

func TestFileTemplateRendererEscapesPasswordResetHTML(t *testing.T) {
	renderer := NewFileTemplateRenderer(filepath.Join("..", "..", "email_templates"))
	data := PasswordResetEmailData{
		Username: `<script>alert("x")</script>`,
		Token:    `"><script>alert("token")</script>`,
		Email:    "learner@example.test",
	}

	htmlBody, err := renderer.Render("password_reset/body.html.tmpl", data)
	if err != nil {
		t.Fatalf("render HTML body: %v", err)
	}
	if strings.Contains(htmlBody, "<script>") {
		t.Errorf("HTML body contains unescaped script: %q", htmlBody)
	}
	if !strings.Contains(htmlBody, "&lt;script&gt;") {
		t.Errorf("HTML body does not contain escaped username/token: %q", htmlBody)
	}
}

func TestFileTemplateRendererRejectsPathTraversal(t *testing.T) {
	renderer := NewFileTemplateRenderer(filepath.Join("..", "..", "email_templates"))

	if _, err := renderer.Render("../go.mod", VerificationEmailData{}); err == nil {
		t.Fatal("Render() error = nil, want path traversal error")
	}
}

func TestFileTemplateRendererRequiresDirectory(t *testing.T) {
	renderer := NewFileTemplateRenderer("")

	if _, err := renderer.Render("verification/subject.tmpl", VerificationEmailData{}); err == nil {
		t.Fatal("Render() error = nil, want empty directory error")
	}
}
