package deployment

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	maxYAMLDepth = 64
	maxYAMLNodes = 10_000
)

// Parse is an alias for ParseManifest kept for worker/test ergonomics.
func Parse(data []byte) (*Template, []Finding) { return ParseManifest(data) }

// ParseManifest decodes a YAML manifest into the provider-independent Template
// model with strict semantics:
//
//   - bounded input before decoding;
//   - exactly one YAML document;
//   - no aliases/anchors, non-string mapping keys or duplicate keys;
//   - unknown fields rejected at every typed level (driverOptions bodies are
//     validated separately by trusted driver metadata);
//   - camelCase fields.
//
// It never reads the filesystem and never logs or returns raw manifest content.
func ParseManifest(data []byte) (*Template, []Finding) {
	if len(data) == 0 {
		return nil, []Finding{errorFinding("manifest", "MANIFEST_EMPTY", "manifest is required")}
	}
	if len(data) > MaxManifestBytes {
		return nil, []Finding{errorFinding("manifest", "MANIFEST_TOO_LARGE", fmt.Sprintf("manifest exceeds %d bytes", MaxManifestBytes))}
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, []Finding{errorFinding("manifest", "YAML_INVALID", "manifest is not valid YAML")}
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err == nil {
		return nil, []Finding{errorFinding("manifest", "YAML_MULTIPLE_DOCUMENTS", "manifest must contain exactly one YAML document")}
	} else if !errors.Is(err, io.EOF) {
		return nil, []Finding{errorFinding("manifest", "YAML_INVALID", "manifest is not valid YAML")}
	}
	if len(document.Content) == 0 || document.Content[0] == nil {
		return nil, []Finding{errorFinding("manifest", "MANIFEST_EMPTY", "manifest is empty")}
	}

	root := document.Content[0]
	findings := make([]Finding, 0)
	nodesSeen := 0
	inspectYAMLNode(root, "manifest", 0, &nodesSeen, &findings)
	for _, finding := range findings {
		if finding.Severity == SeverityError {
			return nil, findings
		}
	}

	var value any
	if err := root.Decode(&value); err != nil {
		return nil, []Finding{errorFinding("manifest", "YAML_INVALID", "manifest structure is invalid")}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, []Finding{errorFinding("manifest", "YAML_KEY_INVALID", "manifest mapping keys must be strings")}
	}

	var template Template
	strict := json.NewDecoder(bytes.NewReader(raw))
	strict.DisallowUnknownFields()
	if err := strict.Decode(&template); err != nil {
		return nil, findingsFromJSONError(err)
	}
	var trailing any
	if err := strict.Decode(&trailing); err != io.EOF {
		return nil, []Finding{errorFinding("manifest", "YAML_INVALID", "manifest must contain exactly one document")}
	}
	return &template, findings
}

func inspectYAMLNode(node *yaml.Node, fieldPath string, depth int, count *int, findings *[]Finding) {
	if node == nil {
		return
	}
	*count = *count + 1
	if *count > maxYAMLNodes {
		if !hasErrorCode(*findings, "YAML_TOO_COMPLEX") {
			*findings = append(*findings, errorFinding("manifest", "YAML_TOO_COMPLEX", "manifest contains too many YAML nodes"))
		}
		return
	}
	if depth > maxYAMLDepth {
		if !hasErrorCode(*findings, "YAML_TOO_DEEP") {
			*findings = append(*findings, errorFinding("manifest", "YAML_TOO_DEEP", "manifest nesting is too deep"))
		}
		return
	}
	switch node.Kind {
	case yaml.AliasNode:
		*findings = append(*findings, errorFinding(fieldPath, "YAML_ALIAS_UNSUPPORTED", "YAML aliases and anchors are not allowed"))
		return
	case yaml.MappingNode:
		seen := make(map[string]struct{}, len(node.Content)/2)
		for index := 0; index+1 < len(node.Content); index += 2 {
			key := node.Content[index]
			value := node.Content[index+1]
			if key == nil || key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				*findings = append(*findings, errorFinding(fieldPath, "YAML_KEY_INVALID", "manifest mapping keys must be strings"))
				continue
			}
			if key.Value == "<<" {
				*findings = append(*findings, errorFinding(fieldPath, "YAML_ALIAS_UNSUPPORTED", "YAML merge keys are not allowed"))
				continue
			}
			if _, exists := seen[key.Value]; exists {
				*findings = append(*findings, errorFinding(fieldPath+"."+key.Value, "YAML_DUPLICATE_KEY", "duplicate mapping key"))
				continue
			}
			seen[key.Value] = struct{}{}
			inspectYAMLNode(value, fieldPath+"."+key.Value, depth+1, count, findings)
		}
	case yaml.SequenceNode:
		for index, child := range node.Content {
			inspectYAMLNode(child, fmt.Sprintf("%s[%d]", fieldPath, index), depth+1, count, findings)
		}
	}
}

