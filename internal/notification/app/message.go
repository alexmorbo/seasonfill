package app

// Message is a rendered, provider-agnostic notification. shoutrrr carries
// title (via types.Params) + body.
type Message struct {
	Title string
	Body  string
}
