package deployment

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// ComputeDigest returns a stable sha256 digest over the raw manifest bytes and
// normalized package files. File map iteration order never affects the result.
func ComputeDigest(rawManifest []byte, files map[string][]byte) string {
	hash := sha256.New()
	writeChunk(hash, []byte("actfb.deployment.template.v1"))
	writeChunk(hash, rawManifest)
	for _, filePath := range sortedFilePaths(files) {
		writeChunk(hash, []byte(filePath))
		writeChunk(hash, files[filePath])
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

type chunkWriter interface {
	Write([]byte) (int, error)
}

func writeChunk(writer chunkWriter, chunk []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(chunk)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write(chunk)
}

// Diff kinds match the expanded Swagger enum exactly.
const (
	DiffKindNode      = "NODE"
	DiffKindParameter = "PARAMETER"
	DiffKindFile      = "FILE"
	DiffKindEndpoint  = "ENDPOINT"
	DiffKindCheck     = "CHECK"
	DiffKindLifecycle = "LIFECYCLE"

	DiffAdded   = "ADDED"
	DiffRemoved = "REMOVED"
	DiffChanged = "CHANGED"
)

// DiffEntry is one safe, bounded difference. File contents and secret values
// are represented only by digests or the redaction marker.
type DiffEntry struct {
	Kind   string
	Path   string
	Change string
	Before *string
	After  *string
}

// Diff is a deterministic comparison between two immutable versions.
type Diff struct {
	FromVersion int
	ToVersion   int
	Changes     []DiffEntry
}

// DiffVersions compares published versions without ever returning file content
// or secret values.
func DiffVersions(from, to TemplateVersion) *Diff {
	result := &Diff{FromVersion: from.Version, ToVersion: to.Version, Changes: make([]DiffEntry, 0)}
	diffParameters(from.Manifest.Parameters, to.Manifest.Parameters, result)
	diffFiles(from.Files, to.Files, result)
	diffNodes(from.Manifest.Nodes, to.Manifest.Nodes, result)
	diffChecks(from.Manifest.Checks, to.Manifest.Checks, result)
	diffLifecycle(from.Manifest.Lifecycle, to.Manifest.Lifecycle, result)
	sort.SliceStable(result.Changes, func(i, j int) bool {
		if result.Changes[i].Kind == result.Changes[j].Kind {
			return result.Changes[i].Path < result.Changes[j].Path
		}
		return result.Changes[i].Kind < result.Changes[j].Kind
	})
	return result
}

func diffParameters(before, after []Parameter, result *Diff) {
	beforeMap := make(map[string]Parameter, len(before))
	afterMap := make(map[string]Parameter, len(after))
	for _, parameter := range before {
		beforeMap[parameter.Key] = parameter
	}
	for _, parameter := range after {
		afterMap[parameter.Key] = parameter
	}
	keys := unionKeys(beforeMap, afterMap)
	for _, key := range keys {
		oldParameter, hadOld := beforeMap[key]
		newParameter, hasNew := afterMap[key]
		path := "parameters." + key
		switch {
		case !hadOld:
			value := parameterDigest(newParameter)
			result.Changes = append(result.Changes, DiffEntry{Kind: DiffKindParameter, Path: path, Change: DiffAdded, After: &value})
		case !hasNew:
			value := parameterDigest(oldParameter)
			result.Changes = append(result.Changes, DiffEntry{Kind: DiffKindParameter, Path: path, Change: DiffRemoved, Before: &value})
		default:
			oldValue, newValue := parameterDigest(oldParameter), parameterDigest(newParameter)
			if oldValue != newValue {
				result.Changes = append(result.Changes, DiffEntry{Kind: DiffKindParameter, Path: path, Change: DiffChanged, Before: &oldValue, After: &newValue})
			}
		}
	}
}

func parameterDigest(parameter Parameter) string {
	if parameter.Secret {
		value := PreviewRedacted
		return value
	}
	encoded, _ := json.Marshal(parameter)
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:12])
}

func diffFiles(before, after map[string][]byte, result *Diff) {
	keys := unionKeys(before, after)
	for _, filePath := range keys {
		oldContent, hadOld := before[filePath]
		newContent, hasNew := after[filePath]
		path := "files." + filePath
		switch {
		case !hadOld:
			value := contentDigest(newContent)
			result.Changes = append(result.Changes, DiffEntry{Kind: DiffKindFile, Path: path, Change: DiffAdded, After: &value})
		case !hasNew:
			value := contentDigest(oldContent)
			result.Changes = append(result.Changes, DiffEntry{Kind: DiffKindFile, Path: path, Change: DiffRemoved, Before: &value})
		default:
			oldValue, newValue := contentDigest(oldContent), contentDigest(newContent)
			if oldValue != newValue {
				result.Changes = append(result.Changes, DiffEntry{Kind: DiffKindFile, Path: path, Change: DiffChanged, Before: &oldValue, After: &newValue})
			}
		}
	}
}

func contentDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:12])
}

func diffNodes(before, after []Node, result *Diff) {
	beforeMap := make(map[string]Node, len(before))
	afterMap := make(map[string]Node, len(after))
	for _, node := range before {
		beforeMap[node.Key] = node
	}
	for _, node := range after {
		afterMap[node.Key] = node
	}
	for _, key := range unionKeys(beforeMap, afterMap) {
		oldNode, hadOld := beforeMap[key]
		newNode, hasNew := afterMap[key]
		path := "nodes." + key
		switch {
		case !hadOld:
			value := nodeDigest(newNode)
			result.Changes = append(result.Changes, DiffEntry{Kind: DiffKindNode, Path: path, Change: DiffAdded, After: &value})
		case !hasNew:
			value := nodeDigest(oldNode)
			result.Changes = append(result.Changes, DiffEntry{Kind: DiffKindNode, Path: path, Change: DiffRemoved, Before: &value})
		default:
			oldValue, newValue := nodeDigest(oldNode), nodeDigest(newNode)
			if oldValue != newValue {
				result.Changes = append(result.Changes, DiffEntry{Kind: DiffKindNode, Path: path, Change: DiffChanged, Before: &oldValue, After: &newValue})
			}
			diffEndpointMaps(oldNode, newNode, result)
		}
	}
}

func nodeDigest(node Node) string {
	encoded, _ := json.Marshal(node)
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:12])
}

func diffEndpointMaps(beforeNode, afterNode Node, result *Diff) {
	before := make(map[string]Endpoint, len(beforeNode.Endpoints))
	after := make(map[string]Endpoint, len(afterNode.Endpoints))
	for _, endpoint := range beforeNode.Endpoints {
		before[endpoint.Key] = endpoint
	}
	for _, endpoint := range afterNode.Endpoints {
		after[endpoint.Key] = endpoint
	}
	for _, key := range unionKeys(before, after) {
		oldEndpoint, hadOld := before[key]
		newEndpoint, hasNew := after[key]
		path := fmt.Sprintf("nodes.%s.endpoints.%s", afterNode.Key, key)
		switch {
		case !hadOld:
			value := endpointSummary(newEndpoint)
			result.Changes = append(result.Changes, DiffEntry{Kind: DiffKindEndpoint, Path: path, Change: DiffAdded, After: &value})
		case !hasNew:
			value := endpointSummary(oldEndpoint)
			result.Changes = append(result.Changes, DiffEntry{Kind: DiffKindEndpoint, Path: path, Change: DiffRemoved, Before: &value})
		default:
			oldValue, newValue := endpointSummary(oldEndpoint), endpointSummary(newEndpoint)
			if oldValue != newValue {
				result.Changes = append(result.Changes, DiffEntry{Kind: DiffKindEndpoint, Path: path, Change: DiffChanged, Before: &oldValue, After: &newValue})
			}
		}
	}
}

func endpointSummary(endpoint Endpoint) string {
	return fmt.Sprintf("%s/%d/%s", endpoint.Protocol, endpoint.TargetPort, endpoint.Exposure)
}

func diffChecks(before, after []Check, result *Diff) {
	beforeMap := make(map[string]Check, len(before))
	afterMap := make(map[string]Check, len(after))
	for _, check := range before {
		beforeMap[checkPath(check)] = check
	}
	for _, check := range after {
		afterMap[checkPath(check)] = check
	}
	for _, key := range unionKeys(beforeMap, afterMap) {
		oldCheck, hadOld := beforeMap[key]
		newCheck, hasNew := afterMap[key]
		path := "checks." + key
		switch {
		case !hadOld:
			value := checkSummary(newCheck)
			result.Changes = append(result.Changes, DiffEntry{Kind: DiffKindCheck, Path: path, Change: DiffAdded, After: &value})
		case !hasNew:
			value := checkSummary(oldCheck)
			result.Changes = append(result.Changes, DiffEntry{Kind: DiffKindCheck, Path: path, Change: DiffRemoved, Before: &value})
		default:
			// The map key already contains all check fields, so identical keys
			// are identical checks.
		}
	}
}

func checkPath(check Check) string {
	if check.Endpoint == "" {
		return check.Kind + "." + check.Node
	}
	return check.Kind + "." + check.Node + "." + check.Endpoint
}

func checkSummary(check Check) string {
	return checkPath(check)
}

func diffLifecycle(before, after Lifecycle, result *Diff) {
	if before.Reset != after.Reset {
		oldValue, newValue := before.Reset, after.Reset
		result.Changes = append(result.Changes, DiffEntry{Kind: DiffKindLifecycle, Path: "lifecycle.reset", Change: DiffChanged, Before: &oldValue, After: &newValue})
	}
}

func unionKeys[V any](before, after map[string]V) []string {
	seen := make(map[string]struct{}, len(before)+len(after))
	for key := range before {
		seen[key] = struct{}{}
	}
	for key := range after {
		seen[key] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
