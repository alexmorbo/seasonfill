package dto

// LanguagePref carries an optional BCP-47 language preference. The
// validator tag is registered in internal/shared/ports/validator.go.
type LanguagePref struct {
	Lang string `form:"lang" json:"lang,omitempty" validate:"omitempty,bcp47_language_tag"`
}
