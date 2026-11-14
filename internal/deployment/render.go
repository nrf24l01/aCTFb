package deployment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"text/template"
)

// PreviewRedacted is the value substituted for every secret parameter or
// generated value during preview/diff rendering. It is never a real secret.
const PreviewRedacted = "[REDACTED]"

// RenderInput contains everything needed to render a deployment outside the
// template service. Secret parameter values are supplied by the trusted caller
// (binding/worker) and are never persisted in deployment_template_versions.
type RenderInput struct {
	Template   Template
	Files      map[string][]byte
	Parameters map[string]string
	Generated  map[string]string
}

// RenderedFile is a rendered injectable package file.
type RenderedFile struct {
	Source  string
	Target  string
	Mode    string
	Content []byte
}

// RenderedNode is the provider-independent execution input for one node.
type RenderedNode struct {
	Key       string
	Name      string
	Kind      Kind
	Source    Source
	Resources Resources
	Env       map[string]string
	Files     []RenderedFile
	Bootstrap []byte
	Checks    []Check
}

// RenderedDeployment contains only rendered node inputs; it deliberately has no
// provider credentials, native IDs or public host/port allocations.
type RenderedDeployment struct {
	Nodes []RenderedNode
}

// Render validates parameters and renders every referenced template file with
// strict missing-key semantics and without os/env/file/network functions.
// Recursion is bounded by cancellation and the output budget.
func Render(ctx context.Context, input RenderInput) (*RenderedDeployment, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	data, err := resolveRenderData(input.Template, input.Parameters, input.Generated)
	if err != nil {
		return nil, err
	}
	output := &RenderedDeployment{Nodes: make([]RenderedNode, 0, len(input.Template.Nodes))}
	total := int64(0)
	for nodeIndex, node := range input.Template.Nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rendered := RenderedNode{
			Key:       node.Key,
			Name:      node.Name,
			Kind:      node.Kind,
			Source:    node.Source,
			Resources: node.Resources,
			Env:       make(map[string]string, len(node.Env)),
			Files:     make([]RenderedFile, 0, len(node.Files)),
			Checks:    checksForNode(input.Template.Checks, node.Key),
		}
		for _, env := range node.Env {
			value, err := resolveEnv(node, env, data)
			if err != nil {
				return nil, err
			}
			rendered.Env[env.Name] = value
		}
		for index, file := range node.Files {
			content, exists := input.Files[file.Source]
			if !exists {
				return nil, &ValidationError{Findings: []Finding{
					errorFinding(fmt.Sprintf("nodes[%d].files[%d].source", nodeIndex, index), "FILE_NOT_FOUND", "file is not present in the template package"),
				}}
			}
			renderedContent, err := executeTemplate(ctx, file.Source, content, data, &total)
			if err != nil {
				return nil, err
			}
			rendered.Files = append(rendered.Files, RenderedFile{
				Source: file.Source, Target: file.Target, Mode: file.Mode, Content: renderedContent,
			})
		}
		if node.Bootstrap != nil {
			content, exists := input.Files[node.Bootstrap.CloudInitFile]
			if !exists {
				return nil, &ValidationError{Findings: []Finding{
					errorFinding("nodes.bootstrap.cloudInitFile", "BOOTSTRAP_FILE_NOT_FOUND", "cloud-init file is not present in the template package"),
				}}
			}
			rendered.Bootstrap, err = executeTemplate(ctx, node.Bootstrap.CloudInitFile, content, data, &total)
			if err != nil {
				return nil, err
			}
		}
		output.Nodes = append(output.Nodes, rendered)
	}
	return output, nil
}

func resolveRenderData(manifest Template, parameters, generated map[string]string) (map[string]string, error) {
	data := make(map[string]string, len(parameters)+len(generated))
	declaredParameters := make(map[string]Parameter, len(manifest.Parameters))
	for _, parameter := range manifest.Parameters {
		declaredParameters[parameter.Key] = parameter
		value, supplied := parameters[parameter.Key]
		if !supplied {
			if parameter.Required || parameter.Secret {
				return nil, &ValidationError{Findings: []Finding{
					errorFinding("parameters."+parameter.Key, "TEMPLATE_PARAMETER_MISSING", "required template parameter is missing"),
				}}
			}
			if parameter.Default != "" {
				value = parameter.Default
			}
		}
		if value == "" && (parameter.Required || parameter.Secret) {
			return nil, &ValidationError{Findings: []Finding{
				errorFinding("parameters."+parameter.Key, "TEMPLATE_PARAMETER_INVALID", "required template parameter must not be empty"),
			}}
		}
		if value != "" {
			if err := validateParameterValue(parameter, value); err != nil {
				return nil, &ValidationError{Findings: []Finding{
					errorFinding("parameters."+parameter.Key, "TEMPLATE_PARAMETER_INVALID", err.Error()),
				}}
			}
		}
		data[parameter.Key] = value
	}
	for key := range parameters {
		if _, exists := declaredParameters[key]; !exists {
			return nil, &ValidationError{Findings: []Finding{
				errorFinding("parameters."+key, "TEMPLATE_PARAMETER_UNKNOWN", "parameter is not declared by the template"),
			}}
		}
	}
	declaredGenerated := make(map[string]struct{}, len(manifest.Generated))
	for _, item := range manifest.Generated {
		declaredGenerated[item.Key] = struct{}{}
		value, supplied := generated[item.Key]
		if !supplied {
			return nil, &ValidationError{Findings: []Finding{
				errorFinding("generated."+item.Key, "TEMPLATE_GENERATED_MISSING", "generated value is missing"),
			}}
		}
		data[item.Key] = value
	}
	for key := range generated {
		if _, exists := declaredGenerated[key]; !exists {
			return nil, &ValidationError{Findings: []Finding{
				errorFinding("generated."+key, "TEMPLATE_GENERATED_UNKNOWN", "generated value is not declared by the template"),
			}}
		}
	}
	return data, nil
}

