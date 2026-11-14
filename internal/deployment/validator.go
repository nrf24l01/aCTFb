package deployment

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

var (
	metadataSlugPattern     = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	keyPattern              = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	nodeKeyPattern          = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	envNamePattern          = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
	endpointKeyPattern      = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	ociPinnedPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[0-9a-f]{64}$`)
	vmLogicalRefPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,254}$`)
	secretEnvNamePattern    = regexp.MustCompile(`(?i)(^|_)(TOKEN|SECRET|PASSWORD|PASSWD|API_?KEY|ACCESS_?KEY|PRIVATE_?KEY|CREDENTIAL|CREDENTIALS|AUTH)(_|$)`)
	secretJSONKeyPattern    = regexp.MustCompile(`(?i)^(password|passwd|secret|token|api_?key|access_?key|private_?key|credential|credentials)$`)
	inlineCredentialPattern = regexp.MustCompile(
		`AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{20,}|glpat-[A-Za-z0-9_-]{20,}|xox[baprs]-[A-Za-z0-9-]{10,}|-----BEGIN [A-Z ]*PRIVATE KEY-----`,
	)
	knownDrivers = map[string]Kind{
		DriverProxmox:    KindVM,
		DriverKubeVirt:   KindVM,
		DriverDocker:     KindContainer,
		DriverPodman:     KindContainer,
		DriverKubernetes: KindContainer,
	}
)

// ValidateTemplate runs all provider-independent checks plus trusted driver
// metadata/capability checks. It performs no provider mutation and never
// renders to a file. A nil provider is allowed for offline tests; then driver
// metadata checks are reported as warnings rather than silently passing.
func ValidateTemplate(ctx context.Context, template *Template, files map[string][]byte, provider DriverMetadataProvider) (ValidationResult, error) {
	result := ValidationResult{Findings: make([]Finding, 0)}
	if template == nil {
		result.Findings = append(result.Findings, errorFinding("manifest", "MANIFEST_EMPTY", "manifest is required"))
		return result, nil
	}
	validateMetadata(template, &result.Findings)
	parameters := validateParameters(template, &result.Findings)
	generated := validateGenerated(template, parameters, &result.Findings)
	validateNodes(template, files, parameters, generated, &result.Findings)

	metadata := make(map[string]DriverMetadata)
	if provider != nil {
		list, err := provider.ListDriverMetadata(ctx)
		if err != nil {
			return result, fmt.Errorf("deployment: read driver metadata: %w", err)
		}
		for _, item := range list {
			if item.Driver == "" {
				continue
			}
			metadata[item.Driver] = item
		}
	}
	validateDriverOptionsAndCapabilities(ctx, template, provider, metadata, &result.Findings)
	if hasMixedKinds(template) {
		result.Findings = append(result.Findings, errorFinding("nodes", "MIXED_KIND_UNSUPPORTED", "mixed VM and CONTAINER nodes are rejected until a supported inter-node network policy exists"))
	}
	result.Valid = !result.HasErrors()
	return result, nil
}

func validateMetadata(template *Template, findings *[]Finding) {
	if template.APIVersion != APIVersion {
		*findings = append(*findings, errorFinding("apiVersion", "API_VERSION_UNSUPPORTED", "apiVersion must be "+APIVersion))
	}
	if template.Kind != KindName {
		*findings = append(*findings, errorFinding("kind", "KIND_UNSUPPORTED", "kind must be "+KindName))
	}
	metadata := template.Metadata
	if strings.TrimSpace(metadata.Name) == "" || len(metadata.Name) > 128 {
		*findings = append(*findings, errorFinding("metadata.name", "METADATA_NAME_INVALID", "name must be between 1 and 128 characters"))
	}
	if !metadataSlugPattern.MatchString(metadata.Slug) || len(metadata.Slug) > 128 {
		*findings = append(*findings, errorFinding("metadata.slug", "METADATA_SLUG_INVALID", "slug must be lowercase alphanumeric words separated by hyphens"))
	}
	if len(metadata.Description) > MaxStringLength {
		*findings = append(*findings, errorFinding("metadata.description", "METADATA_DESCRIPTION_TOO_LONG", fmt.Sprintf("description exceeds %d characters", MaxStringLength)))
	}
	if containsControl(metadata.Name) || containsControl(metadata.Description) {
		*findings = append(*findings, errorFinding("metadata", "METADATA_CONTROL_CHARACTER", "metadata must not contain control characters"))
	}
}

