package middleware_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// fixture struct sits intentionally in the test package — exercises
// each tag the F-3 DTOs use without coupling to a real DTO type.
type sampleReq struct {
	Name string `json:"name" validate:"required,min=1,max=64"`
	URL  string `json:"url"  validate:"required,url"`
	Mode string `json:"mode,omitempty" validate:"omitempty,oneof=auto manual"`
	Port int    `json:"port" validate:"gt=0,lte=65535"`
}

func setupRouter(handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/x", handler)
	return r
}

func doPOST(t *testing.T, r *gin.Engine, body any, ctOverride string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	switch v := body.(type) {
	case []byte:
		rdr = bytes.NewReader(v)
	case nil:
		rdr = bytes.NewReader(nil)
	default:
		b, _ := json.Marshal(v)
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/x", rdr)
	ct := "application/json"
	if ctOverride != "" {
		ct = ctOverride
	}
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestBindAndValidateJSON_ValidPayload_HandlerProceeds(t *testing.T) {
	t.Parallel()
	r := setupRouter(func(c *gin.Context) {
		var req sampleReq
		if !middleware.BindAndValidateJSON(c, &req) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"name": req.Name})
	})

	body := map[string]any{
		"name": "alpha",
		"url":  "http://sonarr:8989",
		"mode": "auto",
		"port": 8989,
	}
	w := doPOST(t, r, body, "")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "alpha", got["name"])
}

func TestBindAndValidateJSON_EmptyJSONBody_400_ValidationFailed_MissingRequired(t *testing.T) {
	t.Parallel()
	r := setupRouter(func(c *gin.Context) {
		var req sampleReq
		if !middleware.BindAndValidateJSON(c, &req) {
			return
		}
		c.Status(http.StatusOK)
	})

	// `{}` parses successfully (empty struct) but fails validation on the
	// required fields.
	w := doPOST(t, r, map[string]any{}, "")
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())

	var got middleware.ValidationFailedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "validation_failed", got.Error)
	// name, url required + port gt=0 + (no mode tag fired because of omitempty)
	require.GreaterOrEqual(t, len(got.Fields), 2)

	tags := map[string]string{}
	for _, fe := range got.Fields {
		tags[fe.Field] = fe.Tag
	}
	assert.Equal(t, "required", tags["name"])
	assert.Equal(t, "required", tags["url"])
}

func TestBindAndValidateJSON_MissingRequiredField_400_TargetedField(t *testing.T) {
	t.Parallel()
	r := setupRouter(func(c *gin.Context) {
		var req sampleReq
		if !middleware.BindAndValidateJSON(c, &req) {
			return
		}
		c.Status(http.StatusOK)
	})

	w := doPOST(t, r, map[string]any{
		"url":  "http://sonarr:8989",
		"port": 1234,
	}, "")
	require.Equal(t, http.StatusBadRequest, w.Code)

	var got middleware.ValidationFailedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.Fields, 1)
	assert.Equal(t, "name", got.Fields[0].Field)
	assert.Equal(t, "required", got.Fields[0].Tag)
	assert.Equal(t, "name is required", got.Fields[0].Message)
}

func TestBindAndValidateJSON_OneOfMismatch_400_ListAllowedValues(t *testing.T) {
	t.Parallel()
	r := setupRouter(func(c *gin.Context) {
		var req sampleReq
		if !middleware.BindAndValidateJSON(c, &req) {
			return
		}
		c.Status(http.StatusOK)
	})

	w := doPOST(t, r, map[string]any{
		"name": "alpha",
		"url":  "http://sonarr:8989",
		"mode": "bogus",
		"port": 8989,
	}, "")
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())

	var got middleware.ValidationFailedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.Fields, 1)
	assert.Equal(t, "mode", got.Fields[0].Field)
	assert.Equal(t, "oneof", got.Fields[0].Tag)
	assert.Contains(t, got.Fields[0].Message, "auto, manual",
		"oneof message must enumerate allowed values")
}

func TestBindAndValidateJSON_GTViolated_400_NumericRule(t *testing.T) {
	t.Parallel()
	r := setupRouter(func(c *gin.Context) {
		var req sampleReq
		if !middleware.BindAndValidateJSON(c, &req) {
			return
		}
		c.Status(http.StatusOK)
	})

	w := doPOST(t, r, map[string]any{
		"name": "alpha",
		"url":  "http://sonarr:8989",
		"mode": "auto",
		"port": 0,
	}, "")
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())

	var got middleware.ValidationFailedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.Fields, 1)
	assert.Equal(t, "port", got.Fields[0].Field)
	assert.Equal(t, "gt", got.Fields[0].Tag)
	assert.Equal(t, "port must be greater than 0", got.Fields[0].Message)
}

