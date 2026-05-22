import type { Config } from 'tailwindcss';
import animate from 'tailwindcss-animate';

const config: Config = {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        bg:        'oklch(var(--bg-base) / <alpha-value>)',
        surface:   'oklch(var(--bg-surface) / <alpha-value>)',
        'surface-2': 'oklch(var(--bg-surface-2) / <alpha-value>)',
        hover:     'oklch(var(--bg-hover) / <alpha-value>)',
        input:     'oklch(var(--bg-input) / <alpha-value>)',
        border:    'oklch(var(--border-subtle) / <alpha-value>)',
        'border-strong': 'oklch(var(--border-strong) / <alpha-value>)',
        'border-faint':  'oklch(var(--border-faint) / <alpha-value>)',
        foreground:      'oklch(var(--text-primary) / <alpha-value>)',
        'foreground-2':  'oklch(var(--text-secondary) / <alpha-value>)',
        muted:           'oklch(var(--text-muted) / <alpha-value>)',
        faint:           'oklch(var(--text-faint) / <alpha-value>)',
        accent:          'oklch(var(--accent) / <alpha-value>)',
        'accent-strong': 'oklch(var(--accent-strong) / <alpha-value>)',
        'accent-text':   'oklch(var(--accent-text) / <alpha-value>)',
        'status-success': 'oklch(var(--status-success) / <alpha-value>)',
        'status-warning': 'oklch(var(--status-warning) / <alpha-value>)',
        'status-danger':  'oklch(var(--status-danger) / <alpha-value>)',
        'status-info':    'oklch(var(--status-info) / <alpha-value>)',
        'status-neutral': 'oklch(var(--status-neutral) / <alpha-value>)',
        // shadcn-canonical aliases so vendored primitives work unmodified
        background: 'oklch(var(--bg-base) / <alpha-value>)',
        card:       'oklch(var(--bg-surface) / <alpha-value>)',
        'card-foreground':       'oklch(var(--text-primary) / <alpha-value>)',
        popover:                 'oklch(var(--bg-surface) / <alpha-value>)',
        'popover-foreground':    'oklch(var(--text-primary) / <alpha-value>)',
        primary:                 'oklch(var(--accent) / <alpha-value>)',
        'primary-foreground':    'oklch(var(--accent-text) / <alpha-value>)',
        secondary:               'oklch(var(--bg-surface-2) / <alpha-value>)',
        'secondary-foreground':  'oklch(var(--text-primary) / <alpha-value>)',
        destructive:             'oklch(var(--status-danger) / <alpha-value>)',
        'destructive-foreground': 'oklch(var(--text-primary) / <alpha-value>)',
        'muted-foreground':      'oklch(var(--text-muted) / <alpha-value>)',
        'accent-foreground':     'oklch(var(--accent-text) / <alpha-value>)',
        ring: 'oklch(var(--accent) / 0.5)',
      },
      fontFamily: {
        sans: ['"IBM Plex Sans"', 'ui-sans-serif', 'system-ui', '-apple-system', '"Segoe UI"', 'sans-serif'],
        mono: ['"JetBrains Mono"', '"IBM Plex Mono"', 'ui-monospace', 'SFMono-Regular', 'Menlo', 'monospace'],
      },
      borderRadius: { lg: '10px', md: '6px', sm: '4px' },
      keyframes: { 'fade-in': { from: { opacity: '0' }, to: { opacity: '1' } } },
      animation:  { 'fade-in': 'fade-in 0.15s ease' },
    },
  },
  plugins: [animate],
};
export default config;
