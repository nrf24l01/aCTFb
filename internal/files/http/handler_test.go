package http

import (
	"testing"

	"github.com/labstack/echo/v5"
)

func TestRegisterRoutesInstalls23ScopedOperations(t *testing.T) {
	e := echo.New()
	NewHandler(nil, nil).RegisterRoutes(e)
	routes := e.Router().Routes()
	if len(routes) != 23 {
		t.Fatalf("route count = %d want 23", len(routes))
	}
	want := map[string]bool{
		"POST /api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/submission-drafts":                                        true,
		"GET /api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/submission-drafts/:draftUuid":                              true,
		"DELETE /api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/submission-drafts/:draftUuid":                           true,
		"POST /api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/submission-drafts/:draftUuid/files/presign":               true,
		"POST /api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/submission-drafts/:draftUuid/files/:fileUuid/complete":    true,
		"POST /api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/attachments/:fileUuid/download-url":                       true,
		"POST /api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/submissions/:submissionUuid/files/:fileUuid/download-url": true,
		"POST /api/v2/boards/:boardUuid/modules/:moduleUuid/pentest/submission-drafts":                                                true,
		"GET /api/v2/boards/:boardUuid/modules/:moduleUuid/pentest/submission-drafts/:draftUuid":                                      true,
		"DELETE /api/v2/boards/:boardUuid/modules/:moduleUuid/pentest/submission-drafts/:draftUuid":                                   true,
		"POST /api/v2/boards/:boardUuid/modules/:moduleUuid/pentest/submission-drafts/:draftUuid/files/presign":                       true,
		"POST /api/v2/boards/:boardUuid/modules/:moduleUuid/pentest/submission-drafts/:draftUuid/files/:fileUuid/complete":            true,
		"POST /api/v2/boards/:boardUuid/modules/:moduleUuid/pentest/attachments/:fileUuid/download-url":                               true,
		"POST /api/v2/boards/:boardUuid/modules/:moduleUuid/pentest/submissions/:submissionUuid/files/:fileUuid/download-url":         true,
		"POST /api/v2/admin/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/attachments/presign":                                true,
		"POST /api/v2/admin/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/attachments/:fileUuid/complete":                     true,
		"POST /api/v2/admin/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/attachments/:fileUuid/download-url":                 true,
		"DELETE /api/v2/admin/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/attachments/:fileUuid":                            true,
		"POST /api/v2/admin/boards/:boardUuid/modules/:moduleUuid/pentest/attachments/presign":                                        true,
		"POST /api/v2/admin/boards/:boardUuid/modules/:moduleUuid/pentest/attachments/:fileUuid/complete":                             true,
		"POST /api/v2/admin/boards/:boardUuid/modules/:moduleUuid/pentest/attachments/:fileUuid/download-url":                         true,
		"DELETE /api/v2/admin/boards/:boardUuid/modules/:moduleUuid/pentest/attachments/:fileUuid":                                    true,
		"POST /api/v2/admin/boards/:boardUuid/submissions/:submissionUuid/files/:fileUuid/download-url":                               true,
	}
	for _, route := range routes {
		key := route.Method + " " + route.Path
		if !want[key] {
			t.Fatalf("unexpected route %s", key)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("missing routes: %#v", want)
	}
}
