// Design tokens for the Antares Observatory (docs/UI-DESIGN.md §3).
// U3 imports this object into web/tailwind.config.js; components use semantic
// names (bg-surface, text-muted, text-ok) instead of raw hex values.

/** @type {import('tailwindcss').Config['theme']} */
export const theme = {
  extend: {
    colors: {
      bg: '#0b0d10',
      surface: '#14171c',
      'surface-2': '#1c2027',
      border: '#2a303a',
      text: {
        DEFAULT: '#e6e9ef',
        muted: '#9aa3b2',
        faint: '#6b7484',
      },
      accent: {
        DEFAULT: '#ff5a3c',
        soft: '#3a1712',
      },
      ok: '#3ddc97',
      warn: '#f5c451',
      crit: '#ff6b81',
      info: '#5cc8ff',
    },
    fontFamily: {
      sans: ['ui-sans-serif', 'system-ui', '-apple-system', 'Segoe UI', 'sans-serif'],
      mono: ['ui-monospace', 'SFMono-Regular', 'JetBrains Mono', 'Menlo', 'monospace'],
    },
    fontSize: {
      xs: ['11px', '16px'],
      sm: ['13px', '20px'],
      base: ['14px', '22px'],
      lg: ['16px', '24px'],
      xl: ['20px', '28px'],
    },
    spacing: {
      1: '4px',
      2: '8px',
      3: '12px',
      4: '16px',
      5: '20px',
      6: '24px',
      8: '32px',
    },
    borderRadius: {
      sm: '4px',
      md: '6px',
      lg: '10px',
    },
    boxShadow: {
      drawer: '0 12px 32px rgba(0, 0, 0, .45)',
    },
    transitionDuration: {
      hover: '120ms',
      drawer: '160ms',
    },
  },
};

export default { theme };
