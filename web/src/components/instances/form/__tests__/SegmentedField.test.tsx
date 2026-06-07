import { describe, expect, it, vi } from 'vitest';
import userEvent from '@testing-library/user-event';
import { render, screen } from '@testing-library/react';
import { SegmentedField } from '../SegmentedField';

describe('<SegmentedField />', () => {
  const options = [
    { value: 'a', label: 'A' },
    { value: 'b', label: 'B' },
    { value: 'c', label: 'C' },
  ];

  it('renders one button per option with data-state=on for the active value', () => {
    render(<SegmentedField value="b" onChange={vi.fn()} options={options} />);
    const wrap = screen.getByTestId('segmented-field');
    const btns = wrap.querySelectorAll('button');
    expect(btns).toHaveLength(3);
    const active = wrap.querySelector('[data-value="b"]') as HTMLElement;
    expect(active.getAttribute('data-state')).toBe('on');
  });

  it('invokes onChange with the clicked value', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<SegmentedField value="a" onChange={onChange} options={options} />);
    const btn = screen.getByRole('radio', { name: 'C' });
    await user.click(btn);
    expect(onChange).toHaveBeenCalledWith('c');
  });

  it('ignores empty-string emissions (Radix deselect bug guard)', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<SegmentedField value="a" onChange={onChange} options={options} />);
    // Clicking the already-active item triggers Radix deselect → ''.
    // SegmentedField's `if (v) onChange(v)` guard MUST drop it.
    const active = screen.getByRole('radio', { name: 'A' });
    await user.click(active);
    expect(onChange).not.toHaveBeenCalled();
  });
});