func validateParameters(template *Template, findings *[]Finding) map[string]Parameter {
	parameters := make(map[string]Parameter, len(template.Parameters))
	if len(template.Parameters) > MaxParameters {
		*findings = append(*findings, errorFinding("parameters", "PARAMETER_LIMIT_EXCEEDED", fmt.Sprintf("at most %d parameters are allowed", MaxParameters)))
	}
	for index, parameter := range template.Parameters {
		fieldPath := fmt.Sprintf("parameters[%d]", index)
		if !keyPattern.MatchString(parameter.Key) {
			*findings = append(*findings, errorFinding(fieldPath+".key", "PARAMETER_KEY_INVALID", "parameter key must be lowercase letters, digits and underscores"))
			continue
		}
		if _, exists := parameters[parameter.Key]; exists {
			*findings = append(*findings, errorFinding(fieldPath+".key", "PARAMETER_DUPLICATE_KEY", "parameter key must be unique"))
			continue
		}
		if strings.TrimSpace(parameter.Label) == "" || len(parameter.Label) > 128 {
			*findings = append(*findings, errorFinding(fieldPath+".label", "PARAMETER_LABEL_INVALID", "label must be between 1 and 128 characters"))
		}
		switch parameter.Type {
		case "string", "text", "int", "bool", "choice":
		default:
			*findings = append(*findings, errorFinding(fieldPath+".type", "PARAMETER_TYPE_INVALID", "type must be string, text, int, bool or choice"))
		}
		if parameter.Type != "choice" && len(parameter.Options) > 0 {
			*findings = append(*findings, errorFinding(fieldPath+".options", "PARAMETER_OPTIONS_UNEXPECTED", "options are only valid for choice parameters"))
		}
		if parameter.Type == "choice" {
			if len(parameter.Options) < 2 || len(parameter.Options) > 64 {
				*findings = append(*findings, errorFinding(fieldPath+".options", "PARAMETER_OPTIONS_INVALID", "choice parameters require between 2 and 64 options"))
			}
			seenOptions := make(map[string]struct{}, len(parameter.Options))
			for optionIndex, option := range parameter.Options {
				if strings.TrimSpace(option) == "" || len(option) > 128 {
					*findings = append(*findings, errorFinding(fmt.Sprintf("%s.options[%d]", fieldPath, optionIndex), "PARAMETER_OPTION_INVALID", "choice option must be between 1 and 128 characters"))
					continue
				}
				if _, exists := seenOptions[option]; exists {
					*findings = append(*findings, errorFinding(fmt.Sprintf("%s.options[%d]", fieldPath, optionIndex), "PARAMETER_OPTION_DUPLICATE", "choice options must be unique"))
				}
				seenOptions[option] = struct{}{}
			}
		}
		if parameter.Secret && parameter.Default != "" {
			*findings = append(*findings, errorFinding(fieldPath+".default", "PARAMETER_SECRET_DEFAULT_FORBIDDEN", "secret parameters must not carry a plaintext default"))
		}
		if parameter.Default != "" {
			if inlineCredentialPattern.MatchString(parameter.Default) {
				*findings = append(*findings, errorFinding(fieldPath+".default", "PARAMETER_CREDENTIAL_INLINE", "inline provider credentials are not allowed in a template"))
			}
			if err := validateParameterValue(parameter, parameter.Default); err != nil {
				*findings = append(*findings, errorFinding(fieldPath+".default", "PARAMETER_DEFAULT_INVALID", err.Error()))
			}
		}
		parameters[parameter.Key] = parameter
	}
	return parameters
}

func validateParameterValue(parameter Parameter, value string) error {
	switch parameter.Type {
	case "string":
		if len(value) > MAXParameterValueLength {
			return fmt.Errorf("value exceeds %d characters", MAXParameterValueLength)
		}
	case "text":
		if len(value) > MaxStringLength {
			return fmt.Errorf("value exceeds %d characters", MaxStringLength)
		}
	case "int":
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return fmt.Errorf("value must be an integer")
		}
	case "bool":
		if value != "true" && value != "false" {
			return fmt.Errorf("value must be true or false")
		}
	case "choice":
		for _, option := range parameter.Options {
			if value == option {
				return nil
			}
		}
		return fmt.Errorf("value must be one of the declared options")
	}
	return nil
}

const MAXParameterValueLength = 4096

func validateGenerated(template *Template, parameters map[string]Parameter, findings *[]Finding) map[string]GeneratedValue {
	generated := make(map[string]GeneratedValue, len(template.Generated))
	if len(template.Generated) > MaxGenerated {
		*findings = append(*findings, errorFinding("generated", "GENERATED_LIMIT_EXCEEDED", fmt.Sprintf("at most %d generated values are allowed", MaxGenerated)))
	}
	for index, item := range template.Generated {
		fieldPath := fmt.Sprintf("generated[%d]", index)
		if !keyPattern.MatchString(item.Key) {
			*findings = append(*findings, errorFinding(fieldPath+".key", "GENERATED_KEY_INVALID", "generated key must be lowercase letters, digits and underscores"))
			continue
		}
		if _, exists := parameters[item.Key]; exists {
			*findings = append(*findings, errorFinding(fieldPath+".key", "GENERATED_KEY_CONFLICT", "generated key must not collide with a parameter key"))
			continue
		}
		if _, exists := generated[item.Key]; exists {
			*findings = append(*findings, errorFinding(fieldPath+".key", "GENERATED_DUPLICATE_KEY", "generated key must be unique"))
			continue
		}
		switch item.Kind {
		case "uuid", "password", "hex", "base64":
			if item.Length < MinGeneratedLength || item.Length > MaxGeneratedLength {
				*findings = append(*findings, errorFinding(fieldPath+".length", "GENERATED_LENGTH_INVALID", fmt.Sprintf("length must be between %d and %d", MinGeneratedLength, MaxGeneratedLength)))
			}
		default:
			*findings = append(*findings, errorFinding(fieldPath+".kind", "GENERATED_KIND_INVALID", "kind must be uuid, password, hex or base64"))
		}
		generated[item.Key] = item
	}
	return generated
}

