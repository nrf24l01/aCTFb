package runtime

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/nrf24l01/actfb/internal/deployment"
)

// Hard provider-side resource bounds. Template parsing already bounds values;
// drivers re-check them because a stored version may predate a parser change.
const (
	MaxContainerCPUMillicores = 32_000
	MaxContainerMemoryMiB     = 65_536
	MaxContainerDiskMiB       = 65_536
	MinContainerMemoryMiB     = 16
	MinContainerDiskMiB       = 8
	MaxContainerPIDs          = 4096
	MinContainerPIDs          = 16

	MaxVMCPUMillicores = 128_000
	MaxVMMemoryMiB     = 1_048_576
	MaxVMDiskMiB       = 10_485_760
)

var (
	digestRefPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*(?::[0-9]+)?@sha256:[a-f0-9]{64}$`)
	logicalRefPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
)

// ErrorFinding builds an error finding with a stable code path.
func ErrorFinding(path, code, message string) deployment.Finding {
	return deployment.Finding{Path: path, Code: code, Message: message, Severity: deployment.SeverityError}
}

// WarningFinding builds a warning finding.
func WarningFinding(path, code, message string) deployment.Finding {
	return deployment.Finding{Path: path, Code: code, Message: message, Severity: deployment.SeverityWarning}
}

// ValidateOCIDigest enforces an immutable, digest-pinned image reference. Tags
// and ":latest" are rejected: a container source may not drift under a running
// competition. Registry host allowlisting is enforced by the driver settings.
func ValidateOCIDigest(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return errors.New("image reference is required")
	}
	if strings.Contains(ref, "://") || strings.Contains(ref, "@") && !strings.Contains(ref, "sha256:") {
		return errors.New("image reference must be registry/repository@sha256:<digest>")
	}
	if strings.Contains(ref, "..") || strings.HasPrefix(ref, "/") || strings.ContainsAny(ref, " \t\r\n") {
		return errors.New("image reference is not a safe repository path")
	}
	if !digestRefPattern.MatchString(ref) {
		return errors.New("image reference must be digest-pinned with @sha256 and contain no tag")
	}
	return nil
}

// ValidateLogicalVMRef enforces a logical VM image/template name. Native
// provider templates are never accepted from a template author; the trusted
// integration settings map a logical name to a permitted provider VMID/PVC.
func ValidateLogicalVMRef(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return errors.New("VM source reference is required")
	}
	if strings.Contains(ref, "://") || strings.ContainsAny(ref, "/\\ \t\r\n'\"`$;|&<>") {
		return errors.New("VM source reference must be an immutable logical name")
	}
	if !logicalRefPattern.MatchString(ref) {
		return errors.New("VM source reference must match ^[a-z0-9][a-z0-9._-]{0,127}$")
	}
	return nil
}

// ValidateResources applies common range checks per kind.
func ValidateResources(kind deployment.Kind, resources deployment.Resources) []deployment.Finding {
	findings := make([]deployment.Finding, 0, 3)
	if resources.CPUMillicores <= 0 {
		findings = append(findings, ErrorFinding("resources.cpuMillicores", "RESOURCE_CPU_INVALID", "cpuMillicores must be positive"))
	}
	if resources.MemoryMiB <= 0 {
		findings = append(findings, ErrorFinding("resources.memoryMiB", "RESOURCE_MEMORY_INVALID", "memoryMiB must be positive"))
	}
	if resources.DiskMiB <= 0 {
		findings = append(findings, ErrorFinding("resources.diskMiB", "RESOURCE_DISK_INVALID", "diskMiB must be positive"))
	}
	switch kind {
	case deployment.KindContainer:
		if resources.CPUMillicores > MaxContainerCPUMillicores {
			findings = append(findings, ErrorFinding("resources.cpuMillicores", "RESOURCE_CPU_TOO_LARGE", "cpuMillicores exceeds the container limit"))
		}
		if resources.MemoryMiB < MinContainerMemoryMiB || resources.MemoryMiB > MaxContainerMemoryMiB {
			findings = append(findings, ErrorFinding("resources.memoryMiB", "RESOURCE_MEMORY_OUT_OF_RANGE", "memoryMiB is outside the container range"))
		}
		if resources.DiskMiB < MinContainerDiskMiB || resources.DiskMiB > MaxContainerDiskMiB {
			findings = append(findings, ErrorFinding("resources.diskMiB", "RESOURCE_DISK_OUT_OF_RANGE", "diskMiB is outside the container range"))
		}
	case deployment.KindVM:
		if resources.CPUMillicores > MaxVMCPUMillicores {
			findings = append(findings, ErrorFinding("resources.cpuMillicores", "RESOURCE_CPU_TOO_LARGE", "cpuMillicores exceeds the VM limit"))
		}
		if resources.MemoryMiB > MaxVMMemoryMiB {
			findings = append(findings, ErrorFinding("resources.memoryMiB", "RESOURCE_MEMORY_TOO_LARGE", "memoryMiB exceeds the VM limit"))
		}
		if resources.DiskMiB > MaxVMDiskMiB {
			findings = append(findings, ErrorFinding("resources.diskMiB", "RESOURCE_DISK_TOO_LARGE", "diskMiB exceeds the VM limit"))
		}
	}
	return findings
}