func resolveEnv(node Node, env EnvVar, data map[string]string) (string, error) {
	switch {
	case env.ValueFrom.Generated != nil:
		value, exists := data[*env.ValueFrom.Generated]
		if !exists {
			return "", &ValidationError{Findings: []Finding{
				errorFinding("nodes."+node.Key+".env."+env.Name, "TEMPLATE_GENERATED_MISSING", "generated value is missing"),
			}}
		}
		return value, nil
	case env.ValueFrom.Parameter != nil:
		value, exists := data[*env.ValueFrom.Parameter]
		if !exists {
			return "", &ValidationError{Findings: []Finding{
				errorFinding("nodes."+node.Key+".env."+env.Name, "TEMPLATE_PARAMETER_MISSING", "template parameter is missing"),
			}}
		}
		return value, nil
	case env.ValueFrom.Literal != nil:
		return *env.ValueFrom.Literal, nil
	default:
		return "", &ValidationError{Findings: []Finding{
			errorFinding("nodes."+node.Key+".env."+env.Name, "ENV_SOURCE_ONE_OF", "valueFrom must set exactly one source"),
		}}
	}
}

func checksForNode(checks []Check, nodeKey string) []Check {
	result := make([]Check, 0, len(checks))
	for _, check := range checks {
		if check.Node == nodeKey {
			result = append(result, check)
		}
	}
	return result
}

func executeTemplate(ctx context.Context, name string, source []byte, data map[string]string, total *int64) ([]byte, error) {
	tmpl, err := template.New(name).
		Option("missingkey=error").
		Parse(string(source))
	if err != nil {
		return nil, &ValidationError{Findings: []Finding{
			errorFinding("files."+name, "TEMPLATE_SYNTAX_INVALID", "template syntax is invalid"),
		}}
	}
	writer := &boundedRenderWriter{ctx: ctx, limit: MaxRenderBytes, total: total, totalLimit: MaxRenderTotal}
	if err := tmpl.Execute(writer, data); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		message := "template rendering failed"
		if strings.Contains(err.Error(), "map has no entry for key") {
			message = "template references a parameter or generated value that is not available"
		}
		return nil, &ValidationError{Findings: []Finding{
			errorFinding("files."+name, "TEMPLATE_RENDER_FAILED", message),
		}}
	}
	return writer.buffer.Bytes(), nil
}

type boundedRenderWriter struct {
	ctx        context.Context
	buffer     bytes.Buffer
	limit      int
	total      *int64
	totalLimit int64
	written    int64
}

func (w *boundedRenderWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if w.written+int64(len(p)) > int64(w.limit) {
		return 0, fmt.Errorf("rendered file exceeds limit")
	}
	nextTotal := atomic.AddInt64(w.total, int64(len(p)))
	if nextTotal > w.totalLimit {
		return 0, fmt.Errorf("rendered package exceeds limit")
	}
	w.written += int64(len(p))
	return w.buffer.Write(p)
}

// SampleParameters returns safe preview values for every declared parameter.
// Secret values are always the redacted placeholder.
func SampleParameters(manifest Template) map[string]string {
	values := make(map[string]string, len(manifest.Parameters))
	for _, parameter := range manifest.Parameters {
		if parameter.Secret {
			values[parameter.Key] = PreviewRedacted
			continue
		}
		if parameter.Default != "" {
			values[parameter.Key] = parameter.Default
			continue
		}
		switch parameter.Type {
		case "int":
			values[parameter.Key] = "1"
		case "bool":
			values[parameter.Key] = "false"
		case "choice":
			if len(parameter.Options) > 0 {
				values[parameter.Key] = parameter.Options[0]
			}
		default:
			values[parameter.Key] = "example"
		}
	}
	return values
}

// PreviewGenerated returns a redacted placeholder for every generated value.
func PreviewGenerated(manifest Template) map[string]string {
	values := make(map[string]string, len(manifest.Generated))
	for _, item := range manifest.Generated {
		values[item.Key] = PreviewRedacted
	}
	return values
}