func validateNodes(template *Template, files map[string][]byte, parameters map[string]Parameter, generated map[string]GeneratedValue, findings *[]Finding) {
	if len(template.Nodes) == 0 {
		*findings = append(*findings, errorFinding("nodes", "NODES_REQUIRED", "at least one node is required"))
		return
	}
	if len(template.Nodes) > MaxNodes {
		*findings = append(*findings, errorFinding("nodes", "NODE_LIMIT_EXCEEDED", fmt.Sprintf("at most %d nodes are allowed", MaxNodes)))
	}
	nodeKeys := make(map[string]struct{}, len(template.Nodes))
	checkEndpoints := make(map[string]map[string]struct{}, len(template.Nodes))
	for index, node := range template.Nodes {
		fieldPath := fmt.Sprintf("nodes[%d]", index)
		if !nodeKeyPattern.MatchString(node.Key) || len(node.Key) > 64 {
			*findings = append(*findings, errorFinding(fieldPath+".key", "NODE_KEY_INVALID", "node key must be lowercase alphanumeric words separated by hyphens"))
			continue
		}
		if _, exists := nodeKeys[node.Key]; exists {
			*findings = append(*findings, errorFinding(fieldPath+".key", "NODE_DUPLICATE_KEY", "node key must be unique"))
			continue
		}
		nodeKeys[node.Key] = struct{}{}
		if strings.TrimSpace(node.Name) == "" || len(node.Name) > 128 || containsControl(node.Name) {
			*findings = append(*findings, errorFinding(fieldPath+".name", "NODE_NAME_INVALID", "node name must be between 1 and 128 characters without control characters"))
		}
		if node.Kind != KindVM && node.Kind != KindContainer {
			*findings = append(*findings, errorFinding(fieldPath+".kind", "NODE_KIND_INVALID", "kind must be VM or CONTAINER"))
		}
		validateSource(node, fieldPath, findings)
		validateResources(node, fieldPath, findings)
		endpoints := validateNodeEndpoints(node, fieldPath, findings)
		checkEndpoints[node.Key] = endpoints
		validateEnv(node, fieldPath, parameters, generated, findings)
		validateNodeFiles(node, fieldPath, files, findings)
		validateDriverOptionSyntax(node, fieldPath, findings)
	}
	validateChecks(template, nodeKeys, checkEndpoints, findings)
	validateLifecycle(template, findings)
	for _, filePath := range sortedFilePaths(files) {
		if inlineCredentialPattern.Match(files[filePath]) {
			*findings = append(*findings, errorFinding("files."+filePath, "FILE_CREDENTIAL_INLINE", "package files must not embed provider credentials"))
		}
	}
}

func validateSource(node Node, fieldPath string, findings *[]Finding) {
	field := fieldPath + ".source"
	switch node.Kind {
	case KindContainer:
		if node.Source.Type != SourceOCIImage {
			*findings = append(*findings, errorFinding(field+".type", "SOURCE_TYPE_UNSUPPORTED", "CONTAINER nodes require source type OCI_IMAGE"))
		}
		if !ociPinnedPattern.MatchString(node.Source.Ref) {
			*findings = append(*findings, errorFinding(field+".ref", "SOURCE_REF_NOT_PINNED", "OCI_IMAGE ref must be pinned as image@sha256:<64 lowercase hex>"))
			validateSourceRefLength(node.Source.Ref, field, findings)
		}
	case KindVM:
		if node.Source.Type != SourceVMTemplate && node.Source.Type != SourceVMImage {
			*findings = append(*findings, errorFinding(field+".type", "SOURCE_TYPE_UNSUPPORTED", "VM nodes require source type VM_TEMPLATE or VM_IMAGE"))
		}
		if !vmLogicalRefPattern.MatchString(node.Source.Ref) || strings.Contains(node.Source.Ref, "..") || strings.HasPrefix(node.Source.Ref, "/") {
			*findings = append(*findings, errorFinding(field+".ref", "SOURCE_REF_INVALID", "VM source ref must be a stable logical name"))
			validateSourceRefLength(node.Source.Ref, field, findings)
		}
		if isAllDigits(node.Source.Ref) || strings.Contains(strings.ToLower(node.Source.Ref), "vmid") {
			*findings = append(*findings, errorFinding(field+".ref", "SOURCE_REF_NATIVE_VMID", "VM source ref must be a logical name, not a native VMID"))
		}
	default:
		return
	}
	if inlineCredentialPattern.MatchString(node.Source.Ref) || strings.Contains(strings.ToLower(node.Source.Ref), "replace_with") ||
		strings.Contains(strings.ToLower(node.Source.Ref), "placeholder") {
		*findings = append(*findings, errorFinding(field+".ref", "SOURCE_REF_PLACEHOLDER", "source ref must not contain a placeholder or inline credential"))
	}
	if node.Kind == KindContainer && isZeroSHA256Ref(node.Source.Ref) {
		*findings = append(*findings, errorFinding(field+".ref", "SOURCE_REF_PLACEHOLDER", "source ref must not use an all-zero placeholder digest"))
	}
}

