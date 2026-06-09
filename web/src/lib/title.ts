// Return the series title verbatim, trimmed. Year is rendered as a
// separate subtitle node by callers (operator R2) — we never synthesize
// "(YYYY)" inside the title itself.
//
// The optional `year` parameter is accepted for back-compat with prior
// signatures but is ignored.
export function formatSeriesTitle(
  title: string | null | undefined,
  _year?: number | null | undefined,
): string {
  return (title ?? '').trim();
}
