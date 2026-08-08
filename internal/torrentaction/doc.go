// Package torrentaction owns the write-slice for qBittorrent torrent
// management (ADR-0013 Q2): pause/resume/recheck of torrents that passed
// through seasonfill's grab pipeline. The guard is hash-first — only
// hashes matched to our grab_records are actionable; foreign hashes 404.
// Actions dial the instance the torrent actually lives on (the grab
// record's InstanceName), never the preferred/lex-first instance the
// read path resolves. Every attempt writes a torrent_action_audit row.
package torrentaction