func isZeroSHA256Ref(reference string) bool {
	const marker = "sha256:"
	index := strings.LastIndex(reference, marker)
	if index < 0 || len(reference[index+len(marker):]) != 64 {
		return false
	}
	for _, r := range reference[index+len(marker):] {
		if r != '0' {
			return false
		}
	}
	return true
}

func validateSourceRefLength(reference, field string, findings *[]Finding) {
	if reference == "" {
		*findings = append(*findings, errorFinding(field+".ref", "SOURCE_REF_REQUIRED", "source ref is required"))
		return
	}
	if len(reference) > 512 {
		*findings = append(*findings, errorFinding(field+".ref", "SOURCE_REF_TOO_LONG", "source ref exceeds 512 characters"))
	}
}

func validateResources(node Node, fieldPath string, findings *[]Finding) {
	field := fieldPath + ".resources"
	if node.Resources.CPUMillicores < MinCPUMillicores || node.Resources.CPUMillicores > MaxCPUMillicores {
		*findings = append(*findings, errorFinding(field+".cpuMillicores", "RESOURCE_BOUNDS", fmt.Sprintf("cpuMillicores must be between %d and %d", MinCPUMillicores, MaxCPUMillicores)))
	}
	if node.Resources.MemoryMiB < MinMemoryMiB || node.Resources.MemoryMiB > MaxMemoryMiB {
		*findings = append(*findings, errorFinding(field+".memoryMiB", "RESOURCE_BOUNDS", fmt.Sprintf("memoryMiB must be between %d and %d", MinMemoryMiB, MaxMemoryMiB)))
	}
	if node.Resources.DiskMiB < MinDiskMiB || node.Resources.DiskMiB > MaxDiskMiB {
		*findings = append(*findings, errorFinding(field+".diskMiB", "RESOURCE_BOUNDS", fmt.Sprintf("diskMiB must be between %d and %d", MinDiskMiB, MaxDiskMiB)))
	}
}

func validateNodeEndpoints(node Node, fieldPath string, findings *[]Finding) map[string]struct{} {
	keys := make(map[string]struct{}, len(node.Endpoints))
	if len(node.Endpoints) > MaxEndpoints {
		*findings = append(*findings, errorFinding(fieldPath+".endpoints", "ENDPOINT_LIMIT_EXCEEDED", fmt.Sprintf("at most %d endpoints are allowed per node", MaxEndpoints)))
	}
	for index, endpoint := range node.Endpoints {
		field := fmt.Sprintf("%s.endpoints[%d]", fieldPath, index)
		if !endpointKeyPattern.MatchString(endpoint.Key) {
			*findings = append(*findings, errorFinding(field+".key", "ENDPOINT_KEY_INVALID", "endpoint key contains unsupported characters"))
		}
		if _, exists := keys[endpoint.Key]; exists {
			*findings = append(*findings, errorFinding(field+".key", "ENDPOINT_DUPLICATE_KEY", "endpoint key must be unique within a node"))
		}
		keys[endpoint.Key] = struct{}{}
		switch strings.ToUpper(endpoint.Protocol) {
		case "HTTP", "HTTPS", "TCP", "UDP", "SSH":
		default:
			*findings = append(*findings, errorFinding(field+".protocol", "ENDPOINT_PROTOCOL_INVALID", "protocol must be HTTP, HTTPS, TCP, UDP or SSH"))
		}
		if endpoint.TargetPort < 1 || endpoint.TargetPort > 65535 {
			*findings = append(*findings, errorFinding(field+".targetPort", "ENDPOINT_PORT_INVALID", "targetPort must be between 1 and 65535"))
		}
		if endpoint.Exposure != ExposurePublic && endpoint.Exposure != ExposurePrivate {
			*findings = append(*findings, errorFinding(field+".exposure", "ENDPOINT_EXPOSURE_INVALID", "exposure must be PUBLIC or PRIVATE"))
		}
		if len(endpoint.Label) > 128 || containsControl(endpoint.Label) {
			*findings = append(*findings, errorFinding(field+".label", "ENDPOINT_LABEL_INVALID", "label must be at most 128 characters without control characters"))
		}
	}
	return keys
}

