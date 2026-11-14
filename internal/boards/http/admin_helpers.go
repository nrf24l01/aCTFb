package http

import (
	"strings"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/boards"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

func taskTypes(values []oapimodels.TaskType) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}

func submissionPolicyInput(policy *oapimodels.SubmissionPolicy, raw rawBody) boards.SubmissionPolicyInput {
	input := boards.SubmissionPolicyInput{}
	if policy == nil {
		return input
	}
	if policy.Mode != nil {
		input.Mode = string(*policy.Mode)
	}
	if policy.Checker != nil {
		input.Checker = string(*policy.Checker)
	}
	if policy.CheckerKey != nil {
		input.CheckerKey = strings.TrimSpace(*policy.CheckerKey)
	}
	if policy.Text != nil {
		input.HasTextPolicy = true
		if policy.Text.Required != nil {
			input.TextRequired = *policy.Text.Required
		}
		if policy.Text.MinLength != nil {
			input.TextMinLength = int(*policy.Text.MinLength)
		}
		if policy.Text.MaxLength != nil {
			input.TextMaxLength = int(*policy.Text.MaxLength)
		}
	}
	if policy.Files != nil {
		input.HasFilesPolicy = true
		if policy.Files.Required != nil {
			input.FilesRequired = *policy.Files.Required
		}
		if policy.Files.MinCount != nil {
			input.FilesMinCount = int(*policy.Files.MinCount)
		}
		if policy.Files.MaxCount != nil {
			input.FilesMaxCount = int(*policy.Files.MaxCount)
		}
		if policy.Files.MaxSizeBytes != nil {
			input.FilesMaxSize = *policy.Files.MaxSizeBytes
		}
		input.AllowedContentTypes = append([]string{}, policy.Files.AllowedContentTypes...)
	}
	if raw != nil {
		input.HasTextPolicy = input.HasTextPolicy || (isPresent(raw, "text") && !isNull(raw, "text"))
		input.HasFilesPolicy = input.HasFilesPolicy || (isPresent(raw, "files") && !isNull(raw, "files"))
		if files := nestedRaw(raw, "files"); files != nil {
			input.HasFileContentTypes = len(files) > 0
		}
	}
	return input
}

func taskInstanceInput(instance *oapimodels.TaskInstanceConfig) boards.TaskInstanceInput {
	input := boards.TaskInstanceInput{}
	if instance == nil {
		return input
	}
	input.Enabled = instance.Enabled != nil && *instance.Enabled
	if instance.ProviderIntegrationUUID != nil {
		input.HasProviderIntegration = strings.TrimSpace(string(*instance.ProviderIntegrationUUID)) != ""
	}
	input.HasTTLSeconds = instance.TTLSeconds != nil
	input.HasNodes = len(instance.Nodes) > 0
	if instance.DeploymentTemplateUUID != nil {
		input.HasDeploymentTemplate = true
		if parsed, err := uuid.Parse(strings.TrimSpace(string(*instance.DeploymentTemplateUUID))); err == nil {
			input.DeploymentTemplateID = &parsed
		}
	}
	if instance.TemplateVersion != nil {
		input.HasTemplateVersion = true
		version := int(*instance.TemplateVersion)
		input.TemplateVersion = &version
	}
	if instance.Parameters != nil {
		input.HasParameters = true
		input.Parameters = make(map[string]string, len(instance.Parameters))
		for key, value := range instance.Parameters {
			input.Parameters[key] = value
		}
	}
	if instance.SecretParameters != nil {
		input.HasSecretParameters = true
		input.SecretParameters = make(map[string]string, len(instance.SecretParameters))
		for key, value := range instance.SecretParameters {
			input.SecretParameters[key] = value
		}
	}
	return input
}

func validSubmissionStatus(value string) bool {
	switch value {
	case boards.SubmissionStatusPending, boards.SubmissionStatusChecking, boards.SubmissionStatusPendingRev,
		boards.SubmissionStatusAccepted, boards.SubmissionStatusRejected, boards.SubmissionStatusError:
		return true
	}
	return false
}

func parseUUID(value string) (uuid.UUID, error) {
	return uuid.Parse(value)
}
