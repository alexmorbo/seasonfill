import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, act, fireEvent } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { SearchBar } from './SearchBar';

function renderBar(onDebouncedChange = vi.fn(), delayMs = 250) {
  return render(
    <I18nextProvider i18n={i18n}>
      <SearchBar onDebouncedChange={onDebouncedChange} delayMs={delayMs} />
    </I18nextProvider>,
  );
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe('<SearchBar />', () => {
  it('debounces input changes before emitting', () => {
    const onChange = vi.fn();
    renderBar(onChange);
    const input = screen.getByLabelText('search') as HTMLInputElement;
    fireEvent.change(input, { target: { value: 'r' } });
    fireEvent.change(input, { target: { value: 'ri' } });
    fireEvent.change(input, { target: { value: 'ric' } });
    expect(onChange).not.toHaveBeenCalled();
    act(() => { vi.advanceTimersByTime(250); });
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith('ric');
  });

  it('clear button resets value and emits empty string', () => {
    const onChange = vi.fn();
    renderBar(onChange);
    const input = screen.getByLabelText('search') as HTMLInputElement;
    fireEvent.change(input, { target: { value: 'fargo' } });
    act(() => { vi.advanceTimersByTime(250); });
    expect(onChange).toHaveBeenLastCalledWith('fargo');
    fireEvent.click(screen.getByTestId('discovery-search-clear'));
    expect(input.value).toBe('');
    expect(onChange).toHaveBeenLastCalledWith('');
  });

  it('clear button is hidden when empty', () => {
    renderBar();
    expect(screen.queryByTestId('discovery-search-clear')).toBeNull();
  });
});
