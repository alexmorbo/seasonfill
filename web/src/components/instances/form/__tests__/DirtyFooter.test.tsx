import { describe, expect, it, vi } from 'vitest';
import userEvent from '@testing-library/user-event';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { DirtyFooter } from '../DirtyFooter';

const wrap = (n: React.ReactElement) => <I18nextProvider i18n={i18n}>{n}</I18nextProvider>;

describe('<DirtyFooter />', () => {
  it('shows the dirty indicator in edit mode when isDirty=true', () => {
    render(wrap(
      <DirtyFooter
        mode="edit" isDirty isSubmitting={false} editBlocked={false}
        onCancel={vi.fn()} onSubmit={vi.fn()}
      />,
    ));
    expect(screen.getByTestId('dirty-indicator')).toBeInTheDocument();
  });

  it('omits the dirty indicator in edit mode when clean', () => {
    render(wrap(
      <DirtyFooter
        mode="edit" isDirty={false} isSubmitting={false} editBlocked={false}
        onCancel={vi.fn()} onSubmit={vi.fn()}
      />,
    ));
    expect(screen.queryByTestId('dirty-indicator')).toBeNull();
  });

  it('shows the create webhook hint in create mode', () => {
    render(wrap(
      <DirtyFooter
        mode="create" isDirty={false} isSubmitting={false} editBlocked={false}
        onCancel={vi.fn()} onSubmit={vi.fn()}
      />,
    ));
    expect(screen.getByTestId('create-webhook-hint')).toBeInTheDocument();
  });

  it('Save is disabled while submitting OR editBlocked', () => {
    const { rerender } = render(wrap(
      <DirtyFooter
        mode="edit" isDirty isSubmitting editBlocked={false}
        onCancel={vi.fn()} onSubmit={vi.fn()}
      />,
    ));
    expect(screen.getByTestId('dirty-footer-save')).toBeDisabled();
    rerender(wrap(
      <DirtyFooter
        mode="edit" isDirty isSubmitting={false} editBlocked
        onCancel={vi.fn()} onSubmit={vi.fn()}
      />,
    ));
    expect(screen.getByTestId('dirty-footer-save')).toBeDisabled();
  });

  it('invokes onCancel + onSubmit on the respective buttons', async () => {
    const onCancel = vi.fn();
    const onSubmit = vi.fn();
    const user = userEvent.setup();
    render(wrap(
      <DirtyFooter
        mode="edit" isDirty isSubmitting={false} editBlocked={false}
        onCancel={onCancel} onSubmit={onSubmit}
      />,
    ));
    await user.click(screen.getByRole('button', { name: /cancel|отмен/i }));
    await user.click(screen.getByTestId('dirty-footer-save'));
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });
});