func validateEnv(node Node, fieldPath string, parameters map[string]Parameter, generated map[string]GeneratedValue, findings *[]Finding) {
	if len(node.Env) > 0 && node.Kind != KindContainer {
		*findings = append(*findings, errorFinding(fieldPath+".env", "ENV_UNSUPPORTED", "env is only supported on CONTAINER nodes"))
		return
	}
	if len(node.Env) > MaxEnvPerNode {
		*findings = append(*findings, errorFinding(fieldPath+".env", "ENV_LIMIT_EXCEEDED", fmt.Sprintf("at most %d environment variables are allowed", MaxEnvPerNode)))
	}
	names := make(map[string]struct{}, len(node.Env))
	for index, env := range node.Env {
		field := fmt.Sprintf("%s.env[%d]", fieldPath, index)
		if !envNamePattern.MatchString(env.Name) {
			*findings = append(*findings, errorFinding(field+".name", "ENV_NAME_INVALID", "environment variable name must match [A-Z][A-Z0-9_]*"))
		}
		if _, exists := names[env.Name]; exists {
			*findings = append(*findings, errorFinding(field+".name", "ENV_DUPLICATE_NAME", "environment variable name must be unique"))
		}
		names[env.Name] = struct{}{}
		sources := 0
		if env.ValueFrom.Generated != nil {
			sources++
			if _, ok := generated[*env.ValueFrom.Generated]; !ok {
				*findings = append(*findings, errorFinding(field+".valueFrom.generated", "ENV_GENERATED_UNKNOWN", "generated value is not declared"))
			}
		}
		if env.ValueFrom.Parameter != nil {
			sources++
			parameter, ok := parameters[*env.ValueFrom.Parameter]
			if !ok {
				*findings = append(*findings, errorFinding(field+".valueFrom.parameter", "ENV_PARAMETER_UNKNOWN", "parameter is not declared"))
			} else if !parameter.Secret && secretEnvNamePattern.MatchString(env.Name) {
				*findings = append(*findings, warningFinding(field+".valueFrom.parameter", "ENV_SECRET_NAME_PARAMETER", "secret-looking environment variable should reference a secret parameter"))
			}
		}
		if env.ValueFrom.Literal != nil {
			sources++
			if secretEnvNamePattern.MatchString(env.Name) {
				*findings = append(*findings, errorFinding(field+".valueFrom.literal", "ENV_SECRET_INLINE", "secret-looking values must reference a declared secret parameter, not an inline literal"))
			}
			if inlineCredentialPattern.MatchString(*env.ValueFrom.Literal) {
				*findings = append(*findings, errorFinding(field+".valueFrom.literal", "ENV_CREDENTIAL_INLINE", "inline provider credentials are not allowed in a template"))
			}
		}
		if sources != 1 {
			*findings = append(*findings, errorFinding(field+".valueFrom", "ENV_SOURCE_ONE_OF", "valueFrom must set exactly one of generated, parameter or literal"))
		}
	}
}

func validateNodeFiles(node Node, fieldPath string, files map[string][]byte, findings *[]Finding) {
	if node.Bootstrap != nil && node.Kind != KindVM {
		*findings = append(*findings, errorFinding(fieldPath+".bootstrap", "BOOTSTRAP_UNSUPPORTED", "bootstrap/cloud-init is only supported on VM nodes"))
	}
	if node.Bootstrap != nil {
		if finding := validatePackagePath(node.Bootstrap.CloudInitFile, fieldPath+".bootstrap.cloudInitFile"); finding != nil {
			*findings = append(*findings, *finding)
		} else if _, exists := files[node.Bootstrap.CloudInitFile]; !exists {
			*findings = append(*findings, errorFinding(fieldPath+".bootstrap.cloudInitFile", "BOOTSTRAP_FILE_NOT_FOUND", "cloud-init file is not present in the template package"))
		}
	}
	if len(node.Files) > MaxFilesPerNode {
		*findings = append(*findings, errorFinding(fieldPath+".files", "FILE_LIMIT_EXCEEDED", fmt.Sprintf("at most %d files are allowed per node", MaxFilesPerNode)))
	}
	targets := make(map[string]struct{}, len(node.Files))
	for index, file := range node.Files {
		field := fmt.Sprintf("%s.files[%d]", fieldPath, index)
		if finding := validatePackagePath(file.Source, field+".source"); finding != nil {
			*findings = append(*findings, *finding)
		} else if _, exists := files[file.Source]; !exists {
			*findings = append(*findings, errorFinding(field+".source", "FILE_NOT_FOUND", "file is not present in the template package"))
		}
		if finding := validateTargetPath(file.Target, field+".target"); finding != nil {
			*findings = append(*findings, *finding)
		} else {
			if _, exists := targets[file.Target]; exists {
				*findings = append(*findings, errorFinding(field+".target", "FILE_TARGET_DUPLICATE", "file target must be unique within a node"))
			}
			targets[file.Target] = struct{}{}
		}
		if finding := validateMode(file.Mode, field+".mode"); finding != nil {
			*findings = append(*findings, *finding)
		}
	}
}