func TestBindAndValidateJSON_MultipleViolations_400_AllReported(t *testing.T) {
	t.Parallel()
	r := setupRouter(func(c *gin.Context) {
		var req sampleReq
		if !middleware.BindAndValidateJSON(c, &req) {
			return
		}
		c.Status(http.StatusOK)
	})

	w := doPOST(t, r, map[string]any{
		"name": "",      // required fails
		"url":  "",      // required fails
		"mode": "bogus", // oneof fails
		"port": 999999,  // lte fails
	}, "")
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())

	var got middleware.ValidationFailedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "validation_failed", got.Error)
	require.Len(t, got.Fields, 4, "all four violations must be reported")

	seen := map[string]string{}
	for _, fe := range got.Fields {
		seen[fe.Field] = fe.Tag
	}
	assert.Equal(t, "required", seen["name"])
	assert.Equal(t, "required", seen["url"])
	assert.Equal(t, "oneof", seen["mode"])
	assert.Equal(t, "lte", seen["port"])
}

func TestBindAndValidateJSON_URLMalformed_400_URLRule(t *testing.T) {
	t.Parallel()
	r := setupRouter(func(c *gin.Context) {
		var req sampleReq
		if !middleware.BindAndValidateJSON(c, &req) {
			return
		}
		c.Status(http.StatusOK)
	})

	w := doPOST(t, r, map[string]any{
		"name": "alpha",
		"url":  "::not a url::",
		"mode": "auto",
		"port": 8989,
	}, "")
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())

	var got middleware.ValidationFailedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.Fields, 1)
	assert.Equal(t, "url", got.Fields[0].Field)
	assert.Equal(t, "url", got.Fields[0].Tag)
	assert.Equal(t, "url must be a valid URL", got.Fields[0].Message)
}

func TestBindAndValidateJSON_MinViolated_400_MinRule(t *testing.T) {
	t.Parallel()
	type minReq struct {
		Name string `json:"name" validate:"required,min=3"`
	}
	r := setupRouter(func(c *gin.Context) {
		var req minReq
		if !middleware.BindAndValidateJSON(c, &req) {
			return
		}
		c.Status(http.StatusOK)
	})

	w := doPOST(t, r, map[string]any{"name": "ab"}, "")
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())

	var got middleware.ValidationFailedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.Fields, 1)
	assert.Equal(t, "name", got.Fields[0].Field)
	assert.Equal(t, "min", got.Fields[0].Tag)
	assert.Equal(t, "name must be at least 3", got.Fields[0].Message)
}

func TestBindAndValidateJSON_MaxViolated_400_MaxRule(t *testing.T) {
	t.Parallel()
	type maxReq struct {
		Name string `json:"name" validate:"required,max=5"`
	}
	r := setupRouter(func(c *gin.Context) {
		var req maxReq
		if !middleware.BindAndValidateJSON(c, &req) {
			return
		}
		c.Status(http.StatusOK)
	})

	w := doPOST(t, r, map[string]any{"name": "abcdef"}, "")
	require.Equal(t, http.StatusBadRequest, w.Code)

	var got middleware.ValidationFailedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.Fields, 1)
	assert.Equal(t, "max", got.Fields[0].Tag)
	assert.Equal(t, "name must be at most 5", got.Fields[0].Message)
}

func TestBindAndValidateJSON_GTEAndLTEAndLT_Messages(t *testing.T) {
	t.Parallel()
	type rangeReq struct {
		A int `json:"a" validate:"gte=10"`
		B int `json:"b" validate:"lt=5"`
	}
	r := setupRouter(func(c *gin.Context) {
		var req rangeReq
		if !middleware.BindAndValidateJSON(c, &req) {
			return
		}
		c.Status(http.StatusOK)
	})

	w := doPOST(t, r, map[string]any{"a": 1, "b": 99}, "")
	require.Equal(t, http.StatusBadRequest, w.Code)

	var got middleware.ValidationFailedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.Fields, 2)
	tags := map[string]middleware.FieldError{}
	for _, fe := range got.Fields {
		tags[fe.Field] = fe
	}
	assert.Equal(t, "gte", tags["a"].Tag)
	assert.Equal(t, "a must be greater than or equal to 10", tags["a"].Message)
	assert.Equal(t, "lt", tags["b"].Tag)
	assert.Equal(t, "b must be less than 5", tags["b"].Message)
}

