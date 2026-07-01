import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group';
import { Label } from '@/components/ui/label';
import { Button } from '@/components/ui/button';
import { Avatar } from '@/components/Avatar';
import type { MeResponse } from '@/lib/me-types';
import { useUpdateMeSettings } from '@/hooks/useMe';
import { useLanguage } from '@/hooks/useLanguage';
import { SUPPORTED_LANGS } from '@/i18n';

type AvatarMode = MeResponse['avatar_mode']; // 'auto' | 'monogram' | 'gravatar'

// Labels keyed by BCP-47 (story 564 B-lang alignment).
const LANG_LABELS: Record<string, string> = {
  'en-US': 'English',
  'ru-RU': 'Русский',
};

export interface AppearanceSectionProps {
  readonly me: MeResponse;
}

// AppearanceSection — language + avatar mode preferences. Two
// independent controls in one card:
//   - Language: dual-write via useLanguage (persists on every change).
//   - Avatar mode: local state + explicit Save button (so the operator
//     can preview different choices via the live Avatar above).
//
// The avatar preview uses the *currently-saved* me.avatar_resolved_mode
// while pending. If we previewed the unsaved mode we'd have to mirror
// the BE's "auto → gravatar/monogram" resolution rule on the FE, which
// duplicates logic. Simpler: preview shows the truth, the radio shows
// the pending choice.
//
// Story 487 (N-7c).
export function AppearanceSection({ me }: AppearanceSectionProps) {
  const { t } = useTranslation();
  const language = useLanguage();
  const saveSettings = useUpdateMeSettings();
  const [pendingAvatarMode, setPendingAvatarMode] = useState<AvatarMode>(me.avatar_mode);
  const dirty = pendingAvatarMode !== me.avatar_mode;

  // Sync local state when the server cache changes (e.g. after a
  // successful Save round-trips and useUpdateMeSettings invalidates
  // the ['me'] cache → me.avatar_mode flips to the saved value). We
  // intentionally use a setState-in-effect here instead of a derived
  // state pattern because the local state must be writable BEFORE
  // save (so radio clicks update the preview/dirty bit). See story
  // 487 Open Notes §10.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setPendingAvatarMode(me.avatar_mode);
  }, [me.avatar_mode]);

  const onSave = () => {
    saveSettings.mutate(
      { avatar_mode: pendingAvatarMode },
      {
        onSuccess: () => {
          toast.success(t('settings.profile.saved'));
        },
        onError: (err) => {
          toast.error(t('settings.profile.save_failed', { msg: err.message }));
        },
      },
    );
  };

  return (
    <section
      data-testid="appearance-section"
      className="flex flex-col gap-3.5"
    >
      <header className="flex flex-col gap-[3px]">
        <h2 className="text-[15px] font-[650] tracking-[-0.01em] m-0">
          {t('settings.profile.appearance')}
        </h2>
      </header>

      <div className="flex flex-col gap-2 py-[11px] border-b border-border-faint">
        <Label htmlFor="profile-language">
          {t('settings.profile.language_label')}
        </Label>
        <Select
          value={language.current}
          onValueChange={(v) => {
            if (v) void language.setLanguage(v);
          }}
        >
          <SelectTrigger id="profile-language" className="w-[220px]" data-testid="appearance-language">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {SUPPORTED_LANGS.map((code) => (
              <SelectItem key={code} value={code}>
                {LANG_LABELS[code] ?? code}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="flex flex-col gap-3 py-[11px]">
        <Label>{t('settings.profile.avatar_mode_label')}</Label>
        <div className="flex gap-5 items-start">
          <Avatar
            avatar_resolved_mode={me.avatar_resolved_mode}
            avatar_hash={me.avatar_hash}
            username={me.username}
            size={96}
            data-testid="appearance-section-avatar"
          />
          <RadioGroup
            value={pendingAvatarMode}
            onValueChange={(v) => setPendingAvatarMode(v as AvatarMode)}
            className="flex flex-col gap-2"
            data-testid="appearance-avatar-mode"
          >
            <div className="flex items-center gap-2">
              <RadioGroupItem value="auto" id="avatar-mode-auto" />
              <Label htmlFor="avatar-mode-auto" className="font-normal">
                {t('settings.profile.avatar_mode.auto')}
              </Label>
            </div>
            <div className="flex items-center gap-2">
              <RadioGroupItem value="monogram" id="avatar-mode-monogram" />
              <Label htmlFor="avatar-mode-monogram" className="font-normal">
                {t('settings.profile.avatar_mode.monogram')}
              </Label>
            </div>
            <div className="flex items-center gap-2">
              <RadioGroupItem value="gravatar" id="avatar-mode-gravatar" />
              <Label htmlFor="avatar-mode-gravatar" className="font-normal">
                {t('settings.profile.avatar_mode.gravatar')}
              </Label>
            </div>
          </RadioGroup>
        </div>
      </div>

      <div className="flex items-center gap-3 pt-2">
        <div className="flex-1" />
        <Button
          type="button"
          disabled={!dirty || saveSettings.isPending}
          onClick={onSave}
          data-testid="appearance-save"
        >
          {saveSettings.isPending ? t('common.saving') : t('settings.profile.save')}
        </Button>
      </div>
    </section>
  );
}
