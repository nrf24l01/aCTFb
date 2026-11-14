package http

import (
	"encoding/json"
	"errors"

	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/integrations"
)

type createRequest struct {
	Type                      string                     `json:"type"`
	Name                      string                     `json:"name"`
	Kind                      *string                    `json:"kind,omitempty"`
	Driver                    *string                    `json:"driver,omitempty"`
	BaseURL                   string                     `json:"baseUrl,omitempty"`
	Credentials               map[string]string          `json:"credentials,omitempty"`
	Settings                  json.RawMessage            `json:"settings,omitempty"`
	InstancePublicationPolicy *publicationRequestPayload `json:"instancePublicationPolicy,omitempty"`
}

type patchRequest struct {
	Name                      *string                    `json:"name,omitempty"`
	BaseURL                   *string                    `json:"baseUrl,omitempty"`
	Type                      *string                    `json:"type,omitempty"`
	Kind                      *string                    `json:"kind,omitempty"`
	Driver                    *string                    `json:"driver,omitempty"`
	Credentials               map[string]string          `json:"credentials,omitempty"`
	Settings                  *json.RawMessage           `json:"settings,omitempty"`
	InstancePublicationPolicy *publicationRequestPayload `json:"instancePublicationPolicy,omitempty"`
}

type publicationRequestPayload struct {
	PublicTCPHost  string `json:"publicTcpHost,omitempty"`
	PublicPortFrom int    `json:"publicPortFrom,omitempty"`
	PublicPortTo   int    `json:"publicPortTo,omitempty"`
	HTTPBaseDomain string `json:"httpBaseDomain,omitempty"`
	ForceHTTPS     *bool  `json:"forceHttps,omitempty"`
}

func bindCreate(c *echo.Context) (integrations.CreateInput, error) {
	var request createRequest
	if err := decodeStrict(c, &request); err != nil {
		return integrations.CreateInput{}, err
	}
	input := integrations.CreateInput{
		Type:        integrations.Type(request.Type),
		Name:        request.Name,
		BaseURL:     request.BaseURL,
		Credentials: request.Credentials,
		Settings:    request.Settings,
	}
	if request.Kind != nil {
		input.Kind = *request.Kind
	}
	if request.Driver != nil {
		input.Driver = *request.Driver
	}
	if request.InstancePublicationPolicy != nil {
		input.Publication = request.InstancePublicationPolicy.toDomain()
	}
	return input, nil
}

func bindPatch(c *echo.Context) (integrations.PatchInput, error) {
	var request patchRequest
	if err := decodeStrict(c, &request); err != nil {
		return integrations.PatchInput{}, err
	}
	input := integrations.PatchInput{
		Name:        request.Name,
		BaseURL:     request.BaseURL,
		Credentials: request.Credentials,
		Settings:    request.Settings,
	}
	if request.Type != nil {
		typ := integrations.Type(*request.Type)
		input.Type = &typ
	}
	input.Kind = request.Kind
	input.Driver = request.Driver
	if request.InstancePublicationPolicy != nil {
		input.Publication = request.InstancePublicationPolicy.toDomain()
	}
	return input, nil
}

func (p publicationRequestPayload) toDomain() *deployment.InstancePublicationPolicy {
	policy := &deployment.InstancePublicationPolicy{
		PublicTCPHost:  p.PublicTCPHost,
		PublicPortFrom: p.PublicPortFrom,
		PublicPortTo:   p.PublicPortTo,
		HTTPBaseDomain: p.HTTPBaseDomain,
	}
	if p.ForceHTTPS != nil {
		policy.ForceHTTPS = *p.ForceHTTPS
	}
	return policy
}

func writeBindError(c *echo.Context, err error) error {
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return writeError(c, 400, models.ErrorCodeINVALIDJSON, "Malformed JSON", "")
	}
	return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "body")
}