func validateDriverOptionSyntax(node Node, fieldPath string, findings *[]Finding) {
	drivers := make([]string, 0, len(node.DriverOptions))
	for driver := range node.DriverOptions {
		drivers = append(drivers, driver)
	}
	sort.Strings(drivers)
	for _, driver := range drivers {
		raw := node.DriverOptions[driver]
		field := fieldPath + ".driverOptions." + driver
		if _, ok := knownDrivers[driver]; !ok {
			*findings = append(*findings, errorFinding(field, "DRIVER_OPTION_UNKNOWN", "unknown driver option namespace"))
			continue
		}
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == "" || trimmed == "null" || !strings.HasPrefix(trimmed, "{") {
			*findings = append(*findings, errorFinding(field, "DRIVER_OPTION_TYPE_INVALID", "driver options must be an object"))
			continue
		}
		if key := findSecretJSONKey(raw, ""); key != "" {
			*findings = append(*findings, errorFinding(field+key, "DRIVER_OPTION_SECRET_INLINE", "provider credentials must not be embedded in template driver options"))
		}
		if inlineCredentialPattern.Match(raw) {
			*findings = append(*findings, errorFinding(field, "DRIVER_OPTION_CREDENTIAL_INLINE", "inline provider credentials are not allowed in driver options"))
		}
	}
}

func findSecretJSONKey(raw json.RawMessage, prefix string) string {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return ""
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		childPrefix := prefix + "." + key
		if secretJSONKeyPattern.MatchString(key) {
			return childPrefix
		}
		var child map[string]json.RawMessage
		if err := json.Unmarshal(object[key], &child); err == nil {
			if nested := findSecretJSONKey(object[key], childPrefix); nested != "" {
				return nested
			}
		}
	}
	return ""
}

func validateChecks(template *Template, nodeKeys map[string]struct{}, endpoints map[string]map[string]struct{}, findings *[]Finding) {
	if len(template.Checks) > MaxChecks {
		*findings = append(*findings, errorFinding("checks", "CHECK_LIMIT_EXCEEDED", fmt.Sprintf("at most %d checks are allowed", MaxChecks)))
	}
	for index, check := range template.Checks {
		field := fmt.Sprintf("checks[%d]", index)
		switch check.Kind {
		case "tcp", "http", "https":
		default:
			*findings = append(*findings, errorFinding(field+".kind", "CHECK_KIND_INVALID", "check kind must be tcp, http or https"))
		}
		if _, exists := nodeKeys[check.Node]; !exists {
			*findings = append(*findings, errorFinding(field+".node", "CHECK_NODE_UNKNOWN", "check node is not declared"))
			continue
		}
		if check.Endpoint != "" {
			if _, exists := endpoints[check.Node][check.Endpoint]; !exists {
				*findings = append(*findings, errorFinding(field+".endpoint", "CHECK_ENDPOINT_UNKNOWN", "check endpoint is not declared on the node"))
			}
		}
	}
}

func validateLifecycle(template *Template, findings *[]Finding) {
	switch template.Lifecycle.Reset {
	case "", ResetRecreate, ResetSnapshot:
	default:
		*findings = append(*findings, errorFinding("lifecycle.reset", "LIFECYCLE_RESET_INVALID", "reset must be RECREATE or SNAPSHOT"))
	}
}

