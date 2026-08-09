package app

import "errors"

// ErrInvalidAgent is a 400-class validation error for agent CRUD input.
var ErrInvalidAgent = errors.New("invalid notification agent")
