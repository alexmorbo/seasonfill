package app

import (
	"encoding/json"
	"fmt"
)

// Render builds a localized (RU-primary) title+body for an outbox event from
// its JSON payload. MVP: a per-event_type template map, self-contained (no FE
// i18n coupling). Unknown event_types get a generic title+raw-JSON body so a
// future event still delivers something. NEVER include secrets in payloads.
func Render(eventType string, payload []byte) Message {
	var p map[string]any
	_ = json.Unmarshal(payload, &p) // best-effort; nil map on error
	s := func(k string) string {
		if v, ok := p[k]; ok {
			return fmt.Sprintf("%v", v)
		}
		return ""
	}
	switch eventType {
	case "grab.failed":
		return Message{
			Title: "Seasonfill: ошибка захвата",
			Body:  fmt.Sprintf("%s — S%s: захват релиза не удался (%s)", s("series_title"), s("season"), s("error")),
		}
	case "import.failed":
		return Message{
			Title: "Seasonfill: ошибка импорта",
			Body:  fmt.Sprintf("%s — S%s: Sonarr не смог импортировать релиз (%s)", s("series_title"), s("season"), s("message")),
		}
	case "grab.ok":
		return Message{
			Title: "Seasonfill: релиз захвачен",
			Body:  fmt.Sprintf("%s — S%s: релиз отправлен в загрузку (%s)", s("series_title"), s("season"), s("indexer")),
		}
	case "watchdog.regrab":
		return Message{
			Title: "Seasonfill: повторный захват",
			Body:  fmt.Sprintf("%s — S%s: watchdog перезахватил сезон", s("series_title"), s("season")),
		}
	case "inbox.dead_letter":
		return Message{
			Title: "Seasonfill: webhook в dead-letter",
			Body:  fmt.Sprintf("Событие Sonarr-webhook не обработано после ретраев (inbox #%s, %s)", s("inbox_id"), s("event_type")),
		}
	case "season.premiere":
		return Message{
			Title: "Seasonfill: премьера сезона",
			Body:  fmt.Sprintf("%s — S%s: премьера сезона выходит %s", s("series_title"), s("season"), s("air_date")),
		}
	case "air_date.announced":
		return Message{
			Title: "Seasonfill: назначена дата выхода",
			Body:  fmt.Sprintf("%s: следующий эпизод выходит %s", s("series_title"), s("air_date")),
		}
	case "digest.weekly":
		return Message{
			Title: "Seasonfill: дайджест недели",
			Body: fmt.Sprintf("Неделя %s — %s: премьер — %s, финалов — %s",
				s("from"), s("to"), s("premiere_count"), s("finale_count")),
		}
	case "request.approved":
		return Message{
			Title: "Seasonfill: запрос одобрен",
			Body:  fmt.Sprintf("Запрос #%s (%s) одобрен", s("request_id"), s("media_type")),
		}
	case "request.denied":
		return Message{
			Title: "Seasonfill: запрос отклонён",
			Body:  fmt.Sprintf("Запрос #%s (%s) отклонён", s("request_id"), s("media_type")),
		}
	default:
		return Message{
			Title: "Seasonfill: " + eventType,
			Body:  string(payload),
		}
	}
}
