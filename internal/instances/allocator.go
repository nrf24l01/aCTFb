package instances

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/runtime"
)

type networkConfig struct {
	Kind      string `json:"kind"`
	Bridge    string `json:"bridge"`
	CIDR      string `json:"cidr"`
	Namespace string `json:"namespace"`
	VLANFrom  int    `json:"vlanFrom"`
	VLANTo    int    `json:"vlanTo"`
}

// buildClaim derives the core-owned network claim from the trusted integration
// settings and the live lease set. Nothing here comes from a client manifest.
func buildClaim(target integrationTarget, instanceID uuid.UUID, used map[string]struct{}) (runtime.NetworkClaim, error) {
	config := networkConfig{}
	if len(target.Settings) > 0 {
		var envelope struct {
			Network *networkConfig `json:"network"`
		}
		if err := json.Unmarshal(target.Settings, &envelope); err == nil && envelope.Network != nil {
			config = *envelope.Network
		}
	}
	kind := strings.ToLower(strings.TrimSpace(config.Kind))
	if kind == "" {
		switch target.Kind {
		case "VM":
			kind = "bridge"
		default:
			kind = "network"
		}
	}
	claim := runtime.NetworkClaim{Kind: kind, Bridge: config.Bridge, CIDR: config.CIDR, Namespace: config.Namespace}
	if config.VLANFrom > 0 && config.VLANTo >= config.VLANFrom {
		for vlan := config.VLANFrom; vlan <= config.VLANTo; vlan++ {
			key := "vlan:" + strconv.Itoa(vlan)
			if _, taken := used[key]; taken {
				continue
			}
			claim.Kind = "vlan"
			claim.VLAN = vlan
			claim.Key = key
			return claim, nil
		}
		return runtime.NetworkClaim{}, &Error{Code: CodeLimitReached, Message: "no free VLAN in the provider pool", Status: 409}
	}
	claim.Key = "net:" + instanceID.String()
	if _, taken := used[claim.Key]; taken {
		claim.Key = "net:" + instanceID.String() + "-1"
	}
	if config.Namespace != "" {
		claim.Namespace = config.Namespace
	}
	if claim.Kind == "namespace" && claim.Namespace == "" {
		return runtime.NetworkClaim{}, fmt.Errorf("instances: trusted namespace pool is missing")
	}
	return claim, nil
}
