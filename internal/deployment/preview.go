package deployment

import (
	"context"
	"fmt"
	"strings"
)

// PreviewNode is a dry-run node plan. No provider call is made.
type PreviewNode struct {
	Key      string
	Name     string
	Kind     Kind
	Action   string
	Findings []Finding
}

// PreviewEndpoint is a declared endpoint. Public host/port allocation is absent
// by design; it belongs to trusted integration policy at runtime.
type PreviewEndpoint struct {
	Key      string
	NodeKey  string
	Protocol string
	Exposure Exposure
}

// Preview is a secret-free dry-run result.
type Preview struct {
	Nodes     []PreviewNode
	Endpoints []PreviewEndpoint
	Warnings  []string
}

// BuildPreview parses nothing itself; it validates an already parsed template,
// renders referenced templates with redacted sample values, and returns a plan.
// It never invokes Apply and never persists or returns rendered file content.
func BuildPreview(ctx context.Context, template *Template, files map[string][]byte, provider DriverMetadataProvider) (*Preview, ValidationResult, error) {
	result, err := ValidateTemplate(ctx, template, files, provider)
	if err != nil {
		return nil, result, err
	}
	preview := &Preview{
		Nodes:     make([]PreviewNode, 0, len(template.Nodes)),
		Endpoints: make([]PreviewEndpoint, 0),
		Warnings:  make([]string, 0),
	}
	for index, node := range template.Nodes {
		preview.Nodes = append(preview.Nodes, PreviewNode{
			Key: node.Key, Name: node.Name, Kind: node.Kind, Action: "CREATE",
			Findings: nodeFindings(result.Findings, index),
		})
		for _, endpoint := range node.Endpoints {
			preview.Endpoints = append(preview.Endpoints, PreviewEndpoint{
				Key: endpoint.Key, NodeKey: node.Key, Protocol: endpoint.Protocol, Exposure: endpoint.Exposure,
			})
		}
	}
	for _, finding := range result.Findings {
		if finding.Severity == SeverityWarning {
			preview.Warnings = append(preview.Warnings, truncateMessage(finding.Message))
		}
	}
	if result.HasErrors() {
		return preview, result, nil
	}
	if _, err := Render(ctx, RenderInput{
		Template:   *template,
		Files:      files,
		Parameters: SampleParameters(*template),
		Generated:  PreviewGenerated(*template),
	}); err != nil {
		validation, ok := AsValidationError(err)
		if !ok {
			return preview, result, err
		}
		result.Findings = append(result.Findings, validation.Findings...)
		result.Valid = false
		for index := range preview.Nodes {
			preview.Nodes[index].Findings = nodeFindings(result.Findings, index)
		}
		return preview, result, nil
	}
	return preview, result, nil
}

func nodeFindings(findings []Finding, index int) []Finding {
	prefix := fmt.Sprintf("nodes[%d]", index)
	result := make([]Finding, 0)
	for _, finding := range findings {
		if finding.Path == prefix || strings.HasPrefix(finding.Path, prefix+".") || strings.HasPrefix(finding.Path, prefix+"[") {
			result = append(result, finding)
		}
	}
	return result
}

// ParameterSummary is the secret-safe parameter metadata exposed by published
// metadata reads. Secret defaults and samples are always omitted.
type ParameterSummary struct {
	Key      string
	Label    string
	Type     string
	Options  []string
	Default  *string
	Required bool
	Secret   bool
}

// PublishedMetadata is the secret- and file-free projection used by core
// page/read models. It never includes package file contents or rendered data.
type PublishedMetadata struct {
	UUID        string
	TemplateID  string
	Version     int
	Digest      string
	Name        string
	Slug        string
	Description string
	Parameters  []ParameterSummary
	Nodes       []PreviewNode
}

// MetadataForVersion projects published metadata without files, manifests
// blobs, secret defaults or rendered values.
func MetadataForVersion(version TemplateVersion) PublishedMetadata {
	parameters := make([]ParameterSummary, 0, len(version.Manifest.Parameters))
	for _, parameter := range version.Manifest.Parameters {
		summary := ParameterSummary{
			Key: parameter.Key, Label: parameter.Label, Type: parameter.Type,
			Options: append([]string(nil), parameter.Options...), Required: parameter.Required, Secret: parameter.Secret,
		}
		if !parameter.Secret && parameter.Default != "" {
			value := parameter.Default
			summary.Default = &value
		}
		parameters = append(parameters, summary)
	}
	nodes := make([]PreviewNode, 0, len(version.Manifest.Nodes))
	for _, node := range version.Manifest.Nodes {
		nodes = append(nodes, PreviewNode{Key: node.Key, Name: node.Name, Kind: node.Kind, Action: "CREATE"})
	}
	return PublishedMetadata{
		UUID: version.UUID.String(), TemplateID: version.TemplateID.String(), Version: version.Version,
		Digest: version.Digest, Name: version.Manifest.Metadata.Name, Slug: version.Manifest.Metadata.Slug,
		Description: version.Manifest.Metadata.Description, Parameters: parameters, Nodes: nodes,
	}
}