func validateDriverOptionsAndCapabilities(
	ctx context.Context,
	template *Template,
	provider DriverMetadataProvider,
	metadata map[string]DriverMetadata,
	findings *[]Finding,
) {
	for index, node := range template.Nodes {
		fieldPath := fmt.Sprintf("nodes[%d]", index)
		compatible := make([]DriverMetadata, 0)
		declared := make([]string, 0, len(node.DriverOptions))
		for driver := range node.DriverOptions {
			declared = append(declared, driver)
		}
		sort.Strings(declared)
		for _, driver := range declared {
			driverKind, known := knownDrivers[driver]
			if !known {
				continue
			}
			if driverKind != node.Kind {
				*findings = append(*findings, warningFinding(fieldPath+".driverOptions."+driver, "DRIVER_OPTION_UNUSED", "driver option block is not used by this node kind"))
				continue
			}
			item, exists := metadata[driver]
			if !exists {
				*findings = append(*findings, warningFinding(fieldPath+".driverOptions."+driver, "DRIVER_OPTION_NOT_VERIFIED", "driver metadata is unavailable; option schema was not checked"))
				continue
			}
			compatible = append(compatible, item)
			validateCapabilities(item, node, len(template.Nodes), fieldPath, findings)
			if template.Lifecycle.Reset == ResetSnapshot && !item.Capabilities.SnapshotReset {
				*findings = append(*findings, errorFinding("lifecycle.reset", "SNAPSHOT_CAPABILITY_UNSUPPORTED", "driver "+item.Driver+" does not support snapshot reset"))
			}
			if provider != nil {
				driverFindings := provider.ValidateDriverOptions(ctx, driver, node, node.DriverOptions[driver])
				for _, finding := range driverFindings {
					finding.Path = fieldPath + ".driverOptions." + driver + relativeFindingPath(finding.Path)
					if finding.Severity != SeverityWarning {
						finding.Severity = SeverityError
					}
					finding.Message = truncateMessage(finding.Message)
					if finding.Code == "" {
						finding.Code = "DRIVER_OPTION_INVALID"
					}
					*findings = append(*findings, finding)
				}
			}
		}
		if len(compatible) == 0 {
			for _, item := range metadata {
				if item.Kind == node.Kind {
					compatible = append(compatible, item)
				}
			}
			if len(compatible) == 0 {
				*findings = append(*findings, warningFinding(fieldPath, "DRIVER_METADATA_UNAVAILABLE", "no driver metadata is registered for this kind; capabilities were not verified"))
				continue
			}
			validateAnyCompatibleCapability(compatible, node, len(template.Nodes), fieldPath, findings)
		}
	}
	if template.Lifecycle.Reset == ResetSnapshot && len(metadata) > 0 {
		supported := false
		for _, item := range metadata {
			if item.Capabilities.SnapshotReset {
				supported = true
				break
			}
		}
		if !supported {
			*findings = append(*findings, errorFinding("lifecycle.reset", "SNAPSHOT_CAPABILITY_UNSUPPORTED", "no registered driver supports snapshot reset"))
		}
	}
}

func validateCapabilities(item DriverMetadata, node Node, nodeCount int, fieldPath string, findings *[]Finding) {
	capabilities := item.Capabilities
	if capabilities.MaxNodes > 0 && nodeCount > capabilities.MaxNodes {
		*findings = append(*findings, errorFinding("nodes", "NODE_CAPABILITY_EXCEEDED", fmt.Sprintf("driver %s supports at most %d nodes", item.Driver, capabilities.MaxNodes)))
	}
	if !sourceTypeSupported(capabilities.SourceTypes, node.Source.Type) {
		*findings = append(*findings, errorFinding(fieldPath+".source.type", "SOURCE_CAPABILITY_UNSUPPORTED", "driver "+item.Driver+" does not support this source type"))
	}
	if node.Bootstrap != nil && !capabilities.CloudInit {
		*findings = append(*findings, errorFinding(fieldPath+".bootstrap", "BOOTSTRAP_CAPABILITY_UNSUPPORTED", "driver "+item.Driver+" does not support cloud-init bootstrap"))
	}
	if len(node.Files) > 0 && !capabilities.FileInjection {
		*findings = append(*findings, errorFinding(fieldPath+".files", "FILE_CAPABILITY_UNSUPPORTED", "driver "+item.Driver+" does not support file injection"))
	}
	if hasPublicEndpoint(node) && !capabilities.PublicExposure {
		*findings = append(*findings, errorFinding(fieldPath+".endpoints", "PUBLIC_EXPOSURE_CAPABILITY_UNSUPPORTED", "driver "+item.Driver+" does not support public endpoint exposure"))
	}
}

func validateAnyCompatibleCapability(items []DriverMetadata, node Node, nodeCount int, fieldPath string, findings *[]Finding) {
	sourceSupported, cloudInitSupported, filesSupported, publicSupported := false, false, false, false
	covers := func(item DriverMetadata) bool {
		return capabilitiesSupportNode(item, node, nodeCount)
	}
	for _, item := range items {
		if covers(item) {
			return
		}
		if sourceTypeSupported(item.Capabilities.SourceTypes, node.Source.Type) {
			sourceSupported = true
		}
		if node.Bootstrap != nil && item.Capabilities.CloudInit {
			cloudInitSupported = true
		}
		if len(node.Files) > 0 && item.Capabilities.FileInjection {
			filesSupported = true
		}
		if hasPublicEndpoint(node) && item.Capabilities.PublicExposure {
			publicSupported = true
		}
	}
	if !sourceSupported {
		*findings = append(*findings, errorFinding(fieldPath+".source.type", "SOURCE_CAPABILITY_UNSUPPORTED", "no compatible driver supports this source type"))
	}
	if node.Bootstrap != nil && !cloudInitSupported {
		*findings = append(*findings, errorFinding(fieldPath+".bootstrap", "BOOTSTRAP_CAPABILITY_UNSUPPORTED", "no compatible driver supports cloud-init bootstrap"))
	}
	if len(node.Files) > 0 && !filesSupported {
		*findings = append(*findings, errorFinding(fieldPath+".files", "FILE_CAPABILITY_UNSUPPORTED", "no compatible driver supports file injection"))
	}
	if hasPublicEndpoint(node) && !publicSupported {
		*findings = append(*findings, errorFinding(fieldPath+".endpoints", "PUBLIC_EXPOSURE_CAPABILITY_UNSUPPORTED", "no compatible driver supports public endpoint exposure"))
	}
	for _, item := range items {
		if item.Capabilities.MaxNodes > 0 && nodeCount > item.Capabilities.MaxNodes {
			*findings = append(*findings, errorFinding("nodes", "NODE_CAPABILITY_EXCEEDED", "no compatible driver supports this many nodes"))
			break
		}
	}
}

