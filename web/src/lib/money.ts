// Full USD currency formatting for movie budget/revenue (ADR-0021 §S5).
// Mirrors the operator dashboards' non-compact number style — src/lib/grabs/
// format.ts uses full Intl.NumberFormat, not compact ($85M) notation — so we
// render the full "$85,000,000" form.
const usd = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  maximumFractionDigits: 0,
});

// formatMoney renders a whole-dollar amount as e.g. "$85,000,000". Callers
// gate on isMoneyPresent first, so a zero/undefined value never reaches here.
export function formatMoney(value: number): string {
  return usd.format(value);
}

// isMoneyPresent encodes the ADR §S5 nil-vs-zero rule: a non-nil pointer to 0
// means "no reported budget/revenue" and MUST hide the row — so only a strictly
// positive number is considered present. undefined is likewise absent.
export function isMoneyPresent(value: number | undefined): value is number {
  return typeof value === 'number' && value > 0;
}
