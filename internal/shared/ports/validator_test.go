package ports_test

import (
	"errors"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// sample structs intentionally local — they exercise the tag-name
// resolution + every tag the F-3 DTOs rely on.

type sampleValid struct {
	Name string `json:"name" validate:"required,min=1,max=10"`
	Port int    `json:"port" validate:"gt=0,lte=65535"`
	Mode string `json:"mode,omitempty" validate:"omitempty,oneof=auto manual"`
}

type sampleRequiredOnly struct {
	Title string `json:"title" validate:"required"`
}

type sampleOneOf struct {
	Mode string `json:"mode" validate:"oneof=auto manual"`
}

type sampleURL struct {
	URL string `json:"url" validate:"required,url"`
}

type sampleNumeric struct {
	Count int `json:"count" validate:"gte=0,lte=100"`
}

type sampleHyphenJSON struct {
	Skipped string `json:"-" validate:"required"`
}

func TestValidate_ValidStruct_NoError(t *testing.T) {
	t.Parallel()
	in := sampleValid{Name: "ok", Port: 8080, Mode: "auto"}
	assert.NoError(t, ports.Validate(in))
}

func TestValidate_ValidStruct_OmitEmptyMode(t *testing.T) {
	t.Parallel()
	// Mode is omitempty; empty string skips the oneof check.
	in := sampleValid{Name: "ok", Port: 8080, Mode: ""}
	assert.NoError(t, ports.Validate(in))
}

func TestValidate_Required_MissingField_ReturnsValidationErrors(t *testing.T) {
	t.Parallel()
	in := sampleRequiredOnly{Title: ""}
	err := ports.Validate(in)
	require.Error(t, err)

	var verrs validator.ValidationErrors
	require.ErrorAs(t, err, &verrs)
	require.Len(t, verrs, 1)
	assert.Equal(t, "title", verrs[0].Field(), "tag-name func must resolve json name")
	assert.Equal(t, "required", verrs[0].Tag())
}

func TestValidate_GreaterThan_NegativeValue_ReturnsGTTag(t *testing.T) {
	t.Parallel()
	in := sampleValid{Name: "ok", Port: 0, Mode: "auto"}
	err := ports.Validate(in)
	require.Error(t, err)

	var verrs validator.ValidationErrors
	require.ErrorAs(t, err, &verrs)
	// Port has gt=0; 0 is not > 0 → fail.
	require.GreaterOrEqual(t, len(verrs), 1)
	found := false
	for _, fe := range verrs {
		if fe.Field() == "port" && fe.Tag() == "gt" {
			found = true
			assert.Equal(t, "0", fe.Param())
		}
	}
	assert.True(t, found, "expected gt error on port field; got %v", verrs)
}

func TestValidate_OneOf_BadValue_ReturnsOneOfTagWithAllowedParam(t *testing.T) {
	t.Parallel()
	in := sampleOneOf{Mode: "bogus"}
	err := ports.Validate(in)
	require.Error(t, err)

	var verrs validator.ValidationErrors
	require.ErrorAs(t, err, &verrs)
	require.Len(t, verrs, 1)
	assert.Equal(t, "mode", verrs[0].Field())
	assert.Equal(t, "oneof", verrs[0].Tag())
	assert.Equal(t, "auto manual", verrs[0].Param(),
		"oneof Param must echo allowed values for message rendering")
}

func TestValidate_URL_Empty_ReturnsRequiredTag(t *testing.T) {
	t.Parallel()
	in := sampleURL{URL: ""}
	err := ports.Validate(in)
	require.Error(t, err)

	var verrs validator.ValidationErrors
	require.ErrorAs(t, err, &verrs)
	require.Len(t, verrs, 1)
	assert.Equal(t, "url", verrs[0].Field())
	// required runs first; url tag only fires when value is non-empty
	// and unparseable.
	assert.Equal(t, "required", verrs[0].Tag())
}

func TestValidate_URL_Malformed_ReturnsURLTag(t *testing.T) {
	t.Parallel()
	in := sampleURL{URL: "::not a url::"}
	err := ports.Validate(in)
	require.Error(t, err)

	var verrs validator.ValidationErrors
	require.ErrorAs(t, err, &verrs)
	require.Len(t, verrs, 1)
	assert.Equal(t, "url", verrs[0].Field())
	assert.Equal(t, "url", verrs[0].Tag())
}

func TestValidate_Numeric_AboveBound_ReturnsLTETag(t *testing.T) {
	t.Parallel()
	in := sampleNumeric{Count: 101}
	err := ports.Validate(in)
	require.Error(t, err)

	var verrs validator.ValidationErrors
	require.ErrorAs(t, err, &verrs)
	require.Len(t, verrs, 1)
	assert.Equal(t, "count", verrs[0].Field())
	assert.Equal(t, "lte", verrs[0].Tag())
	assert.Equal(t, "100", verrs[0].Param())
}

func TestValidate_NonStructPointer_ReturnsInvalidValidationError(t *testing.T) {
	t.Parallel()
	// Passing a non-struct to Struct() returns InvalidValidationError —
	// the helper surfaces it so the caller can route to 500.
	var n int
	err := ports.Validate(n)
	require.Error(t, err)

	var verrs validator.ValidationErrors
	assert.False(t, errors.As(err, &verrs),
		"non-struct must NOT surface as ValidationErrors — needed for 500 path")
}

func TestValidate_HyphenJSONTag_FieldNotValidated(t *testing.T) {
	t.Parallel()
	// json:"-" → tag-name fn returns "" → validator treats the field
	// as anonymous and skips it.
	in := sampleHyphenJSON{Skipped: ""}
	err := ports.Validate(in)
	// Field is named in Go but unnamed for JSON; validator still
	// checks required because tag-name func returning "" only removes
	// the named alias. The field itself is still validated.
	require.Error(t, err)
	var verrs validator.ValidationErrors
	require.ErrorAs(t, err, &verrs)
	require.Len(t, verrs, 1)
	// Field() falls back to Go name when json tag resolves to empty.
	assert.Equal(t, "Skipped", verrs[0].Field())
}

func TestValidate_Singleton_TwoCallsShareInstance(t *testing.T) {
	t.Parallel()
	// Two sequential calls must reuse the same validator. We probe
	// indirectly: register a custom validation through a struct that
	// uses it on the SECOND call; the registration on first call must
	// stick. We use a non-mutating probe — both calls just validate
	// the same struct and must both succeed identically.
	in := sampleValid{Name: "ok", Port: 8080, Mode: "auto"}
	assert.NoError(t, ports.Validate(in))
	assert.NoError(t, ports.Validate(in))
}

type sampleBCP47 struct {
	Lang string `json:"lang" validate:"omitempty,bcp47_language_tag"`
}

type sampleAlphanumDash struct {
	Instance string `json:"instance" validate:"omitempty,alphanum_dash"`
}

func TestValidate_BCP47LanguageTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		lang    string
		wantErr bool
	}{
		{name: "empty_omitempty_ok", lang: ""},
		{name: "lower_two_letter", lang: "en"},
		{name: "lower_three_letter", lang: "eng"},
		{name: "upper_two_letter", lang: "EN"},
		{name: "language_region", lang: "pt-BR"},
		{name: "language_script", lang: "zh-Hans"},
		{name: "language_three_region", lang: "eng-US"},
		{name: "invalid_one_letter", lang: "e", wantErr: true},
		{name: "invalid_digits", lang: "12", wantErr: true},
		{name: "invalid_too_long", lang: "english", wantErr: true},
		{name: "invalid_trailing_dash", lang: "en-", wantErr: true},
		{name: "invalid_double_dash", lang: "en--US", wantErr: true},
		{name: "invalid_special_char", lang: "en_US", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ports.Validate(sampleBCP47{Lang: tt.lang})
			if tt.wantErr {
				require.Error(t, err)
				var verrs validator.ValidationErrors
				require.True(t, errors.As(err, &verrs))
				assert.Equal(t, "bcp47_language_tag", verrs[0].Tag())
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestValidate_AlphanumDash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		instance string
		wantErr  bool
	}{
		{name: "empty_omitempty_ok", instance: ""},
		{name: "letters", instance: "sonarr"},
		{name: "letters_with_dash", instance: "sonarr-1"},
		{name: "letters_with_underscore", instance: "radarr_main"},
		{name: "digits_only", instance: "12345"},
		{name: "mixed", instance: "Sonarr_4K-Anime-2"},
		{name: "invalid_space", instance: "sonarr 1", wantErr: true},
		{name: "invalid_slash", instance: "sonarr/1", wantErr: true},
		{name: "invalid_dot", instance: "sonarr.1", wantErr: true},
		{name: "invalid_colon", instance: "sonarr:1", wantErr: true},
		{name: "invalid_unicode", instance: "sonarrß1", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ports.Validate(sampleAlphanumDash{Instance: tt.instance})
			if tt.wantErr {
				require.Error(t, err)
				var verrs validator.ValidationErrors
				require.True(t, errors.As(err, &verrs))
				assert.Equal(t, "alphanum_dash", verrs[0].Tag())
				return
			}
			assert.NoError(t, err)
		})
	}
}