func hasErrorCode(findings []Finding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func findingsFromJSONError(err error) []Finding {
	const unknownPrefix = `json: unknown field "`
	message := err.Error()
	switch {
	case strings.HasPrefix(message, unknownPrefix):
		field := strings.TrimSuffix(strings.TrimPrefix(message, unknownPrefix), `"`)
		if strings.TrimSpace(field) == "" {
			field = "manifest"
		}
		return []Finding{errorFinding("manifest."+field, "YAML_UNKNOWN_FIELD", "unknown manifest field")}
	default:
		var typeError *json.UnmarshalTypeError
		if errors.As(err, &typeError) {
			field := typeError.Field
			if field == "" {
				field = "manifest"
			}
			return []Finding{errorFinding("manifest."+field, "YAML_WRONG_TYPE", "manifest field has the wrong type")}
		}
		return []Finding{errorFinding("manifest", "YAML_INVALID", "manifest fields are invalid")}
	}
}

// UnmarshalJSON accepts canonical string defaults as well as YAML-native
// booleans/integers. Unknown fields are rejected here as well because a custom
// unmarshaller would otherwise bypass the parent decoder's DisallowUnknownFields.
func (p *Parameter) UnmarshalJSON(data []byte) error {
	type wire struct {
		Key      string   `json:"key"`
		Label    string   `json:"label"`
		Type     string   `json:"type"`
		Options  []string `json:"options"`
		Default  any      `json:"default"`
		Required bool     `json:"required"`
		Secret   bool     `json:"secret"`
	}
	var decoded wire
	if err := decodeStrictJSON(data, &decoded); err != nil {
		return err
	}
	defaultValue := ""
	if decoded.Default != nil {
		canonical, err := canonicalScalar(decoded.Default)
		if err != nil {
			return err
		}
		defaultValue = canonical
	}
	*p = Parameter{
		Key:      decoded.Key,
		Label:    decoded.Label,
		Type:     decoded.Type,
		Options:  decoded.Options,
		Default:  defaultValue,
		Required: decoded.Required,
		Secret:   decoded.Secret,
	}
	return nil
}

// UnmarshalJSON accepts both `label` and the documented `display: {label: ...}`
// endpoint form while rejecting unknown fields.
func (e *Endpoint) UnmarshalJSON(data []byte) error {
	type display struct {
		Label string `json:"label"`
	}
	type wire struct {
		Key        string   `json:"key"`
		Protocol   string   `json:"protocol"`
		TargetPort int      `json:"targetPort"`
		Exposure   Exposure `json:"exposure"`
		Label      string   `json:"label"`
		Display    *display `json:"display"`
	}
	var decoded wire
	if err := decodeStrictJSON(data, &decoded); err != nil {
		return err
	}
	label := decoded.Label
	if decoded.Display != nil {
		label = decoded.Display.Label
	}
	*e = Endpoint{
		Key:        decoded.Key,
		Protocol:   decoded.Protocol,
		TargetPort: decoded.TargetPort,
		Exposure:   decoded.Exposure,
		Label:      label,
	}
	return nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

func canonicalScalar(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case bool:
		return strconv.FormatBool(typed), nil
	case json.Number:
		return typed.String(), nil
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), nil
	case int:
		return strconv.Itoa(typed), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	default:
		return "", fmt.Errorf("default must be a string, boolean or integer")
	}
}