func TestBindAndValidateJSON_LTEViolated_Message(t *testing.T) {
	t.Parallel()
	type lteReq struct {
		N int `json:"n" validate:"lte=10"`
	}
	r := setupRouter(func(c *gin.Context) {
		var req lteReq
		if !middleware.BindAndValidateJSON(c, &req) {
			return
		}
		c.Status(http.StatusOK)
	})

	w := doPOST(t, r, map[string]any{"n": 100}, "")
	require.Equal(t, http.StatusBadRequest, w.Code)

	var got middleware.ValidationFailedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.Fields, 1)
	assert.Equal(t, "lte", got.Fields[0].Tag)
	assert.Equal(t, "n must be less than or equal to 10", got.Fields[0].Message)
}

func TestBindAndValidateJSON_UnknownTag_FallsThroughGenericMessage(t *testing.T) {
	t.Parallel()
	// `alpha` is a valid validator tag (alphabetic-only). Test the
	// default-branch wording on the messageFor switch.
	type unknownReq struct {
		Code string `json:"code" validate:"alpha"`
	}
	r := setupRouter(func(c *gin.Context) {
		var req unknownReq
		if !middleware.BindAndValidateJSON(c, &req) {
			return
		}
		c.Status(http.StatusOK)
	})

	w := doPOST(t, r, map[string]any{"code": "123"}, "")
	require.Equal(t, http.StatusBadRequest, w.Code)

	var got middleware.ValidationFailedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.Fields, 1)
	assert.Equal(t, "code", got.Fields[0].Field)
	assert.Equal(t, "alpha", got.Fields[0].Tag)
	assert.Contains(t, got.Fields[0].Message, "alpha",
		"default branch must echo the failing tag name")
}

func TestBindAndValidateJSON_MalformedJSON_400_LegacyEnvelope(t *testing.T) {
	t.Parallel()
	r := setupRouter(func(c *gin.Context) {
		var req sampleReq
		if !middleware.BindAndValidateJSON(c, &req) {
			return
		}
		c.Status(http.StatusOK)
	})

	w := doPOST(t, r, []byte("not json"), "")
	require.Equal(t, http.StatusBadRequest, w.Code)

	// Parse-stage rejection uses the legacy {error, code} envelope, NOT
	// the validation_failed shape.
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "BAD_REQUEST", got["code"])
	assert.NotEqual(t, "validation_failed", got["error"])
}

func TestBindAndValidateJSON_WrongContentType_400_LegacyEnvelope(t *testing.T) {
	t.Parallel()
	r := setupRouter(func(c *gin.Context) {
		var req sampleReq
		if !middleware.BindAndValidateJSON(c, &req) {
			return
		}
		c.Status(http.StatusOK)
	})

	w := doPOST(t, r, map[string]any{"name": "x"}, "text/xml")
	require.Equal(t, http.StatusBadRequest, w.Code)

	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "BAD_REQUEST", got["code"])
}

func TestBindAndValidateJSON_BodyTooLarge_400_LegacyEnvelope(t *testing.T) {
	t.Parallel()
	r := setupRouter(func(c *gin.Context) {
		var req sampleReq
		if !middleware.BindAndValidateJSON(c, &req) {
			return
		}
		c.Status(http.StatusOK)
	})

	body := bytes.Repeat([]byte("x"), middleware.MaxJSONBodyBytes+1)
	w := doPOST(t, r, body, "")
	require.Equal(t, http.StatusBadRequest, w.Code)

	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "BAD_REQUEST", got["code"])
}

func TestBindAndValidateJSON_NonStructPointer_500_InternalError(t *testing.T) {
	t.Parallel()
	r := setupRouter(func(c *gin.Context) {
		// Intentionally pass a non-struct pointer — programmer error.
		var n int
		if !middleware.BindAndValidateJSON(c, &n) {
			return
		}
		c.Status(http.StatusOK)
	})

	w := doPOST(t, r, 42, "")
	require.Equal(t, http.StatusInternalServerError, w.Code, "body=%s", w.Body.String())

	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "internal_error", got["error"])
	msg, _ := got["message"].(string)
	assert.True(t, strings.Contains(msg, "validation engine error"),
		"500 body must mention the engine error: %q", msg)
}