func capabilitiesSupportNode(item DriverMetadata, node Node, nodeCount int) bool {
	capabilities := item.Capabilities
	if !sourceTypeSupported(capabilities.SourceTypes, node.Source.Type) {
		return false
	}
	if node.Bootstrap != nil && !capabilities.CloudInit {
		return false
	}
	if len(node.Files) > 0 && !capabilities.FileInjection {
		return false
	}
	if hasPublicEndpoint(node) && !capabilities.PublicExposure {
		return false
	}
	if nodeCount > 0 && capabilities.MaxNodes > 0 && nodeCount > capabilities.MaxNodes {
		return false
	}
	return true
}

func sourceTypeSupported(types []SourceType, target SourceType) bool {
	for _, item := range types {
		if item == target {
			return true
		}
	}
	return false
}

func hasPublicEndpoint(node Node) bool {
	for _, endpoint := range node.Endpoints {
		if endpoint.Exposure == ExposurePublic {
			return true
		}
	}
	return false
}

func hasMixedKinds(template *Template) bool {
	hasVM, hasContainer := false, false
	for _, node := range template.Nodes {
		switch node.Kind {
		case KindVM:
			hasVM = true
		case KindContainer:
			hasContainer = true
		}
	}
	return hasVM && hasContainer
}

func relativeFindingPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, ".") {
		return path
	}
	return "." + path
}

func truncateMessage(message string) string {
	if len(message) <= 2000 {
		return message
	}
	return message[:1997] + "..."
}

func containsControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func isAllDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// type manifestValidator adapts ValidateTemplate to the exact Validator
// interface without executing a driver.
type manifestValidator struct {
	Files    map[string][]byte
	Provider DriverMetadataProvider
}

// NewValidator returns a provider-independent validator. capabilities are
// accepted for contract compatibility and are supplemented by trusted driver
// metadata when a provider is configured.
func NewValidator(files map[string][]byte, provider DriverMetadataProvider, _ map[Kind]Capabilities) Validator {
	return &manifestValidator{Files: files, Provider: provider}
}

// Validate is a convenience wrapper matching the §5.5 Validator contract. The
// supplied capabilities are exposed for every known driver of that kind; driver
// option payloads still receive no raw forwarding beyond their namespace.
func Validate(ctx context.Context, version TemplateVersion, capabilities map[Kind]Capabilities) (ValidationResult, error) {
	return ValidateTemplate(ctx, &version.Manifest, version.Files, newStaticCapabilityProvider(capabilities))
}

// PreviewVersion is the convenience wrapper for a published version.
func PreviewVersion(ctx context.Context, version TemplateVersion, capabilities map[Kind]Capabilities) (*Preview, ValidationResult, error) {
	return BuildPreview(ctx, &version.Manifest, version.Files, newStaticCapabilityProvider(capabilities))
}

type staticCapabilityProvider struct {
	metadata map[string]DriverMetadata
}

func newStaticCapabilityProvider(capabilities map[Kind]Capabilities) DriverMetadataProvider {
	provider := &staticCapabilityProvider{metadata: make(map[string]DriverMetadata)}
	for _, driver := range []string{DriverProxmox, DriverKubeVirt, DriverDocker, DriverPodman, DriverKubernetes} {
		kind := knownDrivers[driver]
		capability, ok := capabilities[kind]
		if !ok {
			continue
		}
		provider.metadata[driver] = DriverMetadata{Driver: driver, Kind: kind, Capabilities: capability}
	}
	return provider
}

func (p *staticCapabilityProvider) ListDriverMetadata(context.Context) ([]DriverMetadata, error) {
	result := make([]DriverMetadata, 0, len(p.metadata))
	for _, driver := range []string{DriverProxmox, DriverKubeVirt, DriverDocker, DriverPodman, DriverKubernetes} {
		if metadata, ok := p.metadata[driver]; ok {
			result = append(result, metadata)
		}
	}
	return result, nil
}

func (p *staticCapabilityProvider) ValidateDriverOptions(context.Context, string, Node, json.RawMessage) []Finding {
	return nil
}

func (v *manifestValidator) Validate(ctx context.Context, version TemplateVersion, _ map[Kind]Capabilities) (ValidationResult, error) {
	return ValidateTemplate(ctx, &version.Manifest, v.Files, v.Provider)
}