// EndpointKeys returns endpoints keyed by their declared key and validates
// uniqueness and ports.
func ValidateEndpoints(kind deployment.Kind, endpoints []deployment.Endpoint) []deployment.Finding {
	findings := make([]deployment.Finding, 0)
	seen := make(map[string]struct{}, len(endpoints))
	ports := make(map[int]struct{}, len(endpoints))
	for index, endpoint := range endpoints {
		path := fmt.Sprintf("endpoints[%d]", index)
		if _, exists := seen[endpoint.Key]; exists {
			findings = append(findings, ErrorFinding(path+".key", "ENDPOINT_KEY_DUPLICATE", "endpoint keys must be unique"))
		}
		seen[endpoint.Key] = struct{}{}
		if endpoint.TargetPort < 1 || endpoint.TargetPort > 65535 {
			findings = append(findings, ErrorFinding(path+".targetPort", "ENDPOINT_PORT_INVALID", "targetPort must be between 1 and 65535"))
		}
		if endpoint.Exposure != deployment.ExposurePublic && endpoint.Exposure != deployment.ExposurePrivate {
			findings = append(findings, ErrorFinding(path+".exposure", "ENDPOINT_EXPOSURE_INVALID", "exposure must be PUBLIC or PRIVATE"))
		}
		switch endpoint.Protocol {
		case "tcp", "http", "https":
		default:
			findings = append(findings, ErrorFinding(path+".protocol", "ENDPOINT_PROTOCOL_UNSUPPORTED", "protocol must be tcp, http or https"))
		}
		if endpoint.Exposure == deployment.ExposurePublic && kind == deployment.KindContainer {
			if _, exists := ports[endpoint.TargetPort]; exists {
				findings = append(findings, ErrorFinding(path+".targetPort", "ENDPOINT_PORT_DUPLICATE", "public endpoints on one container must use distinct target ports"))
			}
			ports[endpoint.TargetPort] = struct{}{}
		}
	}
	return findings
}

// LoopbackOrUnix reports whether a provider address is a local daemon socket.
// Remote plain-HTTP daemon URLs are rejected by the integrations URL policy.
func LoopbackOrUnix(raw string) bool {
	if strings.HasPrefix(raw, "unix://") {
		return true
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if parsed.Scheme == "unix" {
		return true
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}

// SanitizeName turns an arbitrary identifier into a provider-safe DNS label.
func SanitizeName(parts ...string) string {
	joined := strings.ToLower(strings.Join(parts, "-"))
	var builder strings.Builder
	lastDash := false
	for _, r := range joined {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case r == '-' || r == '.' || r == '_':
			if !lastDash && builder.Len() > 0 {
				builder.WriteByte('-')
				lastDash = true
			}
		}
		if builder.Len() >= 63 {
			break
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return "actfb"
	}
	return result
}

// ValidateNetworkPool validates the trusted network allocation pool.
func ValidateNetworkPool(pool *NetworkPool, allowedKinds ...string) error {
	if pool == nil {
		return nil
	}
	kind := strings.ToLower(strings.TrimSpace(pool.Kind))
	allowed := false
	for _, candidate := range allowedKinds {
		if kind == candidate {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("network.kind %q is not supported by this driver", pool.Kind)
	}
	if pool.Bridge != "" && (strings.ContainsAny(pool.Bridge, " \t\r\n/\\") || len(pool.Bridge) > 63) {
		return errors.New("network.bridge is invalid")
	}
	if pool.VLANFrom != 0 || pool.VLANTo != 0 {
		if kind != "vlan" && kind != "bridge" {
			return errors.New("network VLAN range requires kind bridge or vlan")
		}
		if pool.VLANFrom < 1 || pool.VLANTo > 4094 || pool.VLANTo < pool.VLANFrom || pool.VLANTo-pool.VLANFrom > 4000 {
			return errors.New("network VLAN range is invalid")
		}
	}
	if pool.CIDR != "" {
		if _, _, err := net.ParseCIDR(pool.CIDR); err != nil {
			return errors.New("network.cidr is invalid")
		}
	}
	return nil
}
